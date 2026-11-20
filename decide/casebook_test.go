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
