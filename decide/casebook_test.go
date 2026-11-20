package decide

import (
	"autoweight/features"
	m "autoweight/model"
	"strings"
	"testing"
)

// CASE-AW-001: sanitized aggregate shape from 2026-11-20 23:16 +08:00.
// No production log rows, request identifiers, model names or real token IDs.
// This locks in existing v5.01 semantics; it does not change the algorithm.
func TestCasebookConcentrationThroughSettle(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		topErrors                           int
		status                              *int
		wantN, wantErr, wantEff, wantClient int
		wantRate                            float64
		wantWeight                          int
	}{
		{"concentrated_35_to_4_hold", 33, nil, 491, 35, 4, 0, .0087, 20},
		{"distributed_faults_cut_two", 18, nil, 491, 35, 35, 0, .0713, 18},
		{"capacity_is_not_discounted", 33, m.I(429), 491, 35, 35, 0, .0713, 18},
		{"client_errors_not_channel_faults", 33, m.I(400), 456, 0, 0, 35, 0, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := make([]m.Row, 0, 491)
			for n := 0; n < 456; n++ {
				rows = append(rows, m.Row{Ch: 26, T: 100, Type: 2, Tok: 900003})
			}
			for n := 0; n < 35; n++ {
				tok := 900002
				if n < tc.topErrors {
					tok = 900001
				}
				rows = append(rows, m.Row{Ch: 26, T: 100, Type: 5, Tok: tok, Sc: tc.status})
			}
			stats := features.ChanStats(m.M{}, rows, 0, 2, .8)[26]
			if stats.N != tc.wantN || stats.Err != tc.wantErr || stats.Eff != tc.wantEff || stats.Client != tc.wantClient || stats.Rate != tc.wantRate {
				t.Fatalf("unexpected channel evidence: %+v", stats)
			}
			// 4/491 is below 3%, but production's adjusted denominator is
			// N-Err+Eff=460, so the actual rounded rate is 4/460=.0087.
			next := map[int]int{26: 0}
			fs := m.Feats{26: {Chan: stats, Long: &m.Window{N: 491, ErrRate: m.F(35.0 / 491)}}}
			cfg := m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}
			Settle(cfg, next, map[int]int{26: 20}, fs, []int{26}, nil, nil)
			if next[26] != tc.wantWeight {
				t.Fatalf("weight=%d want %d", next[26], tc.wantWeight)
			}
		})
	}
}

// CASE-AW-015: sanitized aggregate shape from 2026-11-21 02:21 +08:00 CC #72.
// No production log rows, request identifiers, model names or real token IDs.
// Locks in existing v5.01 settle semantics; it does not change the algorithm.
func TestCasebookTrustCapHeldWithoutFault(t *testing.T) {
	// Rate uses the discounted denominator Eff/(N-Err+Eff)=2/11≈.1818 (reported 18.2%).
	rate := 2.0 / 11.0
	feat := &m.Feat{
		Long: &m.Window{N: 11, ErrRate: m.F(rate)},
		Chan: &m.ChanStat{N: 11, Err: 2, Eff: 2, Rate: rate},
	}
	cap := &Cap{Cap: 3, Reason: "信任 0.28 < 0.3且 1h 样本 15 < 20，恢复期限幅", Kind: "trust"}

	t.Run("cc_cut_needs_fault_holds_20", func(t *testing.T) {
		next := map[int]int{72: 0}
		var notes []string
		cfg := m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}
		Settle(cfg, next, map[int]int{72: 20}, m.Feats{72: feat}, []int{72}, map[int]*Cap{72: cap}, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20", next[72])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"系统上限", "不执行", "18.2"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected held-cap note to contain %q, got %q", part, notes)
			}
		}
	})

	t.Run("gpt_without_flag_applies_trust_cap", func(t *testing.T) {
		next := map[int]int{72: 0}
		Settle(m.M{"settle": m.M{"raise_clean": false}}, next, map[int]int{72: 20}, m.Feats{72: feat}, []int{72}, map[int]*Cap{72: cap}, nil)
		if next[72] != 3 {
			t.Fatalf("weight=%d want 3", next[72])
		}
	})

	t.Run("fault_threshold_allows_system_cap", func(t *testing.T) {
		// Same 3/30 rate shape that meets N≥30, Eff≥3, Rate≥.03.
		fault := &m.Feat{
			Long: &m.Window{N: 30, ErrRate: m.F(0.1)},
			Chan: &m.ChanStat{N: 30, Err: 3, Eff: 3, Rate: 0.1},
		}
		next := map[int]int{72: 0}
		cfg := m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}
		Settle(cfg, next, map[int]int{72: 20}, m.Feats{72: fault}, []int{72}, map[int]*Cap{72: cap}, nil)
		if next[72] != 3 {
			t.Fatalf("weight=%d want 3 once the 30/3/3%% gate is met", next[72])
		}
	})
}
