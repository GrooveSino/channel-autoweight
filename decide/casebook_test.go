package decide

import (
	"autoweight/features"
	m "autoweight/model"
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
