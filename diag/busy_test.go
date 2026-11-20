package diag

import (
	"strings"
	"testing"

	m "autoweight/model"
)

func TestBusyFlags(t *testing.T) {
	rows := []m.Row{
		{Ch: 26, T: 100, Type: 5, Tok: 7, Mdl: "a", Ut: 50, Bz: 1},
		{Ch: 26, T: 3000, Type: 5, Tok: 7, Mdl: "a", Ut: 52, Bz: 1},
		{Ch: 5, T: 3050, Type: 2, Tok: 7, Mdl: "a"},
		{Ch: 23, T: 3100, Type: 5, Tok: 9}, // plain error, no flag
	}
	got := BusyFlags(rows, map[int]int{26: 0}, 50, 3600, 1)
	if len(got) != 1 || !strings.Contains(got[0], "BUSY #26: 1h 真实错误中 2 条") || !strings.Contains(got[0], "近 30 分钟 1 条") || !strings.Contains(got[0], "平均先等 51s") || !strings.Contains(got[0], "其中 1 条 120s") {
		t.Fatalf("got %v", got)
	}
}
