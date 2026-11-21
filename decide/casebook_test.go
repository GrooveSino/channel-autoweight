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

// CASE-AW-018: sanitized aggregate shape from 2026-11-21 04:28 +08:00 GPT #23.
// No production log rows, request identifiers, model names or real token IDs.
// Locks current v5.01 behavior: settle's ordinary cut is a conjunction
// (N>=30 AND Eff>=3 AND Rate>=.03). Rate alone at 3.23% does not cut when
// Eff=2. Trust still reads the raw scoring window (3/68=4.4%), not ChanStat.
// It does not change the algorithm.
func TestCasebookRateMeetsCutButEffBelowMin(t *testing.T) {
	now := int64(100000)
	build := func(n500, n400 int) m.Feats {
		t.Helper()
		var rows []m.Row
		for n := 0; n < 60; n++ {
			rows = append(rows, m.Row{Ch: 23, T: now - 30, Type: 2, Tok: 900003})
		}
		for n := 0; n < n500; n++ {
			rows = append(rows, m.Row{Ch: 23, T: now - 40*60, Type: 5, Tok: 900010 + n, Sc: m.I(500)})
		}
		for n := 0; n < n400; n++ {
			rows = append(rows, m.Row{Ch: 23, T: now - 40*60, Type: 5, Tok: 900020 + n, Sc: m.I(400)})
		}
		for n := 0; n < 5; n++ {
			rows = append(rows, m.Row{Ch: 23, T: now - 10, Type: 2, Tok: 1318})
		}
		cfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
		return features.Build(cfg, []m.Channel{{ID: 23}}, rows, now)
	}

	t.Run("rate_3pct_but_eff_2_holds_and_trust_uses_raw_window", func(t *testing.T) {
		fs := build(2, 1)
		f := fs[23]
		if f == nil || f.Long == nil || f.Chan == nil {
			t.Fatalf("missing features: %+v", f)
		}
		if f.Long.N != 68 || f.Long.Err != 3 {
			t.Fatalf("raw window n=%d err=%d want 68/3", f.Long.N, f.Long.Err)
		}
		rate := m.Or(f.Long.ErrRate, 0)
		if rate != m.Round(3.0/68.0, 4) {
			t.Fatalf("raw err rate=%v want rounded 3/68=0.0441", rate)
		}
		if f.Chan.N != 62 || f.Chan.Err != 2 || f.Chan.Client != 1 || f.Chan.Eff != 2 {
			t.Fatalf("chanstat=%+v want N=62 Err=2 Client=1 Eff=2", f.Chan)
		}
		if f.Chan.Rate != m.Round(2.0/62.0, 4) {
			t.Fatalf("chan rate=%v want rounded 2/62=0.0323", f.Chan.Rate)
		}

		cfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{"enabled": true}}
		prev := 0.7858
		st := trust.Step(cfg, []int{23}, fs, nil, map[int]float64{23: prev}, nil)[23]
		if st == nil {
			t.Fatal("missing trust state")
		}
		if st.Sev != 0.12 || st.Trust != 0.7292 {
			t.Fatalf("trust sev=%v value=%v want 0.12 / 0.7292", st.Sev, st.Trust)
		}
		joined := strings.Join(st.Why, ";")
		if !strings.Contains(joined, "1h 错误率 4.4%") {
			t.Fatalf("expected raw-window trust note, got %q", st.Why)
		}

		// Live shape: already at the strategy target, so settle may emit no note.
		// Absence of a 定档 note is not a skipped threshold check.
		atTarget := map[int]int{23: 20}
		var silent []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, atTarget, map[int]int{23: 20}, fs, []int{23}, nil, &silent)
		if atTarget[23] != 20 {
			t.Fatalf("weight=%d want 20 when Eff=2 even though Rate>=3%%", atTarget[23])
		}
		if strings.Contains(strings.Join(silent, ";"), "达降权门槛") {
			t.Fatalf("did not expect a cut note when Eff<3, got %q", silent)
		}

		// Same ChanStat with a strategy cut: hold, and now the note is explicit.
		held := map[int]int{23: 16}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, held, map[int]int{23: 20}, fs, []int{23}, nil, &notes)
		if held[23] != 20 {
			t.Fatalf("weight=%d want 20", held[23])
		}
		hold := strings.Join(notes, ";")
		for _, part := range []string{"无渠道级错误证据", "1h 真实 62", "渠道错误 2", "去集中 2"} {
			if !strings.Contains(hold, part) {
				t.Fatalf("expected hold note to contain %q, got %q", part, notes)
			}
		}
	})

	t.Run("eff_3_and_rate_above_3pct_cuts", func(t *testing.T) {
		fs := build(3, 0)
		f := fs[23]
		if f.Long.N != 68 || f.Long.Err != 3 {
			t.Fatalf("raw window n=%d err=%d want 68/3", f.Long.N, f.Long.Err)
		}
		if f.Chan.N != 63 || f.Chan.Err != 3 || f.Chan.Client != 0 || f.Chan.Eff != 3 {
			t.Fatalf("chanstat=%+v want N=63 Err=3 Client=0 Eff=3", f.Chan)
		}
		if f.Chan.Rate != m.Round(3.0/63.0, 4) {
			t.Fatalf("chan rate=%v want rounded 3/63=0.0476", f.Chan.Rate)
		}

		next := map[int]int{23: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{23: 20}, fs, []int{23}, nil, &notes)
		if next[23] != 18 {
			t.Fatalf("weight=%d want 18 once 30/3/3%% is met", next[23])
		}
		joined := strings.Join(notes, ";")
		if !strings.Contains(joined, "渠道错误 3/63") || !strings.Contains(joined, "去集中 3") || !strings.Contains(joined, "达降权门槛") {
			t.Fatalf("expected fault cut note, got %q", notes)
		}
	})
}

// CASE-AW-019: sanitized aggregate shape from 2026-11-21 05:20 +08:00 GPT #73.
// No production log rows, request identifiers, model names or real token IDs.
// Locks current v5.01 behavior: a 10m length-normalized np50 above 2.5× the
// peer 1h np50 median multiplies the score (here ×0.90), but settle still
// holds the weight when the 30/3/3% fault gate is not met. SlowBurst is a
// score factor, not a PairSlow override and not a standalone cut.
// It does not change the algorithm.
func TestCasebookSlowBurstPenaltyDoesNotCut(t *testing.T) {
	ids := []int{26, 2, 23, 73, 32}
	burstCfg := m.M{
		"windows_min": []any{60.0, 10.0},
		"score": m.M{"slow_burst": m.M{
			"enabled": true, "min_nn": 4.0, "peer_min_nn": 5.0,
			"ratio": 2.5, "floor": 0.2, "exp": 1.0,
		}},
	}
	live := m.Feats{
		2:  {Long: &m.Window{Nn: 12, Np50: m.F(40)}},
		23: {Long: &m.Window{Nn: 34, Np50: m.F(25.4)}},
		26: {Long: &m.Window{Nn: 28, Np50: m.F(21.51)}},
		73: {Short: &m.Window{Nn: 6, Np50: m.F(70.5)}, Long: &m.Window{Nn: 20, Np50: m.F(38.71)}},
		32: {Long: &m.Window{Nn: 0}},
	}

	t.Run("live_shape_multiplies_score_by_0.90", func(t *testing.T) {
		f, why := SlowBurstFactor(live, ids, 73, burstCfg)
		if why != "10m 归一化 np50 70.5s > 2.5×同伴 25.4s" {
			t.Fatalf("why=%q", why)
		}
		if m.Round(f, 3) != 0.901 {
			t.Fatalf("factor=%v want 0.9007 rounded to 0.901", f)
		}
	})

	t.Run("no_penalty_at_or_below_2_5x_peer_median", func(t *testing.T) {
		fs := m.Feats{
			2: live[2], 23: live[23], 26: live[26], 32: live[32],
			73: {Short: &m.Window{Nn: 6, Np50: m.F(63.5)}, Long: live[73].Long},
		}
		f, why := SlowBurstFactor(fs, ids, 73, burstCfg)
		if f != 1 || why != "" {
			t.Fatalf("equal to 2.5×ref must not penalize, got %v %q", f, why)
		}
	})

	t.Run("no_penalty_when_short_nn_below_min", func(t *testing.T) {
		fs := m.Feats{
			2: live[2], 23: live[23], 26: live[26], 32: live[32],
			73: {Short: &m.Window{Nn: 3, Np50: m.F(70.5)}, Long: live[73].Long},
		}
		f, why := SlowBurstFactor(fs, ids, 73, burstCfg)
		if f != 1 || why != "" {
			t.Fatalf("short nn=3 must not penalize, got %v %q", f, why)
		}
	})

	t.Run("disabled_or_no_peers_is_neutral", func(t *testing.T) {
		off := m.M{"windows_min": []any{60.0, 10.0}, "score": m.M{"slow_burst": m.M{"enabled": false}}}
		f, why := SlowBurstFactor(live, ids, 73, off)
		if f != 1 || why != "" {
			t.Fatalf("disabled must be neutral, got %v %q", f, why)
		}
		thinPeers := m.Feats{
			2:  {Long: &m.Window{Nn: 4, Np50: m.F(40)}},
			23: {Long: &m.Window{Nn: 4, Np50: m.F(25.4)}},
			26: {Long: &m.Window{Nn: 4, Np50: m.F(21.51)}},
			73: live[73],
			32: live[32],
		}
		f, why = SlowBurstFactor(thinPeers, ids, 73, burstCfg)
		if f != 1 || why != "" {
			t.Fatalf("peers below peer_min_nn must be neutral, got %v %q", f, why)
		}
	})

	t.Run("decide_notes_burst_and_settle_holds_below_fault_gate", func(t *testing.T) {
		cfg := m.M{
			"windows_min": []any{60.0, 10.0},
			"strategy":    "rank",
			"min_n_rank1": 10.0,
			"w_min":       0.0,
			"w_max":       20.0,
			"score": m.M{
				"a_p50": 1.0, "b_p90": 0.0, "c_ttft": 0.0, "t_ref": 20.0,
				"min_n_window": 5.0, "empty_weight": 0.0, "w_10m": 0.0, "ttft_missing_s": 30.0,
				"slow_burst": m.M{"enabled": true, "min_nn": 4.0, "peer_min_nn": 5.0, "ratio": 2.5, "floor": 0.2, "exp": 1.0},
			},
			"strategies": m.M{"rank": m.M{"top_w": 20.0, "close_ratio": 0.85, "close_factor": 0.8, "mid_ratio": 0.7, "mid_w": 3.0, "low_w": 0.0, "hysteresis": 0.0, "band": 0.0, "ramp": 0.0, "lead_min_n": 0.0}},
			"settle":     m.M{"cut_needs_fault": true, "raise_clean": false},
		}
		win := func(n int, p50 float64, nn int, np50 *float64) *m.Window {
			return &m.Window{N: n, P50: m.F(p50), P90: m.F(p50), ErrRate: m.F(0), Nn: nn, Np50: np50}
		}
		fs := m.Feats{
			2:  {Long: win(21, 16, 12, m.F(40)), Chan: &m.ChanStat{N: 17, Err: 0, Eff: 0, Rate: 0}},
			23: {Long: win(41, 20, 34, m.F(25.4)), Chan: &m.ChanStat{N: 37, Err: 0, Eff: 0, Rate: 0}},
			26: {Long: win(37, 15, 28, m.F(21.51)), Chan: &m.ChanStat{N: 33, Err: 0, Eff: 0, Rate: 0}},
			73: {Long: win(28, 24, 20, m.F(38.71)), Short: &m.Window{N: 6, Nn: 6, Np50: m.F(70.5)}, Chan: &m.ChanStat{N: 24, Err: 1, Client: 0, Eff: 1, Rate: m.Round(1.0/24.0, 4)}},
			32: {Long: win(4, 3, 0, nil), Chan: &m.ChanStat{N: 0, Err: 0, Eff: 0, Rate: 0}},
		}
		d, err := Decide(Input{
			Cfg:      cfg,
			Chans:    []m.Channel{{ID: 26}, {ID: 2}, {ID: 23}, {ID: 73}, {ID: 32}},
			Feats:    fs,
			Strategy: "rank",
			Prev:     map[int]int{2: 20, 23: 20, 26: 20, 32: 20, 73: 20},
		})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		row := d.Scores[73]
		if row == nil || row.SlowBurst == nil || *row.SlowBurst != 0.901 {
			t.Fatalf("slow_burst=%v want 0.901", row)
		}
		joined := strings.Join(d.Notes, ";")
		if !strings.Contains(joined, "#73 突发变慢：10m 归一化 np50 70.5s > 2.5×同伴 25.4s，分数×0.90") {
			t.Fatalf("missing burst note, got %q", d.Notes)
		}
		if d.Target[73] != 20 {
			t.Fatalf("#73 weight=%d want 20 after settle hold; notes=%q", d.Target[73], d.Notes)
		}
		if !strings.Contains(joined, "无渠道级错误证据") || !strings.Contains(joined, "1h 真实 24") || !strings.Contains(joined, "渠道错误 1") || !strings.Contains(joined, "去集中 1") || !strings.Contains(joined, "延迟/小样本不单独降权") {
			t.Fatalf("expected settle hold note, got %q", d.Notes)
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault cut, got %q", d.Notes)
		}
	})

	t.Run("fault_gate_still_cuts_even_with_slow_burst", func(t *testing.T) {
		feat := &m.Feat{
			Long:  &m.Window{N: 30, ErrRate: m.F(0.1)},
			Short: &m.Window{Nn: 6, Np50: m.F(70.5)},
			Chan:  &m.ChanStat{N: 30, Err: 3, Eff: 3, Rate: 0.1},
		}
		next := map[int]int{73: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{73: 20}, m.Feats{73: feat}, []int{73}, nil, &notes)
		if next[73] != 18 {
			t.Fatalf("weight=%d want 18 once 30/3/3%% is met", next[73])
		}
		joined := strings.Join(notes, ";")
		if !strings.Contains(joined, "达降权门槛") {
			t.Fatalf("expected fault cut note, got %q", notes)
		}
	})
}

// CASE-AW-020: sanitized aggregate shape from 2026-11-21 06:05 +08:00 CC #68.
// No production log rows, request identifiers, model names or real token IDs.
// Locks current v5.01 behavior: trust's 1h slow-rate penalty reads the
// scoring window (N and SlowRate), including a probe-thin window with no
// real user traffic. ChanStat / mix_60m n_user are not consulted. Settle
// still holds the weight when there is no channel-level fault evidence.
// It does not change the algorithm.
func TestCasebookTrustSlowRateOnProbeThinWindow(t *testing.T) {
	cfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{"enabled": true}}
	zeroGap := m.F(0)
	live := func() m.Feats {
		return m.Feats{
			68: {
				Long:  &m.Window{N: 5, Err: 0, ErrRate: m.F(0), SlowRate: m.F(0.2), P50: m.F(3), P90: m.F(110)},
				Short: &m.Window{N: 1, Err: 0, ErrRate: m.F(0)},
				Mix:   &m.Mix{NUser: 0, NProbe: 5},
			},
		}
	}

	t.Run("live_shape_penalizes_trust_on_probe_thin_window", func(t *testing.T) {
		fs := live()
		st := trust.Step(cfg, []int{68}, fs, nil, map[int]float64{68: 0.4614}, nil)[68]
		if st == nil {
			t.Fatal("missing trust state")
		}
		if st.Sev != 0.3 || st.Trust != 0.3783 {
			t.Fatalf("trust sev=%v value=%v want 0.3 / 0.3783", st.Sev, st.Trust)
		}
		if !strings.Contains(strings.Join(st.Why, ";"), "1h 慢请求 20%") {
			t.Fatalf("expected slow-rate trust note, got %q", st.Why)
		}
	})

	t.Run("chanstat_zero_does_not_block_penalty", func(t *testing.T) {
		fs := live()
		fs[68].Chan = &m.ChanStat{N: 0, Err: 0, Eff: 0, Rate: 0}
		st := trust.Step(cfg, []int{68}, fs, nil, map[int]float64{68: 0.4614}, nil)[68]
		if st.Sev != 0.3 || st.Trust != 0.3783 {
			t.Fatalf("trust still reads the scoring window, sev=%v value=%v", st.Sev, st.Trust)
		}
	})

	t.Run("no_penalty_when_n_below_slow_min", func(t *testing.T) {
		fs := live()
		fs[68].Long.N = 4
		st := trust.Step(cfg, []int{68}, fs, nil, map[int]float64{68: 0.4614}, zeroGap)[68]
		if st.Sev != 0 || st.Trust != 0.4614 {
			t.Fatalf("n=4 must not apply slow penalty, sev=%v value=%v", st.Sev, st.Trust)
		}
		if strings.Contains(strings.Join(st.Why, ";"), "慢请求") {
			t.Fatalf("did not expect slow-rate note, got %q", st.Why)
		}
	})

	t.Run("no_penalty_at_or_below_slow_threshold", func(t *testing.T) {
		fs := live()
		fs[68].Long.SlowRate = m.F(0.15)
		st := trust.Step(cfg, []int{68}, fs, nil, map[int]float64{68: 0.4614}, zeroGap)[68]
		if st.Sev != 0 || st.Trust != 0.4614 {
			t.Fatalf("slow_rate=0.15 must not penalize, sev=%v value=%v", st.Sev, st.Trust)
		}
		fs = live()
		fs[68].Long.SlowRate = m.F(0.14)
		st = trust.Step(cfg, []int{68}, fs, nil, map[int]float64{68: 0.4614}, zeroGap)[68]
		if st.Sev != 0 || st.Trust != 0.4614 {
			t.Fatalf("slow_rate=0.14 must not penalize, sev=%v value=%v", st.Sev, st.Trust)
		}
	})

	t.Run("busy_clean_channel_below_threshold_does_not_penalize", func(t *testing.T) {
		fs := m.Feats{
			67: {
				Long: &m.Window{N: 236, Err: 0, ErrRate: m.F(0), SlowRate: m.F(0.0508)},
				Chan: &m.ChanStat{N: 232, Err: 0, Client: 0, Eff: 0, Rate: 0},
			},
		}
		st := trust.Step(cfg, []int{67}, fs, nil, map[int]float64{67: 1}, zeroGap)[67]
		if st.Sev != 0 || st.Trust != 1 {
			t.Fatalf("#67 slow_rate 5.08%% must not penalize, sev=%v value=%v", st.Sev, st.Trust)
		}
	})

	t.Run("settle_holds_at_20_with_zero_real_traffic", func(t *testing.T) {
		fs := live()
		next := map[int]int{68: 0}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{68: 20}, fs, []int{68}, nil, &notes)
		if next[68] != 20 {
			t.Fatalf("weight=%d want 20 after settle hold; notes=%q", next[68], notes)
		}
		joined := strings.Join(notes, ";")
		if !strings.Contains(joined, "无渠道级错误证据") || !strings.Contains(joined, "1h 真实 0") || !strings.Contains(joined, "渠道错误 0") || !strings.Contains(joined, "去集中 0") || !strings.Contains(joined, "延迟/小样本不单独降权") {
			t.Fatalf("expected settle hold note, got %q", notes)
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault cut, got %q", notes)
		}
	})
}

// CASE-AW-021: sanitized follow-up to CASE-AW-020 from 2026-11-21 06:49 +08:00.
// No production log rows, request identifiers, model names or real token IDs.
// Locks in existing v5.01 trust-recovery semantics; it does not change the algorithm.
func TestCasebookTrustRecoversOnProbeThinQuietWindow(t *testing.T) {
	// Live v5.01 trust config uses up=0.2 (code default is 0.08). The other
	// knobs match both Defaults and the live file; they are listed so the
	// recovery arithmetic does not silently depend on a different default.
	cfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2612s.
	gap := m.F(2612.0 / 60.0)
	live := func() m.Feats {
		return m.Feats{
			68: {
				Long:  &m.Window{N: 4, Err: 0, ErrRate: m.F(0), SlowRate: m.F(0), P50: m.F(3), P90: m.F(3)},
				Short: &m.Window{N: 0, Err: 0},
				Mix:   &m.Mix{NUser: 0, NProbe: 4},
			},
		}
	}

	t.Run("live_shape_recovers_trust_after_probe_thin_slow_penalty", func(t *testing.T) {
		fs := live()
		st := trust.Step(cfg, []int{68}, fs, nil, map[int]float64{68: 0.3783}, gap)[68]
		if st == nil {
			t.Fatal("missing trust state")
		}
		if st.Sev != 0 || st.Trust != 0.4041 {
			t.Fatalf("trust sev=%v value=%v want 0 / 0.4041", st.Sev, st.Trust)
		}
		if strings.Contains(strings.Join(st.Why, ";"), "慢请求") {
			t.Fatalf("did not expect a slow-rate note after recovery, got %q", st.Why)
		}
	})

	t.Run("recovery_uses_scoring_window_n_including_probes", func(t *testing.T) {
		fs := live()
		fs[68].Chan = &m.ChanStat{N: 0, Err: 0, Eff: 0, Rate: 0}
		st := trust.Step(cfg, []int{68}, fs, nil, map[int]float64{68: 0.3783}, gap)[68]
		if st.Sev != 0 || st.Trust != 0.4041 {
			t.Fatalf("zero real ChanStat must not freeze recovery, sev=%v value=%v", st.Sev, st.Trust)
		}
	})

	t.Run("empty_scoring_window_does_not_raise_trust", func(t *testing.T) {
		fs := live()
		fs[68].Long.N = 0
		st := trust.Step(cfg, []int{68}, fs, nil, map[int]float64{68: 0.3783}, gap)[68]
		if st.Sev != 0 || st.Trust != 0.3783 {
			t.Fatalf("n=0 must leave trust unchanged, sev=%v value=%v", st.Sev, st.Trust)
		}
	})

	t.Run("peer_channel_recovers_in_proportion_to_scoring_n", func(t *testing.T) {
		fs := m.Feats{
			72: {
				Long:  &m.Window{N: 6, Err: 0, ErrRate: m.F(0), SlowRate: m.F(0)},
				Short: &m.Window{N: 2, Err: 0, ErrRate: m.F(0)},
				Mix:   &m.Mix{NUser: 2, NProbe: 4},
				Chan:  &m.ChanStat{N: 2, Err: 0, Client: 0, Eff: 0, Rate: 0},
			},
		}
		st := trust.Step(cfg, []int{72}, fs, nil, map[int]float64{72: 0.6918}, gap)[72]
		if st.Sev != 0 || st.Trust != 0.7305 {
			t.Fatalf("#72 trust sev=%v value=%v want 0 / 0.7305", st.Sev, st.Trust)
		}
	})

	t.Run("still_penalizes_if_slow_rate_threshold_still_met", func(t *testing.T) {
		fs := live()
		fs[68].Long.N = 5
		fs[68].Long.SlowRate = m.F(0.2)
		st := trust.Step(cfg, []int{68}, fs, nil, map[int]float64{68: 0.3783}, gap)[68]
		// 0.3783 * (1 - 0.6*0.3) = 0.310206 -> 0.3102; must not recover.
		if st.Sev != 0.3 || st.Trust != 0.3102 {
			t.Fatalf("n=5 / slow_rate=20%% must keep penalizing, sev=%v value=%v", st.Sev, st.Trust)
		}
	})

	t.Run("settle_holds_at_20_with_zero_real_traffic", func(t *testing.T) {
		fs := live()
		next := map[int]int{68: 0}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{68: 20}, fs, []int{68}, nil, &notes)
		if next[68] != 20 {
			t.Fatalf("weight=%d want 20 after settle hold; notes=%q", next[68], notes)
		}
		joined := strings.Join(notes, ";")
		if !strings.Contains(joined, "无渠道级错误证据") || !strings.Contains(joined, "1h 真实 0") || !strings.Contains(joined, "渠道错误 0") || !strings.Contains(joined, "去集中 0") || !strings.Contains(joined, "延迟/小样本不单独降权") {
			t.Fatalf("expected settle hold note, got %q", notes)
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault cut, got %q", notes)
		}
	})
}

// CASE-AW-022: sanitized aggregate shape from 2026-11-21 07:34 +08:00 CC #67.
// No production log rows, request identifiers, model names or real token IDs.
// Locks current v5.01 trust+settle semantics; it does not change the algorithm.
func TestCasebookThinWindowErrorCrashesTrustButSettleHolds(t *testing.T) {
	// Live v5.01 trust config uses up=0.2 (code default is 0.08). down, err_full
	// and err_1h_thr match Defaults / the live file so the 1.0→0.4 arithmetic
	// does not silently depend on a different default.
	cfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Scoring window 2/6=33.33% (includes probes). Mix real errors 2/4=50%.
	// ChanStat after excluding one client-class error: N=3, Err=1, Eff=1, Rate=1/3.
	live := func() m.Feats {
		return m.Feats{
			67: {
				Long:  &m.Window{N: 6, Err: 2, ErrRate: m.F(2.0 / 6.0)},
				Short: &m.Window{N: 2, Err: 2, ErrRate: m.F(1.0)},
				Mix:   &m.Mix{NUser: 4, NProbe: 2, NErrReal: 2, ErrRateReal: m.F(0.5)},
				Chan:  &m.ChanStat{N: 3, Err: 1, Client: 1, Eff: 1, Rate: 1.0 / 3.0},
			},
		}
	}

	t.Run("live_shape_trust_drops_1_to_0_4", func(t *testing.T) {
		st := trust.Step(cfg, []int{67}, live(), nil, map[int]float64{67: 1.0}, nil)[67]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// Both parts saturate independently at 1.0; maxSev keeps 1.0 (not additive).
		// Update: 1.0 * (1 - 0.6*1.0) = 0.4.
		if st.Sev != 1 || st.Trust != 0.4 {
			t.Fatalf("trust sev=%v value=%v want 1 / 0.4", st.Sev, st.Trust)
		}
		joined := strings.Join(st.Why, ";")
		if !strings.Contains(joined, "10m 错误 2/2") || !strings.Contains(joined, "1h 错误率 33.3%") {
			t.Fatalf("expected both raw-window notes, got %q", st.Why)
		}
	})

	t.Run("ten_minute_all_error_alone_saturates", func(t *testing.T) {
		fs := live()
		fs[67].Long = &m.Window{N: 6, Err: 0, ErrRate: m.F(0)}
		st := trust.Step(cfg, []int{67}, fs, nil, map[int]float64{67: 1.0}, nil)[67]
		if st.Sev != 1 || st.Trust != 0.4 {
			t.Fatalf("10m 2/2 alone must saturate, sev=%v value=%v", st.Sev, st.Trust)
		}
		joined := strings.Join(st.Why, ";")
		if !strings.Contains(joined, "10m 错误 2/2") || strings.Contains(joined, "1h 错误率") {
			t.Fatalf("expected only the 10m note, got %q", st.Why)
		}
	})

	t.Run("one_hour_raw_rate_alone_saturates", func(t *testing.T) {
		fs := live()
		fs[67].Short = &m.Window{N: 0, Err: 0}
		st := trust.Step(cfg, []int{67}, fs, nil, map[int]float64{67: 1.0}, nil)[67]
		if st.Sev != 1 || st.Trust != 0.4 {
			t.Fatalf("1h 33.3%% alone must saturate, sev=%v value=%v", st.Sev, st.Trust)
		}
		joined := strings.Join(st.Why, ";")
		if !strings.Contains(joined, "1h 错误率 33.3%") || strings.Contains(joined, "10m 错误") {
			t.Fatalf("expected only the 1h note, got %q", st.Why)
		}
	})

	t.Run("trust_reads_raw_window_not_chan_or_mix", func(t *testing.T) {
		fs := live()
		// Same ChanStat 1/3 and Mix 2/4, but a clean scoring window must not
		// inherit those rates. This is the CASE-AW-017 interaction on this shape.
		fs[67].Long = &m.Window{N: 6, Err: 0, ErrRate: m.F(0)}
		fs[67].Short = &m.Window{N: 2, Err: 0, ErrRate: m.F(0)}
		st := trust.Step(cfg, []int{67}, fs, nil, map[int]float64{67: 1.0}, nil)[67]
		if st.Sev != 0 || st.Trust != 1 {
			t.Fatalf("clean scoring window must not read Chan/Mix rates, sev=%v value=%v", st.Sev, st.Trust)
		}
	})

	t.Run("chanstats_excludes_one_client_from_thin_window", func(t *testing.T) {
		// Two successes, one channel error, one client-class 400.
		// Fictional token numbers only; no production identifiers.
		rows := []m.Row{
			{Ch: 67, T: 100, Type: 2, Tok: 900003},
			{Ch: 67, T: 100, Type: 2, Tok: 900004},
			{Ch: 67, T: 100, Type: 5, Tok: 900005},
			{Ch: 67, T: 100, Type: 5, Tok: 900006, Sc: m.I(400)},
		}
		stats := features.ChanStats(m.M{}, rows, 0, 2, .8)[67]
		if stats.N != 3 || stats.Err != 1 || stats.Client != 1 || stats.Eff != 1 || stats.Rate != 0.3333 {
			t.Fatalf("unexpected channel evidence: %+v", stats)
		}
	})

	t.Run("settle_holds_20_when_n3_eff1", func(t *testing.T) {
		fs := live()
		next := map[int]int{67: 0}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fs, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20 after settle hold; notes=%q", next[67], notes)
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"定档：策略 0→20", "无渠道级错误证据", "1h 真实 3", "渠道错误 1", "去集中 1", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle hold note to contain %q, got %q", part, notes)
			}
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault cut, got %q", notes)
		}
	})

	t.Run("meeting_the_gate_allows_a_cut", func(t *testing.T) {
		// Meet 30/3/3% but stay below the extreme path (Rate>=20% and N>=20
		// would go straight to 0 and emit no 定档 note when strategy is already 0).
		fault := m.Feats{67: {
			Long: &m.Window{N: 30, Err: 3, ErrRate: m.F(0.1)},
			Chan: &m.ChanStat{N: 30, Err: 3, Eff: 3, Rate: 0.1},
		}}
		next := map[int]int{67: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fault, []int{67}, nil, &notes)
		if next[67] != 18 {
			t.Fatalf("weight=%d want 18 after meeting 30/3/3%%; notes=%q", next[67], notes)
		}
		if !strings.Contains(strings.Join(notes, ";"), "达降权门槛") {
			t.Fatalf("expected a fault-cut note, got %q", notes)
		}
	})
}

// CASE-AW-023: sanitized follow-up to CASE-AW-022 from 2026-11-21 08:15 +08:00 CC #67.
// No production log rows, request identifiers, model names or real token IDs.
// Locks current v5.01 trust+settle semantics; it does not change the algorithm.
func TestCasebookSlightlyOver2pctBlocksTrustRecovery(t *testing.T) {
	// Live v5.01 trust config uses up=0.2 (code default is 0.08). down and
	// err_1h_thr match Defaults / the live file so the 0.4->0.3976 arithmetic
	// and the blocked-recovery contrast do not silently depend on a different default.
	cfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2473s.
	gap := m.F(2473.0 / 60.0)
	// Scoring window 2/91=2.20% stored as ErrRate=0.022. Mix real errors 2/89=2.25%.
	// ChanStat after excluding one client-class error: N=88, Err=1, Eff=1, Rate=1/88.
	live := func() m.Feats {
		return m.Feats{
			67: {
				Long:  &m.Window{N: 91, Err: 2, ErrRate: m.F(0.022)},
				Short: &m.Window{N: 0, Err: 0},
				Mix:   &m.Mix{NUser: 89, NProbe: 2, NErrReal: 2, ErrRateReal: m.F(2.0 / 89.0)},
				Chan:  &m.ChanStat{N: 88, Err: 1, Client: 1, Eff: 1, Rate: 1.0 / 88.0},
			},
		}
	}

	t.Run("live_shape_blocks_recovery_0_4_to_0_3976", func(t *testing.T) {
		st := trust.Step(cfg, []int{67}, live(), nil, map[int]float64{67: 0.4}, gap)[67]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=(0.022-0.02)/0.2=0.01; Update takes the down branch because sev>0:
		// 0.4*(1-0.6*0.01)=0.3976. A clean window would have recovered instead.
		if st.Sev != 0.01 || st.Trust != 0.3976 {
			t.Fatalf("trust sev=%v value=%v want 0.01 / 0.3976", st.Sev, st.Trust)
		}
		joined := strings.Join(st.Why, ";")
		if !strings.Contains(joined, "1h 错误率 2.2%") || strings.Contains(joined, "10m 错误") {
			t.Fatalf("expected only the 1h 2.2%% note, got %q", st.Why)
		}
	})

	t.Run("clean_scoring_window_recovers_despite_chan_and_mix", func(t *testing.T) {
		fs := live()
		fs[67].Long = &m.Window{N: 91, Err: 0, ErrRate: m.F(0)}
		st := trust.Step(cfg, []int{67}, fs, nil, map[int]float64{67: 0.4}, gap)[67]
		// 0.4 + 0.2*(2473/60/45)*min(1, 91/30) = 0.583185... -> 0.5832
		if st.Sev != 0 || st.Trust != 0.5832 {
			t.Fatalf("clean scoring window must recover, sev=%v value=%v want 0 / 0.5832", st.Sev, st.Trust)
		}
	})

	t.Run("exactly_2_percent_does_not_penalize_and_recovers", func(t *testing.T) {
		fs := live()
		fs[67].Long = &m.Window{N: 91, Err: 2, ErrRate: m.F(0.02)}
		st := trust.Step(cfg, []int{67}, fs, nil, map[int]float64{67: 0.4}, gap)[67]
		// Threshold is strict greater-than 2%, so 2.0% takes the recovery branch.
		if st.Sev != 0 || st.Trust != 0.5832 {
			t.Fatalf("ErrRate=2.0%% must recover, sev=%v value=%v want 0 / 0.5832", st.Sev, st.Trust)
		}
	})

	t.Run("settle_holds_20_when_n88_eff1", func(t *testing.T) {
		fs := live()
		next := map[int]int{67: 3}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fs, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20 after settle hold; notes=%q", next[67], notes)
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"定档：策略 3→20", "无渠道级错误证据", "1h 真实 88", "渠道错误 1", "去集中 1", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle hold note to contain %q, got %q", part, notes)
			}
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault cut, got %q", notes)
		}
	})

	t.Run("strategy_already_20_has_no_settle_note", func(t *testing.T) {
		fs := live()
		next := map[int]int{67: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fs, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[67], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("strategy already 20 should not emit a settle note, got %q", notes)
		}
	})

	t.Run("meeting_the_gate_allows_a_cut", func(t *testing.T) {
		fault := m.Feats{67: {
			Long: &m.Window{N: 91, Err: 3, ErrRate: m.F(0.1)},
			Chan: &m.ChanStat{N: 88, Err: 3, Client: 1, Eff: 3, Rate: 0.1},
		}}
		next := map[int]int{67: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fault, []int{67}, nil, &notes)
		if next[67] != 18 {
			t.Fatalf("weight=%d want 18 after meeting 30/3/3%%; notes=%q", next[67], notes)
		}
		if !strings.Contains(strings.Join(notes, ";"), "达降权门槛") {
			t.Fatalf("expected a fault-cut note, got %q", notes)
		}
	})
}

// CASE-AW-024: sanitized aggregate shape from 2026-11-21 09:01 +08:00 CC #72.
// No production log rows, request identifiers, model names or real token IDs.
// Locks current v5.01 trust+settle semantics; it does not change the algorithm.
// Counterpart to CASE-AW-020: the 1h slow-rate penalty still fires when the
// window is thin but not empty (16 real requests + 4 probes), and Settle still
// holds 20 because ChanStat N=16 misses the 30/3/3% gate.
func TestCasebookThinRealSlowRateCutsTrustButSettleHolds(t *testing.T) {
	// Live v5.01 trust config uses up=0.2 (code default is 0.08). down,
	// slow threshold and recovery knobs match Defaults / the live file so
	// the 0.9868->0.8092 arithmetic and the <=15% recovery contrast do not
	// silently depend on a different default.
	cfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of CC algorithm now values: 2789s.
	gap := m.F(2789.0 / 60.0)
	live := func() m.Feats {
		return m.Feats{
			72: {
				Long:  &m.Window{N: 20, Err: 0, ErrRate: m.F(0), SlowRate: m.F(0.25), P50: m.F(26), P90: m.F(240)},
				Short: &m.Window{N: 0, Err: 0},
				Mix:   &m.Mix{NUser: 16, NProbe: 4, NErrReal: 0, ErrRateReal: m.F(0)},
				Chan:  &m.ChanStat{N: 16, Err: 0, Client: 0, Eff: 0, Rate: 0},
			},
		}
	}

	t.Run("live_shape_penalizes_trust_0_9868_to_0_8092", func(t *testing.T) {
		st := trust.Step(cfg, []int{72}, live(), nil, map[int]float64{72: 0.9868}, gap)[72]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// N=20>=slow_min_n and SlowRate=25%>15% => fixed sev=0.3.
		// Update takes the down branch: 0.9868*(1-0.6*0.3)=0.809176 -> 0.8092.
		if st.Sev != 0.3 || st.Trust != 0.8092 {
			t.Fatalf("trust sev=%v value=%v want 0.3 / 0.8092", st.Sev, st.Trust)
		}
		if !strings.Contains(strings.Join(st.Why, ";"), "1h 慢请求 25%") {
			t.Fatalf("expected slow-rate trust note, got %q", st.Why)
		}
	})

	t.Run("thin_real_chanstat_does_not_block_penalty", func(t *testing.T) {
		fs := live()
		// Even an ample clean ChanStat cannot stop a scoring-window slow penalty.
		fs[72].Chan = &m.ChanStat{N: 100, Err: 0, Client: 0, Eff: 0, Rate: 0}
		st := trust.Step(cfg, []int{72}, fs, nil, map[int]float64{72: 0.9868}, gap)[72]
		if st.Sev != 0.3 || st.Trust != 0.8092 {
			t.Fatalf("trust still reads the scoring window, sev=%v value=%v", st.Sev, st.Trust)
		}
	})

	t.Run("at_or_below_slow_threshold_recovers_toward_1", func(t *testing.T) {
		for _, rate := range []float64{0.15, 0.14} {
			fs := live()
			fs[72].Long.SlowRate = m.F(rate)
			st := trust.Step(cfg, []int{72}, fs, nil, map[int]float64{72: 0.9868}, gap)[72]
			// sev=0, so recovery: 0.9868 + 0.2*min(1, 2789/60/45)*min(1, 20/30)
			// gap>45m so g=1; 0.9868+0.2*(20/30)=1.12013... capped at 1.
			if st.Sev != 0 || st.Trust != 1 {
				t.Fatalf("slow_rate=%v must recover to 1, sev=%v value=%v", rate, st.Sev, st.Trust)
			}
			if strings.Contains(strings.Join(st.Why, ";"), "慢请求") {
				t.Fatalf("did not expect slow-rate note at %v, got %q", rate, st.Why)
			}
		}
	})

	t.Run("n_below_slow_min_does_not_penalize", func(t *testing.T) {
		fs := live()
		fs[72].Long.N = 4
		st := trust.Step(cfg, []int{72}, fs, nil, map[int]float64{72: 0.9868}, gap)[72]
		// N=4 < slow_min_n=5: no slow penalty. Recovery uses scoring N=4:
		// 0.9868 + 0.2*1*min(1, 4/30)=1.01346... capped at 1.
		if st.Sev != 0 || st.Trust != 1 {
			t.Fatalf("n=4 must not apply slow penalty, sev=%v value=%v", st.Sev, st.Trust)
		}
		if strings.Contains(strings.Join(st.Why, ";"), "慢请求") {
			t.Fatalf("did not expect slow-rate note, got %q", st.Why)
		}
	})

	t.Run("settle_holds_20_from_strategy_6", func(t *testing.T) {
		fs := live()
		next := map[int]int{72: 6}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20 after settle hold; notes=%q", next[72], notes)
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"定档：策略 6→20", "无渠道级错误证据", "1h 真实 16", "渠道错误 0", "去集中 0", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle hold note to contain %q, got %q", part, notes)
			}
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault cut, got %q", notes)
		}
	})

	t.Run("strategy_already_20_has_no_settle_note", func(t *testing.T) {
		fs := live()
		next := map[int]int{72: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[72], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("strategy already 20 should not emit a settle note, got %q", notes)
		}
	})
}

// CASE-AW-025: sanitized relative-score shape from 2026-11-21 09:46 +08:00 CC #72/#68/#67.
// No production log rows, request identifiers, model names or real token IDs.
// Locks current v5.01 behavior when every channel misses rank-1 eligibility:
// the score order is kept, no promotion note is emitted, and settle holds 20.
// It does not change the algorithm.
func TestCasebookAllIneligibleRank1KeepsOrderAndSettleHolds(t *testing.T) {
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
	// p50 4/5/20 => scores 20/24, 20/25, 20/40. Second is inside the close
	// band (0.96); third is below mid_ratio (0.60). The 0.5 third score also
	// keeps the set below tie_ratio, so all-tie split does not fire.
	feat := func(n60, chanN int, p50 float64) *m.Feat {
		return &m.Feat{
			Long: &m.Window{N: n60, P50: m.F(p50), P90: m.F(p50), ErrRate: m.F(0)},
			Chan: &m.ChanStat{N: chanN, Err: 0, Eff: 0, Rate: 0},
		}
	}
	run := func(n60Second int) *Decision {
		t.Helper()
		d, err := Decide(Input{
			Cfg:      cfg,
			Chans:    []m.Channel{{ID: 67}, {ID: 68}, {ID: 72}},
			Feats:    m.Feats{72: feat(5, 1, 4), 68: feat(n60Second, 1, 5), 67: feat(8, 0, 20)},
			Strategy: "rank",
			Prev:     map[int]int{67: 20, 68: 20, 72: 20},
		})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		return d
	}

	t.Run("all_ineligible_keeps_score_order_and_holds_20", func(t *testing.T) {
		d := run(5)
		for _, id := range []int{72, 68, 67} {
			if d.Scores[id].EligibleRank1 {
				t.Fatalf("#%d n60=%d should be ineligible for rank 1", id, d.Scores[id].N60)
			}
		}
		if got := d.Order; len(got) != 3 || got[0] != 72 || got[1] != 68 || got[2] != 67 {
			t.Fatalf("order=%v want [72 68 67] with the ineligible leader left first", d.Order)
		}
		if d.Target[72] != 20 || d.Target[68] != 20 || d.Target[67] != 20 {
			t.Fatalf("weights=%v want all held at 20", d.Target)
		}
		joined := strings.Join(d.Notes, ";")
		if strings.Contains(joined, "不得排第一") {
			t.Fatalf("did not expect a promotion note when nobody is eligible, got %q", d.Notes)
		}
		if !strings.Contains(joined, "#68 定档：策略 16→20") || !strings.Contains(joined, "1h 真实 1") {
			t.Fatalf("expected #68 close band settled 16→20, got %q", d.Notes)
		}
		if !strings.Contains(joined, "#67 定档：策略 0→20") || !strings.Contains(joined, "1h 真实 0") {
			t.Fatalf("expected #67 low band settled 0→20, got %q", d.Notes)
		}
		if strings.Contains(joined, "#72 定档") {
			t.Fatalf("strategy already 20 for the ineligible leader should not emit a settle note, got %q", d.Notes)
		}
		for _, part := range []string{"无渠道级错误证据", "渠道错误 0", "去集中 0", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle hold note to contain %q, got %q", part, d.Notes)
			}
		}
	})

	t.Run("eligible_second_is_promoted", func(t *testing.T) {
		d := run(12)
		if d.Scores[72].EligibleRank1 || d.Scores[67].EligibleRank1 {
			t.Fatalf("thin channels should stay ineligible, scores=%v", d.Scores)
		}
		if !d.Scores[68].EligibleRank1 {
			t.Fatalf("n60=12 should be eligible for rank 1")
		}
		if d.Order[0] != 68 {
			t.Fatalf("order=%v want #68 first after promotion", d.Order)
		}
		if d.Target[72] != 20 || d.Target[68] != 20 || d.Target[67] != 20 {
			t.Fatalf("weights=%v want all held at 20 after promotion", d.Target)
		}
		joined := strings.Join(d.Notes, ";")
		if !strings.Contains(joined, "#72 样本不足，不得排第一，由 #68 顶上") {
			t.Fatalf("missing promotion note, got %q", d.Notes)
		}
		if !strings.Contains(joined, "#72 定档：策略 16→20") {
			t.Fatalf("expected former leader to land in the close band 16→20, got %q", d.Notes)
		}
		if !strings.Contains(joined, "#67 定档：策略 0→20") {
			t.Fatalf("expected #67 to stay in the low band 0→20, got %q", d.Notes)
		}
		if strings.Contains(joined, "#68 定档") {
			t.Fatalf("promoted leader already at 20 should not emit a settle note, got %q", d.Notes)
		}
	})
}
