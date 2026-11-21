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
