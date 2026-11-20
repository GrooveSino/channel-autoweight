package trial

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func line(ch int, big bool, st int, ttft, total float64, deltas, out int) string {
	return fmt.Sprintf(`{"ch":%d,"big":%v,"status":%d,"ttft":%g,"total":%g,"deltas":%d,"out":%d,"err":null}`, ch, big, st, ttft, total, deltas, out)
}

func TestJudge(t *testing.T) {
	var b strings.Builder
	b.WriteString("noise\n")
	for k := 0; k < 50; k++ {
		big := k%10 == 9
		b.WriteString(line(28, big, 200, 1, 5, 30, 200) + "\n") // 50 tok/s
		b.WriteString(line(2, big, 200, 1, 6, 30, 200) + "\n")  // 40 tok/s
	}
	rs := Parse(b.String())
	if len(rs) != 100 {
		t.Fatalf("parsed %d", len(rs))
	}
	c, r := Summarise(rs, 28), Summarise(rs, 2)
	if ok, why := Default.Judge(c, r); !ok {
		t.Fatalf("want pass, got %v", why)
	}
	// site rate-limit 429s are excluded, not counted as channel errors
	rs = append(rs, Parse(`{"ch":28,"status":429,"total":0.1,"deltas":0,"err":"您已达到请求数限制：1分钟内最多请求30次"}`)...)
	if s := Summarise(rs, 28); s.Throttled != 1 || s.Err != 0 {
		t.Fatalf("throttled not excluded: %+v", s)
	}
	// one empty reply fails it (2% > 1% and empty)
	rs[1] = Parse(line(28, false, 200, 0, 20, 0, 0))[0]
	if ok, why := Default.Judge(Summarise(rs, 28), r); ok || len(why) < 2 {
		t.Fatalf("want fail with empty+err, got %v %v", ok, why)
	}
}

func TestActivePasses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "trial.jsonl")
	os.WriteFile(p, []byte(`{"ch":28,"pass":true,"ts":"2026-11-01T18:29:53+08:00"}
{"ch":29,"pass":true,"ts":"2026-11-01T18:00:00+08:00"}
{"ch":29,"pass":false,"ts":"2026-11-01T19:00:00+08:00"}
{"ch":30,"pass":true,"ts":"2026-10-28T10:00:00+08:00"}
`), 0o644)
	now, _ := time.Parse(time.RFC3339, "2026-11-01T20:30:00+08:00")
	a := ActivePasses(p, now, 48)
	if len(a) != 1 || a[28] == 0 {
		t.Fatalf("want only 28 active, got %v", a)
	}
}
