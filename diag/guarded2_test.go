package diag

import (
	"testing"

	m "autoweight/model"
)

func TestGuardedProposal(t *testing.T) {
	now := int64(10000)
	rows := []m.Row{}
	for i := 0; i < 250; i++ {
		rows = append(rows, m.Row{T: now - 100, Ch: 2, Type: 2})
	}
	for i := 0; i < 40; i++ {
		rows = append(rows, m.Row{T: now - 100, Ch: 22, Type: 2})
	}
	rows = append(rows, m.Row{T: now - 100, Ch: 20, Type: 2}, m.Row{T: now - 60, Ch: 23, Type: 5})
	for i := 0; i < 40; i++ {
		rows = append(rows, m.Row{T: now - 100, Ch: 23, Type: 2})
	}
	p, _ := GuardedProposal(rows, map[int]int{2: 0, 20: 12, 22: 0, 23: 0, 26: 0}, map[int]int{2: 10, 20: 3, 22: 4, 23: 6, 26: 1}, now, DefaultGuard)
	want := map[int]int{2: 10, 20: 5, 22: 2, 23: 0, 26: 2}
	for id, w := range want {
		if p[id] != w {
			t.Errorf("#%d got %d want %d", id, p[id], w)
		}
	}
}

func TestGuardedQuietMainByShare(t *testing.T) {
	now := int64(10000)
	rows := []m.Row{}
	add := func(ch, k int) {
		for i := 0; i < k; i++ {
			rows = append(rows, m.Row{T: now - 100, Ch: ch, Type: 2})
		}
	}
	add(2, 140)
	add(20, 60)
	add(23, 50)
	p, why := GuardedProposal(rows, map[int]int{2: 0, 20: 0, 23: 12}, map[int]int{2: 10, 20: 4, 23: 6}, now, DefaultGuard)
	if p[2] != 10 || p[20] != 2 || p[23] != 8 {
		t.Errorf("got %v why %v", p, why)
	}
}

func TestGuardedProposalNoRealHolds(t *testing.T) {
	old := map[int]int{2: 10, 22: 3, 26: 1}
	p, why := GuardedProposal(nil, map[int]int{2: 10, 22: 12, 26: 3}, old, 10000, DefaultGuard)
	for id, w := range old {
		if p[id] != w {
			t.Errorf("#%d got %d want %d (why %v)", id, p[id], w, why)
		}
	}
}

func TestGuardedMainHeavyTailStepsDown(t *testing.T) {
	now := int64(10000)
	rows := []m.Row{}
	for i := 0; i < 250; i++ {
		ut := 10
		if i%10 >= 8 {
			ut = 70
		}
		rows = append(rows, m.Row{T: now - 100, Ch: 23, Type: 2, Ut: ut}, m.Row{T: now - 100, Ch: 2, Type: 2, Ut: 8})
	}
	p, why := GuardedProposal(rows, map[int]int{2: 0, 23: 0}, map[int]int{2: 10, 23: 10}, now, DefaultGuard)
	if p[2] != 10 || p[23] != 8 {
		t.Errorf("got %v why %v", p, why)
	}
}

func TestGuardedProposalTrialHold(t *testing.T) {
	now := int64(100000)
	rows := []m.Row{}
	for i := 0; i < 37; i++ {
		rows = append(rows, m.Row{T: now - int64(i*60), Ch: 28, Type: 2, Ut: 8})
	}
	g := DefaultGuard
	g.Trial = map[int]bool{28: true}
	p, why := GuardedProposal(rows, map[int]int{28: 0}, map[int]int{28: 2}, now, g)
	if p[28] != 2 || why[28] == "" {
		t.Fatalf("trial channel should hold 2, got %v %v", p, why)
	}
	rows = append(rows, m.Row{T: now - 30, Ch: 28, Type: 5})
	if p, _ := GuardedProposal(rows, map[int]int{28: 0}, map[int]int{28: 2}, now, g); p[28] != 0 {
		t.Fatalf("errors must bypass trial hold, got %v", p)
	}
}

func TestGuardedLullHoldsEstablished(t *testing.T) {
	now := int64(10000)
	rows := []m.Row{{Ch: 2, T: now - 100, Type: 2, Ut: 3}, {Ch: 9, T: now - 100, Type: 2, Ut: 3}}
	g := DefaultGuard
	g.Est = map[int]bool{2: true}
	p, _ := GuardedProposal(rows, map[int]int{2: 12, 9: 12}, map[int]int{2: 12, 9: 2}, now, g)
	if p[2] != 12 {
		t.Fatalf("established lull channel must hold 12, got %d", p[2])
	}
	if p[9] != 4 {
		t.Fatalf("newcomer still ramps by step under cap, got %d", p[9])
	}
}

func TestGuardedLullIgnoresUnsupported(t *testing.T) {
	now := int64(10000)
	rows := []m.Row{{T: now - 100, Ch: 2, Type: 5, Uf: 1}, {T: now - 100, Ch: 5, Type: 5}}
	for i := 0; i < 10; i++ {
		rows = append(rows, m.Row{T: now - 100, Ch: 2, Type: 2}, m.Row{T: now - 100, Ch: 5, Type: 2})
	}
	g := DefaultGuard
	g.Est = map[int]bool{2: true, 5: true}
	p, why := GuardedProposal(rows, map[int]int{2: 3, 5: 3}, map[int]int{2: 12, 5: 6}, now, g)
	if p[2] != 12 || p[5] != 3 {
		t.Errorf("got %v why %v", p, why)
	}
}
