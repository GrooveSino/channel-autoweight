package probe

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func hangRun(ctx context.Context, _ []string, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

// v5.00 rule 5: a hung ssh/site probe is cut by a hard per-probe timeout.
func TestRemoteSiteHardTimeout(t *testing.T) {
	cfg := cfgFor("https://reverix.ai")
	cfg.Sub("probe")["remote_site"] = true
	cfg.Sub("probe")["hard_timeout_s"] = 1.0
	c := NewClient(cfg)
	c.Run = hangRun
	t0 := time.Now()
	got := c.Site([]int{26, 23})
	if d := time.Since(t0); d > 4*time.Second {
		t.Fatalf("site probe hung %v", d)
	}
	if got[26][0].OK || got[26][0].Err != "probe_timeout" {
		t.Fatalf("got %+v", got[26])
	}
}

// The whole probing phase (site + recheck + upstream tests) has one budget;
// once spent, the remaining probes fail fast instead of hanging the round.
func TestProbeBudget(t *testing.T) {
	cfg := cfgFor("https://reverix.ai")
	cfg.Sub("probe")["remote_site"] = true
	cfg.Sub("probe")["hard_timeout_s"] = 1.0
	cfg.Sub("probe")["budget_s"] = 1.0
	c := NewClient(cfg)
	calls := 0
	c.Run = func(ctx context.Context, a []string, s string) (string, error) { calls++; return hangRun(ctx, a, s) }
	l := &Live{C: c}
	ps, _, _ := l.Site([]int{26})
	if ps[26][0].OK {
		t.Fatal("hung probe ok")
	}
	if len(l.RecheckedIDs()) != 0 {
		t.Error("recheck ran after budget spent")
	}
	n := calls
	t0 := time.Now()
	up, _ := l.Upstream(21, 3)
	if time.Since(t0) > time.Second || calls != n || len(up) != 3 || up[0].Err != "probe_budget_exceeded" {
		t.Fatalf("upstream after budget: %+v calls %d→%d", up, n, calls)
	}
}

// execRun must not wait for grandchildren that keep stdout open after a kill.
func TestExecRunWaitDelay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	_, err := execRun(ctx, []string{"sh", "-c", "(sleep 20) & sleep 20"}, "")
	if d := time.Since(t0); d > 5*time.Second {
		t.Fatalf("execRun blocked %v", d)
	}
	if err == nil {
		t.Fatal("killed command returned nil error")
	}
}

// The remote script enforces a wall-clock deadline even when the stream keeps
// trickling (requests' timeout is per read, not total).
func TestRemoteSiteScriptTotalDeadline(t *testing.T) {
	harness := `
import sys, types, subprocess, time
sys.argv=["probe","26","gpt-6.1-sol","1","hi","new-api-postgres","newapi","newapi"]
subprocess.run=lambda argv,**kw: types.SimpleNamespace(returncode=0,stdout="BEGIN\nK\nROLLBACK\n")
class Resp:
    status_code=200
    def __enter__(self): return self
    def __exit__(self,*a): pass
    def iter_lines(self,**kw):
        for i in range(100):
            time.sleep(0.2)
            yield 'data: {"type":"response.output_text.delta","delta":"x"}'
class Session:
    def post(self,url,**kw): return Resp()
sys.modules["requests"]=types.SimpleNamespace(Session=Session)
`
	t0 := time.Now()
	out, err := exec.Command("python3", "-c", harness+RemoteSiteScript).CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	if d := time.Since(t0); d > 5*time.Second {
		t.Fatalf("script ran %v", d)
	}
	var got map[string]any
	if err = json.Unmarshal(out, &got); err != nil || got["ok"] != false || !strings.Contains(got["err"].(string), "timeout") {
		t.Fatal("trickle stream not cut", string(out))
	}
}
