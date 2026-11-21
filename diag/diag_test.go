package diag

import (
	"strings"
	"testing"
	"time"

	m "autoweight/model"
)

func rows(ch, tok, n, empty int) []m.Row {
	out := []m.Row{}
	for i := 0; i < n; i++ {
		ct := 100
		if i < empty {
			ct = 0
		}
		out = append(out, m.Row{Ch: ch, Tok: tok, Type: 2, Ct: ct})
	}
	return out
}

// Same tokens empty only on #20 -> channel; empties that follow a token everywhere -> user.
func TestEmptyPairVerdicts(t *testing.T) {
	var rs []m.Row
	rs = append(rs, rows(20, 1, 20, 10)...)
	rs = append(rs, rows(2, 1, 20, 0)...)
	rs = append(rs, rows(5, 2, 10, 5)...)
	rs = append(rs, rows(22, 2, 10, 5)...)
	rs = append(rs, rows(23, 3, 10, 1)...) // single-channel token -> no pair
	got := map[int]string{}
	for _, p := range AnalyzeEmptyPair(rs, DefaultEmptyPair) {
		got[p.Ch] = p.Verdict
	}
	want := map[int]string{20: "channel", 2: "cleaner", 5: "user", 22: "user", 23: "no_pair"}
	for c, v := range want {
		if got[c] != v {
			t.Errorf("#%d verdict %q want %q", c, got[c], v)
		}
	}
}

func lat(ch, tok, n, ut int) []m.Row {
	out := []m.Row{}
	for i := 0; i < n; i++ {
		out = append(out, m.Row{Ch: ch, Tok: tok, Type: 2, Ct: 1000, Ut: ut})
	}
	return out
}

// Same tokens consistently faster on #22 than #23; a channel seen by one token only is no_pair.
func TestTokenPairVerdicts(t *testing.T) {
	var rs []m.Row
	for tok := 1; tok <= 3; tok++ {
		rs = append(rs, lat(22, tok, 4, 5)...)
		rs = append(rs, lat(23, tok, 4, 20)...)
	}
	rs = append(rs, lat(26, 9, 10, 1)...)
	got := map[int]string{}
	for _, p := range AnalyzeTokenPair(rs, DefaultTokenPair) {
		got[p.Ch] = p.Verdict
	}
	want := map[int]string{22: "faster", 23: "slower", 26: "no_pair"}
	for c, v := range want {
		if got[c] != v {
			t.Errorf("#%d verdict %q want %q", c, got[c], v)
		}
	}
	ps := AnalyzeTokenPair(rs, DefaultTokenPair)
	if f := TokenPairFlags(ps, map[int]int{26: 12, 22: 3}, 30); len(f) != 1 {
		t.Errorf("thin no_pair top should flag, got %v", f)
	}
	if f := TokenPairFlags(ps, map[int]int{23: 12, 22: 3}, 0); len(f) != 1 {
		t.Errorf("paired-slower top should flag regardless of n, got %v", f)
	}
	if f := TokenPairFlags(ps, map[int]int{22: 12, 23: 3}, 0); len(f) != 0 {
		t.Errorf("paired-faster top must not flag, got %v", f)
	}
}

// Unpaired lead dominated by one token flags; unpaired high-empty channel flags.
func TestConcentratedAndUnpairedEmptyFlags(t *testing.T) {
	var rs []m.Row
	rs = append(rs, lat(20, 7, 40, 2)...)
	rs = append(rs, lat(20, 8, 5, 2)...)
	ps := AnalyzeTokenPair(rs, DefaultTokenPair)
	if f := TokenPairFlags(ps, map[int]int{20: 12}, 30); len(f) != 1 {
		t.Errorf("concentrated unpaired top should flag, got %v", f)
	}
	var es []m.Row
	for i := 0; i < 12; i++ {
		ct := 100
		if i < 5 {
			ct = 0
		}
		es = append(es, m.Row{Ch: 21, Tok: 3, Type: 2, Ct: ct, Ut: 5})
	}
	if f := EmptyPairFlags(AnalyzeEmptyPair(es, DefaultEmptyPair), 10, 0.25); len(f) != 1 {
		t.Errorf("unpaired 5/12 empty should flag, got %v", f)
	}
	for i := 0; i < 5; i++ {
		es[i].Ue = 1 // prompt_tokens=0: client-side empty request, not the channel's
	}
	if f := EmptyPairFlags(AnalyzeEmptyPair(es, DefaultEmptyPair), 5, 0.25); len(f) != 0 {
		t.Errorf("client-side empties must not flag, got %v", f)
	}
}

func TestThinCapFlags(t *testing.T) {
	rs := append(rows(26, 1, 10, 0), rows(23, 2, 40, 0)...)
	for i := range rs {
		rs[i].T = 100
	}
	rs = append(rs, m.Row{Ch: 26, T: 10, Type: 2, Ct: 1}) // outside window
	got := ThinCapFlags(rs, map[int]int{26: 12, 23: 10, 5: 3}, 50, 30, 6, nil, 60, 24)
	if len(got) != 1 || got[0][:14] != "THINCAPGAP #26" {
		t.Fatalf("got %v", got)
	}
	lull := ThinCapFlags(rs, map[int]int{26: 12}, 50, 30, 6, map[int]int{26: 95}, 60, 24)
	if len(lull) != 1 || !strings.Contains(lull[0], "低峰期") {
		t.Fatalf("established channel in lull must be tagged, got %v", lull)
	}
}

func TestMainZeroFlags(t *testing.T) {
	rs := append(rows(2, 1, 250, 0), rows(20, 2, 250, 0)...)
	for i := range rs {
		rs[i].T = 100
		if rs[i].Ch == 20 && i%3 == 0 {
			rs[i].Type = 5
		}
	}
	got := MainZeroFlags(rs, map[int]int{2: 0, 20: 0, 26: 12}, 50, 200, 0.02)
	if len(got) != 1 || got[0][:11] != "MAINZERO #2" {
		t.Fatalf("got %v", got)
	}
}

func TestRecoveredFlags(t *testing.T) {
	now := int64(10000)
	rows := []m.Row{}
	for i := 0; i < 10; i++ {
		rows = append(rows, m.Row{Ch: 20, T: now - 3000, Type: 5}, m.Row{Ch: 2, T: now - 3000, Type: 5})
	}
	for i := 0; i < 6; i++ {
		rows = append(rows, m.Row{Ch: 20, T: now - 300, Type: 2}, m.Row{Ch: 2, T: now - 300, Type: 2})
	}
	rows = append(rows, m.Row{Ch: 2, T: now - 100, Type: 5})
	got := RecoveredFlags(rows, map[int]int{20: 0, 2: 0}, now-3600, now, 15, 0.2, 5)
	if len(got) != 1 || got[0][:13] != "RECOVERED #20" {
		t.Fatalf("got %v", got)
	}
}

func TestLongTailFlags(t *testing.T) {
	var rs []m.Row
	for i := 0; i < 50; i++ {
		u := 10
		if i >= 40 {
			u = 60
		}
		rs = append(rs, m.Row{Ch: 22, Type: 2, T: 100, Ut: u}, m.Row{Ch: 2, Type: 2, T: 100, Ut: 8})
	}
	got := LongTailFlags(rs, map[int]int{22: 6, 2: 10}, 50, 50, 0, 3.5, 30)
	if len(got) != 1 || got[0][:12] != "LONGTAIL #22" {
		t.Fatalf("got %v", got)
	}
}

func TestContentSafeFlags(t *testing.T) {
	rows := []m.Row{
		{Ch: 23, T: 100, Type: 5, Tok: 7, Cs: 1},
		{Ch: 23, T: 110, Type: 5, Tok: 7, Cs: 1},
		{Ch: 23, T: 120, Type: 5, Tok: 8, Cs: 1},
		{Ch: 23, T: 130, Type: 5, Tok: 9},
		{Ch: 2, T: 140, Type: 2, Tok: 7},
		{Ch: 2, T: 10, Type: 5, Tok: 1, Cs: 1}, // before window
	}
	got := ContentSafeFlags(rows, map[int]int{23: 3, 2: 0}, 50, 3)
	if len(got) != 1 || !strings.Contains(got[0], "#23: 1h 真实错误 4 条中 3 条") || !strings.Contains(got[0], "2 个令牌，其中 1 个") {
		t.Fatalf("got %v", got)
	}
}

func TestUnsupportedFlags(t *testing.T) {
	rows := []m.Row{
		{Ch: 26, T: 100, Type: 5, Tok: 49, Uf: 1},
		{Ch: 26, T: 110, Type: 5, Tok: 50},
		{Ch: 2, T: 120, Type: 2, Tok: 49},
		{Ch: 23, T: 130, Type: 5, Tok: 9}, // plain error, no flag
	}
	got := UnsupportedFlags(rows, map[int]int{26: 0}, 50, 1)
	if len(got) != 1 || !strings.Contains(got[0], "UNSUPPORTED #26: 1h 真实错误 2 条中 1 条") || !strings.Contains(got[0], "1 个令牌，其中 1 个") {
		t.Fatalf("got %v", got)
	}
}

func TestStickyFlags(t *testing.T) {
	rows := []m.Row{}
	add := func(ch, k, aff int) {
		for i := 0; i < k; i++ {
			x := m.Row{T: 100, Ch: ch, Type: 2}
			if i < aff {
				x.Aff = 1
			}
			rows = append(rows, x)
		}
	}
	add(2, 80, 70)
	add(20, 80, 78)
	add(23, 5, 0)
	rows = append(rows, m.Row{T: 1, Ch: 20, Type: 2, Aff: 1}) // before window
	st := AnalyzeSticky(rows, map[int]int{2: 10, 20: 2, 23: 6}, 50)
	got := StickyFlags(st, 30, 0.7, 2)
	if len(got) != 1 || !strings.Contains(got[0], "STICKY #20: 1h 真实 80 条中 78 条") {
		t.Fatalf("got %v", got)
	}
}

func TestFreshFlags(t *testing.T) {
	st := []Sticky{{Ch: 2, N: 0, WeightShare: 0.33}, {Ch: 20, N: 63, Aff: 63}, {Ch: 22, N: 52, Aff: 50, WeightShare: 0.1}}
	got := FreshFlags(st, 10)
	if len(got) != 1 || !strings.Contains(got[0], "115 条里只有 2 条") || !strings.Contains(got[0], "#22=2") {
		t.Fatalf("got %v", got)
	}
	if FreshFlags([]Sticky{{Ch: 2, N: 40}}, 10) != nil {
		t.Fatal("enough fresh traffic should not flag")
	}
}

func TestProbeSlowFlags(t *testing.T) {
	var rows []m.Row
	for i := 0; i < 6; i++ {
		rows = append(rows, m.Row{Ch: 2, T: 100, Type: 2, Ut: 3, Tok: 1318},
			m.Row{Ch: 5, T: 100, Type: 2, Ut: 4, Tok: 1318},
			m.Row{Ch: 23, T: 100, Type: 2, Ut: 3, Tok: 1318},
			m.Row{Ch: 26, T: 100, Type: 2, Ut: 20, Tok: 1318},
			m.Row{Ch: 22, T: 100, Type: 2, Ut: 90, Tok: 15}) // real token ignored
	}
	f := ProbeSlowFlags(rows, 1318, map[int]int{26: 3}, 0, 5, 2.5)
	if len(f) != 1 || !strings.Contains(f[0], "#26") {
		t.Fatalf("want only #26 flagged, got %v", f)
	}
}

func TestProbeTpsFlags(t *testing.T) {
	var rows []m.Row
	for i := 0; i < 6; i++ {
		rows = append(rows, m.Row{Ch: 2, T: 100, Type: 2, Ut: 4, Ct: 200, Tok: 1318, Mdl: "gpt-x"},
			m.Row{Ch: 5, T: 100, Type: 2, Ut: 2, Ct: 100, Tok: 1318, Mdl: "gpt-x"},
			m.Row{Ch: 20, T: 100, Type: 2, Ut: 8, Ct: 400, Tok: 1318, Mdl: "gpt-x"}, // slow total but same speed
			m.Row{Ch: 26, T: 100, Type: 2, Ut: 10, Ct: 100, Tok: 1318, Mdl: "gpt-x"})
	}
	f := ProbeTpsFlags(rows, 1318, map[int]int{26: 3}, 0, 5, 2.5)
	if len(f) != 2 || !strings.Contains(f[0], "#26") || strings.Contains(f[0], "#20") {
		t.Fatalf("want only #26 flagged plus ranking, got %v", f)
	}
}

func TestNoRealFlags(t *testing.T) {
	st := []Sticky{{Ch: 2}, {Ch: 22}}
	f := NoRealFlags(st, map[int]int{2: 12, 22: 10}, map[int]int{2: 10, 22: 2}, 2)
	if len(f) != 1 || !strings.Contains(f[0], "#22 2→10") || strings.Contains(f[0], "#2 10") {
		t.Fatalf("got %v", f)
	}
	if NoRealFlags([]Sticky{{Ch: 2, N: 1}}, map[int]int{2: 12}, map[int]int{2: 0}, 2) != nil {
		t.Fatal("real traffic present should not flag")
	}
}

func TestEmptyStreamFlags(t *testing.T) {
	st, z, tot := 200, 0, 48.0
	r := &m.Rec{TsTime: time.Unix(1000, 0)}
	r.Probes = m.Probes{26: {{OK: false, Status: &st, Deltas: &z, Total: &tot}, {OK: true, Status: &st}}, 2: {{OK: true, Status: &st}}}
	got := EmptyStreamFlags([]*m.Rec{r}, map[int]int{2: 1, 26: 1}, 1000+600, 120)
	if len(got) != 1 || !strings.Contains(got[0], "#26") || !strings.Contains(got[0], "1/2") {
		t.Fatalf("got %v", got)
	}
}

func TestSuggestFlipFlags(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	mk := func(min int, w int) *m.Rec {
		r := &m.Rec{TsTime: base.Add(time.Duration(min) * time.Minute)}
		r.Decision.Target = map[int]int{2: w, 5: 6}
		return r
	}
	sh := []*m.Rec{mk(0, 12), mk(5, 0), mk(10, 3), mk(15, 12), mk(20, 0)}
	now := m.Epoch(base.Add(25 * time.Minute))
	got := SuggestFlipFlags(sh, map[int]int{2: 0, 5: 6}, now, 60, 3, 10, 2)
	if len(got) != 1 || !strings.Contains(got[0], "#2") || !strings.Contains(got[0], "3 次") {
		t.Fatalf("got %v", got)
	}
}

func TestTokenBanFlags(t *testing.T) {
	rows := []m.Row{}
	for i := 0; i < 6; i++ {
		rows = append(rows, m.Row{Ch: 20, T: 100, Type: 5, Tok: 1028})
		rows = append(rows, m.Row{Ch: 5, T: 100, Type: 5, Tok: 1}) // no other-token success: outage-like
	}
	rows = append(rows, m.Row{Ch: 20, T: 100, Type: 5, Tok: 9}, m.Row{Ch: 20, T: 100, Type: 2, Tok: 9}, m.Row{Ch: 20, T: 100, Type: 2, Tok: 31})
	got := TokenBanFlags(rows, map[int]int{20: 2}, 50, 5, 0.8)
	if len(got) != 1 || !strings.Contains(got[0], "TOKENBAN #20: 1h 真实错误 7 条中 6 条来自令牌 1028") || !strings.Contains(got[0], "其他 2 个令牌同期在该渠道成功 2 条") {
		t.Fatalf("got %v", got)
	}
}

func TestSlowDriftFlags(t *testing.T) {
	var rs []m.Row
	for i := 0; i < 20; i++ {
		rs = append(rs, m.Row{Ch: 2, Type: 2, T: 5000, Ut: 2}, m.Row{Ch: 2, Type: 2, T: 9000, Ut: 13},
			m.Row{Ch: 22, Type: 2, T: 5000, Ut: 10}, m.Row{Ch: 22, Type: 2, T: 9000, Ut: 11})
	}
	got := SlowDriftFlags(rs, map[int]int{2: 10, 22: 6}, 10000, 20, 2.5, 8)
	if len(got) != 1 || got[0][:12] != "SLOWDRIFT #2" {
		t.Fatalf("got %v", got)
	}
}

func TestHangTailFlags(t *testing.T) {
	rs := []m.Row{}
	for i := 0; i < 16; i++ {
		ut := 5
		if i < 3 {
			ut = 300
		}
		rs = append(rs, m.Row{Ch: 20, T: 9000, Type: 2, Ut: ut, Tok: 7})
		rs = append(rs, m.Row{Ch: 2, T: 9000, Type: 2, Ut: 5, Tok: 7})
	}
	rs = append(rs, m.Row{Ch: 2, T: 9000, Type: 5, Ut: 400, Tok: 1318})
	got := HangTailFlags(rs, map[int]int{20: 0}, 10000, 180, 2, 0.05, 1318)
	if len(got) != 1 || !strings.Contains(got[0], "#20") || !strings.Contains(got[0], "3/16") {
		t.Fatalf("got %v", got)
	}
}

// CASE-AW-029 addendum: sanitized 8/127 hang-tail shape from 2026-11-21 13:30
// +08:00 CC #72. Locks the diagnostic wording and that the flag is record-only
// (the supplied target weight of 20 is echoed, not changed). Defaults match
// core/hangflags.go: hang_sec=180, min_hang=2, min_share=0.05, probe_tok=1318.
func TestHangTailFlagsAmpleSlowWindowStillRecordsOnly(t *testing.T) {
	now := int64(100000)
	rows := make([]m.Row, 0, 127)
	for i := 0; i < 119; i++ {
		rows = append(rows, m.Row{Ch: 72, T: now - 30, Type: 2, Ut: 15, Tok: 900003})
	}
	for i := 0; i < 7; i++ {
		rows = append(rows, m.Row{Ch: 72, T: now - 30, Type: 2, Ut: 180, Tok: 900003})
	}
	rows = append(rows, m.Row{Ch: 72, T: now - 30, Type: 2, Ut: 629, Tok: 900003})
	got := HangTailFlags(rows, map[int]int{72: 20}, now, 180, 2, 0.05, 1318)
	want := "HANGTAIL #72: 1h 内 8/127 条（6%）总耗时 ≥180s（其中探测 0 条），最长 629s，系统给 20；只记录"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v want %q", got, want)
	}
}

func TestLoadQueueFlags(t *testing.T) {
	rows := []m.Row{}
	// 12 lone fast successes on ch 9
	for i := 0; i < 12; i++ {
		rows = append(rows, m.Row{Ch: 9, Type: 2, T: int64(1000 + i*100), Ut: 4})
	}
	// burst of 16 overlapping slow successes on ch 9
	for i := 0; i < 16; i++ {
		rows = append(rows, m.Row{Ch: 9, Type: 2, T: int64(3000 + i), Ut: 30})
	}
	out := LoadQueueFlags(rows, map[int]int{9: 6}, 0, 5, 10, 2)
	if len(out) != 1 || !strings.Contains(out[0], "LOADQUEUE #9") {
		t.Fatalf("want LOADQUEUE #9, got %v", out)
	}
	if len(LoadQueueFlags(rows[:12], nil, 0, 5, 10, 2)) != 0 {
		t.Fatal("no burst should not flag")
	}
}

func TestCrossEncFlags(t *testing.T) {
	rows := []m.Row{
		{Ch: 26, T: 100, Type: 2, Tok: 31},
		{Ch: 2, T: 150, Type: 5, Tok: 31, Ec: 1}, // switched from #26
		{Ch: 2, T: 160, Type: 5, Tok: 40, Ec: 1}, // no prior success
		{Ch: 5, T: 170, Type: 5, Tok: 41},        // plain error
	}
	got := CrossEncFlags(rows, map[int]int{2: 10}, 50, 1800)
	if len(got) != 1 || !strings.Contains(got[0], "CROSSENC #2: 真实 400 加密内容错误 2 条，其中 1 条") || !strings.Contains(got[0], "#26×1") {
		t.Fatalf("got %v", got)
	}
}

func TestCrossEncNoSwitchCause(t *testing.T) {
	rows := []m.Row{
		{Ch: 2, T: 100, Type: 2, Tok: 7},
		{Ch: 2, T: 400, Type: 5, Tok: 7, Ec: 1}, // same channel
	}
	got := CrossEncFlags(rows, map[int]int{2: 12}, 50, 1800)
	if len(got) != 1 || !strings.Contains(got[0], "上一次成功就在本渠道") {
		t.Fatalf("got %v", got)
	}
}
