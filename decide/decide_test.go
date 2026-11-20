package decide

import (
	"math"
	"strings"
	"testing"

	m "autoweight/model"
)

var ocfg = m.M{"w_min": 0.0, "w_max": 20.0}
var ochans = []m.Channel{{ID: 2}, {ID: 5}, {ID: 22}}

func TestParseOverrideFinal(t *testing.T) {
	cases := []struct {
		spec, err string
		final     bool
		want      map[int]int
	}{
		{spec: "5=3,#2=12", want: map[int]int{5: 3, 2: 12}},
		{spec: "9=3", err: "不在本轮"},
		{spec: "5=21", err: "超出"},
		{spec: "5", err: "bad override"},
		{spec: "2=1,5=2", final: true, err: "缺少 [22]"},
		{spec: "2=1,5=2,22=0", final: true, want: map[int]int{2: 1, 5: 2, 22: 0}},
	}
	for _, c := range cases {
		f := ParseOverride
		if c.final {
			f = ParseFinal
		}
		got, err := f(c.spec, ochans, ocfg)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: want %q got %v", c.spec, c.err, err)
			}
			continue
		}
		if err != nil || len(got) != len(c.want) {
			t.Errorf("%s: %v %v", c.spec, got, err)
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("%s: %v", c.spec, got)
			}
		}
	}
}

// Drift between dry-run and -apply; v3.18 tolerates small non-zero jitter on
// follow channels when -final is given.
func TestBlockingDrift(t *testing.T) {
	d := SystemDrift(map[int]int{2: 3, 5: 6, 22: 1, 20: 10, 21: 2}, map[int]int{2: 3, 5: 4, 20: 12, 21: 0})
	fin := map[int]int{2: 3, 5: 9, 22: 1, 20: 10, 21: 2}
	ids := func(x Drift) []int { return m.SortedIDs(x) }
	for _, c := range []struct {
		name  string
		final map[int]int
		tol   int
		want  []int
	}{
		{"no final blocks all", nil, 2, []int{5, 20, 21, 22}},
		{"manual channel 5 does not block", fin, 0, []int{20, 21, 22}},
		{"v3.18 10->12 jitter ok, crossing 0 blocks", fin, 2, []int{21, 22}},
	} {
		if got := ids(BlockingDrift(d, c.final, c.tol)); !equal(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNewcomerIDs(t *testing.T) {
	rec := func(ts string, applied bool, ids ...int) *m.Rec {
		r := &m.Rec{Ts: ts, Applied: applied, Features: m.Feats{}}
		for _, i := range ids {
			r.Channels = append(r.Channels, m.Channel{ID: i})
		}
		return r
	}
	rs := []*m.Rec{rec("2026-10-29T01:00:00+08:00", true, 2, 5), rec("2026-10-29T02:00:00+08:00", false, 2, 5, 22),
		rec("2026-10-29T03:00:00+08:00", true, 2, 5, 22)}
	got := NewcomerIDs(m.M{"newcomer": m.M{"enabled": true, "min_rounds": 2.0}}, ochans, rs, "")
	if !got[22] || got[2] || got[5] {
		t.Fatalf("%v", got)
	}
	got = NewcomerIDs(m.M{"newcomer": m.M{"enabled": true, "min_rounds": 1.0, "max_age_h": 2.0}}, ochans, rs, "2026-10-29T03:30:00+08:00")
	if !got[22] || got[2] {
		t.Fatalf("age: %v", got)
	}
}

// Thin-sample scoring helpers: log-TTFT pool (v2.86) and empty-rate prior (v2.82).
func TestThinSampleScoring(t *testing.T) {
	cfg := func(k float64) m.M {
		return m.M{"windows_min": []any{60.0, 10.0}, "score": map[string]any{
			"log_ttft": map[string]any{"enabled": true, "min_n": 10.0, "thin_pool_k": k}}}
	}
	fs := m.Feats{1: {Long: &m.Window{LtN: 20, Lt50: m.F(6)}}, 2: {Long: &m.Window{LtN: 30, Lt50: m.F(10)}},
		3: {Long: &m.Window{LtN: 3, Lt50: m.F(1)}}}
	if p := LtPool(cfg(20), fs, []int{1, 2, 3}); p == nil || *p != 8 {
		t.Errorf("pool %v", p)
	}
	if LtPool(cfg(0), fs, []int{1, 2, 3}) != nil || LtPool(cfg(20), fs, []int{1, 3}) != nil {
		t.Error("off / single rich channel must give nil")
	}
	pool := m.F(8)
	for _, c := range []struct {
		name string
		k    float64
		pool *float64
		w    *m.Window
		want float64
	}{
		{"no log samples -> pool", 20, pool, &m.Window{}, 8},
		{"5 samples shrink", 20, pool, &m.Window{LtN: 5, Lt50: m.F(3)}, (5*2.0 + 20*8.0) / 25},
		{"rich unaffected", 20, pool, &m.Window{LtN: 40, Lt50: m.F(4)}, 3},
		{"off is identity", 0, pool, &m.Window{}, 2},
	} {
		if got := BlendLogTtft(2, m.F(2), c.w, cfg(c.k).Sub("score"), nil, c.pool); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	w := &m.Window{N: 50, Empty: m.I(0), EmptyRate: m.F(0)}
	if got := EmptyRateEff(w, m.M{"empty_prior_k": 50.0}, Pair{Empty: m.F(0.1)}); math.Abs(got-0.05) > 1e-12 {
		t.Errorf("empty prior %v", got)
	}
}

// Settle: the three hand rules of 11-12 (token-concentrated errors, sample gate, +-2 step).
func TestSettle(t *testing.T) {
	cs := func(n, err, eff int) *m.Feat {
		r := 0.0
		if d := n - err + eff; d > 0 {
			r = float64(eff) / float64(d)
		}
		return &m.Feat{Long: &m.Window{N: n, ErrRate: m.F(0)}, Chan: &m.ChanStat{N: n, Err: err, Eff: eff, Rate: r}}
	}
	prev := map[int]int{2: 18, 23: 20, 67: 9, 68: 2, 6: 2, 9: 10, 5: 6, 18: 14, 70: 15}
	nw := map[int]int{2: 16, 23: 18, 67: 20, 68: 6, 6: 0, 9: 0, 5: 20, 18: 0, 70: 15}
	fs := m.Feats{
		2:  cs(160, 0, 0),   // clean, latency-only cut -> hold
		23: cs(345, 7, 2),   // 7 errors all from one token -> hold
		67: cs(35, 0, 0),    // clean, enough real -> +2 only
		68: cs(20, 0, 0),    // <30 real -> no raise
		6:  cs(100, 76, 8),  // spread real errors -> extreme 0
		9:  cs(200, 12, 12), // 6% real errors -> -2
		5:  cs(100, 0, 0),   // operator cap 0 wins
		18: cs(10, 0, 0),    // small sample alone never cuts
		70: cs(91, 13, 13),  // 14% spread errors but strategy held -> still -2
	}
	caps := map[int]*Cap{5: {Cap: 0, Reason: "下线"}}
	Settle(m.M{}, nw, prev, fs, []int{2, 23, 67, 68, 6, 9, 5, 18, 70}, caps, &[]string{})
	want := map[int]int{2: 18, 23: 20, 67: 11, 68: 2, 6: 0, 9: 8, 5: 0, 18: 14, 70: 13}
	for id, w := range want {
		if nw[id] != w {
			t.Errorf("#%d got %d want %d", id, nw[id], w)
		}
	}
}
