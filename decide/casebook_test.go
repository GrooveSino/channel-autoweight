package decide

import (
	"autoweight/features"
	m "autoweight/model"
	"autoweight/trust"
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

// CASE-AW-016: sanitized relative-score shape from 2026-11-21 03:06 +08:00 GPT #23/#32.
// No production log rows, request identifiers, model names or real token IDs.
// Locks current v5.01 rank-1 eligibility and settle raise; it does not change the algorithm.
func TestCasebookRank1PromotionRecover(t *testing.T) {
	cfg := m.M{
		"windows_min": []any{60.0, 10.0},
		"strategy":    "rank",
		"min_n_rank1": 10.0,
		"w_min":       0.0,
		"w_max":       20.0,
		"score":       m.M{"a_p50": 1.0, "b_p90": 0.0, "c_ttft": 0.0, "t_ref": 20.0, "min_n_window": 5.0, "empty_weight": 0.0, "w_10m": 0.0, "ttft_missing_s": 30.0},
		"strategies":  m.M{"rank": m.M{"top_w": 20.0, "close_ratio": 0.85, "close_factor": 0.8, "mid_ratio": 0.7, "mid_w": 3.0, "low_w": 0.0, "hysteresis": 0.0, "band": 0.0, "ramp": 0.0, "lead_min_n": 0.0}},
		"settle":      m.M{"cut_needs_fault": true, "raise_clean": false},
	}
	feat := func(n60, chanN int, p50 float64) *m.Feat {
		return &m.Feat{
			Long: &m.Window{N: n60, P50: m.F(p50), P90: m.F(p50), ErrRate: m.F(0)},
			Chan: &m.ChanStat{N: chanN, Err: 0, Eff: 0, Rate: 0},
		}
	}
	// p50 10 vs 14 keeps the thinner channel ahead on score, with the richer
	// channel inside the close band (ratio ≈ 0.88) when the thinner one leads.
	// A third, slower channel is required: after promoting a lower-scoring
	// leader, StratRank uses that new leader as "top". A still-higher ineligible
	// score then sits above tie_ratio*top, and with only two channels the
	// all-tie split (w_total/n) would fire. Production this round had five
	// channels and a below-threshold score, so top_w was assigned normally.
	run := func(n60Thin int, chanNThin int, extra bool) *Decision {
		t.Helper()
		chans := []m.Channel{{ID: 32}, {ID: 23}}
		fs := m.Feats{32: feat(n60Thin, chanNThin, 10), 23: feat(303, 297, 14)}
		prev := map[int]int{32: 20, 23: 18}
		if extra {
			chans = append(chans, m.Channel{ID: 26})
			fs[26] = feat(100, 100, 22)
			prev[26] = 20
		}
		d, err := Decide(Input{
			Cfg:      cfg,
			Chans:    chans,
			Feats:    fs,
			Strategy: "rank",
			Prev:     prev,
		})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		return d
	}

	t.Run("thin_leader_is_replaced_and_second_raises", func(t *testing.T) {
		d := run(7, 3, true)
		if d.Scores[32].EligibleRank1 {
			t.Fatalf("n60=7 should be ineligible for rank 1")
		}
		if !d.Scores[23].EligibleRank1 {
			t.Fatalf("#23 should be eligible for rank 1")
		}
		if d.Order[0] != 23 {
			t.Fatalf("order=%v want #23 first after promotion", d.Order)
		}
		if d.Target[23] != 20 {
			t.Fatalf("#23 weight=%d want 20 (strategy raise accepted); notes=%q", d.Target[23], d.Notes)
		}
		if d.Target[32] != 20 || d.Target[26] != 20 {
			t.Fatalf("weights=%v want #32 and #26 held at 20", d.Target)
		}
		joined := strings.Join(d.Notes, ";")
		if !strings.Contains(joined, "#32 样本不足，不得排第一，由 #23 顶上") {
			t.Fatalf("missing promotion note, got %q", d.Notes)
		}
	})

	t.Run("sufficient_leader_stays_first_and_close_is_held", func(t *testing.T) {
		d := run(22, 22, true)
		if !d.Scores[32].EligibleRank1 {
			t.Fatalf("n60=22 should be eligible for rank 1")
		}
		if d.Order[0] != 32 {
			t.Fatalf("order=%v want #32 first", d.Order)
		}
		if d.Target[32] != 20 || d.Target[23] != 18 || d.Target[26] != 20 {
			t.Fatalf("weights=%v want #32=20 #23=18 #26=20", d.Target)
		}
		joined := strings.Join(d.Notes, ";")
		if strings.Contains(joined, "不得排第一") {
			t.Fatalf("did not expect promotion note, got %q", d.Notes)
		}
	})

	t.Run("two_channel_all_tie_after_promotion_holds", func(t *testing.T) {
		// Current behavior: only two channels, ineligible leader still outscores
		// the promoted one, all-tie split assigns 9, settle holds 18/20.
		d := run(7, 3, false)
		if d.Order[0] != 23 {
			t.Fatalf("order=%v want #23 first after promotion", d.Order)
		}
		if d.Target[23] != 18 || d.Target[32] != 20 {
			t.Fatalf("weights=%v want #23 held at 18 and #32 held at 20 after all-tie split", d.Target)
		}
		joined := strings.Join(d.Notes, ";")
		if !strings.Contains(joined, "#32 样本不足，不得排第一，由 #23 顶上") {
			t.Fatalf("missing promotion note, got %q", d.Notes)
		}
		if !strings.Contains(joined, "策略 9→18") {
			t.Fatalf("expected all-tie strategy 9 held to 18, got %q", d.Notes)
		}
	})

	t.Run("strategy_raise_refused_when_chanstat_below_min_real", func(t *testing.T) {
		next := map[int]int{23: 20}
		feat := &m.Feat{Long: &m.Window{N: 7, ErrRate: m.F(0)}, Chan: &m.ChanStat{N: 7, Err: 0, Eff: 0, Rate: 0}}
		var notes []string
		Settle(cfg, next, map[int]int{23: 18}, m.Feats{23: feat}, []int{23}, nil, &notes)
		if next[23] != 18 {
			t.Fatalf("weight=%d want 18 (N<30 must not take a strategy raise)", next[23])
		}
		joined := strings.Join(notes, ";")
		if !strings.Contains(joined, "不加") {
			t.Fatalf("expected refused-raise note, got %q", notes)
		}
	})
}

// CASE-AW-017: sanitized aggregate shape from 2026-11-21 03:46 +08:00 GPT #73.
// No production log rows, request identifiers, model names or real token IDs.
// Locks current v5.01 behavior: trust reads the raw scoring window, not ChanStat
// after client-class exclusion. It does not change the algorithm.
func TestCasebookTrustUsesRawWindowNotChanStat(t *testing.T) {
	now := int64(100000)
	build := func(status *int) m.Feats {
		t.Helper()
		var rows []m.Row
		for n := 0; n < 61; n++ {
			rows = append(rows, m.Row{Ch: 73, T: now - 30, Type: 2, Tok: 900003})
		}
		for n := 0; n < 4; n++ {
			// Spread faults across tokens so a 5xx contrast is not
			// discounted by the dominant-token cap (tok_cap=2).
			rows = append(rows, m.Row{Ch: 73, T: now - 40*60, Type: 5, Tok: 900010 + n, Sc: status})
		}
		for n := 0; n < 4; n++ {
			rows = append(rows, m.Row{Ch: 73, T: now - 10, Type: 2, Tok: 1318})
		}
		cfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
		return features.Build(cfg, []m.Channel{{ID: 73}}, rows, now)
	}

	t.Run("client_400_penalizes_trust_but_settle_holds", func(t *testing.T) {
		fs := build(m.I(400))
		f := fs[73]
		if f == nil || f.Long == nil || f.Chan == nil {
			t.Fatalf("missing features: %+v", f)
		}
		if f.Long.N != 69 || f.Long.Err != 4 {
			t.Fatalf("raw window n=%d err=%d want 69/4", f.Long.N, f.Long.Err)
		}
		rate := m.Or(f.Long.ErrRate, 0)
		if rate != m.Round(4.0/69.0, 4) {
			t.Fatalf("raw err rate=%v want rounded 4/69=0.058", rate)
		}
		if f.Chan.N != 61 || f.Chan.Err != 0 || f.Chan.Client != 4 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
			t.Fatalf("chanstat=%+v want N=61 Err=0 Client=4 Eff=0 Rate=0", f.Chan)
		}

		cfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{"enabled": true}}
		st := trust.Step(cfg, []int{73}, fs, nil, map[int]float64{73: 0.9508}, nil)[73]
		if st == nil {
			t.Fatal("missing trust state")
		}
		if st.Sev != 0.19 || st.Trust != 0.8424 {
			t.Fatalf("trust sev=%v value=%v want 0.19 / 0.8424", st.Sev, st.Trust)
		}
		joined := strings.Join(st.Why, ";")
		if !strings.Contains(joined, "1h 错误率 5.8%") {
			t.Fatalf("expected raw-window trust note, got %q", st.Why)
		}

		next := map[int]int{73: 16}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{73: 20}, fs, []int{73}, nil, &notes)
		if next[73] != 20 {
			t.Fatalf("weight=%d want 20", next[73])
		}
		hold := strings.Join(notes, ";")
		for _, part := range []string{"无渠道级错误证据", "1h 真实 61", "渠道错误 0", "去集中 0"} {
			if !strings.Contains(hold, part) {
				t.Fatalf("expected hold note to contain %q, got %q", part, notes)
			}
		}
	})

	t.Run("channel_500_still_penalizes_trust_and_settle_cuts", func(t *testing.T) {
		fs := build(m.I(500))
		f := fs[73]
		if f.Long.N != 69 || f.Long.Err != 4 {
			t.Fatalf("raw window n=%d err=%d want 69/4", f.Long.N, f.Long.Err)
		}
		if f.Chan.N != 65 || f.Chan.Err != 4 || f.Chan.Client != 0 || f.Chan.Eff != 4 {
			t.Fatalf("chanstat=%+v want N=65 Err=4 Client=0 Eff=4", f.Chan)
		}
		if f.Chan.Rate != m.Round(4.0/65.0, 4) {
			t.Fatalf("chan rate=%v want rounded 4/65=0.0615", f.Chan.Rate)
		}

		cfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{"enabled": true}}
		st := trust.Step(cfg, []int{73}, fs, nil, map[int]float64{73: 0.9508}, nil)[73]
		if st.Sev != 0.19 || st.Trust != 0.8424 {
			t.Fatalf("trust sev=%v value=%v want 0.19 / 0.8424 (raw window unchanged)", st.Sev, st.Trust)
		}

		next := map[int]int{73: 16}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{73: 20}, fs, []int{73}, nil, &notes)
		if next[73] != 18 {
			t.Fatalf("weight=%d want 18 once 30/3/3%% is met", next[73])
		}
		joined := strings.Join(notes, ";")
		if !strings.Contains(joined, "渠道错误 4/65") || !strings.Contains(joined, "去集中 4") {
			t.Fatalf("expected fault cut note, got %q", notes)
		}
	})
}
