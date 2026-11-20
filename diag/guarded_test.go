package diag

import (
	"testing"

	m "autoweight/model"
)

func TestGuardedUnsupportedNoFastDrop(t *testing.T) {
	now := int64(100000)
	rows := []m.Row{}
	for i := 0; i < 150; i++ {
		rows = append(rows, m.Row{T: now - int64(i*20), Ch: 26, Type: 2, Ut: 10})
	}
	for i := 0; i < 400; i++ { // #2 carries most traffic so #26 is not a main by share
		rows = append(rows, m.Row{T: now - int64(i*5), Ch: 2, Type: 2, Ut: 7})
	}
	unsup := append(append([]m.Row(nil), rows...), m.Row{T: now - 60, Ch: 26, Type: 5, Uf: 1})
	if p, _ := GuardedProposal(unsup, map[int]int{26: 3}, map[int]int{26: 8}, now, DefaultGuard); p[26] != 6 {
		t.Fatalf("unsupported-feature 400 should step, not fast drop: got %v", p)
	}
	real := append(append([]m.Row(nil), rows...), m.Row{T: now - 60, Ch: 26, Type: 5})
	if p, _ := GuardedProposal(real, map[int]int{26: 3}, map[int]int{26: 8}, now, DefaultGuard); p[26] != 3 {
		t.Fatalf("ordinary error should fast drop: got %v", p)
	}
}

func TestGuardedTpsHold(t *testing.T) {
	now := int64(100000)
	rows := []m.Row{}
	for i := 0; i < 60; i++ {
		rows = append(rows, m.Row{T: now - int64(i*30), Ch: 26, Type: 2, Ut: 10})
	}
	g := DefaultGuard
	g.TpsIdx = map[int]float64{26: 0.35}
	if p, why := GuardedProposal(rows, map[int]int{26: 10}, map[int]int{26: 4}, now, g); p[26] != 4 {
		t.Fatalf("slow-TPS channel should hold: got %v %v", p, why)
	}
	g.TpsIdx = map[int]float64{26: 0.9}
	if p, _ := GuardedProposal(rows, map[int]int{26: 10}, map[int]int{26: 4}, now, g); p[26] != 6 {
		t.Fatalf("normal-TPS channel should step up: got %v", p)
	}
}
