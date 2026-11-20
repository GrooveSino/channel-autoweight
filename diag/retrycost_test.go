package diag

import (
	"strings"
	"testing"

	m "autoweight/model"
)

func TestRetryCostFlags(t *testing.T) {
	rows := []m.Row{
		{Ch: 5, T: 100, Type: 5, Ut: 30, Tok: 7, Mdl: "g"},
		{Ch: 2, T: 140, Type: 2, Ut: 4, Tok: 7, Mdl: "g"},
		{Ch: 5, T: 200, Type: 5, Ut: 20, Tok: 7, Mdl: "g"},
		{Ch: 23, T: 210, Type: 2, Ut: 4, Tok: 7, Mdl: "g"},
		{Ch: 5, T: 300, Type: 5, Ut: 9, Tok: 8, Mdl: "g"},  // no rescue
		{Ch: 20, T: 400, Type: 5, Ut: 9, Tok: 7, Mdl: "g"}, // single rescue, below minN
		{Ch: 2, T: 900, Type: 2, Ut: 4, Tok: 7, Mdl: "g"},
		{Ch: 20, T: 880, Type: 5, Ut: 9, Tok: 7, Mdl: "h"}, // other model
	}
	f := RetryCostFlags(rows, map[int]int{5: 4}, 0, 60, 2)
	if len(f) != 1 || !strings.Contains(f[0], "#5") || !strings.Contains(f[0], "3 条，其中 2 条") || !strings.Contains(f[0], "#2×1、#23×1") || !strings.Contains(f[0], "p50 25s") {
		t.Fatalf("got %v", f)
	}
}
