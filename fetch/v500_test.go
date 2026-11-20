package fetch

import (
	"strings"
	"testing"

	m "autoweight/model"
)

// v5.00: log content classes behind the client/real-fault split.
func TestLogsSQLV500Classes(t *testing.T) {
	s := NewPsql(m.M{}).LogsSQL([]int{67}, 60)
	for _, h := range []string{"adaptive thinking", "Client connection closed", "signature", "Unsupported parameter",
		"returned HTTP 404", "model_not_found", "timed out", "Concurrency limit exceeded"} {
		if !strings.Contains(s, h) {
			t.Errorf("LogsSQL missing %q", h)
		}
	}
	if writeRe.MatchString(s) {
		t.Fatal("LogsSQL trips the read-only guard")
	}
}

func TestParseLogRowV500(t *testing.T) {
	base := []string{"67", "9", "5", "0", "0", "1438", "-1", "-1", "-1", "-1", "0", "0", "claude-opus-5-5", "1", "0", "0", "0", "0", "0", "0", "0"}
	row := func(cl, nf, to, cp string) []string { return append(append([]string{}, base...), cl, nf, to, cp) }
	if got := js(ParseLogRow(row("1", "0", "0", "0"))); !strings.Contains(got, `"cl":1`) {
		t.Errorf("client flag: %s", got)
	}
	if got := js(ParseLogRow(row("0", "1", "0", "0"))); !strings.Contains(got, `"nf":1`) {
		t.Errorf("404 flag: %s", got)
	}
	if got := js(ParseLogRow(row("0", "0", "1", "0"))); !strings.Contains(got, `"to":1`) {
		t.Errorf("timeout flag: %s", got)
	}
	if got := js(ParseLogRow(row("0", "0", "0", "1"))); !strings.Contains(got, `"cap":1`) {
		t.Errorf("capacity flag: %s", got)
	}
	if got := js(ParseLogRow(base)); strings.Contains(got, `"cl"`) || strings.Contains(got, `"to"`) {
		t.Errorf("old-width row grew flags: %s", got)
	}
}
