// Package trust ports autoweight/trust.py and shadow.trust_step: per-channel
// trust (slow up, fast down). Pure: features+probes+prev state in, state out.
package trust

import (
	"fmt"
	"math"

	"autoweight/features"
	m "autoweight/model"
)

var Defaults = m.M{"enabled": false, "t_init": 0.6, "t_min": 0.1, "up": 0.08, "down": 0.6, "up_full_n": 30.0, "floor": 0.5,
	"err_10m_min_n": 2.0, "err_full": 0.2, "err_1h_thr": 0.02, "probe_fail_sev": 0.8, "buffered_sev": 0.5,
	"slow_thr": 0.15, "slow_sev": 0.3, "gap_reset_min": 180.0, "round_min": 45.0}

// Params ports trust.params.
func Params(cfg m.M) m.M {
	p := m.Merge(Defaults, cfg.Sub("trust"))
	p["log_stream_veto"] = map[string]any(cfg.Sub("log_stream_veto"))
	return p
}

// State is one channel's trust step result.
type State struct {
	Trust  float64  `json:"trust"`
	Prev   *float64 `json:"prev"`
	Sev    float64  `json:"sev"`
	SevEff *float64 `json:"sev_eff,omitempty"`
	Why    []string `json:"why"`
}

type Part struct {
	Sev float64
	Why string
	Src string // short | long | probe
}

// SlowPosterior ports slow_posterior (midpoint integration of Beta posterior).
func SlowPosterior(n int, r, thr, a0, b0 float64) float64 {
	k := float64(m.RoundInt(float64(n) * r))
	a, b := a0+k, b0+float64(n)-k
	la, _ := math.Lgamma(a)
	lb_, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	lb := lab - la - lb_
	steps := 2000
	h := (1 - thr) / float64(steps)
	tot := 0.0
	for t := 0; t < steps; t++ {
		x := thr + (float64(t)+0.5)*h
		tot += math.Exp(lb + (a-1)*math.Log(x) + (b-1)*math.Log(1-x))
	}
	return math.Min(1.0, tot*h)
}

// SeverityParts ports severity_parts.
func SeverityParts(f *m.Feat, probes []m.Probe, p m.M, wl, ws string) []Part {
	var parts []Part
	s, l := f.Win(ws), f.Win(wl)
	if s == nil {
		s = &m.Window{}
	}
	if l == nil {
		l = &m.Window{}
	}
	if float64(s.N) >= p.F("err_10m_min_n", 2) && s.Err != 0 {
		sev10 := math.Min(1.0, m.Or(s.ErrRate, 0)/p.F("err_full", 0.2))
		if fn := p.F("err_10m_full_n", 0); fn > 0 {
			sev10 *= math.Min(1.0, float64(s.N)/fn)
		}
		parts = append(parts, Part{sev10, fmt.Sprintf("10m 错误 %d/%d", s.Err, s.N), "short"})
	}
	if l.N != 0 && m.Or(l.ErrRate, 0) > p.F("err_1h_thr", 0.02) {
		parts = append(parts, Part{math.Min(1.0, (*l.ErrRate-p.F("err_1h_thr", 0.02))/p.F("err_full", 0.2)),
			fmt.Sprintf("1h 错误率 %.1f%%", *l.ErrRate*100), "long"})
	}
	anyOK := false
	anyT, anyF := false, false
	for _, x := range probes {
		if x.OK {
			anyOK = true
			if x.Streaming != nil && *x.Streaming {
				anyT = true
			} else if x.Streaming != nil {
				anyF = true
			}
		}
	}
	if len(probes) > 0 && !anyOK {
		parts = append(parts, Part{p.F("probe_fail_sev", 0.8), "探测全部失败", "probe"})
	}
	if anyOK && !anyT && anyF && !features.LogStreamVeto(probes, f, p.Sub("log_stream_veto"), wl, "trust") {
		parts = append(parts, Part{p.F("buffered_sev", 0.5), "探测整段缓冲", "probe"})
	}
	n, r := l.N, m.Or(l.SlowRate, 0)
	if float64(n) >= p.F("slow_min_n", 5) && r > p.F("slow_thr", 0.15) {
		ok := true
		thr := p.F("slow_thr", 0.15)
		if p.B("slow_wilson", false) {
			z := 1.96
			nf := float64(n)
			lb := (r + z*z/(2*nf) - z*math.Sqrt((r*(1-r)+z*z/(4*nf))/nf)) / (1 + z*z/nf)
			ok = lb > thr
		}
		sev := p.F("slow_sev", 0.3)
		if ok && p.B("slow_bayes", false) {
			q := SlowPosterior(n, r, thr, p.F("slow_prior_a", 1), p.F("slow_prior_b", 1))
			sev = p.F("slow_sev", 0.3) * math.Max(0, math.Min(1, (q-0.5)/0.5))
			ok = sev > 0
		}
		if ok {
			parts = append(parts, Part{sev, fmt.Sprintf("1h 慢请求 %.0f%%", r*100), "long"})
		}
	}
	for i := range parts {
		parts[i].Sev = m.Round(parts[i].Sev, 3)
	}
	return parts
}

// Update ports trust.update. prev nil = new channel.
func Update(prev *float64, sev float64, n60 int, p m.M, gap *float64) float64 {
	var t float64
	if prev == nil {
		t = p.F("t_new", p.F("t_init", 0.6))
	} else {
		t = *prev
	}
	if gap != nil && *gap > p.F("gap_reset_min", 180) {
		t = (t + p.F("t_init", 0.6)) / 2
	}
	if sev > 0 {
		t = math.Max(p.F("t_min", 0.1), t*(1-p.F("down", 0.6)*sev))
	} else {
		g := 1.0
		if gap != nil {
			g = math.Max(0, math.Min(1, *gap/p.F("round_min", 45)))
		}
		t = math.Min(1.0, t+p.F("up", 0.08)*g*math.Min(1.0, float64(n60)/p.F("up_full_n", 30)))
	}
	return m.Round(t, 4)
}

func whys(ps []Part) []string {
	out := []string{}
	for _, x := range ps {
		out = append(out, x.Why)
	}
	return out
}

func maxSev(ps []Part, fac func(Part) float64) float64 {
	v := 0.0
	for i, x := range ps {
		s := x.Sev * fac(x)
		if i == 0 || s > v {
			v = s
		}
	}
	return v
}

// Step ports trust.step (formal rounds source).
func Step(cfg m.M, ids []int, feats m.Feats, probes m.Probes, prev map[int]float64, gap *float64) map[int]*State {
	p := Params(cfg)
	wl, ws := m.WindowNames(cfg)
	out := map[int]*State{}
	for _, i := range ids {
		parts := SeverityParts(feats[i], probes[i], p, wl, ws)
		sev := maxSev(parts, func(Part) float64 { return 1 })
		pv := prevPtr(prev, i)
		out[i] = &State{Trust: Update(pv, sev, feats[i].Long.N, p, gap), Prev: pv, Sev: sev, Why: whys(parts)}
	}
	return out
}

func prevPtr(prev map[int]float64, i int) *float64 {
	if v, ok := prev[i]; ok {
		return m.F(v)
	}
	return nil
}

var ShadowDefaults = m.M{"interval_min": 5.0, "min_real_n": 3.0, "probe_window_min": 10.0, "probe_cooldown_min": 30.0,
	"ttft_max_age_min": 90.0, "sev_gap_min": 10.0, "file": "data/shadow.jsonl"}

// ShadowStep ports shadow.trust_step (severity discounted by gap, probe events once).
func ShadowStep(cfg m.M, ids []int, feats m.Feats, probes m.Probes, prev map[int]float64, gap *float64, probeAge map[int]float64) map[int]*State {
	p, sp := Params(cfg), m.Merge(ShadowDefaults, cfg.Sub("shadow"))
	wl, ws := m.WindowNames(cfg)
	kf := func(w float64) float64 {
		if gap == nil {
			return 1
		}
		return math.Max(0, math.Min(1, *gap/w))
	}
	longW := sp.F("sev_gap_long_min", 0)
	if longW == 0 {
		longW = sp.F("sev_gap_min", 10)
	}
	k, kl := kf(sp.F("sev_gap_min", 10)), kf(longW)
	out := map[int]*State{}
	for _, i := range ids {
		parts := SeverityParts(feats[i], probes[i], p, wl, ws)
		sev := maxSev(parts, func(Part) float64 { return 1 })
		once := sp.B("probe_once", false) && probeAge != nil
		eff := m.Round(maxSev(parts, func(x Part) float64 {
			switch {
			case x.Src == "long":
				return kl
			case x.Src == "probe" && once:
				a, ok := probeAge[i]
				if !ok || gap == nil || a < *gap-1e-9 {
					return 1
				}
				return 0
			}
			return k
		}), 3)
		pv := prevPtr(prev, i)
		out[i] = &State{Trust: Update(pv, eff, feats[i].Long.N, p, gap), Prev: pv, Sev: sev, SevEff: m.F(eff), Why: whys(parts)}
	}
	return out
}

// Multiplier: floor + (1-floor)*t.
func Multiplier(t float64, p m.M) float64 {
	f := p.F("floor", 0.5)
	return f + (1-f)*t
}
