package probe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	m "autoweight/model"
)

// Runner executes argv with stdin and returns stdout (injectable for tests).
type Runner func(ctx context.Context, argv []string, stdin string) (string, error)

func execRun(ctx context.Context, argv []string, stdin string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, io.Discard // stderr may echo secrets; drop it
	// v5.00: after a kill, don't wait for grandchildren that still hold the pipes.
	cmd.WaitDelay = 3 * time.Second
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return out.String(), context.DeadlineExceeded
	}
	return out.String(), err
}

// Client performs live probes with the same calls Python makes.
type Client struct {
	Cfg  m.M
	HTTP *http.Client
	Run  Runner
	// CountPath: local jsonl for direct-probe counting (v3.01); "" disables (tests).
	CountPath string
	key       string
	once      sync.Once
	kerr      error
	// v5.00 probe budget: set on the first probe of a round; site probes,
	// rechecks and upstream tests all stop starting once it is spent.
	bmu      sync.Mutex
	deadline time.Time
}

// hardTimeout is the per-probe wall-clock limit (ssh + remote script),
// probe.hard_timeout_s, default timeout_s+40 (the v4 behaviour).
func (c *Client) hardTimeout() time.Duration {
	p := c.Cfg.Sub("probe")
	return time.Duration(p.F("hard_timeout_s", p.F("timeout_s", 90)+40) * float64(time.Second))
}

// slot returns the time the next probe may use: min(hard timeout, budget left).
// ok=false when the round's probe budget (probe.budget_s, 0 = none) is spent.
func (c *Client) slot() (time.Duration, bool) { return c.slotFor(c.hardTimeout()) }

func (c *Client) slotFor(hard time.Duration) (time.Duration, bool) {
	b := c.Cfg.Sub("probe").F("budget_s", 0)
	if b <= 0 {
		return hard, true
	}
	budget := time.Duration(b * float64(time.Second))
	c.bmu.Lock()
	if c.deadline.IsZero() {
		c.deadline = time.Now().Add(budget)
	}
	left := time.Until(c.deadline)
	c.bmu.Unlock()
	if left < min(2*time.Second, budget/4) {
		return 0, false
	}
	return min(hard, left), true
}

// BudgetLeft reports whether the round's probe budget still allows a probe.
func (c *Client) BudgetLeft() bool { _, ok := c.slot(); return ok }

func NewClient(cfg m.M) *Client { return &Client{Cfg: cfg, HTTP: &http.Client{}, Run: execRun} }

// loadKey ports load_key: first existing local path, else `ssh host cat remote`.
// The key is kept in memory only.
func (c *Client) loadKey() (string, error) {
	c.once.Do(func() {
		p := c.Cfg.Sub("probe")
		for _, s := range strSlice(p["key_paths_local"]) {
			if strings.HasPrefix(s, "~/") {
				h, _ := os.UserHomeDir()
				s = filepath.Join(h, s[2:])
			}
			if b, err := os.ReadFile(s); err == nil {
				c.key = strings.TrimSpace(string(b))
				return
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		out, err := c.Run(ctx, []string{"ssh", c.Cfg.S("ssh_host", ""), "cat " + p.S("key_path_remote", "")}, "")
		if err != nil && ctx.Err() != nil {
			c.kerr = errors.New("key load timed out")
		}
		c.key = strings.TrimSpace(out)
	})
	return c.key, c.kerr
}

func strSlice(v any) []string {
	var out []string
	if a, ok := v.([]any); ok {
		for _, x := range a {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// SiteOnce ports probe_once (one streaming /v1/responses request via the site).
func (c *Client) SiteOnce(key string, ch int) m.Probe {
	p := c.Cfg.Sub("probe")
	timeout := time.Duration(p.F("timeout_s", 90) * float64(time.Second))
	t0 := time.Now()
	since := func() float64 { return time.Since(t0).Seconds() }
	var s Stream
	var status *int
	var errText *string
	setErr := func(e string) { errText = &e }
	body, _ := json.Marshal(map[string]any{"model": p.S("model", ""), "input": p.S("prompt", ""),
		"stream": true, "max_output_tokens": 600})
	lim, ok := c.slot()
	if !ok {
		return m.Probe{OK: false, Err: "probe_budget_exceeded"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), lim) // v5.00 hard total limit
	defer cancel()
	// requests' timeout is per connect/read, not total: a watchdog reset on every line.
	wd := time.AfterFunc(timeout, cancel)
	defer wd.Stop()
	req, _ := http.NewRequestWithContext(ctx, "POST", p.S("base_url", "")+p.S("path", ""), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key+"-"+strconv.Itoa(ch))
	req.Header.Set("Content-Type", "application/json")
	// v3.03: Caddy `encode gzip` buffers SSE; probes measure the channel itself.
	// Setting it explicitly also disables Go's transparent gzip.
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		setErr(pyErr(err, ctx, 150))
	} else {
		defer resp.Body.Close()
		status = m.I(resp.StatusCode)
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			setErr(Runes(string(b), 200))
		} else {
			sc := bufio.NewScanner(resp.Body)
			sc.Buffer(make([]byte, 64<<10), 8<<20)
			for sc.Scan() {
				wd.Reset(timeout)
				stop, ferr := s.Feed(sc.Text(), since())
				if ferr != nil {
					setErr(ferr.Error())
					break
				}
				if stop {
					break
				}
			}
			if e := sc.Err(); e != nil && errText == nil {
				setErr(pyErr(e, ctx, 150))
			}
		}
	}
	return s.Finish(status, errText, since(), key)
}

func pyErr(err error, ctx context.Context, n int) string {
	if ctx.Err() != nil || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return "ReadTimeout: " + Runes(err.Error(), n)
	}
	return "ConnectionError: " + Runes(err.Error(), n)
}

// Site ports probe.run: n_per_channel probes per id, all concurrently.
func (c *Client) Site(ids []int) m.Probes {
	p := c.Cfg.Sub("probe")
	if !p.B("enabled", false) {
		return m.Probes{}
	}
	key, err := "", error(nil)
	if !p.B("remote_site", false) {
		key, err = c.loadKey()
	}
	out := m.Probes{}
	if err != nil || (key == "" && !p.B("remote_site", false)) {
		for _, i := range ids {
			out[i] = []m.Probe{{OK: false, Err: "no key"}}
		}
		return out
	}
	type job struct{ id int }
	var jobs []job
	for _, i := range ids {
		for k := 0; k < p.I("n_per_channel", 1); k++ {
			jobs = append(jobs, job{i})
		}
	}
	res := make([]m.Probe, len(jobs))
	var wg sync.WaitGroup
	for n, j := range jobs {
		wg.Add(1)
		go func(n int, id int) {
			defer wg.Done()
			if p.B("remote_site", false) {
				res[n] = c.remoteSiteOnce(id)
			} else {
				res[n] = c.SiteOnce(key, id)
			}
		}(n, j.id)
	}
	wg.Wait()
	for n, j := range jobs {
		out[j.id] = append(out[j.id], res[n])
	}
	return out
}

// Recheck ports probe.recheck: probes outlier/failed channels once more,
// appending results with Recheck=true. Returns the rechecked ids.
func (c *Client) Recheck(probes m.Probes, ids []int, rounds []*m.Rec) []int {
	rid := RecheckIDs(c.Cfg, probes, ids, rounds)
	if len(rid) == 0 {
		return rid
	}
	if !c.BudgetLeft() { // v5.00: no recheck once the probe budget is spent
		return []int{}
	}
	remote := c.Cfg.Sub("probe").B("remote_site", false)
	key, err := "", error(nil)
	if !remote {
		key, err = c.loadKey()
	}
	if err != nil || (key == "" && !remote) {
		return []int{}
	}
	res := make([]m.Probe, len(rid))
	var wg sync.WaitGroup
	for n, i := range rid {
		wg.Add(1)
		go func(n, i int) {
			defer wg.Done()
			if remote {
				res[n] = c.remoteSiteOnce(i)
			} else {
				res[n] = c.SiteOnce(key, i)
			}
		}(n, i)
	}
	wg.Wait()
	for n, i := range rid {
		r := res[n]
		r.Recheck = true
		probes[i] = append(probes[i], r)
	}
	return rid
}

// ShellQuote ports shlex.quote: ssh joins argv into one remote shell command,
// so every arg must be quoted (v3.01: unquoted prompt was split into words).
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("@%+=:,./-_", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// UpstreamArgv ports upstream.remote_args (v3.01; no secrets in it, only the key file path).
func UpstreamArgv(cfg m.M, ch int, model string, timeout int) []string {
	p := cfg.Sub("probe")
	container, user, database := cfg.S("pg_container", "new-api-postgres"), cfg.S("pg_user", ""), cfg.S("pg_database", "")
	if _, err := m.PGExec(container, user, database); err != nil {
		return nil
	}
	args := []string{strconv.Itoa(ch), model, strconv.Itoa(timeout), p.S("prompt", ""), p.S("base_url", ""), p.S("key_path_remote", "")}
	if user != "" || database != "" || container != "new-api-postgres" {
		args = append(args, container, user, database)
	}
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = ShellQuote(a)
	}
	return []string{"ssh", "-o", "ConnectTimeout=10", cfg.S("ssh_host", ""), "python3 - " + strings.Join(q, " ")}
}

// RecordDirect ports upstream.record_direct: direct (non-site) probes are
// counted locally since they never reach the site logs. Empty path = skip.
func RecordDirect(path string, ch int, model string, r m.Probe) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(map[string]any{"ts": time.Now().Format("2006-01-02T15:04:05-0700"), "channel": ch, "model": model,
		"status": r.Status, "ok": r.OK, "cls": Classify(r, 20)})
	_, err = f.Write(append(b, '\n'))
	return err
}

// Upstream ports upstream.probe: n sequential tests through the remote script
// (v3.01: enabled channels go via the site with the test token; others direct).
func (c *Client) Upstream(ch, n int) []m.Probe {
	model, timeout := c.Cfg.Sub("probe").S("model", ""), 90
	out := []m.Probe{}
	for k := 0; k < n; k++ {
		lim, ok := c.slotFor(time.Duration(c.Cfg.Sub("probe").F("hard_timeout_s", float64(timeout+40)) * float64(time.Second)))
		if !ok { // v5.00: budget spent, fail the rest fast (never counted as direct)
			for ; k < n; k++ {
				out = append(out, m.Probe{OK: false, Err: "probe_budget_exceeded"})
			}
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), lim)
		argv := UpstreamArgv(c.Cfg, ch, model, timeout)
		if len(argv) == 0 {
			cancel()
			return []m.Probe{{OK: false, Err: "invalid database connection configuration"}}
		}
		stdout, err := c.Run(ctx, argv, Remote)
		cancel()
		var r m.Probe
		if errors.Is(err, context.DeadlineExceeded) {
			r = m.Probe{OK: false, Err: "ssh_timeout", Total: m.F(m.Round(lim.Seconds(), 1))}
		} else {
			r = ParseRemote(stdout)
		}
		out = append(out, r)
		if r.Route != "site" { // direct or unknown: count it (prefer over-counting)
			_ = RecordDirect(c.CountPath, ch, model, r)
		}
	}
	return out
}

// ParseRemote decodes the remote script's last stdout line (remote_failed otherwise).
func ParseRemote(stdout string) m.Probe {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var p m.Probe
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" && json.Unmarshal([]byte(last), &p) == nil {
		return p
	}
	return m.Probe{OK: false, Err: "remote_failed"}
}

// Live implements core.Prober with real calls (site probe + recheck, upstream tests).
type Live struct {
	C         *Client
	Rounds    []*m.Rec // hist (last 24 rounds) for recheck baselines
	Rechecked []int
}

func (l *Live) Site(ids []int) (m.Probes, bool, error) {
	ps := l.C.Site(ids)
	if len(ps) > 0 {
		l.Rechecked = l.C.Recheck(ps, ids, l.Rounds)
	}
	return ps, false, nil
}

func (l *Live) Upstream(cid, n int) ([]m.Probe, error) {
	if cid <= 0 {
		return nil, fmt.Errorf("bad channel id %d", cid)
	}
	return l.C.Upstream(cid, n), nil
}

func (l *Live) RecheckedIDs() []int { return l.Rechecked }
