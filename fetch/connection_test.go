package fetch

import (
	"autoweight/apply"
	m "autoweight/model"
	"context"
	"strings"
	"testing"
)

func TestMigrationConnection(t *testing.T) {
	cfg := m.M{"ssh_host": "reverixai-personal-site", "pg_container": "new-api-postgres", "pg_user": "newapi", "pg_database": "newapi"}
	p := NewPsql(cfg)
	a := p.Argv()
	s := strings.Join(a, " ")
	for _, want := range []string{"reverixai-personal-site", "POSTGRES_USER=newapi", "POSTGRES_DB=newapi"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s: %s", want, s)
		}
	}
	if strings.Contains(s, "dmit-grooveapi") {
		t.Fatal("old host")
	}
	w := apply.PsqlWriter{Argv: a, Run: func(_ context.Context, args []string, _ string) (string, error) {
		if strings.Join(args, " ") != s {
			t.Fatal("write connection differs")
		}
		return "", nil
	}}
	t.Setenv("AUTOWEIGHT_ALLOW_WRITE", "1")
	if err := w.ExecTx("BEGIN;\nCOMMIT;\n"); err != nil {
		t.Fatal(err)
	}
	cfg["ssh_host"] = ""
	if !strings.Contains(strings.Join(NewPsql(cfg).Argv(), " "), "POSTGRES_USER=newapi") {
		t.Error("local explicit params missing")
	}
	for _, field := range []string{"pg_user", "pg_database", "pg_container"} {
		bad := m.M{"pg_container": "new-api-postgres", field: "bad;touch /tmp/no"}
		if len(NewPsql(bad).Argv()) != 0 {
			t.Errorf("accepted unsafe %s", field)
		}
	}
	old := strings.Join(NewPsql(m.M{"ssh_host": "prod"}).Argv(), " ")
	if strings.Contains(old, " -e ") || !strings.Contains(old, "POSTGRES_USER") {
		t.Error("legacy changed", old)
	}
}
