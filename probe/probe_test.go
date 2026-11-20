package probe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	m "autoweight/model"
)

const fakeKey = "sk-unit-test-key"

func cfgFor(url string) m.M {
	return m.M{"ssh_host": "prod", "probe": map[string]any{"enabled": true, "base_url": url, "path": "/v1/responses",
		"model": "gpt-x", "n_per_channel": 1.0, "timeout_s": 5.0, "prompt": "hi", "key_paths_local": []any{},
		"key_path_remote": "/secret/path", "recheck": map[string]any{"enabled": true, "ratio": 2.0, "min_s": 8.0, "history": 3.0, "min_hist": 2.0}}}
}

func fakeSSH(t *testing.T, upstream string) Runner {
	return func(ctx context.Context, argv []string, stdin string) (string, error) {
		if argv[len(argv)-1] == "cat /secret/path" {
			return fakeKey + "\n", nil
		}
		if stdin != Remote {
			t.Fatalf("upstream must send the Python REMOTE script")
		}
		if upstream == "hang" {
			return "", context.DeadlineExceeded
		}
		return upstream, nil
	}
}

func streamingClassifier(t *testing.T) {
	ev := func(n int, spread float64) []float64 {
		out := make([]float64, n)
		for i := range out {
			out[i] = 1 + spread*float64(i)/float64(n-1)
		}
		return out
	}
	cases := []struct {
		n      int
		spread float64
		want   *bool
	}{{19, 5, nil}, {20, 1.0, m.B(true)}, {20, 0.2999, m.B(false)}, {20, 0.3, nil}, {200, 0.9, nil}, {200, 4, m.B(true)}}
	for _, c := range cases {
		got := Streaming(ev(c.n, c.spread))
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("n=%d spread=%v got %v", c.n, c.spread, got)
		}
	}
}

func feedShapes(t *testing.T) {
	cases := []struct {
		line       string
		text, stop bool
		err        bool
	}{
		{`data: {"type":"response.output_text.delta","delta":"a"}`, true, false, false},
		{`data: {"type":"response.output_text.delta","delta":""}`, false, false, false},
		{`data: {"choices":[{"delta":{"content":"x"}}]}`, true, false, false},
		{`data: {"choices":[]}`, false, false, false},
		{`data: {"type":"response.completed"}`, false, true, false},
		{`data: [DONE]`, false, true, false},
		{`event: x`, false, false, false},
		{`data: {bad`, false, false, false},
		{`data: 5`, false, false, true},
		{`data: {"choices":[1]}`, false, false, true},
	}
	for _, c := range cases {
		var s Stream
		stop, err := s.Feed(c.line, 1)
		if (len(s.Events) > 0) != c.text || stop != c.stop || (err != nil) != c.err {
			t.Errorf("%s: text=%v stop=%v err=%v", c.line, len(s.Events) > 0, stop, err)
		}
	}
}

func sse(w http.ResponseWriter, n int, gap time.Duration) {
	f := w.(http.Flusher)
	fmt.Fprintln(w, `data: {"type":"response.created"}`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"t%d\"}\n\n", i)
		f.Flush()
		time.Sleep(gap)
	}
	fmt.Fprintln(w, `data: {"type":"response.completed"}`)
}

func siteLiveAndRecheck(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		auth := r.Header.Get("Authorization")
		switch auth {
		case "Bearer " + fakeKey + "-2":
			sse(w, 25, 60*time.Millisecond) // true streaming
		case "Bearer " + fakeKey + "-5":
			sse(w, 25, 0) // buffered
		default:
			w.WriteHeader(403)
			fmt.Fprint(w, "denied for "+strings.TrimPrefix(auth, "Bearer "))
		}
	}))
	defer srv.Close()
	c := NewClient(cfgFor(srv.URL))
	c.Run = fakeSSH(t, "")
	l := &Live{C: c}
	ps, reused, err := l.Site([]int{2, 5, 9})
	if err != nil || reused {
		t.Fatal(err)
	}
	if p := ps[2][0]; !p.OK || p.Streaming == nil || !*p.Streaming || *p.Deltas != 25 {
		t.Errorf("#2 %+v", p)
	}
	if p := ps[5][0]; !p.OK || p.Streaming == nil || *p.Streaming {
		t.Errorf("#5 should be buffered: %+v", p)
	}
	p9 := ps[9]
	if len(p9) != 2 || p9[0].OK || *p9[0].Status != 403 || !p9[1].Recheck {
		t.Errorf("#9 failed probe should be rechecked once: %+v", p9)
	}
	if e, _ := p9[0].Err.(string); strings.Contains(e, fakeKey) || !strings.Contains(e, "***") {
		t.Errorf("key not redacted: %q", e)
	}
	if fmt.Sprint(l.RecheckedIDs()) != "[9]" || atomic.LoadInt32(&calls) != 4 {
		t.Errorf("rechecked %v calls %d", l.RecheckedIDs(), calls)
	}
	if a := Anomalies(ps); fmt.Sprint(a) != "map[9:[http_err]]" {
		t.Errorf("anomalies %v", a)
	}
}

func noKey(t *testing.T) {
	c := NewClient(cfgFor("http://127.0.0.1:1"))
	c.Run = func(context.Context, []string, string) (string, error) { return "", nil }
	ps := c.Site([]int{3})
	if len(ps[3]) != 1 || ps[3][0].Err != "no key" {
		t.Errorf("%+v", ps)
	}
}

func upstreamRemote(t *testing.T) {
	c := NewClient(cfgFor("x"))
	c.Run = fakeSSH(t, "noise\n"+`{"ok": true, "status": 200, "ttft": 1.5, "total": 4.2, "deltas": 40, "span": 2.6, "err": null}`)
	rs := c.Upstream(21, 3)
	if len(rs) != 3 || !rs[0].OK || *rs[0].Span != 2.6 || Verdict(rs, 3, 20).Recovered != true {
		t.Errorf("%+v", rs)
	}
	c.Run = fakeSSH(t, "hang")
	if r := c.Upstream(21, 1)[0]; r.Err != "ssh_timeout" || *r.Total != 130 {
		t.Errorf("%+v", r)
	}
	c.Run = fakeSSH(t, "")
	if r := c.Upstream(21, 1)[0]; r.Err != "remote_failed" {
		t.Errorf("%+v", r)
	}
	a := UpstreamArgv(cfgFor("x"), 21, "gpt-x", 90)
	if strings.Join(a, " ") != "ssh -o ConnectTimeout=10 prod python3 - 21 gpt-x 90 hi x /secret/path" {
		t.Errorf("%v", a)
	}
	cfg := cfgFor("https://reverix.ai")
	cfg.Sub("probe")["prompt"] = "请用中文写 it's 200 字"
	a = UpstreamArgv(cfg, 26, "gpt-5", 90)
	if len(a) != 5 || a[4] != `python3 - 26 gpt-5 90 '请用中文写 it'"'"'s 200 字' https://reverix.ai /secret/path` {
		t.Errorf("%q", a)
	}
}

func classifyTable(t *testing.T) {
	cases := []struct {
		p    m.Probe
		want string
	}{
		{m.Probe{OK: true, Ttft: m.F(3)}, "ok"},
		{m.Probe{OK: true, Ttft: m.F(25)}, "slow"},
		{m.Probe{Err: "ReadTimeout: x"}, "timeout"},
		{m.Probe{Status: m.I(524)}, "timeout"},
		{m.Probe{Status: m.I(401), Err: "no"}, "http_err"},
		{m.Probe{Status: m.I(200)}, "empty"},
		{m.Probe{Err: "ssh_timeout"}, "timeout"},
		{m.Probe{Err: "remote_failed"}, "empty"},
	}
	for _, c := range cases {
		if g := Classify(c.p, 20); g != c.want {
			t.Errorf("%+v: %s want %s", c.p, g, c.want)
		}
	}
}

func needsRecheck(t *testing.T) {
	rc := m.M{"ratio": 2.0, "min_s": 8.0, "min_hist": 2.0}
	ok := func(x float64) []m.Probe { return []m.Probe{{OK: true, Ttft: m.F(x)}} }
	cases := []struct {
		ps   []m.Probe
		hist []float64
		want bool
	}{{nil, nil, true}, {ok(20), []float64{3}, false}, {ok(20), []float64{3, 4}, true}, {ok(7.9), []float64{1, 1}, false}, {ok(9), []float64{5, 4, 5}, false}}
	for _, c := range cases {
		if g := NeedsRecheck(c.ps, c.hist, rc); g != c.want {
			t.Errorf("%v %v: %v", c.ps, c.hist, g)
		}
	}
}

func identityHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Accept-Encoding")
		w.WriteHeader(500)
	}))
	defer srv.Close()
	NewClient(cfgFor(srv.URL)).SiteOnce(fakeKey, 26)
	if got != "identity" {
		t.Errorf("Accept-Encoding=%q", got)
	}
	for _, s := range []string{`"Accept-Encoding": "identity"`, `route = "site"`, `key = tk + "-" + str(cid)`} {
		if !strings.Contains(Remote, s) {
			t.Errorf("Remote missing %s", s)
		}
	}
}

func recordDirect(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.jsonl")
	c := NewClient(cfgFor("x"))
	c.CountPath = p
	c.Run = func(ctx context.Context, argv []string, stdin string) (string, error) {
		if strings.HasSuffix(argv[len(argv)-1], "/secret/path") && stdin == Remote {
			if strings.Contains(argv[len(argv)-1], "21") {
				return `{"route":"site","ok":true,"ttft":1.0}`, nil
			}
			return `{"route":"direct","ok":false,"status":502,"err":"x"}`, nil
		}
		return "", nil
	}
	c.Upstream(21, 1)
	c.Upstream(5, 2)
	b, _ := os.ReadFile(p)
	if n := strings.Count(string(b), "\n"); n != 2 || !strings.Contains(string(b), `"channel":5`) {
		t.Errorf("%d %s", n, b)
	}
}

// Pure parsing/classification of probe streams and results.
func TestClassifiers(t *testing.T) {
	t.Run("streaming", streamingClassifier)
	t.Run("sse feed", feedShapes)
	t.Run("classify", classifyTable)
	t.Run("recheck", needsRecheck)
}

// Site probes through a fake site (test token, key redaction, recheck).
func TestSite(t *testing.T) {
	t.Run("live+recheck", siteLiveAndRecheck)
	t.Run("no key", noKey)
	t.Run("identity encoding", identityHeader)
}

// Upstream/direct tests run on the prod host via ssh; the key stays there.
func TestUpstream(t *testing.T) {
	t.Run("remote", upstreamRemote)
	t.Run("direct count", recordDirect)
}
