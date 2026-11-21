package fetch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	m "autoweight/model"
)

func TestReadOnlyWrapper(t *testing.T) {
	var got string
	p := NewPsql(m.M{"ssh_host": "h"})
	p.Run = func(_ context.Context, argv []string, stdin string) (string, error) {
		got = stdin
		return "BEGIN\n1|a|3|10\nROLLBACK\n", nil
	}
	cs, err := p.Channels()
	if err != nil || len(cs) != 1 || cs[0].Weight != 3 {
		t.Fatalf("%v %v", cs, err)
	}
	if !strings.HasPrefix(got, "BEGIN READ ONLY;\n") || !strings.HasSuffix(got, "ROLLBACK;\n") {
		t.Fatalf("not wrapped: %q", got)
	}
	for _, bad := range []string{"update channels set weight=1", "delete from logs", "insert into x values(1)", "select 1; commit"} {
		if _, err := p.RO(bad); err == nil {
			t.Errorf("write SQL accepted: %q", bad)
		}
	}
	p.ModelLike = "gpt-%'; drop"
	if _, err := p.RO("select 1"); err == nil {
		t.Error("bad like accepted")
	}
}

func TestLogsSQLFrozen(t *testing.T) {
	p := NewPsql(m.M{})
	p.At = 1793279957
	s := p.LogsSQL([]int{2, 5}, 180)
	for _, h := range []string{"created_at >= 1793279957 - 10800 and created_at <= 1793279957", "channel_id in (2,5)", "select 1793279957;", "content ilike '%session is blocked%' or content like '%会话已被%') then 1 else 0 end", "content ilike '%content safety policy%' or content like '%内容安全%') then 1 else 0 end", `other like '%"channel_affinity":{"channel_id":%'`} {
		if !strings.Contains(s, h) {
			t.Errorf("missing %q", h)
		}
	}
}

func TestParseLogRow(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"2", "100", "2", "5", "300", "38", "1234.5", "4000", "57", "-1"}, `{"ch":2,"t":100,"type":2,"ut":5,"ct":300,"tok":38,"frt":1.234,"span":4,"chunks":57}`},
		{[]string{"2", "100", "2", "5", "300", "1305", "1234.5", "4000", "57", "-1"}, `{"ch":2,"t":100,"type":2,"ut":5,"ct":300,"tok":1305,"frt":1.234,"span":4,"chunks":57}`},
		{[]string{"2", "100", "2", "5", "300", "1", "-1", "-1", "-1", "-1"}, `{"ch":2,"t":100,"type":2,"ut":5,"ct":300,"tok":1}`},
		{[]string{"5", "9", "5", "0", "0", "1", "-1", "-1", "-1", "429"}, `{"ch":5,"t":9,"type":5,"ut":0,"ct":0,"tok":1,"sc":429}`},
		{[]string{"22", "9", "5", "0", "0", "1", "-1", "-1", "-1", "403", "1"}, `{"ch":22,"t":9,"type":5,"ut":0,"ct":0,"tok":1,"sc":403,"sb":1}`},
		{[]string{"22", "9", "5", "0", "0", "1", "-1", "-1", "-1", "403", "0"}, `{"ch":22,"t":9,"type":5,"ut":0,"ct":0,"tok":1,"sc":403}`},
		{[]string{"20", "9", "2", "7", "500", "665", "-1", "-1", "-1", "-1", "0", "0", "gpt-6-astra", "1", "1"}, `{"ch":20,"t":9,"type":2,"ut":7,"ct":500,"tok":665,"m":"gpt-6-astra","st":1,"aff":1}`},
		{[]string{"5", "9", "2", "1", "1", "1", "2000.5", "0", "0", "-1"}, `{"ch":5,"t":9,"type":2,"ut":1,"ct":1,"tok":1,"frt":2.001,"span":0,"chunks":0}`},
	}
	for _, c := range cases {
		if got := js(ParseLogRow(c.in)); got != c.want {
			t.Errorf("%v: got %s want %s", c.in, got, c.want)
		}
	}
}

func TestSnapshotLogsWindow(t *testing.T) {
	s := &Snapshot{Now: 1000, LogMin: 60, Rows: []m.Row{{Ch: 1, T: 1000}, {Ch: 1, T: 400}, {Ch: 1, T: 399}, {Ch: 2, T: 900}, {Ch: 1, T: 1001}}}
	n, rows, err := SnapSource{S: s}.Logs([]int{1}, 10)
	if err != nil || n != 1000 || len(rows) != 2 {
		t.Fatalf("%d %v %v", n, rows, err)
	}
	if _, _, err := (SnapSource{S: s}).Logs([]int{1}, 61); err == nil {
		t.Fatal("window beyond snapshot accepted")
	}
}

func js(v any) string {
	b, _ := jsonMarshal(v)
	return string(b)
}

var jsonMarshal = json.Marshal

// Post-apply verify: read-only SQL, and a channel is OK only with priority 10,
// abilities present and in sync.
func TestVerifyChecks(t *testing.T) {
	p := NewPsql(m.M{})
	if writeRe.MatchString(p.VerifySQL()) {
		t.Fatal("verify SQL must be read-only")
	}
	cs, err := ParseChecks([][]string{{"2", "a", "1", "8", "10", "4", "0", "1"}, {"5", "b", "1", "3", "0", "4", "0", "1"},
		{"7", "c", "1", "3", "10", "4", "2", "1"}, {"9", "d", "1", "3", "10", "0", "0", "1"}})
	if err != nil || len(cs) != 4 || cs[0].Weight != 8 {
		t.Fatal(cs, err)
	}
	for i, want := range []bool{true, false, false, false} {
		if cs[i].OK() != want {
			t.Errorf("#%d OK=%v", cs[i].ID, cs[i].OK())
		}
	}
	if _, err := ParseChecks([][]string{{"1"}}); err == nil {
		t.Error("short row must fail")
	}
}

// CASE-AW-030: Channels() is scoped by status=1 and model_like only.
// Configured priority, channel name, and group never enter the WHERE clause;
// a test-named newcomer that the database returns is kept at this layer.
func TestChannelsSQLScopeIsStatusAndModelNotPriorityNameOrGroup(t *testing.T) {
	var got string
	p := NewPsql(m.M{"model_like": "gpt-%", "exclude_model_like": "gpt-image-%", "priority": 10})
	if p.Priority != 10 {
		t.Fatalf("priority field %d", p.Priority)
	}
	p.Run = func(_ context.Context, argv []string, stdin string) (string, error) {
		got = stdin
		return "BEGIN\n2|[SWBS]-GPT-0.16|20|10\n23|[BL]-GPT-0.13|20|10\n88|测试-[HL]-GPT-Pro-0.18|5|10\n89|测试-[HL]-GPT-Ultrafast-0.20|5|10\nROLLBACK\n", nil
	}
	cs, err := p.Channels()
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 4 || cs[2].ID != 88 || cs[3].ID != 89 || !strings.HasPrefix(cs[2].Name, "测试") || cs[2].Weight != 5 || cs[2].Priority != 10 {
		t.Fatalf("test-named newcomers must pass through Channels(): %+v", cs)
	}
	if !strings.HasPrefix(got, "BEGIN READ ONLY;\n") || !strings.HasSuffix(got, "ROLLBACK;\n") {
		t.Fatalf("not wrapped: %q", got)
	}
	if !strings.Contains(got, "c.status=1") || !strings.Contains(got, "a.model like 'gpt-%'") || !strings.Contains(got, "a.model not like 'gpt-image-%'") {
		t.Fatalf("missing status/model scope: %q", got)
	}
	where := got
	if i := strings.Index(strings.ToLower(got), " where "); i >= 0 {
		where = got[i:]
	} else {
		t.Fatalf("no WHERE: %q", got)
	}
	low := strings.ToLower(where)
	banned := []string{"c.priority=", "c.priority =", "priority=", "c.name", "测试", "c.group", "group="}
	banned = append(banned, string('"')+"group"+string('"'))
	for _, b := range banned {
		if strings.Contains(low, strings.ToLower(b)) || strings.Contains(where, b) {
			t.Fatalf("Channels WHERE must not filter %q: %q", b, where)
		}
	}
}
