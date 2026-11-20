package probe

import (
	m "autoweight/model"
	"strings"
	"testing"
)

func TestMigrationRemoteConnection(t *testing.T) {
	cfg := cfgFor("x")
	cfg["pg_user"] = "newapi"
	cfg["pg_database"] = "newapi"
	cfg["pg_container"] = "new-api-postgres"
	a := UpstreamArgv(cfg, 26, "gpt-x", 90)
	if !strings.HasSuffix(strings.Join(a, " "), "new-api-postgres newapi newapi") {
		t.Error("missing remote pg args", a)
	}
	if !strings.Contains(Remote, "POSTGRES_USER=") || !strings.Contains(Remote, "sys.argv[7]") {
		t.Error("remote docker override missing")
	}
	for _, field := range []string{"pg_user", "pg_database", "pg_container"} {
		bad := m.M{"pg_container": "new-api-postgres", field: "bad;touch /tmp/no"}
		if len(UpstreamArgv(bad, 26, "x", 90)) != 0 {
			t.Errorf("accepted unsafe %s", field)
		}
	}
}
