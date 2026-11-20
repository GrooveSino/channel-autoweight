package fetch

import (
	"strings"
	"testing"

	m "autoweight/model"
)

// v5.01: [ReverixAI] formats are matched; old formats still are.
func TestLogsSQLV501Formats(t *testing.T) {
	s := NewPsql(m.M{}).LogsSQL([]int{78}, 60)
	for _, h := range []string{"do request failed", "Model request could not be transmitted",
		"content not like '%status_code=%'", "Encrypted context cannot be read", "Model service temporarily limited",
		"Invalid request parameter", "Invalid ''input"} {
		if !strings.Contains(s, h) {
			t.Errorf("LogsSQL missing %q", h)
		}
	}
	if writeRe.MatchString(s) {
		t.Fatal("LogsSQL trips the read-only guard")
	}
}
