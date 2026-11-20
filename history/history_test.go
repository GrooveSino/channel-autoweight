package history

import (
	"testing"

	m "autoweight/model"
)

func r(ts string, dry *bool, probes bool, reused bool, trust map[int]float64) *m.Rec {
	x, _ := m.ParseRound([]byte(`{"ts":"` + ts + `"}`))
	x.DryRun, x.ProbesReused = dry, reused
	if probes {
		x.Probes = m.Probes{2: {{}}, 5: {{}}}
	}
	if trust != nil {
		x.Trust = map[int]m.TrustRec{}
		for k, v := range trust {
			x.Trust[k] = m.TrustRec{Trust: v}
		}
	}
	return x
}

func TestHistory(t *testing.T) {
	f, tr := false, true
	rs := []*m.Rec{
		r("2026-10-29T20:00:00+08:00", &f, true, false, map[int]float64{2: 0.5}),
		r("2026-10-29T20:30:00+08:00", &tr, true, false, map[int]float64{2: 0.9}),
		r("2026-10-29T20:40:00+08:00", &f, true, true, nil),
	}
	now := m.Epoch(rs[2].TsTime) + 300 // 20:45
	cases := []struct {
		name string
		fn   func() bool
	}{
		{"last_state skips dry-run", func() bool { s, ts := LastState(rs); return s[2] == 0.5 && ts == rs[0].Ts }},
		{"missing dry_run counts as dry", func() bool {
			s, _ := LastState([]*m.Rec{r("2026-10-29T20:00:00+08:00", nil, false, false, map[int]float64{2: 1})})
			return len(s) == 0
		}},
		{"reuse within window", func() bool { p := Reuse(rs, []int{2}, now, 10); return len(p) == 1 && p[2] != nil }},
		{"reuse too old", func() bool { return len(Reuse(rs, []int{2}, now, 4)) == 0 }},
		{"reuse_age skips reused copies", func() bool { a := ReuseAge(rs, now); return a != nil && *a == 15 }},
		{"reuse_age none", func() bool { return ReuseAge(nil, now) == nil }},
		{"shadow trust_prev", func() bool {
			x := r("2026-10-29T20:41:00+08:00", nil, false, false, map[int]float64{5: 0.3})
			x.Mode = "shadow"
			s, ts := ShadowTrustPrev([]*m.Rec{x, rs[0]})
			return s[5] == 0.3 && ts == x.Ts
		}},
	}
	for _, c := range cases {
		if !c.fn() {
			t.Errorf("%s failed", c.name)
		}
	}
}
