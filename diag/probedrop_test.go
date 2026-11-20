package diag

import (
	"strings"
	"testing"

	m "autoweight/model"
)

func TestProbeDropFlags(t *testing.T) {
	rows := []m.Row{
		{Ch: 5, T: 100, Type: 2}, {Ch: 5, T: 100, Type: 2},
		{Ch: 26, T: 100, Type: 5},
		{Ch: 23, T: 10, Type: 5}, // before window, ignored
	}
	for i := 0; i < 40; i++ {
		rows = append(rows, m.Row{Ch: 2, T: 100, Type: 2})
	}
	target := map[int]int{2: 6, 5: 3, 23: 3, 26: 0, 28: 2}
	old := map[int]int{2: 12, 5: 6, 23: 10, 26: 6, 28: 2}
	got := ProbeDropFlags(rows, target, old, 50, 30)
	j := strings.Join(got, "\n")
	if len(got) != 2 || !strings.Contains(j, "#5: 系统 6→3") || !strings.Contains(j, "#23: 系统 10→3") {
		t.Fatalf("got %v", got)
	}
}
