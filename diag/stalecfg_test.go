package diag

import (
	"strings"
	"testing"
)

func TestStaleCfgAndFloor(t *testing.T) {
	cfg := map[string]any{"capacity": map[string]any{"enabled": true, "21": map[string]any{}, "2": map[string]any{}}}
	f := StaleCfgFlags(cfg, []int{2, 5}, []string{"capacity", "missing"})
	if len(f) != 1 || !strings.Contains(f[0], "[21]") {
		t.Fatalf("got %v", f)
	}
	if FloorFlags(5, 4, 3) != nil || len(FloorFlags(4, 4, 3)) != 1 {
		t.Fatal("floor")
	}
}
