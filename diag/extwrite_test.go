package diag

import (
	"strings"
	"testing"

	m "autoweight/model"
)

func TestExtWriteFlags(t *testing.T) {
	a := &m.Rec{Ts: "t1", Applied: true}
	a.Decision.Target = map[int]int{2: 12, 5: 4}
	dry := &m.Rec{Ts: "t2"}
	dry.Decision.Target = map[int]int{5: 9}
	got := ExtWriteFlags([]*m.Rec{a, dry}, map[int]int{2: 12, 5: 2, 28: 2}, nil)
	if len(got) != 1 || !strings.Contains(got[0], "#5") || !strings.Contains(got[0], "是 4，现在库里是 2") {
		t.Fatalf("got %v", got)
	}
	if ExtWriteFlags([]*m.Rec{dry}, map[int]int{5: 2}, nil) != nil {
		t.Fatal("no applied round should give nil")
	}
	a.Ts = "2026-11-02T00:13:00+08:00"
	mw := []ManualWrite{{Ts: "2026-11-02T01:12:00+08:00", ID: 5, W: 2, By: "bot", Reason: "tail"}}
	got = ExtWriteFlags([]*m.Rec{a}, map[int]int{5: 2}, mw)
	if len(got) != 1 || !strings.Contains(got[0], "手动改") {
		t.Fatalf("manual attribution: %v", got)
	}
	old := []ManualWrite{{Ts: "2026-11-01T23:00:00+08:00", ID: 5, W: 2}}
	if got = ExtWriteFlags([]*m.Rec{a}, map[int]int{5: 2}, old); strings.Contains(got[0], "手动改") {
		t.Fatalf("manual before last write must not count: %v", got)
	}
}
