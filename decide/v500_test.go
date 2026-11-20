package decide

import (
	"testing"
	"time"

	m "autoweight/model"
)

// v5.00 rule 2 (Claude, settle.cut_needs_fault): no cut and no non-operator cap
// without real >= 30 and >= 3 discounted errors at >= 3%; operator caps still win.
func TestSettleCutNeedsFault(t *testing.T) {
	cs := func(n, err, eff int) *m.Feat {
		r := 0.0
		if d := n - err + eff; d > 0 {
			r = float64(eff) / float64(d)
		}
		return &m.Feat{Long: &m.Window{N: n, ErrRate: m.F(r)}, Chan: &m.ChanStat{N: n, Err: err, Eff: eff, Rate: r}}
	}
	cfg := m.M{"settle": m.M{"cut_needs_fault": true}}
	ids := []int{72, 172, 68, 67, 18, 6, 9}
	prev := map[int]int{72: 20, 172: 20, 68: 20, 67: 20, 18: 2, 6: 20, 9: 20}
	nw := map[int]int{72: 3, 172: 18, 68: 0, 67: 0, 18: 20, 6: 3, 9: 12}
	fs := m.Feats{
		72:  cs(5, 1, 1),    // trust cap 3 on 5 real requests -> hold 20
		172: cs(56, 1, 1),   // one real timeout -> hold 20
		68:  cs(25, 10, 10), // "extreme" but real < 30 -> hold
		67:  cs(40, 12, 12), // real extreme -> w_min
		18:  cs(200, 0, 0),  // operator soft cap 2 wins
		6:   cs(100, 4, 4),  // real fault, trust cap 3 -> cap allowed, but step 2? cap wins
		9:   cs(100, 6, 6),  // real fault -> -2 only
	}
	caps := map[int]*Cap{72: {Cap: 3, Reason: "信任", Kind: "trust"}, 18: {Cap: 2, Reason: "余额", Kind: "soft"},
		6: {Cap: 3, Reason: "信任", Kind: "trust"}}
	Settle(cfg, nw, prev, fs, ids, caps, &[]string{})
	want := map[int]int{72: 20, 172: 20, 68: 20, 67: 0, 18: 2, 6: 3, 9: 18}
	for id, w := range want {
		if nw[id] != w {
			t.Errorf("#%d got %d want %d", id, nw[id], w)
		}
	}
	// without the flag (GPT) a trust cap still applies as before
	nw2 := map[int]int{72: 3}
	Settle(m.M{}, nw2, map[int]int{72: 20}, m.Feats{72: cs(5, 1, 1)}, []int{72}, map[int]*Cap{72: caps[72]}, nil)
	if nw2[72] != 3 {
		t.Errorf("GPT trust cap changed: %d", nw2[72])
	}
}

// soft_caps entries are tagged as operator caps.
func TestActiveCapsKind(t *testing.T) {
	c, err := ActiveCaps(m.M{"soft_caps": m.M{"18": m.M{"cap": 2.0}}}, time.Now())
	if err != nil || c[18] == nil || c[18].Kind != "soft" {
		t.Fatalf("%+v %v", c[18], err)
	}
}

// v5.00 settle.raise_clean: a channel below its cap with real >= 30 and
// discounted error rate < 2% climbs +step even when the strategy held or cut
// it (the operator's "clean, give it back" step); small samples still don't.
func TestSettleRaiseClean(t *testing.T) {
	cs := func(n, eff int) *m.Feat {
		return &m.Feat{Long: &m.Window{N: n, ErrRate: m.F(0)}, Chan: &m.ChanStat{N: n, Err: eff, Eff: eff, Rate: float64(eff) / float64(n)}}
	}
	cfg := m.M{"settle": m.M{"raise_clean": true}}
	prev := map[int]int{73: 18, 72: 18, 67: 16, 5: 10, 9: 19}
	nw := map[int]int{73: 0, 72: 16, 67: 0, 5: 20, 9: 0}
	fs := m.Feats{73: cs(45, 0), 72: cs(60, 1), 67: cs(14, 0), 5: cs(200, 0), 9: cs(100, 0)}
	caps := map[int]*Cap{5: {Cap: 10, Reason: "op", Kind: "soft"}}
	Settle(cfg, nw, prev, fs, []int{73, 72, 67, 5, 9}, caps, nil)
	want := map[int]int{73: 20, 72: 20, 67: 16, 5: 10, 9: 20}
	for id, w := range want {
		if nw[id] != w {
			t.Errorf("#%d got %d want %d", id, nw[id], w)
		}
	}
}
