package decide

import (
	"testing"

	m "autoweight/model"
)

func TestPairSlow(t *testing.T) {
	cfg := m.M{"pair_slow": map[string]any{"enabled": true}}
	ps := func(r float64, tok, n int) *m.PairStat { return &m.PairStat{Ratio: r, Tokens: tok, N: n, Min: 60} }
	fs := m.Feats{
		78: {Pair: ps(1.2, 1, 7), PairRef: ps(1.9, 40, 2400)},   // 1h thin -> 24h slow
		73: {Pair: ps(1.4, 5, 200)},                             // hold: no raise
		26: {Pair: ps(2.5, 2, 300), PairRef: ps(1.0, 80, 9000)}, // 1h thin -> 24h ok
		32: {Pair: ps(1.9, 3, 40)},                              // at floor
		2:  {Pair: ps(1.7, 4, 100)},                             // already cut harder by errors
	}
	nw := map[int]int{78: 20, 73: 20, 26: 20, 32: 10, 2: 14}
	prev := map[int]int{78: 20, 73: 18, 26: 18, 32: 10, 2: 18}
	var notes []string
	PairSlow(cfg, nw, prev, fs, []int{2, 26, 32, 73, 78}, &notes)
	want := map[int]int{78: 18, 73: 18, 26: 20, 32: 10, 2: 14}
	for i, w := range want {
		if nw[i] != w {
			t.Errorf("#%d = %d, want %d (%v)", i, nw[i], w, notes)
		}
	}
	nw2 := map[int]int{78: 20}
	PairSlow(m.M{}, nw2, map[int]int{78: 20}, fs, []int{78}, nil)
	if nw2[78] != 20 {
		t.Error("disabled pair_slow changed weights")
	}
}
