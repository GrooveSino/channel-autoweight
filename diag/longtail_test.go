package diag

import (
	"strings"
	"testing"

	m "autoweight/model"
)

// A thin channel (thinN <= n < minN) with a heavy tail is flagged and tagged small sample;
// below thinN it stays silent.
func TestLongTailThinSample(t *testing.T) {
	var rs []m.Row
	for i := 0; i < 30; i++ {
		ut := 5
		if i >= 25 {
			ut = 90
		}
		rs = append(rs, m.Row{Ch: 23, T: 100, Type: 2, Ut: ut})
	}
	got := LongTailFlags(rs, map[int]int{23: 4}, 50, 50, 20, 3.5, 30)
	if len(got) != 1 || !strings.Contains(got[0], "小样本") || !strings.Contains(got[0], "#23") {
		t.Fatalf("thin long tail not flagged: %v", got)
	}
	if got := LongTailFlags(rs, map[int]int{23: 4}, 50, 50, 40, 3.5, 30); len(got) != 0 {
		t.Fatalf("below thinN must be silent: %v", got)
	}
}
