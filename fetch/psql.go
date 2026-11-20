package fetch

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	m "autoweight/model"
)

// Psql reads production through `ssh host docker exec -i container psql`.
// Credentials stay in the container env ($POSTGRES_USER/$POSTGRES_DB) and are
// never seen by this process. Every statement runs inside BEGIN READ ONLY ...
// ROLLBACK. At>0 freezes "now" for historical snapshots (created_at <= At).
type Psql struct {
	SSHHost, Container, ModelLike, ExcludeLike string
	PGUser, PGDatabase                         string
	Priority                                   int // configured channel priority (gpt 10, claude 7)
	At                                         int64
	Timeout                                    time.Duration
	// Run is injectable for tests; default executes ssh/docker.
	Run func(ctx context.Context, argv []string, stdin string) (string, error)
}

func NewPsql(cfg m.M) *Psql {
	return &Psql{SSHHost: cfg.S("ssh_host", ""), Container: cfg.S("pg_container", "new-api-postgres"),
		PGUser: cfg.S("pg_user", ""), PGDatabase: cfg.S("pg_database", ""),
		ModelLike: cfg.S("model_like", "gpt-%"), ExcludeLike: cfg.S("exclude_model_like", "gpt-image-%"),
		Priority: cfg.I("priority", 10), Timeout: 120 * time.Second}
}

var likeRe = regexp.MustCompile(`^[A-Za-z0-9.%_-]+$`)
var writeRe = regexp.MustCompile(`(?i)\b(update|insert|delete|alter|drop|truncate|create|grant|commit)\b`)

// Argv is the psql command line (no secrets in it).
func (p *Psql) Argv() []string {
	prefix, err := m.PGExec(p.Container, p.PGUser, p.PGDatabase)
	if err != nil {
		return nil
	}
	inner := `psql -v ON_ERROR_STOP=1 -U $POSTGRES_USER -d $POSTGRES_DB -At -F"|"`
	if p.SSHHost != "" {
		esc := `psql -v ON_ERROR_STOP=1 -U \$POSTGRES_USER -d \$POSTGRES_DB -At -F\"|\"`
		return []string{"ssh", "-o", "ConnectTimeout=10", "-o", "BatchMode=yes", p.SSHHost,
			fmt.Sprintf(`%s sh -c "%s"`, strings.Join(prefix, " "), esc)}
	}
	return append(prefix, "sh", "-c", inner)
}

// DefaultRun executes argv with stdin; stderr is truncated, never echoed whole.
func DefaultRun(ctx context.Context, argv []string, stdin string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("invalid database connection configuration")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		e := strings.TrimSpace(errb.String())
		if len(e) > 300 {
			e = e[:300]
		}
		return "", fmt.Errorf("psql failed: %v: %s", err, e)
	}
	return out.String(), nil
}

// RO runs sql in a read-only transaction that is always rolled back.
func (p *Psql) RO(sql string) ([][]string, error) {
	if writeRe.MatchString(sql) {
		return nil, fmt.Errorf("refusing non-read SQL in read-only path")
	}
	if !likeRe.MatchString(p.ModelLike) || !likeRe.MatchString(p.ExcludeLike) {
		return nil, fmt.Errorf("bad model_like pattern")
	}
	if len(p.Argv()) == 0 {
		return nil, fmt.Errorf("invalid database connection configuration")
	}
	run := p.Run
	if run == nil {
		run = DefaultRun
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.Timeout)
	defer cancel()
	out, err := run(ctx, p.Argv(), "BEGIN READ ONLY;\n"+sql+"\nROLLBACK;\n")
	if err != nil {
		return nil, err
	}
	var rows [][]string
	for _, l := range strings.Split(out, "\n") {
		if l == "" || l == "BEGIN" || l == "ROLLBACK" {
			continue
		}
		rows = append(rows, strings.Split(l, "|"))
	}
	return rows, nil
}

func (p *Psql) scope() string {
	return fmt.Sprintf(`exists (select 1 from abilities a where a.channel_id=c.id
  and a.model like '%s' and a.model not like '%s')`, p.ModelLike, p.ExcludeLike)
}

func atoi(s string) int { v, _ := strconv.Atoi(s); return v }

func (p *Psql) Channels() ([]m.Channel, error) {
	rs, err := p.RO("select c.id, c.name, c.weight, c.priority from channels c where c.status=1 and " + p.scope() + " order by c.id;")
	if err != nil {
		return nil, err
	}
	out := []m.Channel{}
	for _, r := range rs {
		if len(r) < 4 {
			return nil, fmt.Errorf("bad channel row")
		}
		out = append(out, m.Channel{ID: atoi(r[0]), Name: r[1], Weight: atoi(r[2]), Priority: atoi(r[3])})
	}
	return out, nil
}

func (p *Psql) Disabled() ([]m.Channel, error) {
	rs, err := p.RO("select c.id, c.name, c.status from channels c where c.status<>1 and " + p.scope() + " order by c.id;")
	if err != nil {
		return nil, err
	}
	out := []m.Channel{}
	for _, r := range rs {
		if len(r) < 3 {
			return nil, fmt.Errorf("bad channel row")
		}
		out = append(out, m.Channel{ID: atoi(r[0]), Name: r[1], Status: atoi(r[2])})
	}
	return out, nil
}

// LogsSQL is db.logs' query; with at>0 the window ends at `at`.
func (p *Psql) LogsSQL(ids []int, minutes int) string {
	ss := make([]string, len(ids))
	for i, v := range ids {
		ss[i] = strconv.Itoa(v)
	}
	now := "extract(epoch from now())::bigint"
	upper := ""
	if p.At > 0 {
		now = strconv.FormatInt(p.At, 10)
		upper = " and created_at <= " + now
	}
	return fmt.Sprintf(`select channel_id, created_at, type, coalesce(use_time,0), coalesce(completion_tokens,0), coalesce(token_id,0),
 case when type=2 and other like '%%"frt_version":2%%' then coalesce((substring(other from '"frt":([0-9.]+)'))::float, -1) else -1 end,
 case when type=2 and other like '%%"frt_version":2%%' then coalesce((substring(other from '"content_span_ms":([0-9.]+)'))::float, -1) else -1 end,
 case when type=2 and other like '%%"frt_version":2%%' then coalesce((substring(other from '"content_chunks":([0-9]+)'))::int, -1) else -1 end,
 case when type=5 then coalesce((substring(content from 'status_code=([0-9]{3})'))::int, -1) else -1 end,
 case when type=5 and (content ilike '%%session is blocked%%' or content like '%%会话已被%%') then 1 else 0 end,
 case when type=5 and (content ilike '%%content safety policy%%' or content like '%%内容安全%%') then 1 else 0 end,
 model_name, case when is_stream then 1 when is_stream = false then 2 else 0 end,
 case when other like '%%"channel_affinity":{"channel_id":%%' then 1 else 0 end,
 case when type=2 and coalesce(completion_tokens,0)=0 and coalesce(prompt_tokens,0)=0 then 1 else 0 end,
 case when type=5 and (content like '%%status_code=400%%' or content not like '%%status_code=%%') and (content ilike '%%does not support%%' or content ilike '%%not supported%%' or content ilike '%%unsupported%%') then 1 else 0 end,
 case when type=5 and content ilike '%%is not available for this group%%' then 1 else 0 end,
 case when type=5 and (content like '%%status_code=400%%' or content not like '%%status_code=%%') and (content ilike '%%encrypted content%%' or content ilike '%%Encrypted context cannot be read%%') then 1 else 0 end,
 case when type=5 and (content ilike '%%currently busy%%' or content ilike '%%server is busy%%' or content ilike '%%overloaded%%') then 1 else 0 end,
 case when type=5 and (content ilike '%%do request failed%%' or content ilike '%%Model request could not be transmitted%%') then 1 else 0 end,
 case when type=5 and (content ilike '%%adaptive thinking%%' or content ilike '%%Client connection closed%%'
   or (content ilike '%%signature%%' and (content like '%%status_code=400%%' or content not like '%%status_code=%%'))
   or content ilike '%%Unsupported parameter%%' or content ilike '%%Tool result is missing call_id%%'
   or content ilike '%%Encrypted context cannot be read%%' or content ilike '%%Invalid request parameter%%' or content ilike '%%Invalid ''input%%') then 1 else 0 end,
 case when type=5 and (content ilike '%%returned HTTP 404%%' or content ilike '%%model_not_found%%' or content like '%%status_code=404%%') then 1 else 0 end,
 case when type=5 and (content ilike '%%timed out%%' or content ~ 'status_code=(408|504|524)') then 1 else 0 end,
 case when type=5 and (content like '%%status_code=429%%' or content ilike '%%Concurrency limit exceeded%%' or content ilike '%%Model service temporarily limited%%') then 1 else 0 end
from logs where created_at >= %s - %d%s
and type in (2,5) and channel_id in (%s)
and model_name like '%s' and model_name not like '%s';
select %s;`, now, minutes*60, upper, strings.Join(ss, ","), p.ModelLike, p.ExcludeLike, now)
}

func (p *Psql) Logs(ids []int, minutes int) (int64, []m.Row, error) {
	if len(ids) == 0 {
		return 0, nil, fmt.Errorf("no channel ids")
	}
	rs, err := p.RO(p.LogsSQL(ids, minutes))
	if err != nil {
		return 0, nil, err
	}
	if len(rs) == 0 {
		return 0, nil, fmt.Errorf("no now row")
	}
	now, err := strconv.ParseInt(rs[len(rs)-1][0], 10, 64)
	if err != nil {
		return 0, nil, err
	}
	rows := make([]m.Row, 0, len(rs)-1)
	for _, r := range rs[:len(rs)-1] {
		rows = append(rows, ParseLogRow(r))
	}
	return now, rows, nil
}

// ParseLogRow mirrors db.logs' dict construction.
func ParseLogRow(r []string) m.Row {
	x := m.Row{Ch: atoi(r[0]), Type: atoi(r[2]), Ut: atoi(r[3]), Ct: atoi(r[4]), Tok: atoi(r[5])}
	x.T, _ = strconv.ParseInt(r[1], 10, 64)
	pf := func(s string) (float64, bool) {
		v, err := strconv.ParseFloat(s, 64)
		return v, err == nil
	}
	if len(r) > 6 {
		if v, ok := pf(r[6]); ok && v > 0 {
			x.Frt = m.F(m.Round(v/1000, 3))
		}
	}
	if len(r) > 8 && r[7] != "" && r[8] != "" {
		v, ok := pf(r[7])
		c, err := strconv.Atoi(r[8])
		if ok && err == nil && v >= 0 && c >= 0 {
			x.Span, x.Chunks = m.F(m.Round(v/1000, 3)), m.I(c)
		}
	}
	if len(r) > 9 && r[9] != "" {
		if c, err := strconv.Atoi(r[9]); err == nil && c > 0 {
			x.Sc = m.I(c)
		}
	}
	// v3.11: session-level security-policy block 403 ("请开启新会话")
	if len(r) > 10 && r[10] == "1" {
		x.Sb = 1
	}
	if len(r) > 11 && r[11] == "1" {
		x.Cs = 1
	}
	if len(r) > 13 {
		x.Mdl, x.St = r[12], atoi(r[13])
	}
	if len(r) > 14 && r[14] == "1" {
		x.Aff = 1
	}
	if len(r) > 15 && r[15] == "1" {
		x.Ue = 1
	}
	if len(r) > 16 && r[16] == "1" {
		x.Uf = 1
	}
	if len(r) > 17 && r[17] == "1" {
		x.Mm = 1
	}
	if len(r) > 18 && r[18] == "1" {
		x.Ec = 1
	}
	if len(r) > 19 && r[19] == "1" {
		x.Bz = 1
	}
	if len(r) > 20 && r[20] == "1" {
		x.Df = 1
	}
	if len(r) > 21 && r[21] == "1" {
		x.Cl = 1
	}
	if len(r) > 22 && r[22] == "1" {
		x.Nf = 1
	}
	if len(r) > 23 && r[23] == "1" {
		x.To = 1
	}
	if len(r) > 24 && r[24] == "1" {
		x.Cap = 1
	}
	return x
}
