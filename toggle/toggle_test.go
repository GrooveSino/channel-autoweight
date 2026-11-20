package toggle

import (
	"encoding/json"
	"reflect"
	"testing"

	m "autoweight/model"
)

func mustJSON[T any](t *testing.T, s string) T {
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func stream(tt, o float64) []m.Probe {
	return []m.Probe{{OK: true, Ttft: m.F(tt), Total: m.F(o), Streaming: m.B(true)}}
}

// Re-enable baseline exclusions: buffered, dormant (v2.80, v3.32 w_max),
// nodata (v3.09).
func TestIsDormantOrBuffered(t *testing.T) {
	buf := []m.Probe{{OK: true, Ttft: m.F(9), Total: m.F(9), Streaming: m.B(false)}}
	cases := []struct {
		name      string
		ps        []m.Probe
		oldW, n60 *int
		nodata    bool
		wMax      int
		want      string
	}{
		{"buffered probes", buf, m.I(10), m.I(300), false, 0, "buffered"},
		{"weight 0, thin", nil, m.I(0), m.I(8), false, 0, "dormant"},
		{"weight 0, busy", nil, m.I(0), m.I(40), false, 0, ""},
		{"unknown weight", nil, nil, nil, false, 0, ""},
		{"v3.32 weight 1 counts with w_max=1", nil, m.I(1), m.I(0), false, 1, "dormant"},
		{"v3.32 weight 1 not with w_max=0", stream(3, 8), m.I(1), m.I(0), false, 0, ""},
		{"v3.32 weight 2 never", stream(3, 8), m.I(2), m.I(0), false, 1, ""},
		{"v3.32 weight 1 busy", nil, m.I(1), m.I(30), false, 1, ""},
		{"v3.09 nodata", []m.Probe{{OK: false}}, m.I(2), m.I(3), true, 0, "nodata"},
		{"v3.09 nodata off", []m.Probe{{OK: false}}, m.I(2), m.I(3), false, 0, ""},
		{"v3.09 probed ok", stream(4, 10), m.I(6), m.I(14), true, 0, ""},
	}
	for _, c := range cases {
		if got := IsDormantOrBuffered(c.ps, c.oldW, c.n60, 20, c.nodata, c.wMax); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestBottomForBase(t *testing.T) {
	probes := mustJSON[m.Probes](t, `{"2":[{"ok":true,"ttft":6.9,"total":9.1,"streaming":true}],
"5":[{"ok":true,"ttft":2.8,"total":4.7,"streaming":true}],"20":[{"ok":true,"ttft":9.5,"total":9.6,"streaming":false}],
"22":[{"ok":true,"ttft":6.4,"total":10.2,"streaming":true}],"23":[{"ok":true,"ttft":3.0,"total":9.5,"streaming":true}],
"26":[{"ok":true,"ttft":21.6,"total":21.6,"streaming":false}]}`)
	old := map[int]int{2: 8, 5: 6, 20: 3, 22: 10, 23: 12, 26: 0}
	n60 := map[int]*int{2: m.I(300), 5: m.I(36), 20: m.I(75), 22: m.I(106), 23: m.I(430), 26: m.I(8)}
	all := `{"2":0.36,"5":0.61,"20":0.33,"22":0.41,"23":0.42,"26":0.24}`
	// v3.32 shape (12:57): #23 weight 1 with 0 real requests ranks last.
	p332 := m.Probes{}
	for _, i := range []int{2, 5, 20, 21, 22, 23} {
		p332[i] = stream(3, 8)
	}
	old332 := map[int]int{2: 7, 5: 3, 20: 10, 23: 1, 22: 6, 21: 4}
	n332 := map[int]*int{2: m.I(90), 5: m.I(400), 20: m.I(80), 23: m.I(0), 22: m.I(16), 21: m.I(40)}
	s332 := `{"2":0.3,"5":0.4,"20":0.5,"23":0.1,"22":0.6,"21":0.7}`
	cases := []struct {
		name, scores string
		probes       m.Probes
		old          map[int]int
		n60          map[int]*int
		p            m.M
		bot, sk      []int
	}{
		{"off keeps old behaviour", all, probes, old, n60, m.M{"skip_broken_base": true}, []int{26, 20}, []int{}},
		{"excludes buffered", all, probes, old, n60, m.M{"skip_broken_base": true, "skip_dormant_base": true}, []int{2, 22}, []int{20, 26}},
		{"all lax falls back", `{"20":0.3,"26":0.2}`, probes, old, n60, m.M{"skip_dormant_base": true}, []int{26}, []int{}},
		{"v3.32 w_max=0 keeps #23", s332, p332, old332, n332, m.M{"skip_dormant_base": true, "dormant_w_max": 0.0}, []int{23, 2}, []int{}},
		{"v3.32 w_max=1 drops #23", s332, p332, old332, n332, m.M{"skip_dormant_base": true, "dormant_w_max": 1.0}, []int{2, 5}, []int{23}},
	}
	for _, c := range cases {
		bot, sk := BottomForBase(mustJSON[*m.OrderedScores](t, c.scores), c.probes, m.Feats{}, c.p, c.old, c.n60, nil)
		if !reflect.DeepEqual(bot, c.bot) || !reflect.DeepEqual(sk, c.sk) {
			t.Errorf("%s: got %v %v want %v %v", c.name, bot, sk, c.bot, c.sk)
		}
	}
}

// v3.32 base_real_log: a bottom channel with < min_log_n real requests does not
// contribute its (probe-row) log p50/lt50.
func TestBaselineRealLog(t *testing.T) {
	pr := m.Probes{23: stream(3.5, 8)}
	fe := m.Feats{23: {Long: &m.Window{N: 28, P50: m.F(2), LtN: 20, Lt50: m.F(1.5)}}}
	for _, c := range []struct {
		name      string
		n60       map[int]*int
		tt, total float64
	}{
		{"off", nil, 1.5, 2},
		{"thin real", map[int]*int{23: m.I(0)}, 3.5, 8},
		{"unknown counts as 0", map[int]*int{}, 3.5, 8},
		{"enough real", map[int]*int{23: m.I(50)}, 1.5, 2},
	} {
		b := Baseline([]int{23}, pr, fe, 10, false, 2, nil, c.n60)
		if b == nil || *b.Ttft != c.tt || *b.Total != c.total {
			t.Errorf("%s: %+v", c.name, b)
		}
	}
}

// Retired channels (owner took down) are never direct-tested.
func TestDropRetired(t *testing.T) {
	cfg := mustJSON[m.M](t, `{"toggle":{"retired":[21]}}`)
	got := DropRetired(cfg, []m.Channel{{ID: 5, Status: 2}, {ID: 21, Status: 2}})
	if len(got) != 1 || got[0].ID != 5 {
		t.Fatalf("got %+v", got)
	}
}

func TestMainGuard(t *testing.T) {
	p := P(m.M{})
	cur := &Formal{N60: map[int]*int{2: m.I(687), 22: m.I(226), 5: m.I(31), 9: m.I(500)},
		Err60R: map[int]*float64{2: m.F(0), 22: m.F(0.01), 5: m.F(0), 9: m.F(0.05)}}
	for cid, want := range map[int]bool{2: true, 22: true, 5: false, 9: false, 7: false} {
		if g, _ := MainGuard(p, cur, cid); g != want {
			t.Errorf("#%d: got %v want %v", cid, g, want)
		}
	}
	if g, _ := MainGuard(P(m.M{"toggle": m.M{"main_guard_n": 0.0}}), cur, 2); g {
		t.Error("main_guard_n=0 must disable the guard")
	}
}
