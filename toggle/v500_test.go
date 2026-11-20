package toggle

import (
	"strings"
	"testing"

	m "autoweight/model"
)

// v5.00 rule 4: being last on latency for many rounds is not a disable reason
// when the channel has (almost) no real errors; it becomes a diagnostic note.
func TestRankDisableNeedsRealFault(t *testing.T) {
	ids := []int{2, 23, 26, 32, 73, 78}
	mk := func(ch *m.ChanStat) []*Formal {
		var fm []*Formal
		for k := 0; k < 6; k++ {
			sc := m.NewOrderedScores()
			n := map[int]*int{}
			for j, i := range ids {
				v := 1.0 - float64(j)*0.1 // #78 always last
				sc.Set(i, m.F(v))
				n[i] = m.I(200)
			}
			fs := m.Feats{}
			for _, i := range ids {
				fs[i] = &m.Feat{Long: &m.Window{N: 200}, Chan: &m.ChanStat{N: 200}}
			}
			fs[78].Chan = ch
			fm = append(fm, &Formal{TsEpoch: float64(k * 3600), Scores: sc, N60: n, Old: map[int]int{}, Probes: m.Probes{}, Feats: fs})
		}
		return fm
	}
	cfg := m.M{"toggle": m.M{"enabled": true}}
	p := MakePlan(cfg, ids, nil, nil, mk(&m.ChanStat{N: 157}), nil)
	e := p.Enabled[78]
	if e.LastStreak < 5 || e.Disable || len(p.Actions) != 0 {
		t.Fatalf("clean slow channel disabled: %+v actions=%v", e, p.Actions)
	}
	if !strings.Contains(strings.Join(e.Why, ";"), "只记诊断") {
		t.Errorf("missing diagnostic note: %v", e.Why)
	}
	p = MakePlan(cfg, ids, nil, nil, mk(&m.ChanStat{N: 157, Err: 12, Eff: 12, Rate: 0.0764}), nil)
	if !p.Enabled[78].Disable {
		t.Fatalf("last + real faults not disabled: %+v", p.Enabled[78])
	}
	// opt-out keeps the old rank-only behaviour
	cfg = m.M{"toggle": m.M{"enabled": true, "rank_needs_fault": false}}
	if p = MakePlan(cfg, ids, nil, nil, mk(&m.ChanStat{N: 157}), nil); !p.Enabled[78].Disable {
		t.Fatal("rank_needs_fault=false lost old behaviour")
	}
}
