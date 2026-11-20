package trust

import (
	"testing"

	m "autoweight/model"
)

// Trust rises slowly with clean traffic, falls fast on bad events, and drifts
// back toward t_init after a long gap.
func TestUpdate(t *testing.T) {
	p := m.Merge(Defaults, m.M{})
	for _, c := range []struct {
		name string
		prev *float64
		sev  float64
		n60  int
		gap  *float64
		want float64
	}{
		{"new channel starts at t_init", nil, 0, 0, nil, 0.6},
		{"clean full round +up", m.F(0.5), 0, 30, m.F(45), 0.58},
		{"thin traffic scales up", m.F(0.5), 0, 15, m.F(45), 0.54},
		{"short gap scales up", m.F(0.5), 0, 30, m.F(5), 0.5089},
		{"capped at 1", m.F(0.99), 0, 30, m.F(45), 1},
		{"bad event x(1-down*sev)", m.F(1), 0.5, 30, m.F(5), 0.7},
		{"floored at t_min", m.F(0.12), 1, 30, m.F(5), 0.1},
		{"gap reset halfway to t_init", m.F(1), 0, 0, m.F(200), 0.8},
	} {
		if got := Update(c.prev, c.sev, c.n60, p, c.gap); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// Shadow points overlap: 1h events are discounted by gap/60, 10m by gap/10,
// and a reused probe failure is only counted once (probe_once).
func TestShadowStep(t *testing.T) {
	cfg := m.M{"windows_min": []any{60.0, 10.0}, "trust": m.M{"enabled": true},
		"shadow": m.M{"sev_gap_min": 10.0, "sev_gap_long_min": 60.0, "probe_once": true}}
	f := m.Feats{
		2: {Long: &m.Window{N: 100, ErrRate: m.F(0.12), Err: 12}, Short: &m.Window{}},
		5: {Long: &m.Window{N: 30, ErrRate: m.F(0)}, Short: &m.Window{}},
	}
	fail := m.Probes{5: {{OK: false}}}
	gap := m.F(5.0)
	prev := map[int]float64{2: 1, 5: 1}
	fresh := ShadowStep(cfg, []int{2, 5}, f, fail, prev, gap, map[int]float64{5: 0})
	if s := fresh[2]; s.Sev != 0.5 || *s.SevEff != m.Round(0.5*5/60, 3) {
		t.Errorf("1h discount: %+v", s)
	}
	if s := fresh[5]; *s.SevEff != 0.8 || s.Trust >= 1 {
		t.Errorf("fresh probe failure must count fully: %+v", s)
	}
	reused := ShadowStep(cfg, []int{2, 5}, f, fail, prev, gap, map[int]float64{5: 12})
	if s := reused[5]; *s.SevEff != 0 || s.Trust != 1 {
		t.Errorf("reused probe failure must not count again: %+v", s)
	}
}
