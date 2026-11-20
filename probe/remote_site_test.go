package probe

import (
	m "autoweight/model"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestRemoteSiteMigration(t *testing.T) {
	cfg := cfgFor("https://reverix.ai")
	cfg["ssh_host"] = "reverixai-personal-site"
	cfg["pg_user"] = "newapi"
	cfg["pg_database"] = "newapi"
	cfg.Sub("probe")["remote_site"] = true
	c := NewClient(cfg)
	calls := 0
	c.Run = func(_ context.Context, a []string, stdin string) (string, error) {
		calls++
		if strings.Contains(strings.Join(a, " "), "cat ") {
			return "", errors.New("remote site not implemented")
		}
		if a[0] != "ssh" || !strings.Contains(strings.Join(a, " "), "reverixai-personal-site") || strings.Contains(strings.Join(a, " "), "cat ") {
			t.Error("not new-host remote request", a)
		}
		if !strings.Contains(stdin, "id=1318") || strings.Contains(stdin, "id=1305") {
			t.Error("wrong token")
		}
		if !strings.Contains(stdin, "allow_redirects=False") || strings.Contains(stdin, "resp.text") {
			t.Error("unsafe request/output")
		}
		return `{"ok":true,"status":200,"ttft":1,"total":2,"deltas":3,"span":1,"route":"site"}`, nil
	}
	got := c.Site([]int{26})
	if calls != 1 || len(got[26]) != 1 || !got[26][0].OK || c.key != "" {
		t.Errorf("not keyless remote site %+v calls=%d", got, calls)
	}
	c.Run = func(context.Context, []string, string) (string, error) { return "", errors.New("SSH unavailable") }
	got = c.Site([]int{26})
	if got[26][0].OK {
		t.Error("SSH failure became success")
	}
	cfg.Sub("probe")["base_url"] = "https://evil.invalid"
	c.Run = func(context.Context, []string, string) (string, error) {
		t.Error("untrusted endpoint executed")
		return "", nil
	}
	if c.Site([]int{26})[26][0].OK {
		t.Error("untrusted endpoint accepted")
	}
}
func TestRemoteSiteScriptSafety(t *testing.T) {
	// RemoteSiteScript executes only whitelisted metadata; no key is returned.
	s := RemoteSiteScript
	for _, want := range []string{"BEGIN READ ONLY", "id=1318", "https://reverix.ai/v1/responses", "allow_redirects=False", "max_output_tokens", "POSTGRES_USER="} {
		if !strings.Contains(s, want) {
			t.Error("missing", want)
		}
	}
	for _, bad := range []string{"print(key", "resp.text", "str(e)", "id=1305"} {
		if strings.Contains(s, bad) {
			t.Error("unsafe", bad)
		}
	}
	_ = m.M{}
}

func TestRemoteSitePythonWhitelist(t *testing.T) {
	harness := `
import sys, types, subprocess
sys.argv=["probe","26","gpt-6.1-sol","90","hi","new-api-postgres","newapi","newapi"]
secret="SENTINEL_PRIVATE_KEY"
def db_run(argv,**kw):
    assert "POSTGRES_USER=newapi" in argv and "POSTGRES_DB=newapi" in argv
    assert "id=1318" in kw["input"] and "id=1305" not in kw["input"]
    assert kw["input"].startswith("BEGIN READ ONLY;")
    return types.SimpleNamespace(returncode=0,stdout="BEGIN\n"+secret+"\nROLLBACK\n")
subprocess.run=db_run
class Resp:
    status_code=200
    def __enter__(self): return self
    def __exit__(self,*a): pass
    def iter_lines(self,**kw):
        yield 'data: {"type":"response.output_text.delta","delta":"'+secret+'"}'
        yield 'data: {"type":"response.completed"}'
class Session:
    def post(self,url,**kw):
        assert url=="https://reverix.ai/v1/responses"
        assert kw["allow_redirects"] is False
        assert kw["headers"]["Authorization"]=="Bearer "+secret+"-26"
        return Resp()
sys.modules["requests"]=types.SimpleNamespace(Session=Session)
`
	out, err := exec.Command("python3", "-c", harness+RemoteSiteScript).CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	if strings.Contains(string(out), "SENTINEL_PRIVATE_KEY") {
		t.Fatal("secret leaked")
	}
	var got map[string]any
	if err = json.Unmarshal(out, &got); err != nil {
		t.Fatal(err, string(out))
	}
	if got["ok"] != true {
		t.Fatal(string(out))
	}
	allowed := map[string]bool{"ok": true, "status": true, "ttft": true, "total": true, "deltas": true, "span": true, "err": true, "route": true}
	for k := range got {
		if !allowed[k] {
			t.Fatal("non-whitelist output", k)
		}
	}
	failed := strings.Replace(harness, "return Resp()", "raise RuntimeError(secret)", 1)
	out, err = exec.Command("python3", "-c", failed+RemoteSiteScript).CombinedOutput()
	if err != nil || strings.Contains(string(out), "SENTINEL_PRIVATE_KEY") {
		t.Fatal("network failure leak", err, string(out))
	}
	got = nil
	if err = json.Unmarshal(out, &got); err != nil || got["ok"] != false || got["err"] != "network_error" {
		t.Fatal("network failure not safely degraded", string(out))
	}
}
