package features

import (
	"testing"

	m "autoweight/model"
)

// Port of tests/test_v312.py idea: session-block 403s on a sticky single-channel
// token only count in err_user when score.client_err.session_block is on.
func TestSessionBlockV312(t *testing.T) {
	now := int64(100000)
	rows := []m.Row{}
	for i := 0; i < 5; i++ {
		rows = append(rows, m.Row{Ch: 22, T: now - 60, Type: 2, Ut: 5, Ct: 300, Tok: 7})
	}
	for i := 0; i < 3; i++ {
		rows = append(rows, m.Row{Ch: 22, T: now - 120, Type: 5, Tok: 9, Sc: m.I(403), Sb: 1})
	}
	rows = append(rows, m.Row{Ch: 22, T: now - 120, Type: 5, Tok: 9, Sc: m.I(403)}) // no sb flag
	chans := []m.Channel{{ID: 22, Weight: 6}}
	run := func(sb bool) int {
		cfg := m.M{"windows_min": []any{60.0, 10.0}, "score": map[string]any{"client_err": map[string]any{"min_ch": 2.0, "session_block": sb}}}
		f := Build(cfg, chans, rows, now)[22]
		if f == nil || f.Long == nil || f.Long.ErrUser == nil {
			t.Fatalf("no err_user")
		}
		return *f.Long.ErrUser
	}
	if n := run(false); n != 0 {
		t.Errorf("off: err_user=%d want 0", n)
	}
	if n := run(true); n != 3 {
		t.Errorf("on: err_user=%d want 3", n)
	}
}

// Window counts, error rate, empty successes and the real/probe traffic mix.
func TestWindowAndMix(t *testing.T) {
	now := int64(100000)
	cfg := m.M{"windows_min": []any{60.0, 10.0}, "probe_token_ids": []any{1318.0}}
	rows := []m.Row{
		{Ch: 2, T: now - 30, Type: 2, Ut: 4, Ct: 300, Tok: 7},
		{Ch: 2, T: now - 40, Type: 2, Ut: 8, Ct: 300, Tok: 7},
		{Ch: 2, T: now - 900, Type: 2, Ut: 20, Ct: 0, Tok: 8}, // empty success, outside 10m
		{Ch: 2, T: now - 50, Type: 5, Tok: 8, Sc: m.I(502)},
		{Ch: 2, T: now - 60, Type: 2, Ut: 3, Ct: 50, Tok: 1318},  // probe token
		{Ch: 2, T: now - 7200, Type: 2, Ut: 99, Ct: 300, Tok: 7}, // outside 1h
	}
	f := Build(cfg, []m.Channel{{ID: 2, Weight: 5}}, rows, now)[2]
	if f.Long.N != 5 || f.Long.Err != 1 || f.Short.N != 4 {
		t.Errorf("counts 1h n=%d err=%d 10m n=%d", f.Long.N, f.Long.Err, f.Short.N)
	}
	if f.Long.Empty == nil || *f.Long.Empty != 1 {
		t.Errorf("empty %v", f.Long.Empty)
	}
	if f.Mix == nil || f.Mix.NUser != 4 || f.Mix.NProbe != 1 || f.Mix.NErrReal != 1 {
		t.Errorf("mix %+v", f.Mix)
	}
}

// prompt_tokens=0 empty successes (Ue) are client-side: skipped only when score.empty_skip_user is on.
func TestEmptySkipUser(t *testing.T) {
	now := int64(100000)
	rows := []m.Row{
		{Ch: 2, T: now - 30, Type: 2, Ut: 3, Ct: 0, Tok: 7, Ue: 1},
		{Ch: 2, T: now - 40, Type: 2, Ut: 3, Ct: 0, Tok: 7},
		{Ch: 2, T: now - 50, Type: 2, Ut: 3, Ct: 300, Tok: 7},
	}
	for on, want := range map[bool]int{false: 2, true: 1} {
		cfg := m.M{"windows_min": []any{60.0, 10.0}, "score": m.M{"empty_skip_user": on}}
		f := Build(cfg, []m.Channel{{ID: 2}}, rows, now)[2]
		if f.Long.Empty == nil || *f.Long.Empty != want {
			t.Errorf("skip=%v empty %v want %d", on, f.Long.Empty, want)
		}
	}
	if rows[0].Ue != 1 {
		t.Error("Build mutated caller rows")
	}
	hung := append(rows, m.Row{Ch: 2, T: now - 20, Type: 2, Ut: 177, Ct: 0, Tok: 7, Ue: 1})
	cfg := m.M{"windows_min": []any{60.0, 10.0}, "score": m.M{"empty_skip_user": true, "empty_skip_user_max_s": 60.0}}
	f := Build(cfg, []m.Channel{{ID: 2}}, hung, now)[2]
	if f.Long.Empty == nil || *f.Long.Empty != 2 {
		t.Errorf("hung pt=0 empty must count: %v", f.Long.Empty)
	}
}

func TestTpsModelNormalised(t *testing.T) {
	now := int64(10000)
	var rows []m.Row
	for k := 0; k < 10; k++ { // each channel runs a different model
		rows = append(rows, m.Row{Ch: 1, T: now - 60, Type: 2, Ut: 9, Ct: 950, Tok: 7, Mdl: "fast", St: 1})  // 100 tok/s
		rows = append(rows, m.Row{Ch: 2, T: now - 60, Type: 2, Ut: 19, Ct: 390, Tok: 8, Mdl: "slow", St: 1}) // 20 tok/s
		rows = append(rows, m.Row{Ch: 2, T: now - 60, Type: 2, Ut: 1, Ct: 900, Tok: 8, Mdl: "slow", St: 1})  // ut too small: skipped
		rows = append(rows, m.Row{Ch: 2, T: now - 60, Type: 2, Ut: 9, Ct: 20, Tok: 8, Mdl: "slow", St: 1})   // short output: skipped
	}
	fs := m.Feats{1: &m.Feat{}, 2: &m.Feat{}}
	AttachTps(m.M{}, fs, rows, now)
	if fs[1].Tps.P50 != 100 || fs[2].Tps.P50 != 20 || fs[2].Tps.N != 10 {
		t.Fatalf("raw tps %+v %+v", fs[1].Tps, fs[2].Tps)
	}
	if fs[1].Tps.Idx != 1 || fs[2].Tps.Idx != 1 { // different models, each at its model median
		t.Fatalf("idx %+v %+v", fs[1].Tps, fs[2].Tps)
	}
}

func TestDropProbeMismatch(t *testing.T) {
	cfg := m.M{"probe_token_ids": []any{1318.0}, "score": map[string]any{"drop_probe_mismatch": map[string]any{"enabled": true}}}
	rows := []m.Row{{Tok: 1318, Type: 5, Mm: 1}, {Tok: 7, Type: 5, Mm: 1}, {Tok: 1318, Type: 5}, {Tok: 1318, Type: 2}}
	if got := dropProbeMismatch(cfg, rows); len(got) != 3 || got[0].Tok != 7 {
		t.Fatalf("want probe mismatch dropped, real kept: %+v", got)
	}
	cfg["score"] = map[string]any{}
	if got := dropProbeMismatch(cfg, rows); len(got) != 4 {
		t.Fatalf("disabled must keep all: %d", len(got))
	}
}

// ChanStats: probe tokens, client 4xx and cross-channel "do request failed" are not
// channel errors; a dominant token's non-capacity errors count at most tok_cap.
func TestChanStats(t *testing.T) {
	cfg := m.M{"probe_token_ids": []any{1318.0}}
	sc := func(c int) *int { return &c }
	rows := []m.Row{
		{Ch: 2, T: 100, Type: 2, Tok: 1}, {Ch: 2, T: 100, Type: 2, Tok: 2},
		{Ch: 2, T: 101, Type: 5, Tok: 1318, Sc: sc(502)},      // probe
		{Ch: 2, T: 102, Type: 5, Tok: 3, Sc: sc(413)},         // client
		{Ch: 2, T: 103, Type: 5, Tok: 4, Sc: sc(500), Df: 1},  // cross-channel df
		{Ch: 26, T: 110, Type: 5, Tok: 4, Sc: sc(500), Df: 1}, // (same token, other channel)
		{Ch: 2, T: 104, Type: 5, Tok: 5, Sc: sc(502)}, {Ch: 2, T: 105, Type: 5, Tok: 5, Sc: sc(502)},
		{Ch: 2, T: 106, Type: 5, Tok: 5, Sc: sc(502)}, {Ch: 2, T: 107, Type: 5, Tok: 6, Sc: sc(429)},
	}
	s := ChanStats(cfg, rows, 0, 2, 0.8)[2]
	if s.N != 6 || s.Err != 4 || s.Client != 2 || s.Eff != 3 || s.TopTok != 5 || s.ErrTok != 2 {
		t.Fatalf("got %+v", *s)
	}
	// errors spread over tokens (a real outage) are not discounted
	rows = append(rows, m.Row{Ch: 2, T: 108, Type: 5, Tok: 7, Sc: sc(502)}, m.Row{Ch: 2, T: 109, Type: 5, Tok: 8, Sc: sc(502)})
	if s = ChanStats(cfg, rows, 0, 2, 0.8)[2]; s.Eff != s.Err {
		t.Fatalf("spread errors discounted: %+v", *s)
	}
}
