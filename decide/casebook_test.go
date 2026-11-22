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

// CASE-AW-015/017 supplement: sanitized aggregate shape from 2026-11-21 15:50
// +08:00 CC #67. No production log rows, request identifiers, model names or
// real token IDs. Trust reads the raw 4/15=26.7% scoring window (all four
// type5 are client-class) and falls below 0.3, so the recovery-period cap of 3
// is computed. ChanStat is N=5, Err=0, Client=4, Eff=0 and does not meet
// 30/3/3%, so cut_needs_fault refuses to apply that system cap. This locks
// current v5.01 behavior; it does not change the algorithm.
func TestCasebookAllClientThinWindowTrustCapHeld(t *testing.T) {
	now := int64(100000)
	var rows []m.Row
	// Two recent real successes keep the 10m window error-free (n=2).
	for n := 0; n < 2; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 30, Type: 2, Tok: 900003})
	}
	// Three older real successes, still inside 60m.
	for n := 0; n < 3; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 900004 + n})
	}
	// Four client-class faults in 60m, outside 10m, spread across tokens.
	for n := 0; n < 4; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 5, Tok: 900010 + n, Sc: m.I(400)})
	}
	// Six probe successes in 60m, outside 10m.
	for n := 0; n < 6; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 1318})
	}
	featCfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
	fs := features.Build(featCfg, []m.Channel{{ID: 67}}, rows, now)
	f := fs[67]
	if f == nil || f.Long == nil || f.Chan == nil {
		t.Fatalf("missing features: %+v", f)
	}
	if f.Long.N != 15 || f.Long.Err != 4 {
		t.Fatalf("raw window n=%d err=%d want 15/4", f.Long.N, f.Long.Err)
	}
	rate := m.Or(f.Long.ErrRate, 0)
	if rate != m.Round(4.0/15.0, 4) {
		t.Fatalf("raw err rate=%v want rounded 4/15=0.2667", rate)
	}
	if f.Chan.N != 5 || f.Chan.Err != 0 || f.Chan.Client != 4 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
		t.Fatalf("chanstat=%+v want N=5 Err=0 Client=4 Eff=0 Rate=0", f.Chan)
	}

	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{"enabled": true}}
	st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.5766}, nil)[67]
	if st == nil {
		t.Fatal("missing trust state")
	}
	if st.Sev != 1 || st.Trust != 0.2306 {
		t.Fatalf("trust sev=%v value=%v want 1 / 0.2306", st.Sev, st.Trust)
	}
	if len(st.Why) != 1 || st.Why[0] != "1h 错误率 26.7%" {
		t.Fatalf("trust why=%v want [1h 错误率 26.7%%]", st.Why)
	}

	capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
	caps := map[int]*Cap{}
	applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: f.Long.N}}, map[int]float64{67: st.Trust}, []int{67})
	c := caps[67]
	if c == nil || c.Cap != 3 || c.Kind != "trust" {
		t.Fatalf("trust cap=%+v want cap=3 kind=trust", c)
	}
	if c.Reason != "信任 0.23 < 0.3且 1h 样本 15 < 20，恢复期限幅" {
		t.Fatalf("trust cap reason=%q", c.Reason)
	}

	next := map[int]int{67: 0}
	var notes []string
	Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fs, []int{67}, caps, &notes)
	if next[67] != 20 {
		t.Fatalf("weight=%d want 20; system cap 3 must not be applied", next[67])
	}
	joined := strings.Join(notes, ";")
	for _, part := range []string{"系统上限 3", "不执行", "1h 真实 5", "渠道错误 0", "去集中 0", "0.0%", "未达降权门槛"} {
		if !strings.Contains(joined, part) {
			t.Fatalf("expected held-cap note to contain %q, got %q", part, notes)
		}
	}
}

// CASE-AW-031: sanitized aggregate shape from 2026-11-21 16:33 +08:00 CC #67.
// No production log rows, request identifiers, model names or real token IDs.
// Unlike CASE-AW-015 / TestCasebookAllClientThinWindowTrustCapHeld, the scoring
// window is already n60=34 (>=20), so the recovery-period cap of 3 is not
// computed. Trust still reads the raw 11/34=32.4% window (all 11 type5 are
// client-class) and hits the 0.1 floor. ChanStat is N=19, Err=0, Client=11,
// Eff=0 and does not meet 30/3/3%, so a strategy target of 0 is settled back
// to 20. This locks current v5.01 behavior; it does not change the algorithm.
func TestCasebookAllClientAmpleWindowTrustFloorHeld(t *testing.T) {
	now := int64(100000)
	var rows []m.Row
	// 10m window (28): 16 real successes, 1 empty type2, 11 client-class faults.
	for n := 0; n < 16; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 30, Type: 2, Tok: 900100 + n, Ct: 300, Ut: 5})
	}
	rows = append(rows, m.Row{Ch: 67, T: now - 30, Type: 2, Tok: 900200, Ct: 0, Ut: 5})
	for n := 0; n < 11; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 30, Type: 5, Tok: 900300 + n, Sc: m.I(400)})
	}
	// Older than 10m, still inside 60m (6): 4 probe successes, 1 empty, 1 real success.
	for n := 0; n < 4; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 1318, Ct: 300, Ut: 5})
	}
	rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 900400, Ct: 0, Ut: 5})
	rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 900401, Ct: 300, Ut: 5})

	featCfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
	fs := features.Build(featCfg, []m.Channel{{ID: 67}}, rows, now)
	f := fs[67]
	if f == nil || f.Long == nil || f.Short == nil || f.Chan == nil {
		t.Fatalf("missing features: %+v", f)
	}
	if f.Long.N != 34 || f.Long.Err != 11 {
		t.Fatalf("raw 60m n=%d err=%d want 34/11", f.Long.N, f.Long.Err)
	}
	rate := m.Or(f.Long.ErrRate, 0)
	if rate != m.Round(11.0/34.0, 4) {
		t.Fatalf("raw err rate=%v want rounded 11/34=0.3235", rate)
	}
	if f.Long.Empty == nil || *f.Long.Empty != 2 {
		t.Fatalf("60m empty=%v want 2", f.Long.Empty)
	}
	if f.Short.N != 28 || f.Short.Err != 11 {
		t.Fatalf("raw 10m n=%d err=%d want 28/11", f.Short.N, f.Short.Err)
	}
	if f.Chan.N != 19 || f.Chan.Err != 0 || f.Chan.Client != 11 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
		t.Fatalf("chanstat=%+v want N=19 Err=0 Client=11 Eff=0 Rate=0", f.Chan)
	}

	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{"enabled": true}}
	st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.2306}, nil)[67]
	if st == nil {
		t.Fatal("missing trust state")
	}
	if st.Sev != 1 || st.Trust != 0.1 {
		t.Fatalf("trust sev=%v value=%v want 1 / 0.1", st.Sev, st.Trust)
	}
	if len(st.Why) != 2 || st.Why[0] != "10m 错误 11/28" || st.Why[1] != "1h 错误率 32.4%" {
		t.Fatalf("trust why=%v want [10m 错误 11/28, 1h 错误率 32.4%%]", st.Why)
	}

	capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
	caps := map[int]*Cap{}
	applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: f.Long.N}}, map[int]float64{67: st.Trust}, []int{67})
	if caps[67] != nil {
		t.Fatalf("trust cap=%+v; n60=34 is not < 20 so recovery cap 3 must not be computed", caps[67])
	}

	next := map[int]int{67: 0}
	var notes []string
	Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fs, []int{67}, caps, &notes)
	if next[67] != 20 {
		t.Fatalf("weight=%d want 20; strategy 0 must be settled back without a trust cap", next[67])
	}
	joined := strings.Join(notes, ";")
	for _, part := range []string{"#67 定档：策略 0→20", "无渠道级错误证据", "1h 真实 19", "渠道错误 0", "去集中 0"} {
		if !strings.Contains(joined, part) {
			t.Fatalf("expected settle note to contain %q, got %q", part, notes)
		}
	}
	for _, part := range []string{"系统上限 3", "不执行"} {
		if strings.Contains(joined, part) {
			t.Fatalf("ample n60 must not mention held recovery cap %q, got %q", part, notes)
		}
	}
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

// CASE-AW-026: sanitized aggregate shape from 2026-11-21 10:33 +08:00 GPT #26.
// No production log rows, request identifiers, model names or real token IDs.
// When one token accounts for all channel errors but TopErr equals tok_cap (2),
// the guard TopErr > tokCap does not fire, so Eff stays Err=2. This locks
// current v5.01 ChanStats and Settle behavior; it does not change the algorithm.
func TestCasebookTopErrEqualTokCapDoesNotDiscount(t *testing.T) {
	// Live shape: N=236, Err=2, Client=0, TopErr=2, ErrTok=1, Toks=8,
	// Eff=2, Rate=2/(236-2+2)=2/236=.0085.
	successes := func(n, startTok, tokCount int) []m.Row {
		rows := make([]m.Row, 0, n)
		for i := 0; i < n; i++ {
			rows = append(rows, m.Row{Ch: 26, T: 100, Type: 2, Tok: startTok + i%tokCount})
		}
		return rows
	}
	errorsOn := func(n, tok int) []m.Row {
		rows := make([]m.Row, 0, n)
		for i := 0; i < n; i++ {
			rows = append(rows, m.Row{Ch: 26, T: 100, Type: 5, Tok: tok, Sc: m.I(502)})
		}
		return rows
	}

	t.Run("toperr_equals_tokcap_does_not_discount", func(t *testing.T) {
		rows := append(successes(234, 900002, 7), errorsOn(2, 900001)...)
		stats := features.ChanStats(m.M{}, rows, 0, 2, .8)[26]
		if stats.N != 236 || stats.Err != 2 || stats.Client != 0 || stats.Eff != 2 || stats.TopErr != 2 || stats.ErrTok != 1 || stats.Toks != 8 || stats.Rate != .0085 {
			t.Fatalf("unexpected channel evidence: %+v", stats)
		}
		if stats.Eff != stats.Err {
			t.Fatalf("discount should not fire when TopErr==tokCap, Eff=%d Err=%d", stats.Eff, stats.Err)
		}
		next := map[int]int{26: 16}
		var notes []string
		cfg := m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}
		fs := m.Feats{26: {Chan: stats, Long: &m.Window{N: 240, ErrRate: m.F(2.0 / 240)}}}
		Settle(cfg, next, map[int]int{26: 20}, fs, []int{26}, nil, &notes)
		if next[26] != 20 {
			t.Fatalf("weight=%d want 20", next[26])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"无渠道级错误证据", "1h 真实 236", "渠道错误 2", "去集中 2", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle hold note to contain %q, got %q", part, notes)
			}
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a cut note, got %q", notes)
		}
	})

	t.Run("toperr_above_tokcap_discounts_to_cap_still_holds", func(t *testing.T) {
		// Same 234 successes on other tokens, but 5 non-capacity errors on one token.
		// TopErr=5>2, share=100%, otherOK>0, so Eff=5-5+2=2.
		rows := append(successes(234, 900002, 7), errorsOn(5, 900001)...)
		stats := features.ChanStats(m.M{}, rows, 0, 2, .8)[26]
		if stats.N != 239 || stats.Err != 5 || stats.TopErr != 5 || stats.Eff != 2 || stats.Rate != .0085 {
			t.Fatalf("expected discount to tokCap, got %+v", stats)
		}
		next := map[int]int{26: 16}
		cfg := m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}
		Settle(cfg, next, map[int]int{26: 20}, m.Feats{26: {Chan: stats, Long: &m.Window{N: 239}}}, []int{26}, nil, nil)
		if next[26] != 20 {
			t.Fatalf("weight=%d want 20 after discount to Eff=2", next[26])
		}
	})

	t.Run("spread_errors_meeting_gate_are_cut", func(t *testing.T) {
		// Contrast: three spread 500s plus enough successes, no dominant token.
		rows := append(successes(27, 900002, 7),
			m.Row{Ch: 26, T: 100, Type: 5, Tok: 900001, Sc: m.I(502)},
			m.Row{Ch: 26, T: 100, Type: 5, Tok: 900002, Sc: m.I(502)},
			m.Row{Ch: 26, T: 100, Type: 5, Tok: 900003, Sc: m.I(502)},
		)
		stats := features.ChanStats(m.M{}, rows, 0, 2, .8)[26]
		if stats.N != 30 || stats.Err != 3 || stats.Eff != 3 || stats.Rate != .1 {
			t.Fatalf("expected undiscounted 3/30=.1, got %+v", stats)
		}
		next := map[int]int{26: 20}
		cfg := m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}
		Settle(cfg, next, map[int]int{26: 20}, m.Feats{26: {Chan: stats, Long: &m.Window{N: 30, ErrRate: m.F(0.1)}}}, []int{26}, nil, nil)
		if next[26] != 18 {
			t.Fatalf("weight=%d want 18 once the 30/3/3%% gate is met", next[26])
		}
	})
}

// CASE-AW-027: sanitized aggregate shape from 2026-11-21 11:20 +08:00 CC #72.
// No production log rows, request identifiers, model names or real token IDs.
// Locks current v5.01 behavior: a single client-class error in a thin scoring
// window can push the raw 1h error rate over the 2% trust threshold and cut
// trust, while ChanStat excludes that error and Settle holds the weight.
// Strategy already at 20 produces no settle note. It does not change the algorithm.
func TestCasebookThinWindowClientErrorCutsTrustButSettleHolds(t *testing.T) {
	now := int64(100000)
	build := func(status *int, extraSuccess bool) m.Feats {
		t.Helper()
		var rows []m.Row
		// 12 real successes in the last 10m (10m stays error-free).
		for n := 0; n < 12; n++ {
			rows = append(rows, m.Row{Ch: 72, T: now - 30, Type: 2, Tok: 900003})
		}
		if extraSuccess {
			rows = append(rows, m.Row{Ch: 72, T: now - 30, Type: 2, Tok: 900004})
		}
		if status != nil {
			// One fault inside the 60m window but outside the 10m window.
			rows = append(rows, m.Row{Ch: 72, T: now - 40*60, Type: 5, Tok: 900010, Sc: status})
		}
		for n := 0; n < 4; n++ {
			rows = append(rows, m.Row{Ch: 72, T: now - 10, Type: 2, Tok: 1318})
		}
		cfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
		return features.Build(cfg, []m.Channel{{ID: 72}}, rows, now)
	}

	settleCfg := m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{"enabled": true}}
	prev := 0.8824

	t.Run("client_400_cuts_trust_but_weight_holds", func(t *testing.T) {
		fs := build(m.I(400), false)
		f := fs[72]
		if f == nil || f.Long == nil || f.Chan == nil {
			t.Fatalf("missing features: %+v", f)
		}
		if f.Long.N != 17 || f.Long.Err != 1 {
			t.Fatalf("raw window n=%d err=%d want 17/1", f.Long.N, f.Long.Err)
		}
		rate := m.Or(f.Long.ErrRate, 0)
		if rate != m.Round(1.0/17.0, 4) {
			t.Fatalf("raw err rate=%v want rounded 1/17=0.0588", rate)
		}
		if f.Chan.N != 12 || f.Chan.Err != 0 || f.Chan.Client != 1 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
			t.Fatalf("chanstat=%+v want N=12 Err=0 Client=1 Eff=0 Rate=0", f.Chan)
		}

		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: prev}, nil)[72]
		if st == nil {
			t.Fatal("missing trust state")
		}
		if st.Sev != 0.194 || st.Trust != 0.7797 {
			t.Fatalf("trust sev=%v value=%v want 0.194 / 0.7797", st.Sev, st.Trust)
		}
		if len(st.Why) != 1 || st.Why[0] != "1h 错误率 5.9%" {
			t.Fatalf("trust why=%v want [1h 错误率 5.9%%]", st.Why)
		}

		next := map[int]int{72: 20}
		var notes []string
		Settle(settleCfg, next, map[int]int{72: 20}, fs, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20 when already at 20", next[72])
		}
		if len(notes) != 0 {
			t.Fatalf("expected no settle note when strategy is already 20, got %q", notes)
		}

		next = map[int]int{72: 16}
		notes = nil
		Settle(settleCfg, next, map[int]int{72: 20}, fs, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20 after 16\u219220 hold", next[72])
		}
		joined := strings.Join(notes, ";")
		if !strings.Contains(joined, "1h 真实 12") || !strings.Contains(joined, "渠道错误 0") || !strings.Contains(joined, "去集中 0") {
			t.Fatalf("expected hold note with ChanStat 12/0/0, got %q", notes)
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault-gate cut, got %q", notes)
		}
	})

	t.Run("channel_500_keeps_same_raw_trust_rate", func(t *testing.T) {
		// Contrast: the same raw 1/17 window still drives trust, even though
		// ChanStat now counts a channel error and a different rate (1/13).
		fs := build(m.I(500), false)
		f := fs[72]
		if f.Long.N != 17 || f.Long.Err != 1 || m.Or(f.Long.ErrRate, 0) != m.Round(1.0/17.0, 4) {
			t.Fatalf("raw window=%+v want 17/1 rate 0.0588", f.Long)
		}
		if f.Chan.N != 13 || f.Chan.Err != 1 || f.Chan.Client != 0 || f.Chan.Eff != 1 {
			t.Fatalf("chanstat=%+v want N=13 Err=1 Client=0 Eff=1", f.Chan)
		}
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: prev}, nil)[72]
		if st.Sev != 0.194 || st.Trust != 0.7797 || len(st.Why) != 1 || st.Why[0] != "1h 错误率 5.9%" {
			t.Fatalf("trust should still follow raw 1/17, got sev=%v value=%v why=%v", st.Sev, st.Trust, st.Why)
		}
		next := map[int]int{72: 16}
		var notes []string
		Settle(settleCfg, next, map[int]int{72: 20}, fs, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; N=13 is still below 30", next[72])
		}
		joined := strings.Join(notes, ";")
		if !strings.Contains(joined, "1h 真实 13") || !strings.Contains(joined, "渠道错误 1") || !strings.Contains(joined, "去集中 1") {
			t.Fatalf("expected hold note with ChanStat 13/1/1, got %q", notes)
		}
	})

	t.Run("clean_thin_window_recovers_trust", func(t *testing.T) {
		// Same scoring-window size, no errors: recovery is not blocked.
		fs := build(nil, true)
		f := fs[72]
		if f.Long.N != 17 || f.Long.Err != 0 || f.Chan.N != 13 || f.Chan.Err != 0 || f.Chan.Client != 0 {
			t.Fatalf("clean window long=%+v chan=%+v want n60=17 err=0, chan N=13", f.Long, f.Chan)
		}
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: prev}, nil)[72]
		if st.Sev != 0 || st.Trust != 0.9277 || len(st.Why) != 0 {
			t.Fatalf("trust sev=%v value=%v why=%v want 0 / 0.9277 / empty", st.Sev, st.Trust, st.Why)
		}
	})
}

// CASE-AW-028: sanitized aggregate shape from 2026-11-21 12:50 +08:00 GPT #2/#32.
// No production log rows, request identifiers, model names or real token IDs.
// Locks current v5.01 trust 10m semantics; it does not change the algorithm.
// A single 10m error in a non-tiny window (N>=err_10m_min_n=2) cuts trust with
// no 2% rate threshold, even when the 1h raw rate stays <=2% and ChanStat has
// 0 channel errors. Weight still holds because 30/3/3% is not met.
// A later addendum (2026-11-21 13:30 +08:00) locks the same rule for two 10m
// errors (2/57 and 2/51). The original "single error" wording does not itself
// cover two errors; the threshold-free 10m path still applies.
func TestCasebookNonTinyTenMinSingleErrorCutsTrustButSettleHolds(t *testing.T) {
	now := int64(100000)
	build := func(shortSuccess, shortErr int, longSuccess, longErr, probes int, status *int) m.Feats {
		t.Helper()
		var rows []m.Row
		for n := 0; n < shortSuccess; n++ {
			rows = append(rows, m.Row{Ch: 2, T: now - 30, Type: 2, Tok: 900003})
		}
		for n := 0; n < shortErr; n++ {
			rows = append(rows, m.Row{Ch: 2, T: now - 30, Type: 5, Tok: 900010, Sc: status})
		}
		for n := 0; n < longSuccess; n++ {
			rows = append(rows, m.Row{Ch: 2, T: now - 40*60, Type: 2, Tok: 900004})
		}
		for n := 0; n < longErr; n++ {
			rows = append(rows, m.Row{Ch: 2, T: now - 40*60, Type: 5, Tok: 900011, Sc: status})
		}
		for n := 0; n < probes; n++ {
			// Probes sit in the 60m window but outside the 10m window, matching
			// the observed 10m N that did not include the 4 probes.
			rows = append(rows, m.Row{Ch: 2, T: now - 40*60, Type: 2, Tok: 1318})
		}
		cfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
		return features.Build(cfg, []m.Channel{{ID: 2}}, rows, now)
	}

	settleCfg := m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{"enabled": true}}
	prev := 1.0

	t.Run("ten_min_1_of_56_cuts_trust_while_1h_stays_under_2pct", func(t *testing.T) {
		// GPT #2 this round: 10m 1/56, 60m 2/277, mix 273 user + 4 probes,
		// ChanStat N=271 Client=2 Err=0. 1h raw 2/277=0.72% is below 2%.
		fs := build(55, 1, 216, 1, 4, m.I(400))
		f := fs[2]
		if f == nil || f.Long == nil || f.Short == nil || f.Chan == nil {
			t.Fatalf("missing features: %+v", f)
		}
		if f.Short.N != 56 || f.Short.Err != 1 {
			t.Fatalf("10m window n=%d err=%d want 56/1", f.Short.N, f.Short.Err)
		}
		if f.Long.N != 277 || f.Long.Err != 2 {
			t.Fatalf("1h window n=%d err=%d want 277/2", f.Long.N, f.Long.Err)
		}
		if m.Or(f.Long.ErrRate, 0) > 0.02 {
			t.Fatalf("1h err rate=%v should stay at or under 2%%", m.Or(f.Long.ErrRate, 0))
		}
		if f.Chan.N != 271 || f.Chan.Err != 0 || f.Chan.Client != 2 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
			t.Fatalf("chanstat=%+v want N=271 Err=0 Client=2 Eff=0 Rate=0", f.Chan)
		}

		st := trust.Step(trustCfg, []int{2}, fs, nil, map[int]float64{2: prev}, nil)[2]
		if st == nil {
			t.Fatal("missing trust state")
		}
		if st.Sev != 0.089 || st.Trust != 0.9466 {
			t.Fatalf("trust sev=%v value=%v want 0.089 / 0.9466", st.Sev, st.Trust)
		}
		if len(st.Why) != 1 || st.Why[0] != "10m 错误 1/56" {
			t.Fatalf("trust why=%v want [10m 错误 1/56]", st.Why)
		}

		next := map[int]int{2: 3}
		var notes []string
		Settle(settleCfg, next, map[int]int{2: 20}, fs, []int{2}, nil, &notes)
		if next[2] != 20 {
			t.Fatalf("weight=%d want 20 after 3\u219220 hold", next[2])
		}
		joined := strings.Join(notes, ";")
		if !strings.Contains(joined, "1h 真实 271") || !strings.Contains(joined, "渠道错误 0") || !strings.Contains(joined, "去集中 0") {
			t.Fatalf("expected hold note with ChanStat 271/0/0, got %q", notes)
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault-gate cut, got %q", notes)
		}
	})

	t.Run("ten_min_1_of_63_same_rule", func(t *testing.T) {
		// GPT #32 this round: 10m 1/63, 60m 1/117, mix 113 user + 4 probes,
		// ChanStat N=112 Client=1 Err=0. 1h raw 1/117=0.85% is below 2%.
		fs := build(62, 1, 50, 0, 4, m.I(400))
		f := fs[2]
		if f.Short.N != 63 || f.Short.Err != 1 || f.Long.N != 117 || f.Long.Err != 1 {
			t.Fatalf("windows short=%+v long=%+v want 63/1 and 117/1", f.Short, f.Long)
		}
		if m.Or(f.Long.ErrRate, 0) > 0.02 {
			t.Fatalf("1h err rate=%v should stay at or under 2%%", m.Or(f.Long.ErrRate, 0))
		}
		if f.Chan.N != 112 || f.Chan.Err != 0 || f.Chan.Client != 1 || f.Chan.Eff != 0 {
			t.Fatalf("chanstat=%+v want N=112 Err=0 Client=1 Eff=0", f.Chan)
		}
		st := trust.Step(trustCfg, []int{2}, fs, nil, map[int]float64{2: prev}, nil)[2]
		if st.Sev != 0.08 || st.Trust != 0.952 || len(st.Why) != 1 || st.Why[0] != "10m 错误 1/63" {
			t.Fatalf("trust sev=%v value=%v why=%v want 0.08 / 0.952 / [10m 错误 1/63]", st.Sev, st.Trust, st.Why)
		}
		next := map[int]int{2: 20}
		var notes []string
		Settle(settleCfg, next, map[int]int{2: 20}, fs, []int{2}, nil, &notes)
		if next[2] != 20 || len(notes) != 0 {
			t.Fatalf("already-at-20 should hold without a note, weight=%d notes=%q", next[2], notes)
		}
	})

	t.Run("channel_500_keeps_same_10m_trust", func(t *testing.T) {
		// Contrast: raw 10m 1/56 still drives trust, even though ChanStat now
		// counts the two type5 rows as channel errors (still below 30/3/3%).
		fs := build(55, 1, 216, 1, 4, m.I(500))
		f := fs[2]
		if f.Short.N != 56 || f.Short.Err != 1 || f.Long.N != 277 || f.Long.Err != 2 {
			t.Fatalf("raw windows short=%+v long=%+v want 56/1 and 277/2", f.Short, f.Long)
		}
		if f.Chan.N != 273 || f.Chan.Err != 2 || f.Chan.Client != 0 || f.Chan.Eff != 2 {
			t.Fatalf("chanstat=%+v want N=273 Err=2 Client=0 Eff=2", f.Chan)
		}
		st := trust.Step(trustCfg, []int{2}, fs, nil, map[int]float64{2: prev}, nil)[2]
		if st.Sev != 0.089 || st.Trust != 0.9466 || len(st.Why) != 1 || st.Why[0] != "10m 错误 1/56" {
			t.Fatalf("trust should still follow raw 10m 1/56, got sev=%v value=%v why=%v", st.Sev, st.Trust, st.Why)
		}
		next := map[int]int{2: 3}
		var notes []string
		Settle(settleCfg, next, map[int]int{2: 20}, fs, []int{2}, nil, &notes)
		if next[2] != 20 {
			t.Fatalf("weight=%d want 20; Eff=2 is still below 3", next[2])
		}
		joined := strings.Join(notes, ";")
		if !strings.Contains(joined, "1h 真实 273") || !strings.Contains(joined, "渠道错误 2") || !strings.Contains(joined, "去集中 2") {
			t.Fatalf("expected hold note with ChanStat 273/2/2, got %q", notes)
		}
	})

	t.Run("ten_min_n1_does_not_fire", func(t *testing.T) {
		// err_10m_min_n defaults to 2: a lone 10m error must not emit the 10m
		// note when 1h also stays at or under 2%.
		fs := build(0, 1, 99, 0, 0, m.I(400))
		f := fs[2]
		if f.Short.N != 1 || f.Short.Err != 1 {
			t.Fatalf("10m window n=%d err=%d want 1/1", f.Short.N, f.Short.Err)
		}
		if f.Long.N != 100 || f.Long.Err != 1 || m.Or(f.Long.ErrRate, 0) > 0.02 {
			t.Fatalf("1h window=%+v want 100/1 at or under 2%%", f.Long)
		}
		st := trust.Step(trustCfg, []int{2}, fs, nil, map[int]float64{2: prev}, nil)[2]
		if st.Sev != 0 || st.Trust != 1 || len(st.Why) != 0 {
			t.Fatalf("trust sev=%v value=%v why=%v want 0 / 1 / empty", st.Sev, st.Trust, st.Why)
		}
	})

	t.Run("ten_min_2_of_57_same_rule", func(t *testing.T) {
		// Later-round GPT #23 shape: 10m 2/57, 60m 3/364, mix 360 user + 4
		// probes, ChanStat N=357 Client=3 Err=0. 1h raw 3/364=0.82% is below
		// 2%. The original "single error" wording does not itself cover two
		// 10m errors; the same no-2%-threshold path still cuts trust.
		fs := build(55, 2, 302, 1, 4, m.I(400))
		f := fs[2]
		if f.Short.N != 57 || f.Short.Err != 2 || f.Long.N != 364 || f.Long.Err != 3 {
			t.Fatalf("windows short=%+v long=%+v want 57/2 and 364/3", f.Short, f.Long)
		}
		if m.Or(f.Long.ErrRate, 0) > 0.02 {
			t.Fatalf("1h err rate=%v should stay at or under 2%%", m.Or(f.Long.ErrRate, 0))
		}
		if f.Chan.N != 357 || f.Chan.Err != 0 || f.Chan.Client != 3 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
			t.Fatalf("chanstat=%+v want N=357 Err=0 Client=3 Eff=0 Rate=0", f.Chan)
		}
		st := trust.Step(trustCfg, []int{2}, fs, nil, map[int]float64{2: 1}, nil)[2]
		if st.Sev != 0.175 || st.Trust != 0.895 || len(st.Why) != 1 || st.Why[0] != "10m 错误 2/57" {
			t.Fatalf("trust sev=%v value=%v why=%v want 0.175 / 0.895 / [10m 错误 2/57]", st.Sev, st.Trust, st.Why)
		}
		next := map[int]int{2: 3}
		var notes []string
		Settle(settleCfg, next, map[int]int{2: 20}, fs, []int{2}, nil, &notes)
		if next[2] != 20 {
			t.Fatalf("weight=%d want 20 after 3\u219220 hold", next[2])
		}
		joined := strings.Join(notes, ";")
		if !strings.Contains(joined, "1h 真实 357") || !strings.Contains(joined, "渠道错误 0") || !strings.Contains(joined, "去集中 0") {
			t.Fatalf("expected hold note with ChanStat 357/0/0, got %q", notes)
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault-gate cut, got %q", notes)
		}
	})

	t.Run("ten_min_2_of_51_same_rule", func(t *testing.T) {
		// Later-round GPT #73 shape: 10m 2/51, 60m 4/304, mix 300 user + 4
		// probes, ChanStat N=296 Client=4 Err=0. 1h raw 4/304=1.32% is below
		// 2%. prev=0.9664 yields 0.8528 at sev=0.196.
		fs := build(49, 2, 247, 2, 4, m.I(400))
		f := fs[2]
		if f.Short.N != 51 || f.Short.Err != 2 || f.Long.N != 304 || f.Long.Err != 4 {
			t.Fatalf("windows short=%+v long=%+v want 51/2 and 304/4", f.Short, f.Long)
		}
		if m.Or(f.Long.ErrRate, 0) > 0.02 {
			t.Fatalf("1h err rate=%v should stay at or under 2%%", m.Or(f.Long.ErrRate, 0))
		}
		if f.Chan.N != 296 || f.Chan.Err != 0 || f.Chan.Client != 4 || f.Chan.Eff != 0 {
			t.Fatalf("chanstat=%+v want N=296 Err=0 Client=4 Eff=0", f.Chan)
		}
		st := trust.Step(trustCfg, []int{2}, fs, nil, map[int]float64{2: 0.9664}, nil)[2]
		if st.Sev != 0.196 || st.Trust != 0.8528 || len(st.Why) != 1 || st.Why[0] != "10m 错误 2/51" {
			t.Fatalf("trust sev=%v value=%v why=%v want 0.196 / 0.8528 / [10m 错误 2/51]", st.Sev, st.Trust, st.Why)
		}
		next := map[int]int{2: 0}
		var notes []string
		Settle(settleCfg, next, map[int]int{2: 20}, fs, []int{2}, nil, &notes)
		if next[2] != 20 {
			t.Fatalf("weight=%d want 20 after 0\u219220 hold", next[2])
		}
		joined := strings.Join(notes, ";")
		if !strings.Contains(joined, "1h 真实 296") || !strings.Contains(joined, "渠道错误 0") || !strings.Contains(joined, "去集中 0") {
			t.Fatalf("expected hold note with ChanStat 296/0/0, got %q", notes)
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault-gate cut, got %q", notes)
		}
	})
}

// CASE-AW-029: sanitized aggregate shape from 2026-11-21 13:30 +08:00 CC #72/#68/#67.
// No production log rows, request identifiers, model names or real token IDs.
// Locks current v5.01 behavior: an ample 1h scoring window with slow_rate about
// 22% applies the fixed slow-trust penalty (sev=0.3), while ChanStat has 0
// channel errors and Settle holds 20 because 30/3/3% is not met. HANGTAIL is
// diagnostic-only (see TestHangTailFlagsAmpleSlowWindowStillRecordsOnly).
// Distinct from CASE-AW-019 (10m SlowBurst multiplies the score only),
// CASE-AW-020 (1h slow-rate trust cut with zero real users) and CASE-AW-024
// (same trust path on thin but non-zero real traffic). This is not a fault
// gate, not CASE-AW-025 (all ineligible) and not CASE-AW-016 (promotion
// because the leader is ineligible). Live raw scores already had the thinner
// eligible peer ahead; the trust multiplier (floor 0.85) only trims further.
// It does not change the algorithm.
func TestCasebookAmpleSlowRateCutsTrustButSettleHolds(t *testing.T) {
	// Live v5.01 trust config uses up=0.2 (code default is 0.08). down,
	// slow threshold and recovery knobs match Defaults / the example file so
	// the 0.9797->0.8034 arithmetic does not silently depend on a different
	// default. floor=0.85 is the live multiplier floor used in the rank test.
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
		"slow_thr": 0.15, "slow_sev": 0.3, "floor": 0.85,
	}}
	settleCfg := m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}
	// Inter-round gap is the difference of CC algorithm now values: 2440s.
	gap := m.F(2440.0 / 60.0)
	live72 := func() m.Feats {
		return m.Feats{
			72: {
				Long:  &m.Window{N: 127, Err: 0, ErrRate: m.F(0), SlowRate: m.F(0.2205), P50: m.F(15), P90: m.F(120)},
				Short: &m.Window{N: 29, Err: 0, ErrRate: m.F(0), SlowRate: m.F(0.3448), P50: m.F(30), P90: m.F(191)},
				Mix:   &m.Mix{NUser: 123, NProbe: 4, NErrReal: 0, ErrRateReal: m.F(0)},
				Chan:  &m.ChanStat{N: 123, Err: 0, Client: 0, Eff: 0, Rate: 0},
			},
		}
	}

	t.Run("live_shape_penalizes_trust_0_9797_to_0_8034", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, live72(), nil, map[int]float64{72: 0.9797}, gap)[72]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// N=127>=slow_min_n and SlowRate=22.05%>15% => fixed sev=0.3.
		// Update takes the down branch: 0.9797*(1-0.6*0.3)=0.803354 -> 0.8034.
		// "%.0f" of 22.05 is 22, so the note is "1h 慢请求 22%".
		if st.Sev != 0.3 || st.Trust != 0.8034 {
			t.Fatalf("trust sev=%v value=%v want 0.3 / 0.8034", st.Sev, st.Trust)
		}
		if !strings.Contains(strings.Join(st.Why, ";"), "1h 慢请求 22%") {
			t.Fatalf("expected slow-rate trust note, got %q", st.Why)
		}
	})

	t.Run("ample_clean_chanstat_does_not_block_penalty", func(t *testing.T) {
		fs := live72()
		fs[72].Chan = &m.ChanStat{N: 123, Err: 0, Client: 0, Eff: 0, Rate: 0}
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.9797}, gap)[72]
		if st.Sev != 0.3 || st.Trust != 0.8034 {
			t.Fatalf("trust still reads the scoring window, sev=%v value=%v", st.Sev, st.Trust)
		}
	})

	t.Run("ten_min_slow_alone_does_not_drive_trust", func(t *testing.T) {
		// Contrast with CASE-AW-019: 10m slow_rate is not a trust input.
		fs := live72()
		fs[72].Long.SlowRate = m.F(0.15)
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.9797}, gap)[72]
		if st.Sev != 0 || strings.Contains(strings.Join(st.Why, ";"), "慢请求") {
			t.Fatalf("1h slow_rate=15%% must not penalize even if 10m is 34.48%%, sev=%v why=%v", st.Sev, st.Why)
		}
	})

	t.Run("at_or_below_slow_threshold_does_not_penalize", func(t *testing.T) {
		for _, rate := range []float64{0.15, 0.14} {
			fs := live72()
			fs[72].Long.SlowRate = m.F(rate)
			st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.9797}, gap)[72]
			if st.Sev != 0 || strings.Contains(strings.Join(st.Why, ";"), "慢请求") {
				t.Fatalf("slow_rate=%v must not penalize, sev=%v why=%v", rate, st.Sev, st.Why)
			}
		}
	})

	t.Run("settle_holds_20_from_strategy_0", func(t *testing.T) {
		fs := live72()
		next := map[int]int{72: 0}
		var notes []string
		Settle(settleCfg, next, map[int]int{72: 20}, fs, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20 after settle hold; notes=%q", next[72], notes)
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"定档：策略 0→20", "无渠道级错误证据", "1h 真实 123", "渠道错误 0", "去集中 0", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle hold note to contain %q, got %q", part, notes)
			}
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault cut, got %q", notes)
		}
	})

	t.Run("strategy_already_20_has_no_settle_note", func(t *testing.T) {
		fs := live72()
		next := map[int]int{72: 20}
		var notes []string
		Settle(settleCfg, next, map[int]int{72: 20}, fs, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[72], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("strategy already 20 should not emit a settle note, got %q", notes)
		}
	})

	t.Run("thinner_eligible_peer_leads_and_both_hold_20", func(t *testing.T) {
		// Sanitized relative-score shape using the public example latency
		// weights and empty_weight=2, plus the live trust floor of 0.85.
		// This is enough to keep the observed order and settle notes; it is
		// not a claim that every live score knob is reproduced exactly.
		cfg := m.M{
			"windows_min": []any{60.0, 10.0},
			"strategy":    "rank",
			"min_n_rank1": 10.0,
			"w_min":       0.0,
			"w_max":       20.0,
			"trust":       m.M{"enabled": true, "floor": 0.85},
			"score": m.M{
				"a_p50": 0.3, "b_p90": 0.3, "c_ttft": 1.5, "t_ref": 20.0,
				"min_n_window": 5.0, "empty_weight": 2.0, "w_10m": 0.4, "ttft_missing_s": 30.0,
			},
			"strategies": m.M{"rank": m.M{"top_w": 20.0, "close_ratio": 0.85, "close_factor": 0.8, "mid_ratio": 0.7, "mid_w": 3.0, "low_w": 0.0, "hysteresis": 0.0, "band": 0.0, "ramp": 0.0, "lead_min_n": 0.0}},
			"settle":     m.M{"cut_needs_fault": true, "raise_clean": false},
		}
		fs := m.Feats{
			67: {
				Long:  &m.Window{N: 6, Err: 0, ErrRate: m.F(0), Empty: m.I(2), EmptyRate: m.F(0.3333), SlowRate: m.F(0), P50: m.F(3), P90: m.F(53)},
				Short: &m.Window{N: 3, Err: 0, ErrRate: m.F(0), Empty: m.I(1), EmptyRate: m.F(0.3333), P50: m.F(5), P90: m.F(53)},
				Mix:   &m.Mix{NUser: 2, NProbe: 4, NErrReal: 0, ErrRateReal: m.F(0)},
				Chan:  &m.ChanStat{N: 2, Err: 0, Client: 0, Eff: 0, Rate: 0},
			},
			68: {
				Long:  &m.Window{N: 20, Err: 0, ErrRate: m.F(0), SlowRate: m.F(0), P50: m.F(12), P90: m.F(39)},
				Short: &m.Window{N: 1, Err: 0, ErrRate: m.F(0), P50: m.F(2), P90: m.F(2)},
				Mix:   &m.Mix{NUser: 16, NProbe: 4, NErrReal: 0, ErrRateReal: m.F(0)},
				Chan:  &m.ChanStat{N: 16, Err: 0, Client: 0, Eff: 0, Rate: 0},
			},
			72: live72()[72],
		}
		d, err := Decide(Input{
			Cfg:      cfg,
			Chans:    []m.Channel{{ID: 67}, {ID: 68}, {ID: 72}},
			Feats:    fs,
			Strategy: "rank",
			Prev:     map[int]int{67: 20, 68: 20, 72: 20},
			Trust:    map[int]float64{67: 0.7802, 68: 1, 72: 0.8034},
		})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if !d.Scores[68].EligibleRank1 || !d.Scores[72].EligibleRank1 || d.Scores[67].EligibleRank1 {
			t.Fatalf("eligibility=%v want #68/#72 true and #67 false", d.Scores)
		}
		if d.Scores[68].N60 != 20 || d.Scores[72].N60 != 127 || d.Scores[67].N60 != 6 {
			t.Fatalf("n60 unexpected: 68=%d 72=%d 67=%d", d.Scores[68].N60, d.Scores[72].N60, d.Scores[67].N60)
		}
		if d.Scores[68].Trust == nil || *d.Scores[68].Trust != 1 || d.Scores[72].Trust == nil || *d.Scores[72].Trust != 0.8034 {
			t.Fatalf("trust fields=%v", d.Scores)
		}
		// Raw scores already have #68 ahead of ample #72 (latency + 1-slow).
		// The floor=0.85 multiplier only trims #72 further; it is not what
		// creates the order by itself.
		if d.Scores[68].RawScore == nil || d.Scores[72].RawScore == nil || *d.Scores[68].RawScore <= *d.Scores[72].RawScore {
			t.Fatalf("raw scores should already rank #68 ahead, got 68=%v 72=%v", d.Scores[68].RawScore, d.Scores[72].RawScore)
		}
		if d.Scores[72].Score >= *d.Scores[72].RawScore {
			t.Fatalf("#72 trust multiplier should trim the score, raw=%v score=%v", *d.Scores[72].RawScore, d.Scores[72].Score)
		}
		if got := d.Order; len(got) != 3 || got[0] != 68 || got[1] != 72 || got[2] != 67 {
			t.Fatalf("order=%v want [68 72 67] without promoting an ineligible leader", d.Order)
		}
		if d.Target[67] != 20 || d.Target[68] != 20 || d.Target[72] != 20 {
			t.Fatalf("weights=%v want all held at 20", d.Target)
		}
		joined := strings.Join(d.Notes, ";")
		if strings.Contains(joined, "不得排第一") {
			t.Fatalf("did not expect a CASE-AW-016 promotion note when the leader is eligible, got %q", d.Notes)
		}
		if strings.Contains(joined, "#68 定档") {
			t.Fatalf("eligible leader already at 20 should not emit a settle note, got %q", d.Notes)
		}
		if !strings.Contains(joined, "#72 定档：策略 0→20") || !strings.Contains(joined, "1h 真实 123") {
			t.Fatalf("expected #72 low band settled 0→20 on ChanStat 123/0/0, got %q", d.Notes)
		}
		if !strings.Contains(joined, "#67 定档：策略 0→20") || !strings.Contains(joined, "1h 真实 2") {
			t.Fatalf("expected #67 low band settled 0→20 on ChanStat 2/0/0, got %q", d.Notes)
		}
		for _, part := range []string{"无渠道级错误证据", "渠道错误 0", "去集中 0", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle hold note to contain %q, got %q", part, d.Notes)
			}
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault cut, got %q", d.Notes)
		}
	})
}

// CASE-AW-032: sanitized aggregate shape from 2026-11-21 17:20 +08:00 CC #67.
// No production log rows, request identifiers, model names or real token IDs.
// After CASE-AW-031 already floored trust at 0.1, the 10m window is now clean
// (0/40) but the raw 1h scoring window is still 20/197=10.15% (noted 10.2%),
// all of it client-class. Trust therefore stays at the 0.1 floor instead of
// recovering. n60=197 >= 20 so the recovery-period cap of 3 is still not
// computed. ChanStat is N=173, Err=0, Client=20, Eff=0 and does not meet
// 30/3/3%, so a strategy target of 0 is settled back to 20; if the channel is
// already rank-1 at 20, there is no settle note. This locks current v5.01
// behavior; it does not change the algorithm.
func TestCasebookCleanTenMinDoesNotLiftTrustFloorWhileHourStaysHigh(t *testing.T) {
	now := int64(100000)
	var rows []m.Row
	// 10m window (40): 39 real successes and 1 empty type2, no errors.
	for n := 0; n < 39; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 30, Type: 2, Tok: 900100 + n, Ct: 300, Ut: 5})
	}
	rows = append(rows, m.Row{Ch: 67, T: now - 30, Type: 2, Tok: 900200, Ct: 0, Ut: 5})
	// Older than 10m, still inside 60m.
	for n := 0; n < 4; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 1318, Ct: 300, Ut: 5})
	}
	for n := 0; n < 2; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 900201 + n, Ct: 0, Ut: 5})
	}
	for n := 0; n < 131; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 900300 + n, Ct: 300, Ut: 5})
	}
	for n := 0; n < 20; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 5, Tok: 900500 + n, Sc: m.I(400)})
	}

	featCfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
	fs := features.Build(featCfg, []m.Channel{{ID: 67}}, rows, now)
	f := fs[67]
	if f == nil || f.Long == nil || f.Short == nil || f.Chan == nil || f.Mix == nil {
		t.Fatalf("missing features: %+v", f)
	}
	if f.Long.N != 197 || f.Long.Err != 20 {
		t.Fatalf("raw 60m n=%d err=%d want 197/20", f.Long.N, f.Long.Err)
	}
	rate := m.Or(f.Long.ErrRate, 0)
	if rate != m.Round(20.0/197.0, 4) {
		t.Fatalf("raw err rate=%v want rounded 20/197=0.1015", rate)
	}
	if f.Long.Empty == nil || *f.Long.Empty != 3 {
		t.Fatalf("60m empty=%v want 3", f.Long.Empty)
	}
	if f.Short.N != 40 || f.Short.Err != 0 {
		t.Fatalf("raw 10m n=%d err=%d want 40/0", f.Short.N, f.Short.Err)
	}
	if f.Mix.NUser != 193 || f.Mix.NProbe != 4 || f.Mix.NErrReal != 20 {
		t.Fatalf("mix=%+v want NUser=193 NProbe=4 NErrReal=20", f.Mix)
	}
	if f.Chan.N != 173 || f.Chan.Err != 0 || f.Chan.Client != 20 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
		t.Fatalf("chanstat=%+v want N=173 Err=0 Client=20 Eff=0 Rate=0", f.Chan)
	}

	// Live v5.01 trust config uses up=0.2 (code default is 0.08).
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2776s.
	gap := m.F(2776.0 / 60.0)

	t.Run("live_shape_holds_floor_despite_clean_10m", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.1}, gap)[67]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=(0.1015-0.02)/0.2=0.4075 -> 0.407; Update takes the down branch
		// because sev>0, then clamps to the 0.1 floor.
		if st.Sev != 0.407 || st.Trust != 0.1 {
			t.Fatalf("trust sev=%v value=%v want 0.407 / 0.1", st.Sev, st.Trust)
		}
		if len(st.Why) != 1 || st.Why[0] != "1h 错误率 10.2%" {
			t.Fatalf("trust why=%v want [1h 错误率 10.2%%]", st.Why)
		}
	})

	t.Run("clean_1h_would_recover_to_0_3", func(t *testing.T) {
		clean := m.Feats{67: {
			Long:  &m.Window{N: 197, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 40, Err: 0, ErrRate: m.F(0)},
			Mix:   f.Mix,
			Chan:  f.Chan,
		}}
		st := trust.Step(trustCfg, []int{67}, clean, nil, map[int]float64{67: 0.1}, gap)[67]
		// 0.1 + 0.2*min(1, 2776/60/45)*min(1, 197/30) = 0.3
		if st.Sev != 0 || st.Trust != 0.3 {
			t.Fatalf("clean 1h must recover, sev=%v value=%v want 0 / 0.3", st.Sev, st.Trust)
		}
	})

	t.Run("no_recovery_cap_when_n60_already_ample", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.1}, gap)[67]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: f.Long.N}}, map[int]float64{67: st.Trust}, []int{67})
		if caps[67] != nil {
			t.Fatalf("trust cap=%+v; n60=197 is not < 20 so recovery cap 3 must not be computed", caps[67])
		}
	})

	t.Run("settle_holds_0_to_20_without_trust_cap", func(t *testing.T) {
		next := map[int]int{67: 0}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fs, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20; strategy 0 must be settled back without a trust cap", next[67])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#67 定档：策略 0→20", "无渠道级错误证据", "1h 真实 173", "渠道错误 0", "去集中 0"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle note to contain %q, got %q", part, notes)
			}
		}
		for _, part := range []string{"系统上限 3", "不执行"} {
			if strings.Contains(joined, part) {
				t.Fatalf("ample n60 must not mention held recovery cap %q, got %q", part, notes)
			}
		}
	})

	t.Run("already_20_has_no_settle_note", func(t *testing.T) {
		next := map[int]int{67: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fs, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[67], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("rank-1 already at 20 should not emit a settle note, got %q", notes)
		}
	})
}

// CASE-AW-033: the next window after CASE-AW-032. CC #67's 1h raw error rate
// has already fallen below 2% (2/232=0.86%, all client-class in ChanStat),
// but a non-tiny 10m window still has a single error (1/78). SeverityParts
// takes only that 10m path (sev=(0.0128)/0.2=0.064). Because sev>0, Update
// takes the down branch and the 0.1 floor holds; it does not recover along
// the 2633s gap. n60=232 >= 20 so the recovery-period cap of 3 is not
// computed. ChanStat is N=226, Err=0, Client=2, Eff=0 and does not meet
// 30/3/3%, so a strategy target of 16 is settled back to 20; if the channel
// is already at 20, there is no settle note. This locks current v5.01
// behavior; it does not change the algorithm.
func TestCasebookTenMinSingleErrorPinsTrustFloorAfterHourDropsBelow2pct(t *testing.T) {
	now := int64(100000)
	var rows []m.Row
	// 10m window (78): 76 real successes, 1 empty type2, 1 client-class type5.
	for n := 0; n < 76; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 30, Type: 2, Tok: 900100 + n, Ct: 300, Ut: 5})
	}
	rows = append(rows, m.Row{Ch: 67, T: now - 30, Type: 2, Tok: 900200, Ct: 0, Ut: 5})
	rows = append(rows, m.Row{Ch: 67, T: now - 30, Type: 5, Tok: 900500, Sc: m.I(400)})
	// Older than 10m, still inside 60m.
	for n := 0; n < 4; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 1318, Ct: 300, Ut: 5})
	}
	for n := 0; n < 4; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 900201 + n, Ct: 0, Ut: 5})
	}
	for n := 0; n < 145; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 900300 + n, Ct: 300, Ut: 5})
	}
	rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 5, Tok: 900501, Sc: m.I(400)})

	featCfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
	fs := features.Build(featCfg, []m.Channel{{ID: 67}}, rows, now)
	f := fs[67]
	if f == nil || f.Long == nil || f.Short == nil || f.Chan == nil || f.Mix == nil {
		t.Fatalf("missing features: %+v", f)
	}
	if f.Long.N != 232 || f.Long.Err != 2 {
		t.Fatalf("raw 60m n=%d err=%d want 232/2", f.Long.N, f.Long.Err)
	}
	rate := m.Or(f.Long.ErrRate, 0)
	if rate != m.Round(2.0/232.0, 4) {
		t.Fatalf("raw err rate=%v want rounded 2/232=0.0086", rate)
	}
	if rate >= 0.02 {
		t.Fatalf("1h err rate=%v should stay under 2%%", rate)
	}
	if f.Long.Empty == nil || *f.Long.Empty != 5 {
		t.Fatalf("60m empty=%v want 5", f.Long.Empty)
	}
	if f.Short.N != 78 || f.Short.Err != 1 {
		t.Fatalf("raw 10m n=%d err=%d want 78/1", f.Short.N, f.Short.Err)
	}
	if f.Mix.NUser != 228 || f.Mix.NProbe != 4 || f.Mix.NErrReal != 2 {
		t.Fatalf("mix=%+v want NUser=228 NProbe=4 NErrReal=2", f.Mix)
	}
	if f.Chan.N != 226 || f.Chan.Err != 0 || f.Chan.Client != 2 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
		t.Fatalf("chanstat=%+v want N=226 Err=0 Client=2 Eff=0 Rate=0", f.Chan)
	}

	// Live v5.01 trust config uses up=0.2 (code default is 0.08).
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2633s.
	gap := m.F(2633.0 / 60.0)

	t.Run("live_shape_pins_floor_on_10m_1_of_78", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.1}, gap)[67]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=min(1, 0.0128/0.2)=0.064; Update takes the down branch because
		// sev>0, then clamps to the 0.1 floor.
		if st.Sev != 0.064 || st.Trust != 0.1 {
			t.Fatalf("trust sev=%v value=%v want 0.064 / 0.1", st.Sev, st.Trust)
		}
		if len(st.Why) != 1 || st.Why[0] != "10m 错误 1/78" {
			t.Fatalf("trust why=%v want [10m 错误 1/78]", st.Why)
		}
	})

	t.Run("clean_10m_same_1h_would_recover_to_0_295", func(t *testing.T) {
		// Keep the same 1h 2/232 (still under 2%) but move the only 10m error
		// out of the short window. sev would be 0, so Update takes the up path.
		clean := m.Feats{67: {
			Long:  f.Long,
			Short: &m.Window{N: 77, Err: 0, ErrRate: m.F(0)},
			Mix:   f.Mix,
			Chan:  f.Chan,
		}}
		st := trust.Step(trustCfg, []int{67}, clean, nil, map[int]float64{67: 0.1}, gap)[67]
		// 0.1 + 0.2*min(1, 2633/60/45)*min(1, 232/30) = 0.2950
		if st.Sev != 0 || st.Trust != 0.295 {
			t.Fatalf("clean 10m must recover, sev=%v value=%v want 0 / 0.295", st.Sev, st.Trust)
		}
	})

	t.Run("no_recovery_cap_when_n60_already_ample", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.1}, gap)[67]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: f.Long.N}}, map[int]float64{67: st.Trust}, []int{67})
		if caps[67] != nil {
			t.Fatalf("trust cap=%+v; n60=232 is not < 20 so recovery cap 3 must not be computed", caps[67])
		}
	})

	t.Run("settle_holds_16_to_20_without_trust_cap", func(t *testing.T) {
		next := map[int]int{67: 16}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fs, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20; strategy 16 must be settled back without a trust cap", next[67])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#67 定档：策略 16→20", "无渠道级错误证据", "1h 真实 226", "渠道错误 0", "去集中 0"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle note to contain %q, got %q", part, notes)
			}
		}
		for _, part := range []string{"系统上限 3", "不执行"} {
			if strings.Contains(joined, part) {
				t.Fatalf("ample n60 must not mention held recovery cap %q, got %q", part, notes)
			}
		}
	})

	t.Run("already_20_has_no_settle_note", func(t *testing.T) {
		next := map[int]int{67: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fs, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[67], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("already at 20 should not emit a settle note, got %q", notes)
		}
	})
}

// CASE-AW-034: sanitized aggregate shape from 2026-11-21 18:44 +08:00 CC #67.
// No production log rows, request identifiers, model names or real token IDs.
// The next window after CASE-AW-033. The 1h raw scoring window stays under
// 2% (2/250=0.80%, all client-class in ChanStat) and the 10m window is now
// clean, even though it has only 3 samples (still >= err_10m_min_n=2, so it
// is eligible for the 10m error path; there were simply no errors). Because
// sev=0, Update takes the up path and trust recovers from the 0.1 floor to
// 0.2801 over a 2432s gap: 0.1 + 0.2*min(1, 2432/60/45)*min(1, 250/30).
// n60=250 >= 20 so the recovery-period cap of 3 is not computed. ChanStat is
// N=243, Err=0, Client=2, Eff=0 and does not meet 30/3/3%, so a strategy
// target of 3 is settled back to 20; if the channel is already at 20, there
// is no settle note. This locks current v5.01 behavior; it does not change
// the algorithm.
func TestCasebookThinCleanTenMinRecoversTrustFloorAfterHourStaysBelow2pct(t *testing.T) {
	now := int64(100000)
	var rows []m.Row
	// 10m window (3): 3 real successes, no empties, no errors.
	for n := 0; n < 3; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 30, Type: 2, Tok: 900100 + n, Ct: 300, Ut: 5})
	}
	// Older than 10m, still inside 60m.
	for n := 0; n < 5; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 1318, Ct: 300, Ut: 5})
	}
	rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 900200, Ct: 0, Ut: 5})
	for n := 0; n < 239; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 900300 + n, Ct: 300, Ut: 5})
	}
	for n := 0; n < 2; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 5, Tok: 900500 + n, Sc: m.I(400)})
	}

	featCfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
	fs := features.Build(featCfg, []m.Channel{{ID: 67}}, rows, now)
	f := fs[67]
	if f == nil || f.Long == nil || f.Short == nil || f.Chan == nil || f.Mix == nil {
		t.Fatalf("missing features: %+v", f)
	}
	if f.Long.N != 250 || f.Long.Err != 2 {
		t.Fatalf("raw 60m n=%d err=%d want 250/2", f.Long.N, f.Long.Err)
	}
	rate := m.Or(f.Long.ErrRate, 0)
	if rate != m.Round(2.0/250.0, 4) {
		t.Fatalf("raw err rate=%v want rounded 2/250=0.0080", rate)
	}
	if rate >= 0.02 {
		t.Fatalf("1h err rate=%v should stay under 2%%", rate)
	}
	if f.Long.Empty == nil || *f.Long.Empty != 1 {
		t.Fatalf("60m empty=%v want 1", f.Long.Empty)
	}
	if f.Short.N != 3 || f.Short.Err != 0 {
		t.Fatalf("raw 10m n=%d err=%d want 3/0", f.Short.N, f.Short.Err)
	}
	if f.Mix.NUser != 245 || f.Mix.NProbe != 5 || f.Mix.NErrReal != 2 {
		t.Fatalf("mix=%+v want NUser=245 NProbe=5 NErrReal=2", f.Mix)
	}
	if f.Chan.N != 243 || f.Chan.Err != 0 || f.Chan.Client != 2 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
		t.Fatalf("chanstat=%+v want N=243 Err=0 Client=2 Eff=0 Rate=0", f.Chan)
	}

	// Live v5.01 trust config uses up=0.2 (code default is 0.08).
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2432s.
	gap := m.F(2432.0 / 60.0)

	t.Run("live_shape_recovers_from_floor_on_clean_10m_n3", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.1}, gap)[67]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=0 because 1h 0.80%<2% and 10m err=0 (n=3 >= err_10m_min_n=2).
		// 0.1 + 0.2*min(1, 2432/60/45)*min(1, 250/30) = 0.280148... -> 0.2801
		if st.Sev != 0 || st.Trust != 0.2801 {
			t.Fatalf("trust sev=%v value=%v want 0 / 0.2801", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
	})

	t.Run("same_1h_with_10m_single_error_would_pin_floor", func(t *testing.T) {
		// Keep the same 1h 2/250 (still under 2%) but put one client error in
		// a non-tiny 10m window, as in CASE-AW-033. sev>0 must take the down
		// branch and hold the 0.1 floor instead of recovering.
		dirty := m.Feats{67: {
			Long:  f.Long,
			Short: &m.Window{N: 78, Err: 1, ErrRate: m.F(m.Round(1.0/78.0, 4))},
			Mix:   f.Mix,
			Chan:  f.Chan,
		}}
		st := trust.Step(trustCfg, []int{67}, dirty, nil, map[int]float64{67: 0.1}, gap)[67]
		if st.Sev != 0.064 || st.Trust != 0.1 {
			t.Fatalf("dirty 10m must pin the floor, sev=%v value=%v want 0.064 / 0.1", st.Sev, st.Trust)
		}
		if len(st.Why) != 1 || st.Why[0] != "10m 错误 1/78" {
			t.Fatalf("trust why=%v want [10m 错误 1/78]", st.Why)
		}
	})

	t.Run("no_recovery_cap_when_n60_already_ample", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.1}, gap)[67]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: f.Long.N}}, map[int]float64{67: st.Trust}, []int{67})
		if caps[67] != nil {
			t.Fatalf("trust cap=%+v; n60=250 is not < 20 so recovery cap 3 must not be computed even though trust 0.2801 < 0.3", caps[67])
		}
	})

	t.Run("settle_holds_3_to_20_without_trust_cap", func(t *testing.T) {
		next := map[int]int{67: 3}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fs, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20; strategy 3 must be settled back without a trust cap", next[67])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#67 定档：策略 3→20", "无渠道级错误证据", "1h 真实 243", "渠道错误 0", "去集中 0"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle note to contain %q, got %q", part, notes)
			}
		}
		for _, part := range []string{"系统上限 3", "不执行"} {
			if strings.Contains(joined, part) {
				t.Fatalf("ample n60 must not mention held recovery cap %q, got %q", part, notes)
			}
		}
	})

	t.Run("already_20_has_no_settle_note", func(t *testing.T) {
		next := map[int]int{67: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fs, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[67], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("already at 20 should not emit a settle note, got %q", notes)
		}
	})
}

// CASE-AW-035: sanitized aggregate shape from 2026-11-21 19:33 +08:00 CC #67.
// No production log rows, request identifiers, model names or real token IDs.
// The next window after CASE-AW-034. 1h raw scoring is now fully clean
// (0/99) and the 10m window is clean (0/11, including one empty type2 that
// still counts as a success). Because sev=0, Update takes the up path and
// trust continues recovering from the mid-range 0.2801 (already off the 0.1
// floor last window) to 0.4801. The inter-round gap is 2939s (48.983... min),
// which exceeds round_min=45, so the time factor is capped: 0.2801 +
// 0.2*min(1, 2939/60/45)*min(1, 99/30) = 0.2801 + 0.2*1*1 = 0.4801. Without
// the cap the same arithmetic would be 0.4978. n60=99 >= 20 so the
// recovery-period cap of 3 is not computed. ChanStat is N=95, Err=0,
// Client=0, Eff=0 and does not meet 30/3/3%, so a strategy target of 3 is
// settled back to 20. This locks current v5.01 behavior; it does not change
// the algorithm.
func TestCasebookMidTrustRecoveryCapsIntervalFactorAtRoundMin(t *testing.T) {
	now := int64(100000)
	var rows []m.Row
	// 10m window (11): 10 real successes + 1 empty type2, no errors, no probes.
	for n := 0; n < 10; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 30, Type: 2, Tok: 900100 + n, Ct: 300, Ut: 5})
	}
	rows = append(rows, m.Row{Ch: 67, T: now - 30, Type: 2, Tok: 900200, Ct: 0, Ut: 5})
	// Older than 10m, still inside 60m: 4 probes + 84 user successes.
	for n := 0; n < 4; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 1318, Ct: 300, Ut: 5})
	}
	for n := 0; n < 84; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 900300 + n, Ct: 300, Ut: 5})
	}

	featCfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
	fs := features.Build(featCfg, []m.Channel{{ID: 67}}, rows, now)
	f := fs[67]
	if f == nil || f.Long == nil || f.Short == nil || f.Chan == nil || f.Mix == nil {
		t.Fatalf("missing features: %+v", f)
	}
	if f.Long.N != 99 || f.Long.Err != 0 {
		t.Fatalf("raw 60m n=%d err=%d want 99/0", f.Long.N, f.Long.Err)
	}
	rate := m.Or(f.Long.ErrRate, 0)
	if rate != 0 {
		t.Fatalf("raw err rate=%v want 0", rate)
	}
	if f.Long.Empty == nil || *f.Long.Empty != 1 {
		t.Fatalf("60m empty=%v want 1", f.Long.Empty)
	}
	if f.Short.N != 11 || f.Short.Err != 0 {
		t.Fatalf("raw 10m n=%d err=%d want 11/0", f.Short.N, f.Short.Err)
	}
	if f.Short.Empty == nil || *f.Short.Empty != 1 {
		t.Fatalf("10m empty=%v want 1", f.Short.Empty)
	}
	if f.Mix.NUser != 95 || f.Mix.NProbe != 4 || f.Mix.NErrReal != 0 {
		t.Fatalf("mix=%+v want NUser=95 NProbe=4 NErrReal=0", f.Mix)
	}
	if f.Chan.N != 95 || f.Chan.Err != 0 || f.Chan.Client != 0 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
		t.Fatalf("chanstat=%+v want N=95 Err=0 Client=0 Eff=0 Rate=0", f.Chan)
	}

	// Live v5.01 trust config uses up=0.2 (code default is 0.08).
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2939s.
	gap := m.F(2939.0 / 60.0)

	t.Run("live_shape_caps_gap_and_recovers_0_2801_to_0_4801", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.2801}, gap)[67]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=0 because 1h 0%<2% and 10m err=0 (n=11 >= err_10m_min_n=2).
		// 0.2801 + 0.2*min(1, 2939/60/45)*min(1, 99/30) = 0.4801
		// Uncapped 0.2801 + 0.2*(2939/60/45) = 0.497803... would not match.
		if st.Sev != 0 || st.Trust != 0.4801 {
			t.Fatalf("trust sev=%v value=%v want 0 / 0.4801", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
	})

	t.Run("shorter_2432s_gap_only_adds_the_uncapped_fraction", func(t *testing.T) {
		// Same clean windows and same prev, but reuse the 2432s gap from
		// CASE-AW-034. 2432/60/45 < 1, so the increment is smaller than the
		// live 45-minute cap: 0.2801 + 0.2*(2432/60/45) = 0.460248... -> 0.4602.
		short := m.F(2432.0 / 60.0)
		st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.2801}, short)[67]
		if st.Sev != 0 || st.Trust != 0.4602 {
			t.Fatalf("shorter gap must not use the cap, sev=%v value=%v want 0 / 0.4602", st.Sev, st.Trust)
		}
	})

	t.Run("sibling_n60_29_scales_up_under_the_same_capped_gap", func(t *testing.T) {
		// Same live 2939s gap, sanitized CC #68 shape: clean 1h n=29, clean
		// 10m n=6, prev 0.7672. Sample factor is 29/30, time factor still 1:
		// 0.7672 + 0.2*1*min(1, 29/30) = 0.960533... -> 0.9605.
		sib := m.Feats{68: {
			Long:  &m.Window{N: 29, Err: 0},
			Short: &m.Window{N: 6, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{68}, sib, nil, map[int]float64{68: 0.7672}, gap)[68]
		if st.Sev != 0 || st.Trust != 0.9605 {
			t.Fatalf("scaled recovery sev=%v value=%v want 0 / 0.9605", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
	})

	t.Run("no_recovery_cap_when_n60_already_ample", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.2801}, gap)[67]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: f.Long.N}}, map[int]float64{67: st.Trust}, []int{67})
		if caps[67] != nil {
			t.Fatalf("trust cap=%+v; n60=99 is not < 20 so recovery cap 3 must not be computed (trust 0.4801 is also already >= 0.3)", caps[67])
		}
	})

	t.Run("settle_holds_3_to_20_without_trust_cap", func(t *testing.T) {
		next := map[int]int{67: 3}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fs, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20; strategy 3 must be settled back without a trust cap", next[67])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#67 定档：策略 3→20", "无渠道级错误证据", "1h 真实 95", "渠道错误 0", "去集中 0"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle note to contain %q, got %q", part, notes)
			}
		}
		for _, part := range []string{"系统上限 3", "不执行"} {
			if strings.Contains(joined, part) {
				t.Fatalf("ample n60 must not mention held recovery cap %q, got %q", part, notes)
			}
		}
	})

	t.Run("already_20_has_no_settle_note", func(t *testing.T) {
		next := map[int]int{67: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fs, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[67], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("already at 20 should not emit a settle note, got %q", notes)
		}
	})
}

// CASE-AW-036: sanitized aggregate shape from 2026-11-21 20:16 +08:00 CC #72.
// No production log rows, request identifiers, model names or real token IDs.
// The next window after the CASE-AW-035 addendum for #72, when 10m was still
// dirty (1/6) and trust had already fallen to 0.1047. This window's 10m is
// clean again (0/12) but the raw 1h scoring window is still 2/37=5.41%
// (noted 5.4%), all of it client-class. Because sev>0, Update takes the down
// branch: 0.1047*(1-0.6*0.171)=0.0940..., then clamps to the 0.1 floor. It does
// not recover across the 2568s (42.8 min) gap. n60=37 >= 20 so the
// recovery-period cap of 3 is not computed. ChanStat is N=32, Err=0,
// Client=2, Eff=0 and does not meet 30/3/3%, so a strategy target of 6 is
// settled back to 20. This locks current v5.01 behavior; it does not change
// the algorithm.
func TestCasebookNearFloorTrustStillClampedAfterCleanTenMinHourStaysHigh(t *testing.T) {
	now := int64(100000)
	var rows []m.Row
	// 10m window (12): 12 user successes, no errors, no probes, no empty.
	for n := 0; n < 12; n++ {
		rows = append(rows, m.Row{Ch: 72, T: now - 30, Type: 2, Tok: 900100 + n, Ct: 300, Ut: 5})
	}
	// Older than 10m, still inside 60m: 3 probes + 20 user successes + 2
	// client-class errors.
	for n := 0; n < 3; n++ {
		rows = append(rows, m.Row{Ch: 72, T: now - 40*60, Type: 2, Tok: 1318, Ct: 300, Ut: 5})
	}
	for n := 0; n < 20; n++ {
		rows = append(rows, m.Row{Ch: 72, T: now - 40*60, Type: 2, Tok: 900300 + n, Ct: 300, Ut: 5})
	}
	for n := 0; n < 2; n++ {
		rows = append(rows, m.Row{Ch: 72, T: now - 40*60, Type: 5, Tok: 900500 + n, Sc: m.I(400)})
	}

	featCfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
	fs := features.Build(featCfg, []m.Channel{{ID: 72}}, rows, now)
	f := fs[72]
	if f == nil || f.Long == nil || f.Short == nil || f.Chan == nil || f.Mix == nil {
		t.Fatalf("missing features: %+v", f)
	}
	if f.Long.N != 37 || f.Long.Err != 2 {
		t.Fatalf("raw 60m n=%d err=%d want 37/2", f.Long.N, f.Long.Err)
	}
	rate := m.Or(f.Long.ErrRate, 0)
	if rate != m.Round(2.0/37.0, 4) {
		t.Fatalf("raw err rate=%v want rounded 2/37=0.0541", rate)
	}
	if f.Long.Empty == nil || *f.Long.Empty != 0 {
		t.Fatalf("60m empty=%v want 0", f.Long.Empty)
	}
	if f.Short.N != 12 || f.Short.Err != 0 {
		t.Fatalf("raw 10m n=%d err=%d want 12/0", f.Short.N, f.Short.Err)
	}
	if f.Mix.NUser != 34 || f.Mix.NProbe != 3 || f.Mix.NErrReal != 2 {
		t.Fatalf("mix=%+v want NUser=34 NProbe=3 NErrReal=2", f.Mix)
	}
	if f.Chan.N != 32 || f.Chan.Err != 0 || f.Chan.Client != 2 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
		t.Fatalf("chanstat=%+v want N=32 Err=0 Client=2 Eff=0 Rate=0", f.Chan)
	}

	// Live v5.01 trust config uses up=0.2 (code default is 0.08).
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2568s.
	gap := m.F(2568.0 / 60.0)

	t.Run("live_shape_clamps_0_1047_to_floor_despite_clean_10m", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1047}, gap)[72]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=(0.0541-0.02)/0.2=0.1705 -> 0.171; Update takes the down branch
		// because sev>0: 0.1047*(1-0.6*0.171)=0.093957..., then clamps to 0.1.
		if st.Sev != 0.171 || st.Trust != 0.1 {
			t.Fatalf("trust sev=%v value=%v want 0.171 / 0.1", st.Sev, st.Trust)
		}
		if len(st.Why) != 1 || st.Why[0] != "1h 错误率 5.4%" {
			t.Fatalf("trust why=%v want [1h 错误率 5.4%%]", st.Why)
		}
	})

	t.Run("clean_1h_would_recover_to_0_2949", func(t *testing.T) {
		clean := m.Feats{72: {
			Long:  &m.Window{N: 37, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 12, Err: 0, ErrRate: m.F(0)},
			Mix:   f.Mix,
			Chan:  f.Chan,
		}}
		st := trust.Step(trustCfg, []int{72}, clean, nil, map[int]float64{72: 0.1047}, gap)[72]
		// 0.1047 + 0.2*min(1, 2568/60/45)*min(1, 37/30) = 0.294922... -> 0.2949
		if st.Sev != 0 || st.Trust != 0.2949 {
			t.Fatalf("clean 1h must recover, sev=%v value=%v want 0 / 0.2949", st.Sev, st.Trust)
		}
	})

	t.Run("mid_prev_is_multiplied_then_not_floored", func(t *testing.T) {
		// Same 1h rate / sev, but start from the earlier mid value 0.2398
		// (CASE-AW-032 addendum at 18:44). 0.2398*(1-0.6*0.171)=0.215196... -> 0.2152,
		// which is still above the 0.1 floor. This shows the live 0.1 is the
		// clamp of a product, not an immediate jump to the floor.
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.2398}, gap)[72]
		if st.Sev != 0.171 || st.Trust != 0.2152 {
			t.Fatalf("mid prev must be multiplied, sev=%v value=%v want 0.171 / 0.2152", st.Sev, st.Trust)
		}
	})

	t.Run("sibling_67_uncapped_mid_recovery_on_same_gap", func(t *testing.T) {
		// Same live 2568s gap, sanitized CC #67 shape this round: clean 1h
		// n=70, clean 10m n=13, prev 0.4801. Time factor 2568/60/45 < 1, so
		// the increment is not capped: 0.4801 + 0.2*(2568/60/45)*1 = 0.670322...
		// -> 0.6703. This must not be mixed with #72's down path.
		sib := m.Feats{67: {
			Long:  &m.Window{N: 70, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 13, Err: 0, ErrRate: m.F(0)},
		}}
		st := trust.Step(trustCfg, []int{67}, sib, nil, map[int]float64{67: 0.4801}, gap)[67]
		if st.Sev != 0 || st.Trust != 0.6703 {
			t.Fatalf("sibling recovery sev=%v value=%v want 0 / 0.6703", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
	})

	t.Run("no_recovery_cap_when_n60_already_ample", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1047}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: f.Long.N}}, map[int]float64{72: st.Trust}, []int{72})
		if caps[72] != nil {
			t.Fatalf("trust cap=%+v; n60=37 is not < 20 so recovery cap 3 must not be computed", caps[72])
		}
	})

	t.Run("settle_holds_6_to_20_without_trust_cap", func(t *testing.T) {
		next := map[int]int{72: 6}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; strategy 6 must be settled back without a trust cap", next[72])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#72 定档：策略 6→20", "无渠道级错误证据", "1h 真实 32", "渠道错误 0", "去集中 0"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle note to contain %q, got %q", part, notes)
			}
		}
		for _, part := range []string{"系统上限 3", "不执行"} {
			if strings.Contains(joined, part) {
				t.Fatalf("ample n60 must not mention held recovery cap %q, got %q", part, notes)
			}
		}
	})

	t.Run("already_20_has_no_settle_note", func(t *testing.T) {
		next := map[int]int{72: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[72], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("already at 20 should not emit a settle note, got %q", notes)
		}
	})
}

// TestCasebookMidRecoveryTrustMultipliedDownDespiteCleanTenMin locks
// CASE-AW-037: after a clean mid-recovery, a new 1h raw rate of 3/33=9.09%
// takes the multiplicative down branch (0.6703 -> 0.5279) even though the
// 10m window is still clean (0/3, one empty type2). ChanStat records all 3
// faults as client-class (N=26, Err=0) and does not meet 30/3/3%, so a
// strategy target of 0 is settled back to 20. This locks current v5.01
// behavior; it does not change the algorithm.
func TestCasebookMidRecoveryTrustMultipliedDownDespiteCleanTenMin(t *testing.T) {
	now := int64(100000)
	var rows []m.Row
	// 10m window (3): 2 real successes + 1 empty type2, no errors, no probes.
	for n := 0; n < 2; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 30, Type: 2, Tok: 900100 + n, Ct: 300, Ut: 5})
	}
	rows = append(rows, m.Row{Ch: 67, T: now - 30, Type: 2, Tok: 900200, Ct: 0, Ut: 5})
	// Older than 10m, still inside 60m: 4 probes + 22 user successes + 1 empty
	// + 3 client-class errors.
	for n := 0; n < 4; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 1318, Ct: 300, Ut: 5})
	}
	for n := 0; n < 22; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 900300 + n, Ct: 300, Ut: 5})
	}
	rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 900400, Ct: 0, Ut: 5})
	for n := 0; n < 3; n++ {
		rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 5, Tok: 900500 + n, Sc: m.I(400)})
	}

	featCfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
	fs := features.Build(featCfg, []m.Channel{{ID: 67}}, rows, now)
	f := fs[67]
	if f == nil || f.Long == nil || f.Short == nil || f.Chan == nil || f.Mix == nil {
		t.Fatalf("missing features: %+v", f)
	}
	if f.Long.N != 33 || f.Long.Err != 3 {
		t.Fatalf("raw 60m n=%d err=%d want 33/3", f.Long.N, f.Long.Err)
	}
	rate := m.Or(f.Long.ErrRate, 0)
	if rate != m.Round(3.0/33.0, 4) {
		t.Fatalf("raw err rate=%v want rounded 3/33=0.0909", rate)
	}
	if f.Long.Empty == nil || *f.Long.Empty != 2 {
		t.Fatalf("60m empty=%v want 2", f.Long.Empty)
	}
	if f.Short.N != 3 || f.Short.Err != 0 {
		t.Fatalf("raw 10m n=%d err=%d want 3/0", f.Short.N, f.Short.Err)
	}
	if f.Short.Empty == nil || *f.Short.Empty != 1 {
		t.Fatalf("10m empty=%v want 1", f.Short.Empty)
	}
	if f.Mix.NUser != 29 || f.Mix.NProbe != 4 || f.Mix.NErrReal != 3 {
		t.Fatalf("mix=%+v want NUser=29 NProbe=4 NErrReal=3", f.Mix)
	}
	if f.Chan.N != 26 || f.Chan.Err != 0 || f.Chan.Client != 3 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
		t.Fatalf("chanstat=%+v want N=26 Err=0 Client=3 Eff=0 Rate=0", f.Chan)
	}

	// Live v5.01 trust config uses up=0.2 (code default is 0.08).
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2614s.
	gap := m.F(2614.0 / 60.0)

	t.Run("live_shape_multiplies_0_6703_to_0_5279_despite_clean_10m", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.6703}, gap)[67]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=(0.0909-0.02)/0.2=0.3545 -> 0.354; Update takes the down branch
		// because sev>0: 0.6703*(1-0.6*0.354)=0.52792828... -> 0.5279.
		// The 10m empty type2 does not add a why note.
		if st.Sev != 0.354 || st.Trust != 0.5279 {
			t.Fatalf("trust sev=%v value=%v want 0.354 / 0.5279", st.Sev, st.Trust)
		}
		if len(st.Why) != 1 || st.Why[0] != "1h 错误率 9.1%" {
			t.Fatalf("trust why=%v want [1h 错误率 9.1%%]", st.Why)
		}
	})

	t.Run("clean_1h_would_recover_to_0_8639", func(t *testing.T) {
		clean := m.Feats{67: {
			Long:  &m.Window{N: 33, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 3, Err: 0, ErrRate: m.F(0), Empty: m.I(1)},
			Mix:   f.Mix,
			Chan:  f.Chan,
		}}
		st := trust.Step(trustCfg, []int{67}, clean, nil, map[int]float64{67: 0.6703}, gap)[67]
		// 0.6703 + 0.2*min(1, 2614/60/45)*min(1, 33/30) = 0.8639296... -> 0.8639
		if st.Sev != 0 || st.Trust != 0.8639 {
			t.Fatalf("clean 1h must recover, sev=%v value=%v want 0 / 0.8639", st.Sev, st.Trust)
		}
	})

	t.Run("sibling_72_already_at_floor_stays_0_1_on_slightly_over_2pct", func(t *testing.T) {
		// Same live 2614s gap, sanitized CC #72 shape this round: 1h 1/37=2.70%,
		// clean 10m n=3, prev already at the 0.1 floor. sev=(0.0270-0.02)/0.2=0.035;
		// 0.1*(1-0.6*0.035)=0.0979..., then clamps to 0.1. This must not be mixed
		// with #67's mid-value multiplicative drop.
		sib := m.Feats{72: {
			Long:  &m.Window{N: 37, Err: 1, ErrRate: m.F(m.Round(1.0/37.0, 4))},
			Short: &m.Window{N: 3, Err: 0, ErrRate: m.F(0)},
		}}
		st := trust.Step(trustCfg, []int{72}, sib, nil, map[int]float64{72: 0.1}, gap)[72]
		if st.Sev != 0.035 || st.Trust != 0.1 {
			t.Fatalf("sibling floor hold sev=%v value=%v want 0.035 / 0.1", st.Sev, st.Trust)
		}
		if len(st.Why) != 1 || st.Why[0] != "1h 错误率 2.7%" {
			t.Fatalf("trust why=%v want [1h 错误率 2.7%%]", st.Why)
		}
	})

	t.Run("no_recovery_cap_when_n60_already_ample", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.6703}, gap)[67]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: f.Long.N}}, map[int]float64{67: st.Trust}, []int{67})
		if caps[67] != nil {
			t.Fatalf("trust cap=%+v; n60=33 is not < 20 so recovery cap 3 must not be computed", caps[67])
		}
	})

	t.Run("settle_holds_0_to_20_without_trust_cap", func(t *testing.T) {
		next := map[int]int{67: 0}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fs, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20; strategy 0 must be settled back without a trust cap", next[67])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#67 定档：策略 0→20", "无渠道级错误证据", "1h 真实 26", "渠道错误 0", "去集中 0"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle note to contain %q, got %q", part, notes)
			}
		}
		for _, part := range []string{"系统上限 3", "不执行"} {
			if strings.Contains(joined, part) {
				t.Fatalf("ample n60 must not mention held recovery cap %q, got %q", part, notes)
			}
		}
	})

	t.Run("already_20_has_no_settle_note", func(t *testing.T) {
		next := map[int]int{67: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, fs, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[67], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("already at 20 should not emit a settle note, got %q", notes)
		}
	})
}

// CASE-AW-038: sanitized aggregate shape from 2026-11-21 21:50 +08:00 CC #72.
// No production log rows, request identifiers, model names or real token IDs.
// The next window after the CASE-AW-036 addendum, when #72 was already held
// at the 0.1 floor by a 1h raw rate just over 2%. This window's 1h and 10m
// scoring windows are both clean (0/7 and 0/1). Because sev=0, Update takes
// the recovery branch. The inter-round gap is 3065s (51.08 min), so the
// interval factor is already capped at 1, but n60=7 < up_full_n=30 scales
// the increment: 0.1 + 0.2*1*min(1, 7/30) = 0.1467. If the sample factor
// were ignored, trust would reach 0.3 and the recovery-period cap would not
// be computed (0.3 < 0.3 is false). With the scaled value 0.1467 < 0.3 and
// n60=7 < 20, applyTrustCap computes cap 3. ChanStat is N=3, Err=0, Client=0,
// Eff=0 and does not meet 30/3/3%, so cut_needs_fault refuses to apply that
// system cap and a strategy target of 3 is settled back to 20. This locks
// current v5.01 behavior; it does not change the algorithm.
func TestCasebookSampleLimitedFloorRecoveryComputesTrustCapButSettleHolds(t *testing.T) {
	now := int64(100000)
	var rows []m.Row
	// 10m window (1): 1 user success, no errors, no probes, no empty.
	// N=1 is below err_10m_min_n=2, so the 10m error path is not eligible
	// anyway; the window is also clean.
	rows = append(rows, m.Row{Ch: 72, T: now - 30, Type: 2, Tok: 900100, Ct: 300, Ut: 5})
	// Older than 10m, still inside 60m: 4 probes + 2 user successes.
	for n := 0; n < 4; n++ {
		rows = append(rows, m.Row{Ch: 72, T: now - 40*60, Type: 2, Tok: 1318, Ct: 300, Ut: 5})
	}
	for n := 0; n < 2; n++ {
		rows = append(rows, m.Row{Ch: 72, T: now - 40*60, Type: 2, Tok: 900300 + n, Ct: 300, Ut: 5})
	}

	featCfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
	fs := features.Build(featCfg, []m.Channel{{ID: 72}}, rows, now)
	f := fs[72]
	if f == nil || f.Long == nil || f.Short == nil || f.Chan == nil || f.Mix == nil {
		t.Fatalf("missing features: %+v", f)
	}
	if f.Long.N != 7 || f.Long.Err != 0 {
		t.Fatalf("raw 60m n=%d err=%d want 7/0", f.Long.N, f.Long.Err)
	}
	rate := m.Or(f.Long.ErrRate, 0)
	if rate != 0 {
		t.Fatalf("raw err rate=%v want 0", rate)
	}
	if f.Long.Empty == nil || *f.Long.Empty != 0 {
		t.Fatalf("60m empty=%v want 0", f.Long.Empty)
	}
	if f.Short.N != 1 || f.Short.Err != 0 {
		t.Fatalf("raw 10m n=%d err=%d want 1/0", f.Short.N, f.Short.Err)
	}
	if f.Mix.NUser != 3 || f.Mix.NProbe != 4 || f.Mix.NErrReal != 0 {
		t.Fatalf("mix=%+v want NUser=3 NProbe=4 NErrReal=0", f.Mix)
	}
	if f.Chan.N != 3 || f.Chan.Err != 0 || f.Chan.Client != 0 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
		t.Fatalf("chanstat=%+v want N=3 Err=0 Client=0 Eff=0 Rate=0", f.Chan)
	}

	// Live v5.01 trust config uses up=0.2 (code default is 0.08).
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 3065s.
	gap := m.F(3065.0 / 60.0)

	t.Run("live_shape_recovers_0_1_to_0_1467_scaled_by_n60_7", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1}, gap)[72]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=0 because 1h 0%<2% and 10m err=0 (and n=1 < err_10m_min_n=2).
		// 0.1 + 0.2*min(1, 3065/60/45)*min(1, 7/30) = 0.146666... -> 0.1467
		// Time factor is already 1; the increment is limited by 7/30.
		if st.Sev != 0 || st.Trust != 0.1467 {
			t.Fatalf("trust sev=%v value=%v want 0 / 0.1467", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
	})

	t.Run("ample_n60_would_add_full_0_2_and_reach_0_3", func(t *testing.T) {
		// Same gap and prev, but n60 already at up_full_n. Time factor is
		// still 1, so the increment is the full 0.2: 0.1 + 0.2 = 0.3.
		// 0.3 < 0.3 is false, so this contrast also would not compute cap 3.
		ample := m.Feats{72: {
			Long:  &m.Window{N: 30, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 1, Err: 0, ErrRate: m.F(0)},
		}}
		st := trust.Step(trustCfg, []int{72}, ample, nil, map[int]float64{72: 0.1}, gap)[72]
		if st.Sev != 0 || st.Trust != 0.3 {
			t.Fatalf("ample n60 must add the full 0.2, sev=%v value=%v want 0 / 0.3", st.Sev, st.Trust)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: 30}}, map[int]float64{72: st.Trust}, []int{72})
		if caps[72] != nil {
			t.Fatalf("trust cap=%+v; n60=30 is not < 20 and trust 0.3 is not < 0.3, so recovery cap 3 must not be computed", caps[72])
		}
	})

	t.Run("sibling_67_mid_recovery_scales_0_5279_to_0_5812_without_cap", func(t *testing.T) {
		// Same live 3065s gap, sanitized CC #67 shape this round: clean 1h
		// n=8 (empty=4 still counted as type2 success), clean 10m n=3
		// (empty=1), prev 0.5279 after CASE-AW-037. Sample factor is 8/30,
		// time factor still 1: 0.5279 + 0.2*1*min(1, 8/30) = 0.581233... -> 0.5812.
		// Trust is already >= 0.3, so even though n60=8 < 20, cap 3 is not computed.
		sib := m.Feats{67: {
			Long:  &m.Window{N: 8, Err: 0, ErrRate: m.F(0), Empty: m.I(4)},
			Short: &m.Window{N: 3, Err: 0, ErrRate: m.F(0), Empty: m.I(1)},
		}}
		st := trust.Step(trustCfg, []int{67}, sib, nil, map[int]float64{67: 0.5279}, gap)[67]
		if st.Sev != 0 || st.Trust != 0.5812 {
			t.Fatalf("sibling mid recovery sev=%v value=%v want 0 / 0.5812", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: 8}}, map[int]float64{67: st.Trust}, []int{67})
		if caps[67] != nil {
			t.Fatalf("trust cap=%+v; trust 0.5812 is not < 0.3 so recovery cap 3 must not be computed even though n60=8 < 20", caps[67])
		}
	})

	t.Run("recovery_cap_3_is_computed_when_scaled_trust_stays_below_0_3", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: f.Long.N}}, map[int]float64{72: st.Trust}, []int{72})
		c := caps[72]
		if c == nil || c.Cap != 3 || c.Kind != "trust" {
			t.Fatalf("trust cap=%+v want cap=3 kind=trust", c)
		}
		if c.Reason != "信任 0.15 < 0.3且 1h 样本 7 < 20，恢复期限幅" {
			t.Fatalf("trust cap reason=%q", c.Reason)
		}
	})

	t.Run("settle_holds_3_to_20_and_mentions_unapplied_cap", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: f.Long.N}}, map[int]float64{72: st.Trust}, []int{72})
		next := map[int]int{72: 3}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, caps, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; system cap 3 must not be applied", next[72])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#72 定档：策略 3→20", "系统上限 3", "不执行", "1h 真实 3", "渠道错误 0", "去集中 0", "0.0%", "未达降权门槛"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected held-cap note to contain %q, got %q", part, notes)
			}
		}
	})

	t.Run("already_20_has_no_settle_note", func(t *testing.T) {
		next := map[int]int{72: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[72], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("already at 20 should not emit a settle note, got %q", notes)
		}
	})
}

// CASE-AW-039: sanitized aggregate shape from 2026-11-21 22:30 +08:00 GPT #2.
// No production log rows, request identifiers, model names or real token IDs.
// After a modest 10m-path trust cut (CASE-AW-028 addendum in the previous
// counted round), a later window with a clean 10m and a 1h raw rate still
// below 2% fully restores trust to 1, even though the inter-round gap is
// under 45 minutes, because ample n60 makes the increment larger than the
// remaining gap. This locks current v5.01 Update/Settle behavior; it does
// not change the algorithm.
func TestCasebookAmpleCleanWindowFullyRecoversModestTenMinTrustCut(t *testing.T) {
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2379s.
	gap := m.F(2379.0 / 60.0)
	// Live #2 shape this round: 10m 0/11, 1h raw 3/251=1.20% (below 2%),
	// n60=251 already at up_full_n. Prev trust 0.88 after the previous
	// 10m 3/75 cut.
	live := m.Feats{2: {
		Long:  &m.Window{N: 251, Err: 3, ErrRate: m.F(0.012)},
		Short: &m.Window{N: 11, Err: 0, ErrRate: m.F(0)},
		Chan:  &m.ChanStat{N: 246, Err: 2, Client: 1, Eff: 2, Rate: 0.0081},
	}}

	t.Run("live_shape_recovers_0_88_to_1_despite_sub_45m_gap", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{2}, live, nil, map[int]float64{2: 0.88}, gap)[2]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=0 because 1h 1.20% is not > 2% and 10m is clean (0/11, n>=2).
		// 0.88 + 0.2*min(1, 2379/60/45)*min(1, 251/30) = 1.0562... -> 1.0
		// The remaining gap to 1 is 0.12, smaller than the 0.1762 increment.
		if st.Sev != 0 || st.Trust != 1 {
			t.Fatalf("trust sev=%v value=%v want 0 / 1", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
	})

	t.Run("siblings_32_and_73_also_fill_to_1", func(t *testing.T) {
		// Same 2379s gap. Live #32 prev 0.9472, 10m 0/27, 1h 1/230=0.43%.
		// Live #73 prev 0.9538, 10m 0/83, 1h 2/556=0.36%. Both already
		// closer to 1 than #2, so they also cap at 1.
		sibs := m.Feats{
			32: {Long: &m.Window{N: 230, Err: 1, ErrRate: m.F(0.0043)}, Short: &m.Window{N: 27, Err: 0, ErrRate: m.F(0)}},
			73: {Long: &m.Window{N: 556, Err: 2, ErrRate: m.F(0.0036)}, Short: &m.Window{N: 83, Err: 0, ErrRate: m.F(0)}},
		}
		st32 := trust.Step(trustCfg, []int{32}, sibs, nil, map[int]float64{32: 0.9472}, gap)[32]
		st73 := trust.Step(trustCfg, []int{73}, sibs, nil, map[int]float64{73: 0.9538}, gap)[73]
		if st32.Sev != 0 || st32.Trust != 1 {
			t.Fatalf("#32 sev=%v value=%v want 0 / 1", st32.Sev, st32.Trust)
		}
		if st73.Sev != 0 || st73.Trust != 1 {
			t.Fatalf("#73 sev=%v value=%v want 0 / 1", st73.Sev, st73.Trust)
		}
	})

	t.Run("thin_n60_would_not_reach_1", func(t *testing.T) {
		// Same prev, gap and clean 10m, but n60=6 like a thin CC window.
		// 0.88 + 0.2*min(1, 2379/60/45)*min(1, 6/30) = 0.9152
		thin := m.Feats{2: {
			Long:  &m.Window{N: 6, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 11, Err: 0, ErrRate: m.F(0)},
		}}
		st := trust.Step(trustCfg, []int{2}, thin, nil, map[int]float64{2: 0.88}, gap)[2]
		if st.Sev != 0 || st.Trust != 0.9152 {
			t.Fatalf("thin n60 must not fill to 1, sev=%v value=%v want 0 / 0.9152", st.Sev, st.Trust)
		}
	})

	t.Run("ten_minute_gap_would_not_reach_1", func(t *testing.T) {
		// Ample n60, but a 10-minute gap: 0.88 + 0.2*(10/45)*1 = 0.9244
		short := m.F(10.0)
		st := trust.Step(trustCfg, []int{2}, live, nil, map[int]float64{2: 0.88}, short)[2]
		if st.Sev != 0 || st.Trust != 0.9244 {
			t.Fatalf("short gap must not fill to 1, sev=%v value=%v want 0 / 0.9244", st.Sev, st.Trust)
		}
	})

	t.Run("settle_holds_16_to_20_even_with_eff_2", func(t *testing.T) {
		// Live #2 ChanStat N=246, Err=2, Client=1, Eff=2, Rate=0.81%.
		// N and Eff are short of the 30/3/3% conjunction (Err=2<3 and
		// Rate=0.81%<3%). Strategy 16 is pulled back to 20, and the
		// hold note still says 无渠道级错误证据.
		fs := m.Feats{2: {
			Long:  &m.Window{N: 251, Err: 3, ErrRate: m.F(0.012)},
			Short: &m.Window{N: 11, Err: 0, ErrRate: m.F(0)},
			Chan:  &m.ChanStat{N: 246, Err: 2, Client: 1, Eff: 2, Rate: 0.0081},
		}}
		next := map[int]int{2: 16}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{2: 20}, fs, []int{2}, nil, &notes)
		if next[2] != 20 {
			t.Fatalf("weight=%d want 20; Eff=2 must not satisfy the fault gate", next[2])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#2 定档：策略 16→20", "无渠道级错误证据", "1h 真实 246", "渠道错误 2", "去集中 2", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle hold note to contain %q, got %q", part, notes)
			}
		}
	})

	t.Run("already_20_has_no_settle_note", func(t *testing.T) {
		next := map[int]int{2: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{2: 20}, live, []int{2}, nil, &notes)
		if next[2] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[2], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("already at 20 should not emit a settle note, got %q", notes)
		}
	})
}

// CASE-AW-040: sanitized aggregate shape from 2026-11-21 23:17 +08:00 CC #67.
// No production log rows, request identifiers, model names or real token IDs.
// A mid-recovery trust value is multiplied down by a thin 1h slow_rate that
// sits exactly at slow_min_n=5, even though 3/5 scoring-window rows are empty
// successes and 10m is empty. empty_weight=2 * empty_rate=0.6 saturates the
// score "bad" term and zeros raw_score. Settle still holds 20 because ChanStat
// misses 30/3/3%. This locks current v5.01 behavior; it does not change the
// algorithm.
func TestCasebookMidRecoveryThinSlowAndEmptyZerosScoreButSettleHolds(t *testing.T) {
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2794s.
	gap := m.F(2794.0 / 60.0)
	// Live #67 shape this round: scoring n=5 (2 user + 3 probes), empty=3,
	// empty_rate=0.6, err=0, slow_rate=20%, 10m n=0. Prev trust 0.6165 after
	// the previous clean mid-recovery step. ChanStat N=2, no channel errors.
	live := m.Feats{67: {
		Long: &m.Window{
			N: 5, Err: 0, ErrRate: m.F(0),
			Empty: m.I(3), EmptyRate: m.F(0.6),
			P50: m.F(3), P90: m.F(64),
			SlowRate: m.F(0.2),
		},
		Short: &m.Window{N: 0, Err: 0},
		Chan:  &m.ChanStat{N: 2, Err: 0, Client: 0, Eff: 0, Rate: 0},
		Mix:   &m.Mix{NUser: 2, NProbe: 3, NErrReal: 0},
	}}

	t.Run("live_shape_cuts_0_6165_to_0_5055_at_exact_slow_min_n", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{67}, live, nil, map[int]float64{67: 0.6165}, gap)[67]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// n=5 is exactly slow_min_n, slow_rate 20% > 15%, so sev=slow_sev=0.3
		// and the down branch runs: 0.6165*(1-0.6*0.3)=0.50553 -> 0.5055.
		// The 2794s gap is ignored on a down step; empties stay type2 success
		// (err=0) and do not block the slow path; 10m n=0 is below
		// err_10m_min_n so it adds no short-window part.
		if st.Sev != 0.3 || st.Trust != 0.5055 {
			t.Fatalf("trust sev=%v value=%v want 0.3 / 0.5055", st.Sev, st.Trust)
		}
		if !strings.Contains(strings.Join(st.Why, ";"), "1h 慢请求 20%") {
			t.Fatalf("trust why=%v want 1h 慢请求 20%%", st.Why)
		}
	})

	t.Run("n4_does_not_penalize_and_recovers", func(t *testing.T) {
		// Same prev, gap and slow_rate, but n=4 is below slow_min_n.
		thin := m.Feats{67: {
			Long: &m.Window{
				N: 4, Err: 0, ErrRate: m.F(0),
				Empty: m.I(3), EmptyRate: m.F(0.75),
				P50: m.F(3), P90: m.F(64),
				SlowRate: m.F(0.2),
			},
			Short: &m.Window{N: 0, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{67}, thin, nil, map[int]float64{67: 0.6165}, gap)[67]
		// 0.6165 + 0.2*min(1, 2794/60/45)*min(1, 4/30) = 0.643166... -> 0.6432
		if st.Sev != 0 || st.Trust != 0.6432 {
			t.Fatalf("n=4 must recover, not cut, sev=%v value=%v want 0 / 0.6432", st.Sev, st.Trust)
		}
		if strings.Contains(strings.Join(st.Why, ";"), "慢请求") {
			t.Fatalf("n=4 must not emit a slow-rate why, got %v", st.Why)
		}
	})

	t.Run("slow_rate_at_15_percent_does_not_penalize", func(t *testing.T) {
		// n=5 meets slow_min_n, but SlowRate must be strictly above 15%.
		edge := m.Feats{67: {
			Long: &m.Window{
				N: 5, Err: 0, ErrRate: m.F(0),
				Empty: m.I(3), EmptyRate: m.F(0.6),
				P50: m.F(3), P90: m.F(64),
				SlowRate: m.F(0.15),
			},
			Short: &m.Window{N: 0, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{67}, edge, nil, map[int]float64{67: 0.6165}, gap)[67]
		// 0.6165 + 0.2*1*(5/30) = 0.649833... -> 0.6498
		if st.Sev != 0 || st.Trust != 0.6498 {
			t.Fatalf("slow_rate=15%% must recover, sev=%v value=%v want 0 / 0.6498", st.Sev, st.Trust)
		}
	})

	t.Run("empty_weight_2_times_0_6_zeros_raw_score", func(t *testing.T) {
		w := live[67].Long
		zero := ScoreWindow(w, 0, m.M{
			"a_p50": 1.0, "b_p90": 0.0, "c_ttft": 0.0, "t_ref": 20.0,
			"min_n_window": 5.0, "empty_weight": 2.0, "err_exp": 2.0,
		}, Pair{}, nil)
		if zero == nil || *zero != 0 {
			t.Fatalf("empty_weight 2 * empty_rate 0.6 must zero raw_score, got %v", zero)
		}
		positive := ScoreWindow(w, 0, m.M{
			"a_p50": 1.0, "b_p90": 0.0, "c_ttft": 0.0, "t_ref": 20.0,
			"min_n_window": 5.0, "empty_weight": 0.0, "err_exp": 2.0,
		}, Pair{}, nil)
		if positive == nil || *positive == 0 {
			t.Fatalf("empty_weight 0 must not zero the score, got %v", positive)
		}
	})

	t.Run("empty_rate_0_4_does_not_fully_zero_score", func(t *testing.T) {
		w := &m.Window{
			N: 5, Err: 0, ErrRate: m.F(0),
			Empty: m.I(2), EmptyRate: m.F(0.4),
			P50: m.F(3), P90: m.F(64),
			SlowRate: m.F(0.2),
		}
		got := ScoreWindow(w, 0, m.M{
			"a_p50": 1.0, "b_p90": 0.0, "c_ttft": 0.0, "t_ref": 20.0,
			"min_n_window": 5.0, "empty_weight": 2.0, "err_exp": 2.0,
		}, Pair{}, nil)
		// bad=min(1, 2*0.4)=0.8; (0.2)^2 * (1-0.2) * 20/(20+3) > 0
		if got == nil || *got == 0 {
			t.Fatalf("empty_rate 0.4 must not fully zero raw_score, got %v", got)
		}
	})

	t.Run("no_trust_cap_because_cut_trust_stays_at_or_above_0_3", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{67}, live, nil, map[int]float64{67: 0.6165}, gap)[67]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: 5}}, map[int]float64{67: st.Trust}, []int{67})
		if caps[67] != nil {
			t.Fatalf("trust cap=%+v; trust 0.5055 is not < 0.3 so recovery cap 3 must not be computed even though n60=5 < 20", caps[67])
		}
	})

	t.Run("settle_holds_0_to_20", func(t *testing.T) {
		next := map[int]int{67: 0}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, live, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20 after settle hold; notes=%q", next[67], notes)
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#67 定档：策略 0→20", "无渠道级错误证据", "1h 真实 2", "渠道错误 0", "去集中 0", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle hold note to contain %q, got %q", part, notes)
			}
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault cut, got %q", notes)
		}
	})

	t.Run("sibling_72_recovers_0_1878_to_0_2278_with_capped_gap_and_unapplied_cap", func(t *testing.T) {
		// Same 2794s gap (time factor already 1). Live #72 this round: 10m
		// n=0, 1h clean n=6, prev 0.1878 after CASE-AW-038's next window.
		// 0.1878 + 0.2*1*(6/30) = 0.2278. Still < 0.3 and n60=6 < 20, so
		// recovery cap 3 is computed but Settle does not apply it.
		sib := m.Feats{72: {
			Long:  &m.Window{N: 6, Err: 0, ErrRate: m.F(0), SlowRate: m.F(0), P50: m.F(5), P90: m.F(10)},
			Short: &m.Window{N: 0, Err: 0},
			Chan:  &m.ChanStat{N: 3, Err: 0, Client: 0, Eff: 0, Rate: 0},
		}}
		st := trust.Step(trustCfg, []int{72}, sib, nil, map[int]float64{72: 0.1878}, gap)[72]
		if st.Sev != 0 || st.Trust != 0.2278 {
			t.Fatalf("#72 recovery sev=%v value=%v want 0 / 0.2278", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("#72 why=%v want no notes on a clean recovery", st.Why)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: 6}}, map[int]float64{72: st.Trust}, []int{72})
		c := caps[72]
		if c == nil || c.Cap != 3 || c.Kind != "trust" {
			t.Fatalf("trust cap=%+v want cap=3 kind=trust", c)
		}
		if c.Reason != "信任 0.23 < 0.3且 1h 样本 6 < 20，恢复期限幅" {
			t.Fatalf("trust cap reason=%q", c.Reason)
		}
		next := map[int]int{72: 3}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, sib, []int{72}, caps, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; system cap 3 must not be applied", next[72])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#72 定档：策略 3→20", "系统上限 3", "不执行", "1h 真实 3", "渠道错误 0", "去集中 0", "0.0%", "未达降权门槛"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected held-cap note to contain %q, got %q", part, notes)
			}
		}
	})
}

// CASE-AW-041: sanitized aggregate shape from 2026-11-22 00:05 +08:00 CC #72,
// the real next window after CASE-AW-038's 0.2278 recovery. No production log
// rows, request identifiers, model names or real token IDs.
// A mid-recovery 1h slow_rate cut (16.13% -> "16%") lowers trust 0.2278 to
// 0.1868. n60 has now reached 31, so applyTrustCap skips the recovery cap
// even though trust is still below 0.3. The same window's higher-scoring
// #68 stays ineligible (n60=8), so #72 is promoted to rank 1; already-at-20
// emits no settle note, and ChanStat 27/0/0 does not meet 30/3/3%. This
// locks current v5.01 behavior; it does not change the algorithm.
func TestCasebookRecoveryInterruptedBySlowRateDropsCapWhenN60Reaches20(t *testing.T) {
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2931s.
	gap := m.F(2931.0 / 60.0)
	// Live #72 shape this round: scoring n=31 (27 user + 4 probes), empty=0,
	// err=0, slow_rate=16.13%, 10m n=8 err=0 slow_rate=12.5%. Prev trust
	// 0.2278 after the previous clean recovery step. ChanStat N=27, no
	// channel errors.
	live := m.Feats{72: {
		Long: &m.Window{
			N: 31, Err: 0, ErrRate: m.F(0),
			Empty: m.I(0), EmptyRate: m.F(0),
			P50: m.F(10), P90: m.F(90),
			SlowRate: m.F(0.1613),
		},
		Short: &m.Window{
			N: 8, Err: 0, ErrRate: m.F(0),
			SlowRate: m.F(0.125),
			P50:      m.F(13), P90: m.F(26),
		},
		Chan: &m.ChanStat{N: 27, Err: 0, Client: 0, Eff: 0, Rate: 0},
		Mix:  &m.Mix{NUser: 27, NProbe: 4, NErrReal: 0},
	}}

	t.Run("live_shape_cuts_0_2278_to_0_1868", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, live, nil, map[int]float64{72: 0.2278}, gap)[72]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// n=31 >= slow_min_n, slow_rate 16.13% > 15%, so sev=slow_sev=0.3
		// and the down branch runs: 0.2278*(1-0.6*0.3)=0.186796 -> 0.1868.
		// The 2931s gap is ignored on a down step; 10m is clean (err=0) so
		// it adds no short-window part. Why uses %.0f, so 16.13 becomes 16%.
		if st.Sev != 0.3 || st.Trust != 0.1868 {
			t.Fatalf("trust sev=%v value=%v want 0.3 / 0.1868", st.Sev, st.Trust)
		}
		if !strings.Contains(strings.Join(st.Why, ";"), "1h 慢请求 16%") {
			t.Fatalf("trust why=%v want 1h 慢请求 16%%", st.Why)
		}
	})

	t.Run("slow_rate_at_15_percent_would_fully_recover_past_0_3", func(t *testing.T) {
		// Same prev, gap and n=31, but SlowRate must be strictly above 15%.
		// Time factor is already capped (2931/60/45 > 1) and n60>=30, so
		// the up branch would add a full +0.2: 0.2278+0.2=0.4278.
		clean := m.Feats{72: {
			Long: &m.Window{
				N: 31, Err: 0, ErrRate: m.F(0),
				P50: m.F(10), P90: m.F(90),
				SlowRate: m.F(0.15),
			},
			Short: &m.Window{N: 8, Err: 0, SlowRate: m.F(0.125), P50: m.F(13), P90: m.F(26)},
			Chan:  &m.ChanStat{N: 27, Err: 0, Client: 0, Eff: 0, Rate: 0},
			Mix:   &m.Mix{NUser: 27, NProbe: 4, NErrReal: 0},
		}}
		st := trust.Step(trustCfg, []int{72}, clean, nil, map[int]float64{72: 0.2278}, gap)[72]
		if st.Sev != 0 || st.Trust != 0.4278 {
			t.Fatalf("slow_rate=15%% must recover, sev=%v value=%v want 0 / 0.4278", st.Sev, st.Trust)
		}
		if strings.Contains(strings.Join(st.Why, ";"), "慢请求") {
			t.Fatalf("slow_rate=15%% must not emit a slow-rate why, got %v", st.Why)
		}
	})

	t.Run("n60_31_does_not_compute_recovery_cap", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, live, nil, map[int]float64{72: 0.2278}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: 31}}, map[int]float64{72: st.Trust}, []int{72})
		if caps[72] != nil {
			t.Fatalf("trust cap=%+v; n60=31 >= 20 must skip the recovery cap even though trust 0.1868 < 0.3", caps[72])
		}
	})

	t.Run("n60_19_still_computes_unapplied_cap_3", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, live, nil, map[int]float64{72: 0.2278}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: 19}}, map[int]float64{72: st.Trust}, []int{72})
		c := caps[72]
		if c == nil || c.Cap != 3 || c.Kind != "trust" {
			t.Fatalf("trust cap=%+v want cap=3 kind=trust when n60=19 < 20", c)
		}
		if c.Reason != "信任 0.19 < 0.3且 1h 样本 19 < 20，恢复期限幅" {
			t.Fatalf("trust cap reason=%q", c.Reason)
		}
		next := map[int]int{72: 3}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, live, []int{72}, caps, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; system cap 3 must not be applied", next[72])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#72 定档：策略 3→20", "系统上限 3", "不执行", "1h 真实 27", "渠道错误 0", "去集中 0", "0.0%", "未达降权门槛"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected held-cap note to contain %q, got %q", part, notes)
			}
		}
	})

	t.Run("already_at_20_rank1_has_no_settle_note", func(t *testing.T) {
		next := map[int]int{72: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, live, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[72], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("strategy already 20 should not emit a settle note, got %q", notes)
		}
	})

	t.Run("ineligible_higher_score_68_is_promoted_over_by_72", func(t *testing.T) {
		// #68 has the higher raw/final score but n60=8 < min_n_rank1=10, so
		// it cannot lead; eligible #72 (n60=31) is promoted even though
		// its score was trimmed by trust 0.1868. #67 stays at score 0
		// because empty_weight 2 * empty_rate 2/3 saturates bad.
		cfg := m.M{
			"windows_min": []any{60.0, 10.0},
			"strategy":    "rank",
			"min_n_rank1": 10.0,
			"w_min":       0.0,
			"w_max":       20.0,
			"trust":       m.M{"enabled": true, "floor": 0.85},
			"score": m.M{
				"a_p50": 1.0, "b_p90": 0.0, "c_ttft": 0.0, "t_ref": 20.0,
				"min_n_window": 5.0, "empty_weight": 2.0, "err_exp": 2.0, "w_10m": 0.0, "ttft_missing_s": 30.0,
			},
			"strategies": m.M{"rank": m.M{"top_w": 20.0, "close_ratio": 0.85, "close_factor": 0.8, "mid_ratio": 0.7, "mid_w": 3.0, "low_w": 0.0, "hysteresis": 0.0, "band": 0.0, "ramp": 0.0, "lead_min_n": 0.0}},
			"settle":     m.M{"cut_needs_fault": true, "raise_clean": false},
			"trust_cap":  m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0},
		}
		fs := m.Feats{
			67: {
				Long: &m.Window{
					N: 6, Err: 0, ErrRate: m.F(0),
					Empty: m.I(4), EmptyRate: m.F(0.6667),
					P50: m.F(3), P90: m.F(87),
					SlowRate: m.F(0.1667),
				},
				Short: &m.Window{N: 1, Err: 0, P50: m.F(2), P90: m.F(2)},
				Chan:  &m.ChanStat{N: 3, Err: 0, Client: 0, Eff: 0, Rate: 0},
				Mix:   &m.Mix{NUser: 3, NProbe: 3, NErrReal: 0},
			},
			68: {
				Long:  &m.Window{N: 8, Err: 0, ErrRate: m.F(0), P50: m.F(4), P90: m.F(10), SlowRate: m.F(0)},
				Short: &m.Window{N: 1, Err: 0, P50: m.F(4), P90: m.F(4)},
				Chan:  &m.ChanStat{N: 5, Err: 0, Client: 0, Eff: 0, Rate: 0},
				Mix:   &m.Mix{NUser: 5, NProbe: 3, NErrReal: 0},
			},
			72: live[72],
		}
		d, err := Decide(Input{
			Cfg:      cfg,
			Chans:    []m.Channel{{ID: 67}, {ID: 68}, {ID: 72}},
			Feats:    fs,
			Strategy: "rank",
			Prev:     map[int]int{67: 20, 68: 20, 72: 20},
			Trust:    map[int]float64{67: 0.4145, 68: 1, 72: 0.1868},
		})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if d.Scores[68].EligibleRank1 || d.Scores[67].EligibleRank1 {
			t.Fatalf("thin channels should stay ineligible, scores=%v", d.Scores)
		}
		if !d.Scores[72].EligibleRank1 {
			t.Fatalf("n60=31 should be eligible for rank 1")
		}
		if d.Scores[68].N60 != 8 || d.Scores[72].N60 != 31 || d.Scores[67].N60 != 6 {
			t.Fatalf("n60 unexpected: 68=%d 72=%d 67=%d", d.Scores[68].N60, d.Scores[72].N60, d.Scores[67].N60)
		}
		if d.Scores[68].RawScore == nil || d.Scores[72].RawScore == nil || d.Scores[67].RawScore == nil {
			t.Fatalf("missing raw scores: %+v", d.Scores)
		}
		if *d.Scores[67].RawScore != 0 || d.Scores[67].Score != 0 {
			t.Fatalf("#67 empty_weight must zero raw/final score, raw=%v score=%v", d.Scores[67].RawScore, d.Scores[67].Score)
		}
		if *d.Scores[68].RawScore <= *d.Scores[72].RawScore || d.Scores[68].Score <= d.Scores[72].Score {
			t.Fatalf("score order before promotion should keep #68 ahead, 68 raw=%v score=%v 72 raw=%v score=%v", *d.Scores[68].RawScore, d.Scores[68].Score, *d.Scores[72].RawScore, d.Scores[72].Score)
		}
		if d.Scores[72].Score >= *d.Scores[72].RawScore {
			t.Fatalf("#72 trust multiplier (floor 0.85) should trim the score, raw=%v score=%v", *d.Scores[72].RawScore, d.Scores[72].Score)
		}
		if got := d.Order; len(got) != 3 || got[0] != 72 || got[1] != 68 || got[2] != 67 {
			t.Fatalf("order=%v want [72 68 67] after promoting the eligible second over the thin leader", d.Order)
		}
		if d.Target[67] != 20 || d.Target[68] != 20 || d.Target[72] != 20 {
			t.Fatalf("weights=%v want all held at 20", d.Target)
		}
		if d.Caps[72] != nil {
			t.Fatalf("#72 n60=31 must not compute recovery cap 3, got %+v", d.Caps[72])
		}
		joined := strings.Join(d.Notes, ";")
		if !strings.Contains(joined, "#68 样本不足，不得排第一，由 #72 顶上") {
			t.Fatalf("missing promotion note, got %q", d.Notes)
		}
		if strings.Contains(joined, "#72 定档") {
			t.Fatalf("promoted leader already at 20 should not emit a settle note, got %q", d.Notes)
		}
		if !strings.Contains(joined, "#68 定档：策略 16→20") || !strings.Contains(joined, "1h 真实 5") {
			t.Fatalf("expected #68 close band settled 16→20, got %q", d.Notes)
		}
		if !strings.Contains(joined, "#67 定档：策略 0→20") || !strings.Contains(joined, "1h 真实 3") {
			t.Fatalf("expected #67 low band settled 0→20, got %q", d.Notes)
		}
		for _, part := range []string{"无渠道级错误证据", "渠道错误 0", "去集中 0", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle hold note to contain %q, got %q", part, d.Notes)
			}
		}
		if strings.Contains(joined, "达降权门槛") && !strings.Contains(joined, "未达降权门槛") {
			t.Fatalf("did not expect a fault cut, got %q", d.Notes)
		}
	})

	t.Run("sibling_67_mid_trust_again_cut_by_slow_and_empty_zeros_score", func(t *testing.T) {
		// Same 2931s gap. Live #67 this round is a CASE-AW-040 follow-on:
		// n=6, empty=4 / empty_rate=0.6667, slow_rate=16.67% -> "17%",
		// 10m n=1 clean, prev 0.5055. 0.5055*(1-0.6*0.3)=0.41451 -> 0.4145.
		// Trust stays >= 0.3, so no recovery cap; Settle holds 0→20.
		sib := m.Feats{67: {
			Long: &m.Window{
				N: 6, Err: 0, ErrRate: m.F(0),
				Empty: m.I(4), EmptyRate: m.F(0.6667),
				P50: m.F(3), P90: m.F(87),
				SlowRate: m.F(0.1667),
			},
			Short: &m.Window{N: 1, Err: 0, P50: m.F(2), P90: m.F(2)},
			Chan:  &m.ChanStat{N: 3, Err: 0, Client: 0, Eff: 0, Rate: 0},
			Mix:   &m.Mix{NUser: 3, NProbe: 3, NErrReal: 0},
		}}
		st := trust.Step(trustCfg, []int{67}, sib, nil, map[int]float64{67: 0.5055}, gap)[67]
		if st.Sev != 0.3 || st.Trust != 0.4145 {
			t.Fatalf("#67 trust sev=%v value=%v want 0.3 / 0.4145", st.Sev, st.Trust)
		}
		if !strings.Contains(strings.Join(st.Why, ";"), "1h 慢请求 17%") {
			t.Fatalf("#67 why=%v want 1h 慢请求 17%%", st.Why)
		}
		zero := ScoreWindow(sib[67].Long, 0, m.M{
			"a_p50": 1.0, "b_p90": 0.0, "c_ttft": 0.0, "t_ref": 20.0,
			"min_n_window": 5.0, "empty_weight": 2.0, "err_exp": 2.0,
		}, Pair{}, nil)
		if zero == nil || *zero != 0 {
			t.Fatalf("empty_weight 2 * empty_rate 0.6667 must zero raw_score, got %v", zero)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: 6}}, map[int]float64{67: st.Trust}, []int{67})
		if caps[67] != nil {
			t.Fatalf("trust cap=%+v; trust 0.4145 is not < 0.3 so recovery cap 3 must not be computed", caps[67])
		}
		next := map[int]int{67: 0}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, sib, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20 after settle hold; notes=%q", next[67], notes)
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#67 定档：策略 0→20", "无渠道级错误证据", "1h 真实 3", "渠道错误 0", "去集中 0", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle hold note to contain %q, got %q", part, notes)
			}
		}
	})
}

// CASE-AW-042: sanitized aggregate shape from 2026-11-22 00:43 +08:00 CC #72,
// the real next window after CASE-AW-041's mid-recovery slow-rate cut to
// 0.1868. No production log rows, request identifiers, model names or real
// token IDs. Scoring-window errors (10m 3/13 and 1h 3/42=7.1%) take
// severity to 1 and clamp trust from 0.1868 down to the 0.1 floor. ChanStat
// is N=37, Err=2, Client=1, Eff=2, Rate=2/37=0.0541: N and Rate meet the
// 30/3/3% gate but Eff=2 does not, so Settle holds 0→20. n60=42 still
// skips the recovery cap. This locks current v5.01 behavior; it does not
// change the algorithm.
func TestCasebookMidRecoveryErrorRateFloorsTrustWhileChanStatEffBelow3(t *testing.T) {
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2260s.
	gap := m.F(2260.0 / 60.0)
	// Live #72 shape this round: scoring n=42 (38 user + 4 probes), empty=0,
	// err=3, err_rate=3/42, slow_rate=12.82% (at or under the 15% slow
	// threshold), 10m n=13 err=3. Prev trust 0.1868 after the previous
	// mid-recovery slow-rate cut. ChanStat N=37, Err=2, Client=1, Eff=2,
	// Rate=2/37; TopErr=2 does not exceed tok_cap so concentration does
	// not discount.
	live := m.Feats{72: {
		Long: &m.Window{
			N: 42, Err: 3, ErrRate: m.F(3.0 / 42.0),
			Empty: m.I(0), EmptyRate: m.F(0),
			P50: m.F(9), P90: m.F(87),
			SlowRate: m.F(0.1282),
		},
		Short: &m.Window{
			N: 13, Err: 3, ErrRate: m.F(3.0 / 13.0),
			SlowRate: m.F(0),
			P50:      m.F(9), P90: m.F(12),
		},
		Chan: &m.ChanStat{N: 37, Err: 2, Client: 1, Eff: 2, Rate: m.Round(2.0/37.0, 4), ErrTok: 1, Toks: 4, TopTok: 900072, TopErr: 2},
		Mix:  &m.Mix{NUser: 38, NProbe: 4, NErrReal: 3},
	}}

	t.Run("live_shape_floors_0_1868_to_0_1", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, live, nil, map[int]float64{72: 0.1868}, gap)[72]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// 10m 3/13: sev10=min(1, (3/13)/0.2)=1. 1h 3/42=7.14%>2%:
		// sev1h=min(1, (3/42-0.02)/0.2)=0.257. SlowRate 12.82% is not
		// strictly above 15%, so no slow part. maxSev=1, down branch:
		// 0.1868*(1-0.6*1)=0.07472, then t_min clamps to 0.1. The 2260s
		// gap is ignored on a down step.
		if st.Sev != 1 || st.Trust != 0.1 {
			t.Fatalf("trust sev=%v value=%v want 1 / 0.1", st.Sev, st.Trust)
		}
		joined := strings.Join(st.Why, ";")
		if !strings.Contains(joined, "10m 错误 3/13") {
			t.Fatalf("trust why=%v want 10m 错误 3/13", st.Why)
		}
		if !strings.Contains(joined, "1h 错误率 7.1%") {
			t.Fatalf("trust why=%v want 1h 错误率 7.1%%", st.Why)
		}
		if strings.Contains(joined, "慢请求") {
			t.Fatalf("12.82%% slow_rate must not emit a slow-rate why, got %v", st.Why)
		}
	})

	t.Run("clean_errors_recover_to_0_3542_despite_same_slow_rate", func(t *testing.T) {
		// Same prev, gap, n=42 and SlowRate 12.82%, but no scoring-window
		// errors. Time factor is 2260/60/45=0.8370... (not yet capped) and
		// n60>=30, so the up branch adds 0.2*0.8370...: 0.1868+0.1674=0.3542.
		clean := m.Feats{72: {
			Long: &m.Window{
				N: 42, Err: 0, ErrRate: m.F(0),
				P50: m.F(9), P90: m.F(87),
				SlowRate: m.F(0.1282),
			},
			Short: &m.Window{N: 13, Err: 0, ErrRate: m.F(0), SlowRate: m.F(0), P50: m.F(9), P90: m.F(12)},
			Chan:  &m.ChanStat{N: 37, Err: 2, Client: 1, Eff: 2, Rate: m.Round(2.0/37.0, 4)},
			Mix:   &m.Mix{NUser: 38, NProbe: 4, NErrReal: 0},
		}}
		st := trust.Step(trustCfg, []int{72}, clean, nil, map[int]float64{72: 0.1868}, gap)[72]
		if st.Sev != 0 || st.Trust != 0.3542 {
			t.Fatalf("clean errors must recover, sev=%v value=%v want 0 / 0.3542", st.Sev, st.Trust)
		}
		if strings.Contains(strings.Join(st.Why, ";"), "错误") || strings.Contains(strings.Join(st.Why, ";"), "慢请求") {
			t.Fatalf("clean-error recovery must not emit a penalty why, got %v", st.Why)
		}
	})

	t.Run("clean_10m_with_1h_7pct_cuts_to_0_1580_not_floor", func(t *testing.T) {
		// Isolates the 10m 3/13 contribution: 1h 7.1% alone is sev=0.257,
		// 0.1868*(1-0.6*0.257)=0.1580, which is above the 0.1 floor. The
		// live floor therefore requires the short-window part.
		only1h := m.Feats{72: {
			Long: &m.Window{
				N: 42, Err: 3, ErrRate: m.F(3.0 / 42.0),
				P50: m.F(9), P90: m.F(87),
				SlowRate: m.F(0.1282),
			},
			Short: &m.Window{N: 13, Err: 0, ErrRate: m.F(0), SlowRate: m.F(0), P50: m.F(9), P90: m.F(12)},
			Chan:  &m.ChanStat{N: 37, Err: 2, Client: 1, Eff: 2, Rate: m.Round(2.0/37.0, 4)},
			Mix:   &m.Mix{NUser: 38, NProbe: 4, NErrReal: 3},
		}}
		st := trust.Step(trustCfg, []int{72}, only1h, nil, map[int]float64{72: 0.1868}, gap)[72]
		if st.Sev != 0.257 || st.Trust != 0.158 {
			t.Fatalf("1h-only cut sev=%v value=%v want 0.257 / 0.1580", st.Sev, st.Trust)
		}
		joined := strings.Join(st.Why, ";")
		if !strings.Contains(joined, "1h 错误率 7.1%") {
			t.Fatalf("trust why=%v want 1h 错误率 7.1%%", st.Why)
		}
		if strings.Contains(joined, "10m 错误") {
			t.Fatalf("clean 10m must not emit a 10m why, got %v", st.Why)
		}
	})

	t.Run("n60_42_does_not_compute_recovery_cap", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, live, nil, map[int]float64{72: 0.1868}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: 42}}, map[int]float64{72: st.Trust}, []int{72})
		if caps[72] != nil {
			t.Fatalf("trust cap=%+v; n60=42 >= 20 must skip the recovery cap even though trust 0.1 < 0.3", caps[72])
		}
	})

	t.Run("settle_holds_0_to_20_when_eff_is_2_even_if_rate_meets", func(t *testing.T) {
		next := map[int]int{72: 0}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, live, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; Eff=2 must not meet 30/3/3%% even though N=37 and Rate>=3%%; notes=%q", next[72], notes)
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#72 定档：策略 0→20", "无渠道级错误证据", "1h 真实 37", "渠道错误 2", "去集中 2", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle hold note to contain %q, got %q", part, notes)
			}
		}
		if strings.Contains(joined, "达降权门槛") && !strings.Contains(joined, "未达降权门槛") {
			t.Fatalf("did not expect a fault cut, got %q", notes)
		}
	})

	t.Run("all_three_eligible_so_no_promotion_note_and_weights_held", func(t *testing.T) {
		// Contrast CASE-AW-041: this window every CC channel has n60>=10,
		// so there is no "样本不足，不得排第一" promotion. Weights stay 20
		// because ChanStat never meets 30/3/3%. Order is not asserted
		// against the live scores; this uses the same simplified score
		// config as earlier casebook tests.
		cfg := m.M{
			"windows_min": []any{60.0, 10.0},
			"strategy":    "rank",
			"min_n_rank1": 10.0,
			"w_min":       0.0,
			"w_max":       20.0,
			"trust":       m.M{"enabled": true, "floor": 0.85},
			"score": m.M{
				"a_p50": 1.0, "b_p90": 0.0, "c_ttft": 0.0, "t_ref": 20.0,
				"min_n_window": 5.0, "empty_weight": 2.0, "err_exp": 2.0, "w_10m": 0.0, "ttft_missing_s": 30.0,
			},
			"strategies": m.M{"rank": m.M{"top_w": 20.0, "close_ratio": 0.85, "close_factor": 0.8, "mid_ratio": 0.7, "mid_w": 3.0, "low_w": 0.0, "hysteresis": 0.0, "band": 0.0, "ramp": 0.0, "lead_min_n": 0.0}},
			"settle":     m.M{"cut_needs_fault": true, "raise_clean": false},
			"trust_cap":  m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0},
		}
		fs := m.Feats{
			67: {
				Long: &m.Window{
					N: 23, Err: 1, ErrRate: m.F(1.0 / 23.0),
					Empty: m.I(3), EmptyRate: m.F(3.0 / 23.0),
					P50: m.F(8), P90: m.F(19),
					SlowRate: m.F(0),
				},
				Short: &m.Window{N: 15, Err: 1, ErrRate: m.F(1.0 / 15.0), P50: m.F(10), P90: m.F(23)},
				Chan:  &m.ChanStat{N: 18, Err: 0, Client: 1, Eff: 0, Rate: 0},
				Mix:   &m.Mix{NUser: 19, NProbe: 4, NErrReal: 1},
			},
			68: {
				Long: &m.Window{
					N: 32, Err: 2, ErrRate: m.F(2.0 / 32.0),
					Empty: m.I(0), EmptyRate: m.F(0),
					P50: m.F(8), P90: m.F(47),
					SlowRate: m.F(0.1),
				},
				Short: &m.Window{N: 22, Err: 2, ErrRate: m.F(2.0 / 22.0), P50: m.F(9), P90: m.F(67)},
				Chan:  &m.ChanStat{N: 26, Err: 0, Client: 2, Eff: 0, Rate: 0},
				Mix:   &m.Mix{NUser: 28, NProbe: 4, NErrReal: 2},
			},
			72: live[72],
		}
		d, err := Decide(Input{
			Cfg:      cfg,
			Chans:    []m.Channel{{ID: 67}, {ID: 68}, {ID: 72}},
			Feats:    fs,
			Strategy: "rank",
			Prev:     map[int]int{67: 20, 68: 20, 72: 20},
			Trust:    map[int]float64{67: 0.3317, 68: 0.7276, 72: 0.1},
		})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if !d.Scores[67].EligibleRank1 || !d.Scores[68].EligibleRank1 || !d.Scores[72].EligibleRank1 {
			t.Fatalf("all three should be eligible, scores=%v", d.Scores)
		}
		if d.Scores[67].N60 != 23 || d.Scores[68].N60 != 32 || d.Scores[72].N60 != 42 {
			t.Fatalf("n60 unexpected: 67=%d 68=%d 72=%d", d.Scores[67].N60, d.Scores[68].N60, d.Scores[72].N60)
		}
		if d.Target[67] != 20 || d.Target[68] != 20 || d.Target[72] != 20 {
			t.Fatalf("weights=%v want all held at 20", d.Target)
		}
		if d.Caps[67] != nil || d.Caps[68] != nil || d.Caps[72] != nil {
			t.Fatalf("n60>=20 or trust>=0.3 must not compute recovery cap 3, got %+v", d.Caps)
		}
		joined := strings.Join(d.Notes, ";")
		if strings.Contains(joined, "不得排第一") || strings.Contains(joined, "顶上") {
			t.Fatalf("all eligible channels must not emit a promotion note, got %q", d.Notes)
		}
		if strings.Contains(joined, "达降权门槛") && !strings.Contains(joined, "未达降权门槛") {
			t.Fatalf("did not expect a fault cut, got %q", d.Notes)
		}
	})

	t.Run("sibling_67_error_rate_cut_from_0_4145_to_0_3317", func(t *testing.T) {
		// Same 2260s gap. Live #67 this round is a CASE-AW-040 follow-on,
		// but the mechanism has changed: slow_rate is now 0, and trust is
		// cut by 10m 1/15 plus 1h 1/23=4.3%. Prev 0.4145.
		// sev10=Round((1/15)/0.2, 3)=0.333; sev1h=Round((1/23-0.02)/0.2, 3)=0.117;
		// maxSev=0.333; 0.4145*(1-0.6*0.333)=0.3317. ChanStat is all client
		// (N=18, Err=0, Client=1). Trust stays >=0.3, so no recovery cap.
		sib := m.Feats{67: {
			Long: &m.Window{
				N: 23, Err: 1, ErrRate: m.F(1.0 / 23.0),
				Empty: m.I(3), EmptyRate: m.F(3.0 / 23.0),
				P50: m.F(8), P90: m.F(19),
				SlowRate: m.F(0),
			},
			Short: &m.Window{N: 15, Err: 1, ErrRate: m.F(1.0 / 15.0), P50: m.F(10), P90: m.F(23)},
			Chan:  &m.ChanStat{N: 18, Err: 0, Client: 1, Eff: 0, Rate: 0},
			Mix:   &m.Mix{NUser: 19, NProbe: 4, NErrReal: 1},
		}}
		st := trust.Step(trustCfg, []int{67}, sib, nil, map[int]float64{67: 0.4145}, gap)[67]
		if st.Sev != 0.333 || st.Trust != 0.3317 {
			t.Fatalf("#67 trust sev=%v value=%v want 0.333 / 0.3317", st.Sev, st.Trust)
		}
		joined := strings.Join(st.Why, ";")
		if !strings.Contains(joined, "10m 错误 1/15") {
			t.Fatalf("#67 why=%v want 10m 错误 1/15", st.Why)
		}
		if !strings.Contains(joined, "1h 错误率 4.3%") {
			t.Fatalf("#67 why=%v want 1h 错误率 4.3%%", st.Why)
		}
		if strings.Contains(joined, "慢请求") {
			t.Fatalf("#67 slow_rate=0 must not emit a slow-rate why, got %v", st.Why)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: 23}}, map[int]float64{67: st.Trust}, []int{67})
		if caps[67] != nil {
			t.Fatalf("trust cap=%+v; trust 0.3317 is not < 0.3 so recovery cap 3 must not be computed", caps[67])
		}
		next := map[int]int{67: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, sib, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[67], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("strategy already 20 should not emit a settle note, got %q", notes)
		}
	})
}

// CASE-AW-043: sanitized aggregate shape from 2026-11-22 01:28 +08:00 CC #72,
// the real next window after CASE-AW-042 floored trust at 0.1. No production
// log rows, request identifiers, model names or real token IDs. The 10m
// window is now empty (n=0), so the 10m error path does not fire
// (err_10m_min_n=2). The 1h raw rate is still 3/21=14.3% > 2%, which is
// enough on its own to keep the floor: 0.1*(1-0.6*0.614)=0.06316, then
// t_min clamps back to 0.1. The inter-round gap is 2706s so the time
// factor is already capped at 1, but the down branch ignores it.
// ChanStat has thinned to N=16, Err=2, Client=1, Eff=2, Rate=2/16=0.125:
// N and Eff both miss the 30/3/3% gate (the previous window only missed
// Eff), so Settle holds 3→20. n60=21 still skips the recovery cap. This
// locks current v5.01 behavior; it does not change the algorithm.
func TestCasebookEmptyTenMinKeepsTrustFloorWhenHourRateStaysHigh(t *testing.T) {
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2706s.
	gap := m.F(2706.0 / 60.0)
	// Live #72 shape this round: scoring n=21 (17 user + 4 probes), empty=0,
	// err=3, err_rate=3/21, slow_rate=0, 10m n=0. Prev trust already at
	// the 0.1 floor. ChanStat N=16, Err=2, Client=1, Eff=2, Rate=2/16;
	// TopErr=2 does not exceed tok_cap so concentration does not discount.
	live := m.Feats{72: {
		Long: &m.Window{
			N: 21, Err: 3, ErrRate: m.F(3.0 / 21.0),
			Empty: m.I(0), EmptyRate: m.F(0),
			P50: m.F(8), P90: m.F(12),
			SlowRate: m.F(0),
		},
		Short: &m.Window{N: 0, Err: 0},
		Chan:  &m.ChanStat{N: 16, Err: 2, Client: 1, Eff: 2, Rate: m.Round(2.0/16.0, 4), ErrTok: 1, Toks: 3, TopTok: 900072, TopErr: 2},
		Mix:   &m.Mix{NUser: 17, NProbe: 4, NErrReal: 3},
	}}

	t.Run("live_shape_keeps_floor_0_1_with_empty_10m", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, live, nil, map[int]float64{72: 0.1}, gap)[72]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// Empty 10m (n=0) is below err_10m_min_n=2, so no 10m part.
		// 1h 3/21=14.29%>2%: sev1h=min(1, (3/21-0.02)/0.2)=0.614285...
		// rounded to 0.614. SlowRate=0, so no slow part. maxSev=0.614,
		// down branch: 0.1*(1-0.6*0.614)=0.06316, then t_min clamps to
		// 0.1. The already-capped 2706s gap is ignored on a down step.
		if st.Sev != 0.614 || st.Trust != 0.1 {
			t.Fatalf("trust sev=%v value=%v want 0.614 / 0.1", st.Sev, st.Trust)
		}
		joined := strings.Join(st.Why, ";")
		if !strings.Contains(joined, "1h 错误率 14.3%") {
			t.Fatalf("trust why=%v want 1h 错误率 14.3%%", st.Why)
		}
		if strings.Contains(joined, "10m 错误") {
			t.Fatalf("empty 10m must not emit a 10m why, got %v", st.Why)
		}
		if strings.Contains(joined, "慢请求") {
			t.Fatalf("slow_rate=0 must not emit a slow-rate why, got %v", st.Why)
		}
	})

	t.Run("clean_1h_recovers_to_0_24_with_capped_factor_and_21_of_30", func(t *testing.T) {
		// Same prev, gap and n=21, but no scoring-window errors. Time
		// factor is already capped (2706/60/45>1) and sample factor is
		// 21/30, so the up branch adds 0.2*1*(21/30): 0.1+0.14=0.24.
		clean := m.Feats{72: {
			Long: &m.Window{
				N: 21, Err: 0, ErrRate: m.F(0),
				P50: m.F(8), P90: m.F(12),
				SlowRate: m.F(0),
			},
			Short: &m.Window{N: 0, Err: 0},
			Chan:  &m.ChanStat{N: 16, Err: 2, Client: 1, Eff: 2, Rate: m.Round(2.0/16.0, 4)},
			Mix:   &m.Mix{NUser: 17, NProbe: 4, NErrReal: 0},
		}}
		st := trust.Step(trustCfg, []int{72}, clean, nil, map[int]float64{72: 0.1}, gap)[72]
		if st.Sev != 0 || st.Trust != 0.24 {
			t.Fatalf("clean 1h must recover, sev=%v value=%v want 0 / 0.24", st.Sev, st.Trust)
		}
		if strings.Contains(strings.Join(st.Why, ";"), "错误") || strings.Contains(strings.Join(st.Why, ";"), "慢请求") {
			t.Fatalf("clean-error recovery must not emit a penalty why, got %v", st.Why)
		}
	})

	t.Run("populated_clean_10m_same_1h_cut_as_empty_10m", func(t *testing.T) {
		// An empty 10m and a populated but clean 10m (n>=2, err=0) are
		// equivalent on the 10m path: neither emits a 10m why, and the
		// 1h 14.3% cut is unchanged.
		populated := m.Feats{72: {
			Long: &m.Window{
				N: 21, Err: 3, ErrRate: m.F(3.0 / 21.0),
				P50: m.F(8), P90: m.F(12),
				SlowRate: m.F(0),
			},
			Short: &m.Window{N: 6, Err: 0, ErrRate: m.F(0), P50: m.F(8), P90: m.F(12)},
			Chan:  &m.ChanStat{N: 16, Err: 2, Client: 1, Eff: 2, Rate: m.Round(2.0/16.0, 4)},
			Mix:   &m.Mix{NUser: 17, NProbe: 4, NErrReal: 3},
		}}
		emptySt := trust.Step(trustCfg, []int{72}, live, nil, map[int]float64{72: 0.1}, gap)[72]
		popSt := trust.Step(trustCfg, []int{72}, populated, nil, map[int]float64{72: 0.1}, gap)[72]
		if popSt.Sev != emptySt.Sev || popSt.Trust != emptySt.Trust {
			t.Fatalf("populated clean 10m sev=%v value=%v want same as empty 10m sev=%v value=%v", popSt.Sev, popSt.Trust, emptySt.Sev, emptySt.Trust)
		}
		if strings.Contains(strings.Join(popSt.Why, ";"), "10m 错误") {
			t.Fatalf("populated clean 10m must not emit a 10m why, got %v", popSt.Why)
		}
	})

	t.Run("n60_21_does_not_compute_recovery_cap", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, live, nil, map[int]float64{72: 0.1}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: 21}}, map[int]float64{72: st.Trust}, []int{72})
		if caps[72] != nil {
			t.Fatalf("trust cap=%+v; n60=21 >= 20 must skip the recovery cap even though trust 0.1 < 0.3", caps[72])
		}
	})

	t.Run("settle_holds_3_to_20_when_n_is_16", func(t *testing.T) {
		next := map[int]int{72: 3}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, live, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; N=16 must not meet 30/3/3%% even though Rate>=3%% and Eff=2; notes=%q", next[72], notes)
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#72 定档：策略 3→20", "无渠道级错误证据", "1h 真实 16", "渠道错误 2", "去集中 2", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected settle hold note to contain %q, got %q", part, notes)
			}
		}
		if strings.Contains(joined, "达降权门槛") && !strings.Contains(joined, "未达降权门槛") {
			t.Fatalf("did not expect a fault cut, got %q", notes)
		}
	})

	t.Run("sibling_67_empty_10m_1h_4_3pct_cuts_0_3317_to_0_3084", func(t *testing.T) {
		// Same empty-10m shape as #72, but a mid-trust 1h 1/23=4.3% cut
		// that does not reach the floor. Time factor is capped and unused.
		sib := m.Feats{67: {
			Long: &m.Window{
				N: 23, Err: 1, ErrRate: m.F(1.0 / 23.0),
				Empty: m.I(2), EmptyRate: m.F(2.0 / 23.0),
				P50: m.F(9), P90: m.F(23),
				SlowRate: m.F(0),
			},
			Short: &m.Window{N: 0, Err: 0},
			Chan:  &m.ChanStat{N: 18, Err: 0, Client: 1, Eff: 0, Rate: 0},
			Mix:   &m.Mix{NUser: 19, NProbe: 4, NErrReal: 1},
		}}
		st := trust.Step(trustCfg, []int{67}, sib, nil, map[int]float64{67: 0.3317}, gap)[67]
		// sev1h=min(1, (1/23-0.02)/0.2)=0.11739... rounded to 0.117.
		// 0.3317*(1-0.6*0.117)=0.3084.
		if st.Sev != 0.117 || st.Trust != 0.3084 {
			t.Fatalf("#67 trust sev=%v value=%v want 0.117 / 0.3084", st.Sev, st.Trust)
		}
		joined := strings.Join(st.Why, ";")
		if !strings.Contains(joined, "1h 错误率 4.3%") {
			t.Fatalf("#67 why=%v want 1h 错误率 4.3%%", st.Why)
		}
		if strings.Contains(joined, "10m 错误") || strings.Contains(joined, "慢请求") {
			t.Fatalf("empty 10m and slow_rate=0 must not emit those whys, got %v", st.Why)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: 23}}, map[int]float64{67: st.Trust}, []int{67})
		if caps[67] != nil {
			t.Fatalf("trust cap=%+v; n60=23 >= 20 must skip the recovery cap, and trust 0.3084 is not < 0.3", caps[67])
		}
		next := map[int]int{67: 3}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{67: 20}, sib, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[67], notes)
		}
		if !strings.Contains(strings.Join(notes, ";"), "#67 定档：策略 3→20") {
			t.Fatalf("expected settle hold 3→20, got %q", notes)
		}
	})

	t.Run("sibling_68_clean_thin_10m_1h_4_1pct_cuts_0_7276_to_0_6822", func(t *testing.T) {
		// Contrast the empty 10m path with a populated but clean thin 10m
		// (0/3). 1h 2/49=4.08% still mid-multiplies 0.7276→0.6822.
		sib := m.Feats{68: {
			Long: &m.Window{
				N: 49, Err: 2, ErrRate: m.F(2.0 / 49.0),
				Empty: m.I(0), EmptyRate: m.F(0),
				P50: m.F(6), P90: m.F(40),
				SlowRate: m.F(0.0638),
			},
			Short: &m.Window{N: 3, Err: 0, ErrRate: m.F(0), SlowRate: m.F(0), P50: m.F(8), P90: m.F(15)},
			Chan:  &m.ChanStat{N: 43, Err: 0, Client: 2, Eff: 0, Rate: 0},
			Mix:   &m.Mix{NUser: 45, NProbe: 4, NErrReal: 2},
		}}
		st := trust.Step(trustCfg, []int{68}, sib, nil, map[int]float64{68: 0.7276}, gap)[68]
		// sev1h=min(1, (2/49-0.02)/0.2)=0.10408... rounded to 0.104.
		// SlowRate 6.38% is not strictly above 15%. 0.7276*(1-0.6*0.104)=0.6822.
		if st.Sev != 0.104 || st.Trust != 0.6822 {
			t.Fatalf("#68 trust sev=%v value=%v want 0.104 / 0.6822", st.Sev, st.Trust)
		}
		joined := strings.Join(st.Why, ";")
		if !strings.Contains(joined, "1h 错误率 4.1%") {
			t.Fatalf("#68 why=%v want 1h 错误率 4.1%%", st.Why)
		}
		if strings.Contains(joined, "10m 错误") || strings.Contains(joined, "慢请求") {
			t.Fatalf("clean 10m 0/3 and slow_rate=6.38%% must not emit those whys, got %v", st.Why)
		}
	})

	t.Run("all_three_eligible_so_no_promotion_note_and_weights_held", func(t *testing.T) {
		cfg := m.M{
			"windows_min": []any{60.0, 10.0},
			"strategy":    "rank",
			"min_n_rank1": 10.0,
			"w_min":       0.0,
			"w_max":       20.0,
			"trust":       m.M{"enabled": true, "floor": 0.85},
			"score": m.M{
				"a_p50": 1.0, "b_p90": 0.0, "c_ttft": 0.0, "t_ref": 20.0,
				"min_n_window": 5.0, "empty_weight": 2.0, "err_exp": 2.0, "w_10m": 0.0, "ttft_missing_s": 30.0,
			},
			"strategies": m.M{"rank": m.M{"top_w": 20.0, "close_ratio": 0.85, "close_factor": 0.8, "mid_ratio": 0.7, "mid_w": 3.0, "low_w": 0.0, "hysteresis": 0.0, "band": 0.0, "ramp": 0.0, "lead_min_n": 0.0}},
			"settle":     m.M{"cut_needs_fault": true, "raise_clean": false},
			"trust_cap":  m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0},
		}
		fs := m.Feats{
			67: {
				Long: &m.Window{
					N: 23, Err: 1, ErrRate: m.F(1.0 / 23.0),
					Empty: m.I(2), EmptyRate: m.F(2.0 / 23.0),
					P50: m.F(9), P90: m.F(23),
					SlowRate: m.F(0),
				},
				Short: &m.Window{N: 0, Err: 0},
				Chan:  &m.ChanStat{N: 18, Err: 0, Client: 1, Eff: 0, Rate: 0},
				Mix:   &m.Mix{NUser: 19, NProbe: 4, NErrReal: 1},
			},
			68: {
				Long: &m.Window{
					N: 49, Err: 2, ErrRate: m.F(2.0 / 49.0),
					Empty: m.I(0), EmptyRate: m.F(0),
					P50: m.F(6), P90: m.F(40),
					SlowRate: m.F(0.0638),
				},
				Short: &m.Window{N: 3, Err: 0, ErrRate: m.F(0), SlowRate: m.F(0), P50: m.F(8), P90: m.F(15)},
				Chan:  &m.ChanStat{N: 43, Err: 0, Client: 2, Eff: 0, Rate: 0},
				Mix:   &m.Mix{NUser: 45, NProbe: 4, NErrReal: 2},
			},
			72: live[72],
		}
		d, err := Decide(Input{
			Cfg:      cfg,
			Chans:    []m.Channel{{ID: 67}, {ID: 68}, {ID: 72}},
			Feats:    fs,
			Strategy: "rank",
			Prev:     map[int]int{67: 20, 68: 20, 72: 20},
			Trust:    map[int]float64{67: 0.3084, 68: 0.6822, 72: 0.1},
		})
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if !d.Scores[67].EligibleRank1 || !d.Scores[68].EligibleRank1 || !d.Scores[72].EligibleRank1 {
			t.Fatalf("all three should be eligible, scores=%v", d.Scores)
		}
		if d.Scores[67].N60 != 23 || d.Scores[68].N60 != 49 || d.Scores[72].N60 != 21 {
			t.Fatalf("n60 unexpected: 67=%d 68=%d 72=%d", d.Scores[67].N60, d.Scores[68].N60, d.Scores[72].N60)
		}
		if d.Target[67] != 20 || d.Target[68] != 20 || d.Target[72] != 20 {
			t.Fatalf("weights=%v want all held at 20", d.Target)
		}
		if d.Caps[67] != nil || d.Caps[68] != nil || d.Caps[72] != nil {
			t.Fatalf("n60>=20 or trust>=0.3 must not compute recovery cap 3, got %+v", d.Caps)
		}
		joined := strings.Join(d.Notes, ";")
		if strings.Contains(joined, "不得排第一") || strings.Contains(joined, "顶上") {
			t.Fatalf("all eligible channels must not emit a promotion note, got %q", d.Notes)
		}
		// Simplified score config may give #67 strategy 0 rather than the
		// live rank-path 3; both are settled back to 20. #72's mid_w=3
		// matches the live note and is asserted exactly.
		if !strings.Contains(joined, "#67 定档：策略") || !strings.Contains(joined, "→20") || !strings.Contains(joined, "无渠道级错误证据") {
			t.Fatalf("expected #67 settle hold to 20, got %q", d.Notes)
		}
		if !strings.Contains(joined, "#72 定档：策略 3→20") {
			t.Fatalf("expected settle notes for #72 3→20, got %q", d.Notes)
		}
		if strings.Contains(joined, "#68 定档") {
			t.Fatalf("strategy already 20 for first-place #68 should not emit a settle note, got %q", d.Notes)
		}
	})

	t.Run("gpt_capped_interval_fully_recovers_prior_10m_cuts", func(t *testing.T) {
		// CASE-AW-039 addendum: the same "10m clean and 1h < 2%" recovery
		// also reaches 1 when the inter-round gap first exceeds 45
		// minutes (2707s, time factor already capped). Ample n60 makes
		// the increment 0.2, which is larger than the remaining gap
		// from 0.8668/0.9196/0.8848/0.9616.
		gptGap := m.F(2707.0 / 60.0)
		gpt := m.Feats{
			2: {
				Long:  &m.Window{N: 431, Err: 3, ErrRate: m.F(3.0 / 431.0), P50: m.F(13), P90: m.F(39)},
				Short: &m.Window{N: 59, Err: 0, ErrRate: m.F(0), P50: m.F(16), P90: m.F(42)},
				Chan:  &m.ChanStat{N: 425, Err: 1, Client: 2, Eff: 1, Rate: 0.0024, ErrTok: 1, Toks: 11, TopTok: 900002, TopErr: 1},
			},
			23: {
				Long:  &m.Window{N: 414, Err: 2, ErrRate: m.F(2.0 / 414.0), P50: m.F(8), P90: m.F(27)},
				Short: &m.Window{N: 69, Err: 0, ErrRate: m.F(0), P50: m.F(8), P90: m.F(35)},
				Chan:  &m.ChanStat{N: 408, Err: 0, Client: 2, Eff: 0, Rate: 0},
			},
			26: {
				Long:  &m.Window{N: 318, Err: 0, ErrRate: m.F(0), P50: m.F(9), P90: m.F(32)},
				Short: &m.Window{N: 46, Err: 0, ErrRate: m.F(0), P50: m.F(13), P90: m.F(77)},
				Chan:  &m.ChanStat{N: 314, Err: 0, Client: 0, Eff: 0, Rate: 0},
			},
			32: {
				Long:  &m.Window{N: 251, Err: 1, ErrRate: m.F(1.0 / 251.0), P50: m.F(8), P90: m.F(16)},
				Short: &m.Window{N: 63, Err: 0, ErrRate: m.F(0), P50: m.F(8), P90: m.F(11)},
				Chan:  &m.ChanStat{N: 246, Err: 0, Client: 1, Eff: 0, Rate: 0},
			},
			73: {
				Long:  &m.Window{N: 483, Err: 1, ErrRate: m.F(1.0 / 483.0), P50: m.F(10), P90: m.F(35)},
				Short: &m.Window{N: 41, Err: 0, ErrRate: m.F(0), P50: m.F(8), P90: m.F(45)},
				Chan:  &m.ChanStat{N: 478, Err: 0, Client: 1, Eff: 0, Rate: 0},
			},
		}
		prev := map[int]float64{2: 0.9196, 23: 0.8668, 26: 1, 32: 0.8848, 73: 0.9616}
		st := trust.Step(trustCfg, []int{2, 23, 26, 32, 73}, gpt, nil, prev, gptGap)
		for _, id := range []int{2, 23, 26, 32, 73} {
			if st[id].Sev != 0 || st[id].Trust != 1 {
				t.Fatalf("#%d trust sev=%v value=%v want 0 / 1", id, st[id].Sev, st[id].Trust)
			}
			if len(st[id].Why) != 0 {
				t.Fatalf("#%d recovery must not emit a why, got %v", id, st[id].Why)
			}
		}
		next := map[int]int{2: 3}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{2: 20}, gpt, []int{2}, nil, &notes)
		if next[2] != 20 {
			t.Fatalf("GPT #2 weight=%d want 20; 1 channel error does not meet 30/3/3%%; notes=%q", next[2], notes)
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#2 定档：策略 3→20", "无渠道级错误证据", "1h 真实 425", "渠道错误 1", "去集中 1"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected GPT #2 settle hold note to contain %q, got %q", part, notes)
			}
		}
	})
}

// CASE-AW-044: sanitized aggregate shape from 2026-11-22 02:13 +08:00 CC #72.
// No production log rows, request identifiers, model names or real token IDs.
// After CASE-AW-043 held trust at the floor because the 1h raw rate stayed
// high, the next window is clean — but only because the scoring window
// collapsed to five probe rows (zero real user traffic). Trust then leaves
// the floor with an uncapped 2678s interval and a 5/30 sample factor,
// computing recovery cap 3 that Settle does not apply. This locks current
// v5.01 Update/applyTrustCap/Settle behavior; it does not change the algorithm.
func TestCasebookProbeOnlyCleanWindowRecoversTrustFloorUncappedInterval(t *testing.T) {
	now := int64(100000)
	var rows []m.Row
	// 10m window (1): 1 probe success, no errors. N=1 is below
	// err_10m_min_n=2, so the 10m error path is not eligible anyway.
	rows = append(rows, m.Row{Ch: 72, T: now - 30, Type: 2, Tok: 1318, Ct: 16, Ut: 1})
	// Older than 10m, still inside 60m: 4 more probe successes.
	for n := 0; n < 4; n++ {
		rows = append(rows, m.Row{Ch: 72, T: now - 40*60, Type: 2, Tok: 1318, Ct: 16, Ut: 1})
	}

	featCfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
	fs := features.Build(featCfg, []m.Channel{{ID: 72}}, rows, now)
	f := fs[72]
	if f == nil || f.Long == nil || f.Short == nil || f.Mix == nil {
		t.Fatalf("missing features: %+v", f)
	}
	if f.Long.N != 5 || f.Long.Err != 0 {
		t.Fatalf("raw 60m n=%d err=%d want 5/0", f.Long.N, f.Long.Err)
	}
	if m.Or(f.Long.ErrRate, 0) != 0 {
		t.Fatalf("raw err rate=%v want 0", f.Long.ErrRate)
	}
	if f.Short.N != 1 || f.Short.Err != 0 {
		t.Fatalf("raw 10m n=%d err=%d want 1/0", f.Short.N, f.Short.Err)
	}
	if f.Mix.NUser != 0 || f.Mix.NProbe != 5 || f.Mix.NErrReal != 0 {
		t.Fatalf("mix=%+v want NUser=0 NProbe=5 NErrReal=0", f.Mix)
	}
	if f.Chan != nil && f.Chan.N != 0 {
		t.Fatalf("probe-only window must not produce real ChanStat N, got %+v", f.Chan)
	}

	// Live v5.01 trust config uses up=0.2 (code default is 0.08).
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2678s.
	gap := m.F(2678.0 / 60.0)

	t.Run("live_shape_recovers_0_1_to_0_1331_uncapped_and_scaled_by_n60_5", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1}, gap)[72]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=0 because 1h 0%<2% and 10m err=0 (and n=1 < err_10m_min_n=2).
		// 0.1 + 0.2*min(1, 2678/60/45)*min(1, 5/30) = 0.1330617... -> 0.1331
		// Time factor is 2678/2700 < 1, so this is not the 0.1667 capped value
		// and not CASE-AW-043's 0.24 counterfactual (capped, n60=21).
		if st.Sev != 0 || st.Trust != 0.1331 {
			t.Fatalf("trust sev=%v value=%v want 0 / 0.1331", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
	})

	t.Run("capped_interval_with_same_n60_would_reach_0_1333", func(t *testing.T) {
		// Same prev and n60=5, but a gap already at round_min. The increment
		// would be the full sample-limited 0.2*(5/30)=0.0333..., to 0.1333.
		// That is only 0.0002 above the live 0.1331; the large gap versus
		// CASE-AW-043's 0.24 counterfactual is the 5/30 vs 21/30 sample
		// factor, not the uncapped-versus-capped time factor.
		capped := m.F(2706.0 / 60.0)
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1}, capped)[72]
		if st.Sev != 0 || st.Trust != 0.1333 {
			t.Fatalf("capped contrast sev=%v value=%v want 0 / 0.1333", st.Sev, st.Trust)
		}
	})

	t.Run("case_043_counterfactual_capped_n21_would_reach_0_24", func(t *testing.T) {
		// The contrast written into CASE-AW-043: if that window's 1h errors
		// had cleared, the then-capped 2706s gap and n60=21 would recover
		// 0.1 + 0.2*1*(21/30) = 0.24. The live successor is thinner and just
		// under 45 minutes, so it must not be rewritten as 0.24.
		ample := m.Feats{72: {
			Long:  &m.Window{N: 21, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 0, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{72}, ample, nil, map[int]float64{72: 0.1}, m.F(2706.0/60.0))[72]
		if st.Sev != 0 || st.Trust != 0.24 {
			t.Fatalf("043 counterfactual sev=%v value=%v want 0 / 0.24", st.Sev, st.Trust)
		}
	})

	t.Run("if_hour_rate_stayed_high_trust_would_remain_on_the_floor", func(t *testing.T) {
		// CASE-AW-043 continuation: empty 10m plus 3/21 = 14.3% would keep
		// multiplying 0.1 down and clamp back to t_min, even at this 2678s gap.
		dirty := m.Feats{72: {
			Long:  &m.Window{N: 21, Err: 3, ErrRate: m.F(3.0 / 21.0)},
			Short: &m.Window{N: 0, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{72}, dirty, nil, map[int]float64{72: 0.1}, gap)[72]
		if st.Trust != 0.1 {
			t.Fatalf("dirty 1h must keep the floor, value=%v want 0.1", st.Trust)
		}
		if st.Sev == 0 {
			t.Fatalf("dirty 1h must have sev>0, got %v", st.Sev)
		}
	})

	t.Run("sibling_67_mid_recovery_scales_0_3084_to_0_3348_without_cap", func(t *testing.T) {
		// Same live 2678s gap, sanitized CC #67 shape this round: clean 1h
		// n=4 (empty=2 still counted as type2 success, all probes), clean
		// 10m n=1, prev 0.3084 after CASE-AW-043. Sample factor is 4/30,
		// time factor is still 2678/2700: 0.3084 + 0.2*(2678/60/45)*(4/30)
		// = 0.334849... -> 0.3348. Trust is already >= 0.3, so cap 3 is not
		// computed even though n60=4 < 20.
		sib := m.Feats{67: {
			Long:  &m.Window{N: 4, Err: 0, ErrRate: m.F(0), Empty: m.I(2)},
			Short: &m.Window{N: 1, Err: 0, ErrRate: m.F(0)},
		}}
		st := trust.Step(trustCfg, []int{67}, sib, nil, map[int]float64{67: 0.3084}, gap)[67]
		if st.Sev != 0 || st.Trust != 0.3348 {
			t.Fatalf("sibling mid recovery sev=%v value=%v want 0 / 0.3348", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: 4}}, map[int]float64{67: st.Trust}, []int{67})
		if caps[67] != nil {
			t.Fatalf("trust cap=%+v; trust 0.3348 is not < 0.3 so recovery cap 3 must not be computed even though n60=4 < 20", caps[67])
		}
	})

	t.Run("sibling_68_ample_recovery_scales_0_6822_to_0_8806_without_cap", func(t *testing.T) {
		// Same live 2678s gap, sanitized CC #68 shape: clean 1h n=56, clean
		// 10m 0/15, prev 0.6822. n60>=30 so the sample factor is 1, but the
		// time factor is still uncapped: 0.6822 + 0.2*(2678/60/45)*1
		// = 0.880570... -> 0.8806. A capped interval would have reached 0.8822.
		sib := m.Feats{68: {
			Long:  &m.Window{N: 56, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 15, Err: 0, ErrRate: m.F(0)},
		}}
		st := trust.Step(trustCfg, []int{68}, sib, nil, map[int]float64{68: 0.6822}, gap)[68]
		if st.Sev != 0 || st.Trust != 0.8806 {
			t.Fatalf("sibling ample recovery sev=%v value=%v want 0 / 0.8806", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{68: {N60: 56}}, map[int]float64{68: st.Trust}, []int{68})
		if caps[68] != nil {
			t.Fatalf("trust cap=%+v; n60=56 is not < 20 so recovery cap 3 must not be computed", caps[68])
		}
	})

	t.Run("recovery_cap_3_is_computed_when_scaled_trust_stays_below_0_3", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: f.Long.N}}, map[int]float64{72: st.Trust}, []int{72})
		c := caps[72]
		if c == nil || c.Cap != 3 || c.Kind != "trust" {
			t.Fatalf("trust cap=%+v want cap=3 kind=trust", c)
		}
		if c.Reason != "信任 0.13 < 0.3且 1h 样本 5 < 20，恢复期限幅" {
			t.Fatalf("trust cap reason=%q", c.Reason)
		}
	})

	t.Run("settle_holds_3_to_20_and_mentions_unapplied_cap_with_zero_real", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: f.Long.N}}, map[int]float64{72: st.Trust}, []int{72})
		next := map[int]int{72: 3}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, caps, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; system cap 3 must not be applied", next[72])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#72 定档：策略 3→20", "系统上限 3", "不执行", "1h 真实 0", "渠道错误 0", "去集中 0", "0.0%", "未达降权门槛"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected held-cap note to contain %q, got %q", part, notes)
			}
		}
	})

	t.Run("already_20_has_no_settle_note", func(t *testing.T) {
		next := map[int]int{72: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[72], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("already at 20 should not emit a settle note, got %q", notes)
		}
	})
}

func TestCasebookProbeOnlyCleanWindowContinuesFloorRecoveryCappedInterval(t *testing.T) {
	now := int64(100000)
	var rows []m.Row
	// 10m window (1): 1 probe success, no errors. N=1 is below
	// err_10m_min_n=2, so the 10m error path is not eligible anyway.
	rows = append(rows, m.Row{Ch: 72, T: now - 30, Type: 2, Tok: 1318, Ct: 16, Ut: 1})
	// Older than 10m, still inside 60m: 3 more probe successes (n60=4).
	for n := 0; n < 3; n++ {
		rows = append(rows, m.Row{Ch: 72, T: now - 40*60, Type: 2, Tok: 1318, Ct: 16, Ut: 1})
	}

	featCfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
	fs := features.Build(featCfg, []m.Channel{{ID: 72}}, rows, now)
	f := fs[72]
	if f == nil || f.Long == nil || f.Short == nil || f.Mix == nil {
		t.Fatalf("missing features: %+v", f)
	}
	if f.Long.N != 4 || f.Long.Err != 0 {
		t.Fatalf("raw 60m n=%d err=%d want 4/0", f.Long.N, f.Long.Err)
	}
	if m.Or(f.Long.ErrRate, 0) != 0 {
		t.Fatalf("raw err rate=%v want 0", f.Long.ErrRate)
	}
	if f.Short.N != 1 || f.Short.Err != 0 {
		t.Fatalf("raw 10m n=%d err=%d want 1/0", f.Short.N, f.Short.Err)
	}
	if f.Mix.NUser != 0 || f.Mix.NProbe != 4 || f.Mix.NErrReal != 0 {
		t.Fatalf("mix=%+v want NUser=0 NProbe=4 NErrReal=0", f.Mix)
	}
	if f.Chan != nil && f.Chan.N != 0 {
		t.Fatalf("probe-only window must not produce real ChanStat N, got %+v", f.Chan)
	}

	// Live v5.01 trust config uses up=0.2 (code default is 0.08).
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2869s.
	gap := m.F(2869.0 / 60.0)

	t.Run("live_shape_recovers_0_1331_to_0_1598_capped_and_scaled_by_n60_4", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1331}, gap)[72]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=0 because 1h 0%<2% and 10m err=0 (and n=1 < err_10m_min_n=2).
		// 0.1331 + 0.2*min(1, 2869/60/45)*min(1, 4/30) = 0.1597666... -> 0.1598
		// Time factor is capped at 1 because 2869/2700 > 1. This is the live
		// successor of CASE-AW-044 (0.1 -> 0.1331, uncapped, n60=5).
		if st.Sev != 0 || st.Trust != 0.1598 {
			t.Fatalf("trust sev=%v value=%v want 0 / 0.1598", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
	})

	t.Run("uncapped_interval_with_same_n60_would_reach_0_1614", func(t *testing.T) {
		// If the 2869s gap were allowed to exceed 1, the increment would be
		// 0.2*(2869/2700)*(4/30)=0.0283..., to 0.1614. That is only 0.0016
		// above the live 0.1598; the larger change versus CASE-AW-044's
		// 0.1331 is the sample factor falling from 5/30 to 4/30.
		prev := 0.1331
		want := 0.0
		_ = prev
		_ = want
		// Compute the uncapped contrast by stepping a longer-than-round_min
		// gap is not possible through Step (it caps internally), so apply
		// the formula the same way the casebook states it: cancel the cap.
		uncappedInc := 0.2 * (2869.0 / 2700.0) * (4.0 / 30.0)
		uncapped := 0.1331 + uncappedInc
		// Still assert the live path used the cap, and document the 0.1614
		// contrast as a rounded 4-decimal value.
		if rounded := float64(int(uncapped*10000+0.5)) / 10000; rounded != 0.1614 {
			t.Fatalf("uncapped contrast rounded=%v raw=%v want 0.1614", rounded, uncapped)
		}
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1331}, gap)[72]
		if st.Trust != 0.1598 {
			t.Fatalf("live capped value=%v want 0.1598 (uncapped contrast is 0.1614, not the live value)", st.Trust)
		}
	})

	t.Run("same_capped_interval_with_n60_5_would_reach_0_1664", func(t *testing.T) {
		// If this window had kept CASE-AW-044's n60=5, the already-capped
		// 2869s gap would recover 0.1331 + 0.2*1*(5/30) = 0.1664.
		// The live successor is thinner (n=4), so it must not be rewritten
		// as 0.1664; the main delta from 0.1331 is the sample factor.
		sib := m.Feats{72: {
			Long:  &m.Window{N: 5, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 1, Err: 0, ErrRate: m.F(0)},
		}}
		st := trust.Step(trustCfg, []int{72}, sib, nil, map[int]float64{72: 0.1331}, gap)[72]
		if st.Sev != 0 || st.Trust != 0.1664 {
			t.Fatalf("n60=5 contrast sev=%v value=%v want 0 / 0.1664", st.Sev, st.Trust)
		}
	})

	t.Run("case_043_counterfactual_capped_n21_would_still_reach_0_24", func(t *testing.T) {
		// CASE-AW-043's counterfactual remains 0.1 + 0.2*1*(21/30) = 0.24
		// even at this already-capped interval. The live path starts from
		// 0.1331 with n60=4 and must not be rewritten as 0.24.
		ample := m.Feats{72: {
			Long:  &m.Window{N: 21, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 0, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{72}, ample, nil, map[int]float64{72: 0.1}, gap)[72]
		if st.Sev != 0 || st.Trust != 0.24 {
			t.Fatalf("043 counterfactual sev=%v value=%v want 0 / 0.24", st.Sev, st.Trust)
		}
	})

	t.Run("if_hour_rate_stayed_high_trust_would_remain_on_the_floor", func(t *testing.T) {
		// If the 1h window had stayed 3/21 = 14.3%, the same 2869s gap
		// would keep multiplying 0.1331 down and clamp back to t_min.
		dirty := m.Feats{72: {
			Long:  &m.Window{N: 21, Err: 3, ErrRate: m.F(3.0 / 21.0)},
			Short: &m.Window{N: 0, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{72}, dirty, nil, map[int]float64{72: 0.1331}, gap)[72]
		if st.Trust != 0.1 {
			t.Fatalf("dirty 1h must return to the floor, value=%v want 0.1", st.Trust)
		}
		if st.Sev == 0 {
			t.Fatalf("dirty 1h must have sev>0, got %v", st.Sev)
		}
	})

	t.Run("sibling_67_mid_recovery_scales_0_3348_to_0_3615_without_cap", func(t *testing.T) {
		// Same live 2869s gap, sanitized CC #67 shape this round: clean 1h
		// n=4 (empty=1 still counted as type2 success, all probes), clean
		// 10m n=1, prev 0.3348 after CASE-AW-044. Sample factor is 4/30,
		// time factor is capped at 1: 0.3348 + 0.2*1*(4/30) = 0.361466...
		// -> 0.3615. Trust is already >= 0.3, so cap 3 is not computed
		// even though n60=4 < 20.
		sib := m.Feats{67: {
			Long:  &m.Window{N: 4, Err: 0, ErrRate: m.F(0), Empty: m.I(1)},
			Short: &m.Window{N: 1, Err: 0, ErrRate: m.F(0)},
		}}
		st := trust.Step(trustCfg, []int{67}, sib, nil, map[int]float64{67: 0.3348}, gap)[67]
		if st.Sev != 0 || st.Trust != 0.3615 {
			t.Fatalf("sibling mid recovery sev=%v value=%v want 0 / 0.3615", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: 4}}, map[int]float64{67: st.Trust}, []int{67})
		if caps[67] != nil {
			t.Fatalf("trust cap=%+v; trust 0.3615 is not < 0.3 so recovery cap 3 must not be computed even though n60=4 < 20", caps[67])
		}
	})

	t.Run("sibling_68_ample_recovery_reaches_the_ceiling_1_without_cap", func(t *testing.T) {
		// Same live 2869s gap, sanitized CC #68 shape: clean 1h n=38, clean
		// 10m 0/1, prev 0.8806. n60>=30 so the sample factor is 1, and the
		// time factor is already capped: 0.8806 + 0.2*1*1 = 1.0806, then
		// clamped to the trust ceiling of 1. An uncapped 2869/2700 factor
		// would also exceed 1, so the ceiling — not the interval cap — is
		// what stops this sibling at 1.
		sib := m.Feats{68: {
			Long:  &m.Window{N: 38, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 1, Err: 0, ErrRate: m.F(0)},
		}}
		st := trust.Step(trustCfg, []int{68}, sib, nil, map[int]float64{68: 0.8806}, gap)[68]
		if st.Sev != 0 || st.Trust != 1 {
			t.Fatalf("sibling ample recovery sev=%v value=%v want 0 / 1", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{68: {N60: 38}}, map[int]float64{68: st.Trust}, []int{68})
		if caps[68] != nil {
			t.Fatalf("trust cap=%+v; n60=38 is not < 20 so recovery cap 3 must not be computed", caps[68])
		}
	})

	t.Run("recovery_cap_3_is_computed_when_scaled_trust_stays_below_0_3", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1331}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: f.Long.N}}, map[int]float64{72: st.Trust}, []int{72})
		c := caps[72]
		if c == nil || c.Cap != 3 || c.Kind != "trust" {
			t.Fatalf("trust cap=%+v want cap=3 kind=trust", c)
		}
		if c.Reason != "信任 0.16 < 0.3且 1h 样本 4 < 20，恢复期限幅" {
			t.Fatalf("trust cap reason=%q", c.Reason)
		}
	})

	t.Run("settle_holds_3_to_20_and_mentions_unapplied_cap_with_zero_real", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1331}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: f.Long.N}}, map[int]float64{72: st.Trust}, []int{72})
		next := map[int]int{72: 3}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, caps, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; system cap 3 must not be applied", next[72])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#72 定档：策略 3→20", "系统上限 3", "不执行", "1h 真实 0", "渠道错误 0", "去集中 0", "0.0%", "未达降权门槛"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected held-cap note to contain %q, got %q", part, notes)
			}
		}
	})

	t.Run("already_20_has_no_settle_note", func(t *testing.T) {
		next := map[int]int{72: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[72], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("already at 20 should not emit a settle note, got %q", notes)
		}
	})
}
func TestCasebookProbeOnlyCleanWindowContinuesCappedRecoveryWhenN60ReturnsTo5(t *testing.T) {
	now := int64(100000)
	var rows []m.Row
	// 10m window (1): 1 probe success, no errors. N=1 is below
	// err_10m_min_n=2, so the 10m error path is not eligible anyway.
	rows = append(rows, m.Row{Ch: 72, T: now - 30, Type: 2, Tok: 1318, Ct: 16, Ut: 1})
	// Older than 10m, still inside 60m: 4 more probe successes (n60=5).
	for n := 0; n < 4; n++ {
		rows = append(rows, m.Row{Ch: 72, T: now - 40*60, Type: 2, Tok: 1318, Ct: 16, Ut: 1})
	}

	featCfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
	fs := features.Build(featCfg, []m.Channel{{ID: 72}}, rows, now)
	f := fs[72]
	if f == nil || f.Long == nil || f.Short == nil || f.Mix == nil {
		t.Fatalf("missing features: %+v", f)
	}
	if f.Long.N != 5 || f.Long.Err != 0 {
		t.Fatalf("raw 60m n=%d err=%d want 5/0", f.Long.N, f.Long.Err)
	}
	if m.Or(f.Long.ErrRate, 0) != 0 {
		t.Fatalf("raw err rate=%v want 0", f.Long.ErrRate)
	}
	if f.Short.N != 1 || f.Short.Err != 0 {
		t.Fatalf("raw 10m n=%d err=%d want 1/0", f.Short.N, f.Short.Err)
	}
	if f.Mix.NUser != 0 || f.Mix.NProbe != 5 || f.Mix.NErrReal != 0 {
		t.Fatalf("mix=%+v want NUser=0 NProbe=5 NErrReal=0", f.Mix)
	}
	if f.Chan != nil && f.Chan.N != 0 {
		t.Fatalf("probe-only window must not produce real ChanStat N, got %+v", f.Chan)
	}

	// Live v5.01 trust config uses up=0.2 (code default is 0.08).
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 3038s.
	gap := m.F(3038.0 / 60.0)

	t.Run("live_shape_recovers_0_1598_to_0_1931_capped_and_scaled_by_n60_5", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1598}, gap)[72]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=0 because 1h 0%<2% and 10m err=0 (and n=1 < err_10m_min_n=2).
		// 0.1598 + 0.2*min(1, 3038/60/45)*min(1, 5/30) = 0.1931333... -> 0.1931
		// Time factor is capped at 1 because 3038/2700 > 1. This is the live
		// successor of CASE-AW-045 (0.1331 -> 0.1598, capped, n60=4).
		if st.Sev != 0 || st.Trust != 0.1931 {
			t.Fatalf("trust sev=%v value=%v want 0 / 0.1931", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
	})

	t.Run("uncapped_interval_with_same_n60_would_reach_0_1973", func(t *testing.T) {
		// If the 3038s gap were allowed to exceed 1, the increment would be
		// 0.2*(3038/2700)*(5/30)=0.0375..., to 0.1973. That is only 0.0042
		// above the live 0.1931; the larger change versus CASE-AW-045's
		// 0.1598 is the sample factor returning from 4/30 to 5/30.
		uncappedInc := 0.2 * (3038.0 / 2700.0) * (5.0 / 30.0)
		uncapped := 0.1598 + uncappedInc
		if rounded := float64(int(uncapped*10000+0.5)) / 10000; rounded != 0.1973 {
			t.Fatalf("uncapped contrast rounded=%v raw=%v want 0.1973", rounded, uncapped)
		}
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1598}, gap)[72]
		if st.Trust != 0.1931 {
			t.Fatalf("live capped value=%v want 0.1931 (uncapped contrast is 0.1973, not the live value)", st.Trust)
		}
	})

	t.Run("same_capped_interval_with_n60_4_would_reach_0_1865", func(t *testing.T) {
		// If this window had kept CASE-AW-045's n60=4, the already-capped
		// 3038s gap would recover 0.1598 + 0.2*1*(4/30) = 0.186466... -> 0.1865.
		// The live successor returned to n=5, so it must not be rewritten
		// as 0.1865; the main delta from 0.1598 is the sample factor.
		sib := m.Feats{72: {
			Long:  &m.Window{N: 4, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 1, Err: 0, ErrRate: m.F(0)},
		}}
		st := trust.Step(trustCfg, []int{72}, sib, nil, map[int]float64{72: 0.1598}, gap)[72]
		if st.Sev != 0 || st.Trust != 0.1865 {
			t.Fatalf("n60=4 contrast sev=%v value=%v want 0 / 0.1865", st.Sev, st.Trust)
		}
	})

	t.Run("case_043_counterfactual_capped_n21_would_still_reach_0_24", func(t *testing.T) {
		// CASE-AW-043's counterfactual remains 0.1 + 0.2*1*(21/30) = 0.24
		// even at this already-capped interval. The live path starts from
		// 0.1598 with n60=5 and must not be rewritten as 0.24.
		ample := m.Feats{72: {
			Long:  &m.Window{N: 21, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 0, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{72}, ample, nil, map[int]float64{72: 0.1}, gap)[72]
		if st.Sev != 0 || st.Trust != 0.24 {
			t.Fatalf("043 counterfactual sev=%v value=%v want 0 / 0.24", st.Sev, st.Trust)
		}
	})

	t.Run("if_hour_rate_stayed_high_trust_would_remain_on_the_floor", func(t *testing.T) {
		// If the 1h window had stayed 3/21 = 14.3%, the same 3038s gap
		// would keep multiplying 0.1598 down to 0.1009, just above t_min=0.1.
		// CASE-AW-045 started from 0.1331 and did clamp to 0.1; this
		// slightly higher start does not. It still must not recover.
		dirty := m.Feats{72: {
			Long:  &m.Window{N: 21, Err: 3, ErrRate: m.F(3.0 / 21.0)},
			Short: &m.Window{N: 0, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{72}, dirty, nil, map[int]float64{72: 0.1598}, gap)[72]
		if st.Trust != 0.1009 {
			t.Fatalf("dirty 1h must be cut to 0.1009 just above the floor, value=%v", st.Trust)
		}
		if st.Sev == 0 {
			t.Fatalf("dirty 1h must have sev>0, got %v", st.Sev)
		}
	})

	t.Run("sibling_67_mid_recovery_scales_0_3615_to_0_3882_without_cap", func(t *testing.T) {
		// Same live 3038s gap, sanitized CC #67 shape this round: clean 1h
		// n=4 (empty=1 still counted as type2 success, all probes), clean
		// 10m n=1, prev 0.3615 after CASE-AW-045. Sample factor is 4/30,
		// time factor is capped at 1: 0.3615 + 0.2*1*(4/30) = 0.388166...
		// -> 0.3882. Trust is already >= 0.3, so cap 3 is not computed
		// even though n60=4 < 20.
		sib := m.Feats{67: {
			Long:  &m.Window{N: 4, Err: 0, ErrRate: m.F(0), Empty: m.I(1)},
			Short: &m.Window{N: 1, Err: 0, ErrRate: m.F(0)},
		}}
		st := trust.Step(trustCfg, []int{67}, sib, nil, map[int]float64{67: 0.3615}, gap)[67]
		if st.Sev != 0 || st.Trust != 0.3882 {
			t.Fatalf("sibling mid recovery sev=%v value=%v want 0 / 0.3882", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: 4}}, map[int]float64{67: st.Trust}, []int{67})
		if caps[67] != nil {
			t.Fatalf("trust cap=%+v; trust 0.3882 is not < 0.3 so recovery cap 3 must not be computed even though n60=4 < 20", caps[67])
		}
	})

	t.Run("sibling_68_probe_only_window_holds_ceiling_1_without_cap", func(t *testing.T) {
		// Same live 3038s gap, sanitized CC #68 shape this round: unlike
		// CASE-AW-045's ample n=38 climb from 0.8806 to the ceiling, this
		// window is also probe-only. Clean 1h n=5, clean 10m n=1, prev already
		// 1, so trust stays at the ceiling. n60=5 does not generate cap 3
		// because trust is not < 0.3. Do not rewrite this sibling as still
		// having n60=38 or as still being eligible_rank1.
		sib := m.Feats{68: {
			Long:  &m.Window{N: 5, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 1, Err: 0, ErrRate: m.F(0)},
		}}
		st := trust.Step(trustCfg, []int{68}, sib, nil, map[int]float64{68: 1}, gap)[68]
		if st.Sev != 0 || st.Trust != 1 {
			t.Fatalf("sibling ceiling hold sev=%v value=%v want 0 / 1", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean hold at the ceiling", st.Why)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{68: {N60: 5}}, map[int]float64{68: st.Trust}, []int{68})
		if caps[68] != nil {
			t.Fatalf("trust cap=%+v; trust 1 is not < 0.3 so recovery cap 3 must not be computed even though n60=5 < 20", caps[68])
		}
	})

	t.Run("recovery_cap_3_is_computed_when_scaled_trust_stays_below_0_3", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1598}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: f.Long.N}}, map[int]float64{72: st.Trust}, []int{72})
		c := caps[72]
		if c == nil || c.Cap != 3 || c.Kind != "trust" {
			t.Fatalf("trust cap=%+v want cap=3 kind=trust", c)
		}
		if c.Reason != "信任 0.19 < 0.3且 1h 样本 5 < 20，恢复期限幅" {
			t.Fatalf("trust cap reason=%q", c.Reason)
		}
	})

	t.Run("settle_holds_3_to_20_and_mentions_unapplied_cap_with_zero_real", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1598}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: f.Long.N}}, map[int]float64{72: st.Trust}, []int{72})
		next := map[int]int{72: 3}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, caps, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; system cap 3 must not be applied", next[72])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#72 定档：策略 3→20", "系统上限 3", "不执行", "1h 真实 0", "渠道错误 0", "去集中 0", "0.0%", "未达降权门槛"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected held-cap note to contain %q, got %q", part, notes)
			}
		}
	})

	t.Run("already_20_has_no_settle_note", func(t *testing.T) {
		next := map[int]int{72: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[72], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("already at 20 should not emit a settle note, got %q", notes)
		}
	})
}

func TestCasebookProbeOnlyCleanWindowContinuesCappedRecoveryWhenN60ReturnsTo4(t *testing.T) {
	now := int64(100000)
	var rows []m.Row
	// Live successor of CASE-AW-046: 10m is now empty (n=0), and the 60m
	// scoring window is 4 probe successes. No real users.
	for n := 0; n < 4; n++ {
		rows = append(rows, m.Row{Ch: 72, T: now - 40*60, Type: 2, Tok: 1318, Ct: 16, Ut: 1})
	}

	featCfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
	fs := features.Build(featCfg, []m.Channel{{ID: 72}}, rows, now)
	f := fs[72]
	if f == nil || f.Long == nil || f.Short == nil || f.Mix == nil {
		t.Fatalf("missing features: %+v", f)
	}
	if f.Long.N != 4 || f.Long.Err != 0 {
		t.Fatalf("raw 60m n=%d err=%d want 4/0", f.Long.N, f.Long.Err)
	}
	if m.Or(f.Long.ErrRate, 0) != 0 {
		t.Fatalf("raw err rate=%v want 0", f.Long.ErrRate)
	}
	if f.Short.N != 0 || f.Short.Err != 0 {
		t.Fatalf("raw 10m n=%d err=%d want 0/0 (empty 10m, not the previous n=1)", f.Short.N, f.Short.Err)
	}
	if f.Mix.NUser != 0 || f.Mix.NProbe != 4 || f.Mix.NErrReal != 0 {
		t.Fatalf("mix=%+v want NUser=0 NProbe=4 NErrReal=0", f.Mix)
	}
	if f.Chan != nil && f.Chan.N != 0 {
		t.Fatalf("probe-only window must not produce real ChanStat N, got %+v", f.Chan)
	}

	// Live v5.01 trust config uses up=0.2 (code default is 0.08).
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of algorithm now values: 2766s.
	gap := m.F(2766.0 / 60.0)

	t.Run("live_shape_recovers_0_1931_to_0_2198_capped_and_scaled_by_n60_4", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1931}, gap)[72]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=0 because 1h 0%<2% and 10m is empty (n=0 < err_10m_min_n=2, err=0).
		// 0.1931 + 0.2*min(1, 2766/60/45)*min(1, 4/30) = 0.2197666... -> 0.2198
		// Time factor is capped at 1 because 2766/2700 > 1. This is the live
		// successor of CASE-AW-046 (0.1598 -> 0.1931, capped, n60=5).
		if st.Sev != 0 || st.Trust != 0.2198 {
			t.Fatalf("trust sev=%v value=%v want 0 / 0.2198", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
	})

	t.Run("uncapped_interval_with_same_n60_would_reach_0_2204", func(t *testing.T) {
		// If the 2766s gap were allowed to exceed 1, the increment would be
		// 0.2*(2766/2700)*(4/30)=0.027318..., to 0.2204. That is only 0.0006
		// above the live 0.2198; the larger change versus CASE-AW-046's
		// 0.1931 is the sample factor returning from 5/30 to 4/30, not the
		// already-capped interval.
		uncappedInc := 0.2 * (2766.0 / 2700.0) * (4.0 / 30.0)
		uncapped := 0.1931 + uncappedInc
		if rounded := float64(int(uncapped*10000+0.5)) / 10000; rounded != 0.2204 {
			t.Fatalf("uncapped contrast rounded=%v raw=%v want 0.2204", rounded, uncapped)
		}
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1931}, gap)[72]
		if st.Trust != 0.2198 {
			t.Fatalf("live capped value=%v want 0.2198 (uncapped contrast is 0.2204, not the live value)", st.Trust)
		}
	})

	t.Run("same_capped_interval_with_n60_5_would_reach_0_2264", func(t *testing.T) {
		// If this window had kept CASE-AW-046's n60=5, the already-capped
		// 2766s gap would recover 0.1931 + 0.2*1*(5/30) = 0.226433... -> 0.2264.
		// The live successor returned to n=4, so it must not be rewritten
		// as 0.2264; the main delta from 0.1931 is the sample factor.
		sib := m.Feats{72: {
			Long:  &m.Window{N: 5, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 0, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{72}, sib, nil, map[int]float64{72: 0.1931}, gap)[72]
		if st.Sev != 0 || st.Trust != 0.2264 {
			t.Fatalf("n60=5 contrast sev=%v value=%v want 0 / 0.2264", st.Sev, st.Trust)
		}
	})

	t.Run("case_043_counterfactual_capped_n21_would_still_reach_0_24", func(t *testing.T) {
		// CASE-AW-043's counterfactual remains 0.1 + 0.2*1*(21/30) = 0.24
		// even at this already-capped interval. The live path starts from
		// 0.1931 with n60=4 and must not be rewritten as 0.24.
		ample := m.Feats{72: {
			Long:  &m.Window{N: 21, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 0, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{72}, ample, nil, map[int]float64{72: 0.1}, gap)[72]
		if st.Sev != 0 || st.Trust != 0.24 {
			t.Fatalf("043 counterfactual sev=%v value=%v want 0 / 0.24", st.Sev, st.Trust)
		}
	})

	t.Run("sibling_67_mid_recovery_scales_0_3882_to_0_4149_without_cap", func(t *testing.T) {
		// Same live 2766s gap, sanitized CC #67 shape this round: clean 1h
		// n=4 (empty=1 still counted as type2 success, all probes), empty
		// 10m n=0, prev 0.3882 after CASE-AW-046. Sample factor is 4/30,
		// time factor is capped at 1: 0.3882 + 0.2*1*(4/30) = 0.414866...
		// -> 0.4149. Trust is already >= 0.3, so cap 3 is not computed
		// even though n60=4 < 20.
		sib := m.Feats{67: {
			Long:  &m.Window{N: 4, Err: 0, ErrRate: m.F(0), Empty: m.I(1)},
			Short: &m.Window{N: 0, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{67}, sib, nil, map[int]float64{67: 0.3882}, gap)[67]
		if st.Sev != 0 || st.Trust != 0.4149 {
			t.Fatalf("sibling mid recovery sev=%v value=%v want 0 / 0.4149", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: 4}}, map[int]float64{67: st.Trust}, []int{67})
		if caps[67] != nil {
			t.Fatalf("trust cap=%+v; trust 0.4149 is not < 0.3 so recovery cap 3 must not be computed even though n60=4 < 20", caps[67])
		}
	})

	t.Run("sibling_68_probe_only_window_holds_ceiling_1_without_cap", func(t *testing.T) {
		// Same live 2766s gap, sanitized CC #68 shape this round: still
		// probe-only after CASE-AW-046, but n60 thinned from 5 to 4 and
		// the 10m window is now empty. prev already 1, so trust stays at
		// the ceiling. n60=4 does not generate cap 3 because trust is not
		// < 0.3. Do not rewrite this sibling as still having n60=5 or as
		// still being the ineligible-group first place from CASE-AW-046.
		sib := m.Feats{68: {
			Long:  &m.Window{N: 4, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 0, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{68}, sib, nil, map[int]float64{68: 1}, gap)[68]
		if st.Sev != 0 || st.Trust != 1 {
			t.Fatalf("sibling ceiling hold sev=%v value=%v want 0 / 1", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean hold at the ceiling", st.Why)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{68: {N60: 4}}, map[int]float64{68: st.Trust}, []int{68})
		if caps[68] != nil {
			t.Fatalf("trust cap=%+v; trust 1 is not < 0.3 so recovery cap 3 must not be computed even though n60=4 < 20", caps[68])
		}
	})

	t.Run("recovery_cap_3_is_computed_when_scaled_trust_stays_below_0_3", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1931}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: f.Long.N}}, map[int]float64{72: st.Trust}, []int{72})
		c := caps[72]
		if c == nil || c.Cap != 3 || c.Kind != "trust" {
			t.Fatalf("trust cap=%+v want cap=3 kind=trust", c)
		}
		if c.Reason != "信任 0.22 < 0.3且 1h 样本 4 < 20，恢复期限幅" {
			t.Fatalf("trust cap reason=%q", c.Reason)
		}
	})

	t.Run("settle_holds_0_to_20_and_mentions_unapplied_cap_with_zero_real", func(t *testing.T) {
		// Live pre-settle strategy this round is already 0: all three CC
		// channels have score=0 and are ineligible, so rank assignment does
		// not produce a 3. The recovery cap is an upper bound and does not
		// raise 0 to 3. Do not rewrite this live note as CASE-AW-046's
		// 策略 3→20.
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.1931}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: f.Long.N}}, map[int]float64{72: st.Trust}, []int{72})
		next := map[int]int{72: 0}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, caps, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; system cap 3 must not be applied", next[72])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#72 定档：策略 0→20", "系统上限 3", "不执行", "1h 真实 0", "渠道错误 0", "去集中 0", "0.0%", "未达降权门槛"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected held-cap note to contain %q, got %q", part, notes)
			}
		}
		if strings.Contains(joined, "策略 3→20") {
			t.Fatalf("live pre-settle target is 0 this round; note must not be rewritten as 策略 3→20, got %q", notes)
		}
	})

	t.Run("already_20_has_no_settle_note", func(t *testing.T) {
		next := map[int]int{72: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[72], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("already at 20 should not emit a settle note, got %q", notes)
		}
	})
}

func TestCasebookProbeOnlyCleanWindowContinuesUncappedRecoveryAfterEarlyIntervalWithSingleClean10mProbe(t *testing.T) {
	now := int64(100000)
	var rows []m.Row
	// Live successor of CASE-AW-047: the 60m scoring window is still 4
	// probe successes with no real users, but 10m is no longer empty —
	// one of those probes falls inside 10m (n=1, err=0).
	for n := 0; n < 3; n++ {
		rows = append(rows, m.Row{Ch: 72, T: now - 40*60, Type: 2, Tok: 1318, Ct: 16, Ut: 1})
	}
	rows = append(rows, m.Row{Ch: 72, T: now - 5*60, Type: 2, Tok: 1318, Ct: 16, Ut: 1})

	featCfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
	fs := features.Build(featCfg, []m.Channel{{ID: 72}}, rows, now)
	f := fs[72]
	if f == nil || f.Long == nil || f.Short == nil || f.Mix == nil {
		t.Fatalf("missing features: %+v", f)
	}
	if f.Long.N != 4 || f.Long.Err != 0 {
		t.Fatalf("raw 60m n=%d err=%d want 4/0", f.Long.N, f.Long.Err)
	}
	if m.Or(f.Long.ErrRate, 0) != 0 {
		t.Fatalf("raw err rate=%v want 0", f.Long.ErrRate)
	}
	if f.Short.N != 1 || f.Short.Err != 0 {
		t.Fatalf("raw 10m n=%d err=%d want 1/0 (single clean probe, not the previous empty 10m)", f.Short.N, f.Short.Err)
	}
	if f.Mix.NUser != 0 || f.Mix.NProbe != 4 || f.Mix.NErrReal != 0 {
		t.Fatalf("mix=%+v want NUser=0 NProbe=4 NErrReal=0", f.Mix)
	}
	if f.Chan != nil && f.Chan.N != 0 {
		t.Fatalf("probe-only window must not produce real ChanStat N, got %+v", f.Chan)
	}

	// Live v5.01 trust config uses up=0.2 (code default is 0.08).
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "up": 0.2, "up_full_n": 30, "round_min": 45, "down": 0.6,
	}}
	// Inter-round gap is the difference of CC algorithm now values: 2209s.
	// This early interval is below round_min=45, so the time factor is not capped.
	gap := m.F(2209.0 / 60.0)

	t.Run("live_shape_recovers_0_2198_to_0_2416_uncapped_and_scaled_by_n60_4", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.2198}, gap)[72]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=0 because 1h 0%<2% and 10m n=1 < err_10m_min_n=2 with err=0.
		// 0.2198 + 0.2*min(1, 2209/60/45)*min(1, 4/30) = 0.2416170... -> 0.2416
		// Time factor is NOT capped because 2209/2700 < 1. This is the live
		// successor of CASE-AW-047 (0.1931 -> 0.2198, capped, empty 10m, n60=4).
		if st.Sev != 0 || st.Trust != 0.2416 {
			t.Fatalf("trust sev=%v value=%v want 0 / 0.2416", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
	})

	t.Run("empty_10m_with_same_n60_and_gap_also_reaches_0_2416", func(t *testing.T) {
		// The live 10m 0/1 does not change the increment versus an empty
		// 10m: both are below err_10m_min_n=2 and stay on the up branch.
		// The live delta versus CASE-AW-047 is the uncapped 2209s gap, not
		// the return of a single clean 10m probe.
		sib := m.Feats{72: {
			Long:  &m.Window{N: 4, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 0, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{72}, sib, nil, map[int]float64{72: 0.2198}, gap)[72]
		if st.Sev != 0 || st.Trust != 0.2416 {
			t.Fatalf("empty 10m contrast sev=%v value=%v want 0 / 0.2416", st.Sev, st.Trust)
		}
	})

	t.Run("capped_interval_with_same_n60_would_reach_0_2465", func(t *testing.T) {
		// If the 2209s gap were already at the cap of 1, the increment
		// would be 0.2*1*(4/30)=0.026666..., to 0.246466... -> 0.2465.
		// The live early interval must not be rewritten as 0.2465.
		cappedInc := 0.2 * 1.0 * (4.0 / 30.0)
		capped := 0.2198 + cappedInc
		if rounded := float64(int(capped*10000+0.5)) / 10000; rounded != 0.2465 {
			t.Fatalf("capped contrast rounded=%v raw=%v want 0.2465", rounded, capped)
		}
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.2198}, gap)[72]
		if st.Trust != 0.2416 {
			t.Fatalf("live uncapped value=%v want 0.2416 (capped contrast is 0.2465, not the live value)", st.Trust)
		}
		cappedGap := m.F(2766.0 / 60.0)
		held := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.2198}, cappedGap)[72]
		if held.Sev != 0 || held.Trust != 0.2465 {
			t.Fatalf("capped-gap contrast sev=%v value=%v want 0 / 0.2465", held.Sev, held.Trust)
		}
	})

	t.Run("same_uncapped_interval_with_n60_5_would_reach_0_2471", func(t *testing.T) {
		// If this window had n60=5, the still-uncapped 2209s gap would
		// recover 0.2198 + 0.2*(2209/2700)*(5/30) = 0.247071... -> 0.2471.
		// The live successor kept n=4, so it must not be rewritten as 0.2471.
		sib := m.Feats{72: {
			Long:  &m.Window{N: 5, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 1, Err: 0, ErrRate: m.F(0)},
		}}
		st := trust.Step(trustCfg, []int{72}, sib, nil, map[int]float64{72: 0.2198}, gap)[72]
		if st.Sev != 0 || st.Trust != 0.2471 {
			t.Fatalf("n60=5 contrast sev=%v value=%v want 0 / 0.2471", st.Sev, st.Trust)
		}
	})

	t.Run("case_043_counterfactual_capped_n21_would_still_reach_0_24", func(t *testing.T) {
		// CASE-AW-043's counterfactual remains 0.1 + 0.2*1*(21/30) = 0.24
		// only at a capped interval. The live path starts from 0.2198 with
		// n60=4 and an uncapped 2209s gap, and must not be rewritten as 0.24.
		ample := m.Feats{72: {
			Long:  &m.Window{N: 21, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 0, Err: 0},
		}}
		cappedGap := m.F(2766.0 / 60.0)
		st := trust.Step(trustCfg, []int{72}, ample, nil, map[int]float64{72: 0.1}, cappedGap)[72]
		if st.Sev != 0 || st.Trust != 0.24 {
			t.Fatalf("043 counterfactual sev=%v value=%v want 0 / 0.24", st.Sev, st.Trust)
		}
	})

	t.Run("sibling_67_mid_recovery_scales_0_4149_to_0_4367_without_cap", func(t *testing.T) {
		// Same live 2209s gap, sanitized CC #67 shape this round: clean 1h
		// n=4 (empty=1 still counted as type2 success, all probes), empty
		// 10m n=0, prev 0.4149 after CASE-AW-047. Sample factor is 4/30,
		// time factor is uncapped: 0.4149 + 0.2*(2209/2700)*(4/30) = 0.436717...
		// -> 0.4367. Trust is already >= 0.3, so cap 3 is not computed
		// even though n60=4 < 20. Do not rewrite this sibling as sharing
		// #72's 10m 0/1.
		sib := m.Feats{67: {
			Long:  &m.Window{N: 4, Err: 0, ErrRate: m.F(0), Empty: m.I(1)},
			Short: &m.Window{N: 0, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{67}, sib, nil, map[int]float64{67: 0.4149}, gap)[67]
		if st.Sev != 0 || st.Trust != 0.4367 {
			t.Fatalf("sibling mid recovery sev=%v value=%v want 0 / 0.4367", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean recovery", st.Why)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{67: {N60: 4}}, map[int]float64{67: st.Trust}, []int{67})
		if caps[67] != nil {
			t.Fatalf("trust cap=%+v; trust 0.4367 is not < 0.3 so recovery cap 3 must not be computed even though n60=4 < 20", caps[67])
		}
	})

	t.Run("sibling_68_probe_only_window_holds_ceiling_1_without_cap", func(t *testing.T) {
		// Same live 2209s gap, sanitized CC #68 shape this round: still
		// probe-only after CASE-AW-047, n60 stays 4, and the 10m window is
		// still empty (unlike #72). prev already 1, so trust stays at the
		// ceiling. n60=4 does not generate cap 3 because trust is not < 0.3.
		sib := m.Feats{68: {
			Long:  &m.Window{N: 4, Err: 0, ErrRate: m.F(0)},
			Short: &m.Window{N: 0, Err: 0},
		}}
		st := trust.Step(trustCfg, []int{68}, sib, nil, map[int]float64{68: 1}, gap)[68]
		if st.Sev != 0 || st.Trust != 1 {
			t.Fatalf("sibling ceiling hold sev=%v value=%v want 0 / 1", st.Sev, st.Trust)
		}
		if len(st.Why) != 0 {
			t.Fatalf("trust why=%v want no notes on a clean hold at the ceiling", st.Why)
		}
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{68: {N60: 4}}, map[int]float64{68: st.Trust}, []int{68})
		if caps[68] != nil {
			t.Fatalf("trust cap=%+v; trust 1 is not < 0.3 so recovery cap 3 must not be computed even though n60=4 < 20", caps[68])
		}
	})

	t.Run("recovery_cap_3_is_computed_when_scaled_trust_stays_below_0_3", func(t *testing.T) {
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.2198}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: f.Long.N}}, map[int]float64{72: st.Trust}, []int{72})
		c := caps[72]
		if c == nil || c.Cap != 3 || c.Kind != "trust" {
			t.Fatalf("trust cap=%+v want cap=3 kind=trust", c)
		}
		if c.Reason != "信任 0.24 < 0.3且 1h 样本 4 < 20，恢复期限幅" {
			t.Fatalf("trust cap reason=%q", c.Reason)
		}
	})

	t.Run("settle_holds_0_to_20_and_mentions_unapplied_cap_with_zero_real", func(t *testing.T) {
		// Live pre-settle strategy this round is already 0: all three CC
		// channels have score=0 and are ineligible, so rank assignment does
		// not produce a 3. The recovery cap is an upper bound and does not
		// raise 0 to 3. Do not rewrite this live note as 策略 3→20.
		st := trust.Step(trustCfg, []int{72}, fs, nil, map[int]float64{72: 0.2198}, gap)[72]
		capCfg := m.M{"trust_cap": m.M{"enabled": true, "thr": 0.3, "cap": 3.0, "max_n60": 20.0}}
		caps := map[int]*Cap{}
		applyTrustCap(capCfg, caps, map[int]*Row{72: {N60: f.Long.N}}, map[int]float64{72: st.Trust}, []int{72})
		next := map[int]int{72: 0}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, caps, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; system cap 3 must not be applied", next[72])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#72 定档：策略 0→20", "系统上限 3", "不执行", "1h 真实 0", "渠道错误 0", "去集中 0", "0.0%", "未达降权门槛"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected held-cap note to contain %q, got %q", part, notes)
			}
		}
		if strings.Contains(joined, "策略 3→20") {
			t.Fatalf("live pre-settle target is 0 this round; note must not be rewritten as 策略 3→20, got %q", notes)
		}
	})

	t.Run("already_20_has_no_settle_note", func(t *testing.T) {
		next := map[int]int{72: 20}
		var notes []string
		Settle(m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}, next, map[int]int{72: 20}, fs, []int{72}, nil, &notes)
		if next[72] != 20 {
			t.Fatalf("weight=%d want 20; notes=%q", next[72], notes)
		}
		if len(notes) != 0 {
			t.Fatalf("already at 20 should not emit a settle note, got %q", notes)
		}
	})
}

// CASE-AW-049: sanitized aggregate shape from 2026-11-22 06:07 +08:00 GPT #73.
// No production log rows, request identifiers, model names or real token IDs.
// Locks current v5.01 behavior; it does not change the algorithm.
//
// An ample 1h window (raw 1/178=0.56% < 2%, ChanStat N=173 Client=1 Err=0)
// still takes a hard trust cut when a lean but non-tiny 10m window is 1/7.
// SeverityParts reads the raw 10m rate, so one client-class error yields
// sev=min(1, (1/7)/0.2)=0.714. Update then does 1*(1-0.6*0.714)=0.5716.
// The 1h path does not fire. err_10m_full_n is 0, so the lean denominator
// is not scaled down. Settle still holds 20 because 30/3/3% is not met.
func TestCasebookAmpleHourSingleClientErrorInLeanTenMinCutsTrustHard(t *testing.T) {
	now := int64(100000)
	build := func(shortSuccess, shortErr, longSuccess, longErr, probes int, status *int) m.Feats {
		t.Helper()
		var rows []m.Row
		for n := 0; n < shortSuccess; n++ {
			rows = append(rows, m.Row{Ch: 73, T: now - 30, Type: 2, Tok: 900003})
		}
		for n := 0; n < shortErr; n++ {
			rows = append(rows, m.Row{Ch: 73, T: now - 30, Type: 5, Tok: 900010, Sc: status})
		}
		for n := 0; n < longSuccess; n++ {
			rows = append(rows, m.Row{Ch: 73, T: now - 40*60, Type: 2, Tok: 900004})
		}
		for n := 0; n < longErr; n++ {
			rows = append(rows, m.Row{Ch: 73, T: now - 40*60, Type: 5, Tok: 900011, Sc: status})
		}
		for n := 0; n < probes; n++ {
			// Probes sit in the 60m window but outside the 10m window, matching
			// the observed 10m N that did not include the 4 probes.
			rows = append(rows, m.Row{Ch: 73, T: now - 40*60, Type: 2, Tok: 1318})
		}
		cfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
		return features.Build(cfg, []m.Channel{{ID: 73}}, rows, now)
	}

	settleCfg := m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}
	// Live v5.01 trust keys used on the down path. err_10m_full_n=0 is the
	// production default: lean 10m windows are not severity-scaled by n.
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "down": 0.6, "err_10m_min_n": 2, "err_full": 0.2,
		"err_1h_thr": 0.02, "err_10m_full_n": 0, "t_min": 0.1,
	}}

	t.Run("live_shape_10m_1_of_7_cuts_1_to_0_5716_while_1h_stays_under_2pct", func(t *testing.T) {
		// GPT #73 this round: 10m 1/7, 60m 1/178, mix 174 user + 4 probes,
		// ChanStat N=173 Client=1 Err=0. 1h raw 1/178=0.56% is below 2%.
		fs := build(6, 1, 167, 0, 4, m.I(400))
		f := fs[73]
		if f == nil || f.Long == nil || f.Short == nil || f.Mix == nil || f.Chan == nil {
			t.Fatalf("missing features: %+v", f)
		}
		if f.Short.N != 7 || f.Short.Err != 1 {
			t.Fatalf("10m window n=%d err=%d want 7/1", f.Short.N, f.Short.Err)
		}
		if f.Long.N != 178 || f.Long.Err != 1 {
			t.Fatalf("1h window n=%d err=%d want 178/1", f.Long.N, f.Long.Err)
		}
		if m.Or(f.Long.ErrRate, 0) > 0.02 {
			t.Fatalf("1h err rate=%v should stay under 2%%", m.Or(f.Long.ErrRate, 0))
		}
		if f.Mix.NUser != 174 || f.Mix.NProbe != 4 || f.Mix.NErrReal != 1 {
			t.Fatalf("mix=%+v want NUser=174 NProbe=4 NErrReal=1", f.Mix)
		}
		if f.Chan.N != 173 || f.Chan.Err != 0 || f.Chan.Client != 1 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
			t.Fatalf("chanstat=%+v want N=173 Err=0 Client=1 Eff=0 Rate=0", f.Chan)
		}

		st := trust.Step(trustCfg, []int{73}, fs, nil, map[int]float64{73: 1}, nil)[73]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=min(1, (1/7)/0.2)=0.714285... -> 0.714
		// 1*(1-0.6*0.714)=0.5716. The 1h path is silent because 0.56% < 2%.
		if st.Sev != 0.714 || st.Trust != 0.5716 {
			t.Fatalf("trust sev=%v value=%v want 0.714 / 0.5716", st.Sev, st.Trust)
		}
		if len(st.Why) != 1 || st.Why[0] != "10m 错误 1/7" {
			t.Fatalf("trust why=%v want [10m 错误 1/7]", st.Why)
		}
	})

	t.Run("same_client_error_outside_10m_does_not_cut", func(t *testing.T) {
		// Contrast: move the single client-class error out of the 10m window
		// while keeping the same 1h 1/178 < 2%. Trust must stay at 1 because
		// the live cut came from the lean 10m denominator, not the 1h rate.
		fs := build(7, 0, 166, 1, 4, m.I(400))
		f := fs[73]
		if f.Short.N != 7 || f.Short.Err != 0 {
			t.Fatalf("10m window n=%d err=%d want 7/0", f.Short.N, f.Short.Err)
		}
		if f.Long.N != 178 || f.Long.Err != 1 || m.Or(f.Long.ErrRate, 0) > 0.02 {
			t.Fatalf("1h window=%+v want 178/1 under 2%%", f.Long)
		}
		if f.Chan.N != 173 || f.Chan.Err != 0 || f.Chan.Client != 1 || f.Chan.Eff != 0 {
			t.Fatalf("chanstat=%+v want N=173 Err=0 Client=1 Eff=0", f.Chan)
		}
		st := trust.Step(trustCfg, []int{73}, fs, nil, map[int]float64{73: 1}, nil)[73]
		if st.Sev != 0 || st.Trust != 1 || len(st.Why) != 0 {
			t.Fatalf("trust sev=%v value=%v why=%v want 0 / 1 / empty", st.Sev, st.Trust, st.Why)
		}
	})

	t.Run("same_1_of_7_as_500_keeps_same_trust", func(t *testing.T) {
		// Contrast: trust still follows raw 10m 1/7 when the type5 is a
		// channel 500. ChanStat now counts 1 fault, but Eff=1 is below 3.
		fs := build(6, 1, 167, 0, 4, m.I(500))
		f := fs[73]
		if f.Short.N != 7 || f.Short.Err != 1 || f.Long.N != 178 || f.Long.Err != 1 {
			t.Fatalf("raw windows short=%+v long=%+v want 7/1 and 178/1", f.Short, f.Long)
		}
		if f.Chan.N != 174 || f.Chan.Err != 1 || f.Chan.Client != 0 || f.Chan.Eff != 1 {
			t.Fatalf("chanstat=%+v want N=174 Err=1 Client=0 Eff=1", f.Chan)
		}
		st := trust.Step(trustCfg, []int{73}, fs, nil, map[int]float64{73: 1}, nil)[73]
		if st.Sev != 0.714 || st.Trust != 0.5716 || len(st.Why) != 1 || st.Why[0] != "10m 错误 1/7" {
			t.Fatalf("trust should still follow raw 10m 1/7, got sev=%v value=%v why=%v", st.Sev, st.Trust, st.Why)
		}
		next := map[int]int{73: 0}
		var notes []string
		Settle(settleCfg, next, map[int]int{73: 20}, fs, []int{73}, nil, &notes)
		if next[73] != 20 {
			t.Fatalf("weight=%d want 20; Eff=1 is still below 3", next[73])
		}
		joined := strings.Join(notes, ";")
		if !strings.Contains(joined, "1h 真实 174") || !strings.Contains(joined, "渠道错误 1") || !strings.Contains(joined, "去集中 1") {
			t.Fatalf("expected hold note with ChanStat 174/1/1, got %q", notes)
		}
	})

	t.Run("larger_10m_1_of_59_is_much_milder", func(t *testing.T) {
		// Same single client-class error in a larger non-tiny 10m window.
		// This isolates the lean denominator: 1/59 must not be rewritten as
		// the live 1/7 cut to 0.5716.
		fs := build(58, 1, 115, 0, 4, m.I(400))
		f := fs[73]
		if f.Short.N != 59 || f.Short.Err != 1 {
			t.Fatalf("10m window n=%d err=%d want 59/1", f.Short.N, f.Short.Err)
		}
		st := trust.Step(trustCfg, []int{73}, fs, nil, map[int]float64{73: 1}, nil)[73]
		if !((st.Sev == 0.085 && st.Trust == 0.949) || (st.Sev == 0.084 && st.Trust == 0.9496)) {
			t.Fatalf("larger 10m trust sev=%v value=%v want 0.085/0.949 or 0.084/0.9496", st.Sev, st.Trust)
		}
		if len(st.Why) != 1 || st.Why[0] != "10m 错误 1/59" {
			t.Fatalf("trust why=%v want [10m 错误 1/59]", st.Why)
		}
		if st.Sev >= 0.5 || st.Trust < 0.9 {
			t.Fatalf("1/59 must stay far milder than live 1/7 (0.714/0.5716), got sev=%v value=%v", st.Sev, st.Trust)
		}
	})

	t.Run("settle_holds_0_to_20_with_chanstat_173_0_0", func(t *testing.T) {
		fs := build(6, 1, 167, 0, 4, m.I(400))
		next := map[int]int{73: 0}
		var notes []string
		Settle(settleCfg, next, map[int]int{73: 20}, fs, []int{73}, nil, &notes)
		if next[73] != 20 {
			t.Fatalf("weight=%d want 20 after 0->20 hold", next[73])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#73 定档：策略 0→20", "无渠道级错误证据", "1h 真实 173", "渠道错误 0", "去集中 0", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected hold note to contain %q, got %q", part, notes)
			}
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault-gate cut, got %q", notes)
		}
	})

	t.Run("rank_assigns_low_w_0_when_score_ratio_is_below_mid_ratio", func(t *testing.T) {
		// Live post-trust scores this round, after Multiplier floor=0.85:
		// #26=0.554 (thin clean leader, n60=14 >= min_n_rank1=10),
		// #2=0.3727 (ratio 0.6727 < mid_ratio 0.7),
		// #73=0.2746 (ratio 0.4957), #23=0, #32=0.
		// lead_min_n=0, so the thin eligible leader is the denominator.
		// Production mid_ratio=0.7, low_w=0, ramp=0: all but #26 get 0.
		// This does not claim the trust cut alone caused the 0: #73 raw
		// 0.2935/0.554 and #2 raw 0.3755/0.554 are also below 0.7.
		sc := map[int]float64{26: 0.554, 2: 0.3727, 73: 0.2746, 23: 0, 32: 0}
		order := []int{26, 2, 73, 23, 32}
		rp := RankParams{P: m.M{
			"top_w": 20, "close_ratio": 0.85, "close_factor": 0.8,
			"mid_ratio": 0.7, "mid_w": 3, "low_w": 0, "tie_ratio": 0.95,
			"hysteresis": 0, "band": 0, "ramp": 0, "lead_trust": 0,
			"lead_min_n": 0, "co_lead_ratio": 0,
		}}
		got := StratRank(order, sc, rp, m.M{"w_total": 18}, map[int]int{26: 20, 2: 20, 73: 20, 23: 20, 32: 20}, nil)
		if got[26] != 20 || got[2] != 0 || got[73] != 0 || got[23] != 0 || got[32] != 0 {
			t.Fatalf("rank weights=%v want 26=20 and 2/73/23/32=0", got)
		}
	})
}

// CASE-AW-050: sanitized aggregate shape from 2026-11-22 06:47 +08:00 GPT #73.
// No production log rows, request identifiers, model names or real token IDs.
// Locks current v5.01 behavior; it does not change the algorithm.
//
// Follow-on window after CASE-AW-049. The same single client-class error
// remains in the 1h window, but the denominator shrank from 178 to 39, so
// 1/39=2.56% now crosses err_1h_thr=2%. The 10m window is clean 0/1
// (below err_10m_min_n=2), so the 10m path is silent. Trust does not
// recover despite a clean 10m and an uncapped 2368s gap; Update takes the
// down path: 0.5716*(1-0.6*0.028)=0.562, why "1h 错误率 2.6%".
// sev=(1/39-0.02)/0.2=0.028. ChanStat is still Client=1, Err=0, N=34.
// Settle holds 20 (1h real 34, errors 0). Mid-ratio contrast: raw
// 0.4644/0.6209=0.7479 would be >=0.7 (mid_w=3), but post-trust
// 0.4339/0.6209=0.6988 is below 0.7 (low_w=0).
func TestCasebookShrunkHourRateCrossesTwoPctAfterCleanTenMinKeepsCuttingTrust(t *testing.T) {
	now := int64(100000)
	build := func(shortSuccess, longSuccess, longErr, probes int, status *int) m.Feats {
		t.Helper()
		var rows []m.Row
		for n := 0; n < shortSuccess; n++ {
			rows = append(rows, m.Row{Ch: 73, T: now - 30, Type: 2, Tok: 900003})
		}
		for n := 0; n < longSuccess; n++ {
			rows = append(rows, m.Row{Ch: 73, T: now - 40*60, Type: 2, Tok: 900004})
		}
		for n := 0; n < longErr; n++ {
			rows = append(rows, m.Row{Ch: 73, T: now - 40*60, Type: 5, Tok: 900011, Sc: status})
		}
		for n := 0; n < probes; n++ {
			// Probes sit in the 60m window but outside the 10m window, matching
			// the observed 10m N=1 that did not include the 4 probes.
			rows = append(rows, m.Row{Ch: 73, T: now - 40*60, Type: 2, Tok: 1318})
		}
		cfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
		return features.Build(cfg, []m.Channel{{ID: 73}}, rows, now)
	}

	settleCfg := m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}
	// Live v5.01 trust keys. up=0.2 is required so the clean-1h contrast
	// uses the production recovery increment, not the code default 0.08.
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "down": 0.6, "err_10m_min_n": 2, "err_full": 0.2,
		"err_1h_thr": 0.02, "err_10m_full_n": 0, "t_min": 0.1,
		"up": 0.2, "up_full_n": 30, "round_min": 45,
	}}
	// Inter-round gap is the difference of GPT algorithm now values: 2368s.
	// 2368/2700=0.8770 < 1, so a clean window would recover with an uncapped
	// time factor. The live down path must ignore that increment.
	gap := m.F(2368.0 / 60.0)

	t.Run("live_shape_clean_10m_1_of_39_cuts_0_5716_to_0_562_via_1h_path", func(t *testing.T) {
		// GPT #73 this round: 10m 0/1, 60m 1/39=2.56%, mix 35 user + 4 probes,
		// ChanStat N=34 Client=1 Err=0. 1h raw now crosses 2%.
		fs := build(1, 33, 1, 4, m.I(400))
		f := fs[73]
		if f == nil || f.Long == nil || f.Short == nil || f.Mix == nil || f.Chan == nil {
			t.Fatalf("missing features: %+v", f)
		}
		if f.Short.N != 1 || f.Short.Err != 0 {
			t.Fatalf("10m window n=%d err=%d want 1/0", f.Short.N, f.Short.Err)
		}
		if f.Long.N != 39 || f.Long.Err != 1 {
			t.Fatalf("1h window n=%d err=%d want 39/1", f.Long.N, f.Long.Err)
		}
		if m.Or(f.Long.ErrRate, 0) <= 0.02 {
			t.Fatalf("1h err rate=%v should cross 2%%", m.Or(f.Long.ErrRate, 0))
		}
		if f.Mix.NUser != 35 || f.Mix.NProbe != 4 || f.Mix.NErrReal != 1 {
			t.Fatalf("mix=%+v want NUser=35 NProbe=4 NErrReal=1", f.Mix)
		}
		if f.Chan.N != 34 || f.Chan.Err != 0 || f.Chan.Client != 1 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
			t.Fatalf("chanstat=%+v want N=34 Err=0 Client=1 Eff=0 Rate=0", f.Chan)
		}

		st := trust.Step(trustCfg, []int{73}, fs, nil, map[int]float64{73: 0.5716}, gap)[73]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// sev=(1/39-0.02)/0.2=0.028205... -> 0.028
		// 0.5716*(1-0.6*0.028)=0.561996... -> 0.562.
		// 10m N=1 is below err_10m_min_n=2 and Err=0, so only the 1h why fires.
		// The uncapped 2368s gap must not be applied on the down path.
		if st.Sev != 0.028 || st.Trust != 0.562 {
			t.Fatalf("trust sev=%v value=%v want 0.028 / 0.562", st.Sev, st.Trust)
		}
		if len(st.Why) != 1 || st.Why[0] != "1h 错误率 2.6%" {
			t.Fatalf("trust why=%v want [1h 错误率 2.6%%]", st.Why)
		}
	})

	t.Run("same_error_in_ample_1h_under_2pct_with_clean_10m_recovers", func(t *testing.T) {
		// Contrast: keep the same single client-class error, but restore the
		// previous ample 1h denominator 1/178=0.56%<2% and a clean 10m 0/1.
		// This is the CASE-AW-049 leftover error without the shrink. Trust
		// must take the up path, proving the live 0.562 cut comes from the
		// rate crossing 2%, not from the leftover error alone.
		fs := build(1, 172, 1, 4, m.I(400))
		f := fs[73]
		if f.Short.N != 1 || f.Short.Err != 0 {
			t.Fatalf("10m window n=%d err=%d want 1/0", f.Short.N, f.Short.Err)
		}
		if f.Long.N != 178 || f.Long.Err != 1 || m.Or(f.Long.ErrRate, 0) > 0.02 {
			t.Fatalf("1h window=%+v want 178/1 under 2%%", f.Long)
		}
		if f.Chan.N != 173 || f.Chan.Err != 0 || f.Chan.Client != 1 || f.Chan.Eff != 0 {
			t.Fatalf("chanstat=%+v want N=173 Err=0 Client=1 Eff=0", f.Chan)
		}
		st := trust.Step(trustCfg, []int{73}, fs, nil, map[int]float64{73: 0.5716}, gap)[73]
		// 0.5716 + 0.2*(2368/2700)*1 = 0.747007... -> 0.7470
		if st.Sev != 0 || st.Trust != 0.747 || len(st.Why) != 0 {
			t.Fatalf("trust sev=%v value=%v why=%v want 0 / 0.747 / empty", st.Sev, st.Trust, st.Why)
		}
	})

	t.Run("clean_shrunk_1h_with_same_gap_recovers_instead_of_cutting", func(t *testing.T) {
		// Contrast: same shrunk n60=39 and the same uncapped 2368s gap, but
		// no leftover 1h error. The live window would have recovered to
		// 0.7470 rather than ticking down to 0.562.
		fs := build(1, 34, 0, 4, m.I(400))
		f := fs[73]
		if f.Short.N != 1 || f.Short.Err != 0 || f.Long.N != 39 || f.Long.Err != 0 {
			t.Fatalf("windows short=%+v long=%+v want 10m 1/0 and 1h 39/0", f.Short, f.Long)
		}
		if m.Or(f.Long.ErrRate, 0) != 0 {
			t.Fatalf("1h err rate=%v want 0", f.Long.ErrRate)
		}
		st := trust.Step(trustCfg, []int{73}, fs, nil, map[int]float64{73: 0.5716}, gap)[73]
		if st.Sev != 0 || st.Trust != 0.747 || len(st.Why) != 0 {
			t.Fatalf("clean shrink should recover, got sev=%v value=%v why=%v want 0 / 0.747 / empty", st.Sev, st.Trust, st.Why)
		}
	})

	t.Run("same_1_of_39_as_500_keeps_same_trust", func(t *testing.T) {
		// Contrast: trust still follows the raw 1h 1/39 rate when the type5
		// is a channel 500. ChanStat now counts 1 fault, but Eff=1 is below 3.
		fs := build(1, 33, 1, 4, m.I(500))
		f := fs[73]
		if f.Short.N != 1 || f.Short.Err != 0 || f.Long.N != 39 || f.Long.Err != 1 {
			t.Fatalf("raw windows short=%+v long=%+v want 1/0 and 39/1", f.Short, f.Long)
		}
		if f.Chan.N != 35 || f.Chan.Err != 1 || f.Chan.Client != 0 || f.Chan.Eff != 1 {
			t.Fatalf("chanstat=%+v want N=35 Err=1 Client=0 Eff=1", f.Chan)
		}
		st := trust.Step(trustCfg, []int{73}, fs, nil, map[int]float64{73: 0.5716}, gap)[73]
		if st.Sev != 0.028 || st.Trust != 0.562 || len(st.Why) != 1 || st.Why[0] != "1h 错误率 2.6%" {
			t.Fatalf("trust should still follow raw 1h 1/39, got sev=%v value=%v why=%v", st.Sev, st.Trust, st.Why)
		}
		next := map[int]int{73: 0}
		var notes []string
		Settle(settleCfg, next, map[int]int{73: 20}, fs, []int{73}, nil, &notes)
		if next[73] != 20 {
			t.Fatalf("weight=%d want 20; Eff=1 is still below 3", next[73])
		}
		joined := strings.Join(notes, ";")
		if !strings.Contains(joined, "1h 真实 35") || !strings.Contains(joined, "渠道错误 1") || !strings.Contains(joined, "去集中 1") {
			t.Fatalf("expected hold note with ChanStat 35/1/1, got %q", notes)
		}
	})

	t.Run("settle_holds_0_to_20_with_chanstat_34_0_0", func(t *testing.T) {
		fs := build(1, 33, 1, 4, m.I(400))
		next := map[int]int{73: 0}
		var notes []string
		Settle(settleCfg, next, map[int]int{73: 20}, fs, []int{73}, nil, &notes)
		if next[73] != 20 {
			t.Fatalf("weight=%d want 20 after 0->20 hold", next[73])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#73 定档：策略 0→20", "无渠道级错误证据", "1h 真实 34", "渠道错误 0", "去集中 0", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected hold note to contain %q, got %q", part, notes)
			}
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault-gate cut, got %q", notes)
		}
	})

	t.Run("rank_trust_cut_pushes_score_just_below_mid_ratio", func(t *testing.T) {
		// Live post-trust scores this round, after Multiplier floor=0.85:
		// #26=0.6209 (clean leader, n60=107), #23=0.5273 (ratio 0.8493),
		// #2=0.467 (ratio 0.7521), #73=0.4339 (ratio 0.6988 < 0.7), #32=0.
		// Production mid_ratio=0.7, mid_w=3, low_w=0, lead_min_n=0.
		// Unlike CASE-AW-049, the raw #73 score 0.4644/0.6209=0.7479 would
		// have been mid-tier; the 0.562 trust cut is what pushed it below 0.7.
		rp := RankParams{P: m.M{
			"top_w": 20, "close_ratio": 0.85, "close_factor": 0.8,
			"mid_ratio": 0.7, "mid_w": 3, "low_w": 0, "tie_ratio": 0.95,
			"hysteresis": 0, "band": 0, "ramp": 0, "lead_trust": 0,
			"lead_min_n": 0, "co_lead_ratio": 0,
		}}
		old := map[int]int{26: 20, 23: 20, 2: 20, 73: 20, 32: 20}
		sys := m.M{"w_total": 18}
		order := []int{26, 23, 2, 73, 32}

		got := StratRank(order, map[int]float64{26: 0.6209, 23: 0.5273, 2: 0.467, 73: 0.4339, 32: 0}, rp, sys, old, nil)
		if got[26] != 20 || got[23] != 3 || got[2] != 3 || got[73] != 0 || got[32] != 0 {
			t.Fatalf("post-trust rank weights=%v want 26=20, 23/2=3, 73/32=0", got)
		}

		raw := StratRank(order, map[int]float64{26: 0.6209, 23: 0.5273, 2: 0.467, 73: 0.4644, 32: 0}, rp, sys, old, nil)
		if raw[26] != 20 || raw[23] != 3 || raw[2] != 3 || raw[73] != 3 || raw[32] != 0 {
			t.Fatalf("raw rank weights=%v want 26=20 and 23/2/73=3, 32=0", raw)
		}
	})
}

func TestCasebookErrorLeavingShrunkHourRecoversTrustWithCappedSampleStep(t *testing.T) {
	now := int64(100000)
	build := func(shortSuccess, longSuccess, longErr, probes int, status *int) m.Feats {
		t.Helper()
		var rows []m.Row
		for n := 0; n < shortSuccess; n++ {
			rows = append(rows, m.Row{Ch: 73, T: now - 30, Type: 2, Tok: 900003})
		}
		for n := 0; n < longSuccess; n++ {
			rows = append(rows, m.Row{Ch: 73, T: now - 40*60, Type: 2, Tok: 900004})
		}
		for n := 0; n < longErr; n++ {
			rows = append(rows, m.Row{Ch: 73, T: now - 40*60, Type: 5, Tok: 900011, Sc: status})
		}
		for n := 0; n < probes; n++ {
			// Probes sit in the 60m window but outside the 10m window, matching
			// the observed 10m N=1 that did not include the 5 probes.
			rows = append(rows, m.Row{Ch: 73, T: now - 40*60, Type: 2, Tok: 1318})
		}
		cfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
		return features.Build(cfg, []m.Channel{{ID: 73}}, rows, now)
	}

	settleCfg := m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}
	// Live v5.01 trust keys. up=0.2 and up_full_n=30 are required so the
	// recovery increment matches production, not the code default 0.08.
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "down": 0.6, "err_10m_min_n": 2, "err_full": 0.2,
		"err_1h_thr": 0.02, "err_10m_full_n": 0, "t_min": 0.1,
		"up": 0.2, "up_full_n": 30, "round_min": 45,
	}}
	// Inter-round gap is the difference of GPT algorithm now values: 2970s.
	// 2970/2700=1.1, so the time factor is capped at 1.
	gap := m.F(2970.0 / 60.0)
	uncappedGap := m.F(2368.0 / 60.0)

	t.Run("live_shape_clean_0_of_7_recovers_0_562_to_0_6087_capped_step", func(t *testing.T) {
		// GPT #73 this round: 10m 0/1, 60m 0/7, mix 2 user + 5 probes,
		// ChanStat N=2 Client=0 Err=0. The leftover client-class error has
		// left the 1h window after CASE-AW-050's 1/39 shrink cut.
		fs := build(1, 1, 0, 5, m.I(400))
		f := fs[73]
		if f == nil || f.Long == nil || f.Short == nil || f.Mix == nil || f.Chan == nil {
			t.Fatalf("missing features: %+v", f)
		}
		if f.Short.N != 1 || f.Short.Err != 0 {
			t.Fatalf("10m window n=%d err=%d want 1/0", f.Short.N, f.Short.Err)
		}
		if f.Long.N != 7 || f.Long.Err != 0 {
			t.Fatalf("1h window n=%d err=%d want 7/0", f.Long.N, f.Long.Err)
		}
		if m.Or(f.Long.ErrRate, 0) != 0 {
			t.Fatalf("1h err rate=%v want 0", f.Long.ErrRate)
		}
		if f.Mix.NUser != 2 || f.Mix.NProbe != 5 || f.Mix.NErrReal != 0 {
			t.Fatalf("mix=%+v want NUser=2 NProbe=5 NErrReal=0", f.Mix)
		}
		if f.Chan.N != 2 || f.Chan.Err != 0 || f.Chan.Client != 0 || f.Chan.Eff != 0 || f.Chan.Rate != 0 {
			t.Fatalf("chanstat=%+v want N=2 Err=0 Client=0 Eff=0 Rate=0", f.Chan)
		}

		st := trust.Step(trustCfg, []int{73}, fs, nil, map[int]float64{73: 0.562}, gap)[73]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// Capped time factor 1, sample factor 7/30:
		// 0.562 + 0.2*(7/30) = 0.608666... -> 0.6087.
		// Recovery uses scoring n60=7, not ChanStat / 真实 n=2
		// (that would have been 0.562+0.2*(2/30)=0.5753).
		// It is also not the CASE-AW-050 counterfactual 0.7470, which used
		// prev=0.5716, the uncapped 2368s gap, and an ample n60.
		if st.Sev != 0 || st.Trust != 0.6087 || len(st.Why) != 0 {
			t.Fatalf("trust sev=%v value=%v why=%v want 0 / 0.6087 / empty", st.Sev, st.Trust, st.Why)
		}
	})

	t.Run("leftover_1_of_39_with_same_capped_gap_still_cuts", func(t *testing.T) {
		// Contrast: keep CASE-AW-050's leftover 1/39=2.56% error, but use
		// this round's capped 2970s gap and the post-cut prev=0.562.
		// Recovery must not start merely because more time has passed.
		fs := build(1, 33, 1, 4, m.I(400))
		f := fs[73]
		if f.Short.N != 1 || f.Short.Err != 0 || f.Long.N != 39 || f.Long.Err != 1 {
			t.Fatalf("windows short=%+v long=%+v want 10m 1/0 and 1h 39/1", f.Short, f.Long)
		}
		if m.Or(f.Long.ErrRate, 0) <= 0.02 {
			t.Fatalf("1h err rate=%v should still cross 2%%", m.Or(f.Long.ErrRate, 0))
		}
		st := trust.Step(trustCfg, []int{73}, fs, nil, map[int]float64{73: 0.562}, gap)[73]
		// sev stays 0.028; down path ignores the time factor:
		// 0.562*(1-0.6*0.028)=0.5525584... -> 0.5526.
		if st.Sev != 0.028 || st.Trust != 0.5526 {
			t.Fatalf("trust sev=%v value=%v want 0.028 / 0.5526", st.Sev, st.Trust)
		}
		if len(st.Why) != 1 || st.Why[0] != "1h 错误率 2.6%" {
			t.Fatalf("trust why=%v want [1h 错误率 2.6%%]", st.Why)
		}
	})

	t.Run("same_clean_0_of_7_with_previous_uncapped_gap_recovers_less", func(t *testing.T) {
		// Contrast: same clean 0/7 window and prev=0.562, but the previous
		// uncapped 2368s gap. This must not be reported as 0.6087 or 0.7470.
		fs := build(1, 1, 0, 5, m.I(400))
		st := trust.Step(trustCfg, []int{73}, fs, nil, map[int]float64{73: 0.562}, uncappedGap)[73]
		// 0.562 + 0.2*(2368/2700)*(7/30) = 0.602888... -> 0.6029
		if st.Sev != 0 || st.Trust != 0.6029 || len(st.Why) != 0 {
			t.Fatalf("uncapped contrast sev=%v value=%v why=%v want 0 / 0.6029 / empty", st.Sev, st.Trust, st.Why)
		}
	})

	t.Run("ample_clean_window_with_capped_gap_recovers_full_step_from_0_562", func(t *testing.T) {
		// Contrast: once n60>=30 the sample factor is 1, so a clean capped
		// window would add the full +0.2: 0.562+0.2=0.762. That is not the
		// live 0.6087, and it is also not CASE-AW-050's 0.7470 counterfactual
		// (prev=0.5716, uncapped 2368s, ample n).
		fs := build(1, 25, 0, 4, m.I(400))
		f := fs[73]
		if f.Short.N != 1 || f.Short.Err != 0 || f.Long.N != 30 || f.Long.Err != 0 {
			t.Fatalf("windows short=%+v long=%+v want 10m 1/0 and 1h 30/0", f.Short, f.Long)
		}
		st := trust.Step(trustCfg, []int{73}, fs, nil, map[int]float64{73: 0.562}, gap)[73]
		if st.Sev != 0 || st.Trust != 0.762 || len(st.Why) != 0 {
			t.Fatalf("ample contrast sev=%v value=%v why=%v want 0 / 0.762 / empty", st.Sev, st.Trust, st.Why)
		}
	})

	t.Run("settle_holds_16_to_20_with_chanstat_2_0_0", func(t *testing.T) {
		fs := build(1, 1, 0, 5, m.I(400))
		next := map[int]int{73: 16}
		var notes []string
		Settle(settleCfg, next, map[int]int{73: 20}, fs, []int{73}, nil, &notes)
		if next[73] != 20 {
			t.Fatalf("weight=%d want 20 after 16->20 hold", next[73])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#73 定档：策略 16→20", "无渠道级错误证据", "1h 真实 2", "渠道错误 0", "去集中 0", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected hold note to contain %q, got %q", part, notes)
			}
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault-gate cut, got %q", notes)
		}
	})

	t.Run("rank_promotes_eligible_leader_and_keeps_ineligible_high_score_at_close", func(t *testing.T) {
		// Live post-trust scores this round, after Multiplier floor=0.85:
		// 0.85+0.15*0.6087=0.941305, raw 0.792 -> 0.7455.
		// #73=0.7455 (n60=7, ineligible), #32=0.7045 (n60=8, ineligible),
		// #26=0.5383 (n60=190, eligible leader after promotion),
		// #23=0.4808 (ratio 0.8932), #2=0.3685 (ratio 0.6846 < 0.7).
		// Production mid_ratio=0.7, mid_w=3, close_ratio=0.85, close_factor=0.8,
		// low_w=0, lead_min_n=0. After "#73 样本不足，不得排第一，由 #26 顶上",
		// the live pre-settle targets are 26=20, 73/32/23=16, 2=0.
		rp := RankParams{P: m.M{
			"top_w": 20, "close_ratio": 0.85, "close_factor": 0.8,
			"mid_ratio": 0.7, "mid_w": 3, "low_w": 0, "tie_ratio": 0.95,
			"hysteresis": 0, "band": 0, "ramp": 0, "lead_trust": 0,
			"lead_min_n": 0, "co_lead_ratio": 0,
		}}
		old := map[int]int{26: 20, 73: 20, 32: 20, 23: 20, 2: 20}
		sys := m.M{"w_total": 18}
		order := []int{26, 73, 32, 23, 2}

		got := StratRank(order, map[int]float64{26: 0.5383, 73: 0.7455, 32: 0.7045, 23: 0.4808, 2: 0.3685}, rp, sys, old, nil)
		if got[26] != 20 || got[73] != 16 || got[32] != 16 || got[23] != 16 || got[2] != 0 {
			t.Fatalf("post-promotion rank weights=%v want 26=20, 73/32/23=16, 2=0", got)
		}

		// If eligibility did not promote #26, score order would give #73 top_w.
		// Ratios are then against 0.7455, so #32 stays close (0.9450), #26 is
		// only mid (0.7221), and #23/#2 fall below 0.7.
		rawOrder := []int{73, 32, 26, 23, 2}
		raw := StratRank(rawOrder, map[int]float64{26: 0.5383, 73: 0.7455, 32: 0.7045, 23: 0.4808, 2: 0.3685}, rp, sys, old, nil)
		if raw[73] != 20 || raw[32] != 16 || raw[26] != 3 || raw[23] != 0 || raw[2] != 0 {
			t.Fatalf("score-order rank weights=%v want 73=20, 32=16, 26=3, 23/2=0", raw)
		}
	})
}

// CASE-AW-052: sanitized aggregate shape from 2026-11-22 08:16 +08:00 CC #67.
// No production log rows, request identifiers, model names or real token IDs.
// Locks in existing v5.01 trust/settle semantics; it does not change the algorithm.
func TestCasebookResidualSub2pctChannelErrorsDoNotBlockAmpleUncappedMidRecovery(t *testing.T) {
	now := int64(100000)
	build := func(shortSuccess, longSuccess, longErr, probes int) m.Feats {
		t.Helper()
		var rows []m.Row
		for n := 0; n < shortSuccess; n++ {
			rows = append(rows, m.Row{Ch: 67, T: now - 30, Type: 2, Tok: 900003})
		}
		for n := 0; n < longSuccess; n++ {
			rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 900004})
		}
		for n := 0; n < longErr; n++ {
			// Residual channel-class faults (not client-class) sit in the
			// 60m window but outside the 10m window, matching the live 0/144.
			rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 5, Tok: 900005, Sc: m.I(500)})
		}
		for n := 0; n < probes; n++ {
			rows = append(rows, m.Row{Ch: 67, T: now - 40*60, Type: 2, Tok: 1318})
		}
		cfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
		return features.Build(cfg, []m.Channel{{ID: 67}}, rows, now)
	}

	settleCfg := m.M{"settle": m.M{"cut_needs_fault": true, "raise_clean": false}}
	// Live v5.01 trust keys. up=0.2 and up_full_n=30 are required so the
	// recovery increment matches production, not the code default 0.08.
	trustCfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{
		"enabled": true, "down": 0.6, "err_10m_min_n": 2, "err_full": 0.2,
		"err_1h_thr": 0.02, "err_10m_full_n": 0, "t_min": 0.1,
		"up": 0.2, "up_full_n": 30, "round_min": 45,
	}}
	// Inter-round gap is the difference of CC algorithm now values: 2376s.
	// 2376/60=39.6, 39.6/45=0.88, so the time factor is not capped.
	gap := m.F(2376.0 / 60.0)
	cappedGap := m.F(2700.0 / 60.0)

	t.Run("live_shape_2_of_269_below_2pct_recovers_0_4787_to_0_6547_uncapped_ample", func(t *testing.T) {
		// CC #67 this round: 10m 0/144, 60m 2/269=0.74%, mix 264 user + 5 probes
		// with 2 real errors, ChanStat N=264 Err=2 Client=0 Eff=2 Rate=0.0076.
		// After CASE-AW-051's clean 14/30 step to 0.4787, residual channel
		// errors remain but stay below err_1h_thr=2%.
		fs := build(144, 118, 2, 5)
		f := fs[67]
		if f == nil || f.Long == nil || f.Short == nil || f.Mix == nil || f.Chan == nil {
			t.Fatalf("missing features: %+v", f)
		}
		if f.Short.N != 144 || f.Short.Err != 0 {
			t.Fatalf("10m window n=%d err=%d want 144/0", f.Short.N, f.Short.Err)
		}
		if f.Long.N != 269 || f.Long.Err != 2 {
			t.Fatalf("1h window n=%d err=%d want 269/2", f.Long.N, f.Long.Err)
		}
		rate := m.Or(f.Long.ErrRate, 0)
		if rate <= 0 || rate >= 0.02 {
			t.Fatalf("1h err rate=%v want (0, 0.02)", rate)
		}
		if f.Mix.NUser != 264 || f.Mix.NProbe != 5 || f.Mix.NErrReal != 2 {
			t.Fatalf("mix=%+v want NUser=264 NProbe=5 NErrReal=2", f.Mix)
		}
		if f.Chan.N != 264 || f.Chan.Err != 2 || f.Chan.Client != 0 || f.Chan.Eff != 2 {
			t.Fatalf("chanstat=%+v want N=264 Err=2 Client=0 Eff=2", f.Chan)
		}
		if f.Chan.Rate <= 0 || f.Chan.Rate >= 0.03 {
			t.Fatalf("chanstat rate=%v want (0, 0.03)", f.Chan.Rate)
		}

		st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.4787}, gap)[67]
		if st == nil {
			t.Fatal("missing trust state")
		}
		// Uncapped time factor 0.88, ample sample factor 1:
		// 0.4787 + 0.2*0.88 = 0.6547.
		// The 2 residual scoring-window errors do not enter the down path,
		// because 2/269=0.0074 stays below err_1h_thr=2%.
		// This is not the capped-interval full step 0.4787+0.2=0.6787,
		// and it is not a cut merely because ChanStat still has Eff=2.
		if st.Sev != 0 || st.Trust != 0.6547 || len(st.Why) != 0 {
			t.Fatalf("trust sev=%v value=%v why=%v want 0 / 0.6547 / empty", st.Sev, st.Trust, st.Why)
		}
	})

	t.Run("same_n60_but_6_of_269_crossing_2pct_blocks_recovery", func(t *testing.T) {
		// Contrast: keep n60=269, clean 10m, the same 2376s gap and prev,
		// but raise residual errors to 6/269=2.23%>2%. Recovery must not
		// start merely because 10m is clean and ChanStat might still be
		// below the 30/3/3% settle gate.
		fs := build(144, 114, 6, 5)
		f := fs[67]
		if f.Short.N != 144 || f.Short.Err != 0 || f.Long.N != 269 || f.Long.Err != 6 {
			t.Fatalf("windows short=%+v long=%+v want 10m 144/0 and 1h 269/6", f.Short, f.Long)
		}
		if m.Or(f.Long.ErrRate, 0) <= 0.02 {
			t.Fatalf("1h err rate=%v should cross 2%%", m.Or(f.Long.ErrRate, 0))
		}
		st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.4787}, gap)[67]
		if st.Sev <= 0 || st.Trust >= 0.4787 || len(st.Why) == 0 {
			t.Fatalf("over-2pct contrast sev=%v value=%v why=%v want sev>0, trust<0.4787, non-empty why", st.Sev, st.Trust, st.Why)
		}
		if st.Trust == 0.6547 {
			t.Fatalf("over-2pct contrast must not recover to the live 0.6547")
		}
	})

	t.Run("same_2_of_269_with_capped_gap_recovers_full_step_to_0_6787", func(t *testing.T) {
		// Contrast: same residual 2/269 window and prev=0.4787, but a
		// capped 45-minute gap. That would add the full +0.2 because
		// n60>=30. The live 0.6547 must not be reported as 0.6787.
		fs := build(144, 118, 2, 5)
		st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.4787}, cappedGap)[67]
		if st.Sev != 0 || st.Trust != 0.6787 || len(st.Why) != 0 {
			t.Fatalf("capped contrast sev=%v value=%v why=%v want 0 / 0.6787 / empty", st.Sev, st.Trust, st.Why)
		}
	})

	t.Run("same_uncapped_gap_with_previous_n60_14_recovers_less", func(t *testing.T) {
		// Contrast: if the sample factor had stayed at last round's 14/30
		// instead of becoming ample, the same 2376s gap would only reach
		// 0.4787+0.2*0.88*(14/30)=0.560833... -> 0.5608.
		fs := build(9, 0, 0, 5)
		f := fs[67]
		if f.Long.N != 14 || f.Long.Err != 0 {
			t.Fatalf("1h window n=%d err=%d want 14/0", f.Long.N, f.Long.Err)
		}
		st := trust.Step(trustCfg, []int{67}, fs, nil, map[int]float64{67: 0.4787}, gap)[67]
		if st.Sev != 0 || st.Trust != 0.5608 || len(st.Why) != 0 {
			t.Fatalf("n60=14 contrast sev=%v value=%v why=%v want 0 / 0.5608 / empty", st.Sev, st.Trust, st.Why)
		}
	})

	t.Run("settle_holds_6_to_20_with_chanstat_264_2_2", func(t *testing.T) {
		fs := build(144, 118, 2, 5)
		next := map[int]int{67: 6}
		var notes []string
		Settle(settleCfg, next, map[int]int{67: 20}, fs, []int{67}, nil, &notes)
		if next[67] != 20 {
			t.Fatalf("weight=%d want 20 after 6->20 hold", next[67])
		}
		joined := strings.Join(notes, ";")
		for _, part := range []string{"#67 定档：策略 6→20", "无渠道级错误证据", "1h 真实 264", "渠道错误 2", "去集中 2", "延迟/小样本不单独降权"} {
			if !strings.Contains(joined, part) {
				t.Fatalf("expected hold note to contain %q, got %q", part, notes)
			}
		}
		if strings.Contains(joined, "达降权门槛") {
			t.Fatalf("did not expect a fault-gate cut, got %q", notes)
		}
	})
}
