// Package decide ports autoweight/decide.py: scoring, strategies, caps and
// guards. Pure: features/probes/trust in, Decision out. No IO.
package decide

import (
	"fmt"
	"math"
	"sort"

	"autoweight/features"
	m "autoweight/model"
)

// StreamVerdict: any True → True; else any False → False; else None.
func StreamVerdict(probes []m.Probe) *bool {
	anyF := false
	for _, p := range probes {
		if !p.OK || p.Streaming == nil {
			continue
		}
		if *p.Streaming {
			return m.B(true)
		}
		anyF = true
	}
	if anyF {
		return m.B(false)
	}
	return nil
}

// TtftOf: min ttft of ok probes and stream verdict.
func TtftOf(probes []m.Probe) (*float64, *bool) {
	var best *float64
	for _, p := range probes {
		if p.OK && p.Ttft != nil && (best == nil || *p.Ttft < *best) {
			best = m.F(*p.Ttft)
		}
	}
	if best == nil {
		return nil, nil
	}
	return best, StreamVerdict(probes)
}

func Shrink(v *float64, n int, prior *float64, k float64) *float64 {
	if k == 0 || prior == nil || v == nil {
		return v
	}
	return m.F((float64(n)**v + k**prior) / (float64(n) + k))
}

// Pair is a pooled prior (p50, p90) plus, since v2.82, the pooled empty rate.
type Pair struct{ P50, P90, Empty *float64 }

// Pooled: request-weighted p50/p90 over channels.
func Pooled(fs m.Feats, ids []int, win string) Pair {
	var n50, n90, den float64
	for _, i := range ids {
		w := fs[i].Win(win)
		if w == nil || w.N == 0 || w.P50 == nil {
			continue
		}
		n50 += float64(w.N) * *w.P50
		n90 += float64(w.N) * *w.P90
		den += float64(w.N)
	}
	if den == 0 {
		return Pair{}
	}
	return Pair{P50: m.F(n50 / den), P90: m.F(n90 / den)}
}

// CmpSubs ports cmp_subs (score.cmp_guard).
func CmpSubs(fs m.Feats, ids []int, win string, s m.M) map[int]Pair {
	g := s.Sub("cmp_guard")
	if !g.B("enabled", false) {
		return nil
	}
	mn := s.F("min_n_window", 5)
	var ct []int
	mx := math.Inf(-1)
	for _, i := range ids {
		w := fs[i].Win(win)
		if w != nil && float64(w.N) >= mn && w.CtokP50 != nil && w.P50 != nil {
			ct = append(ct, i)
			mx = math.Max(mx, *w.CtokP50)
		}
	}
	if len(ct) < 2 {
		return nil
	}
	thr := math.Max(g.F("min_ctok", 60), g.F("ratio", 0.25)*mx)
	var good, bad []int
	for _, i := range ct {
		if *fs[i].Win(win).CtokP50 >= thr {
			good = append(good, i)
		} else {
			bad = append(bad, i)
		}
	}
	if len(good) == 0 || len(bad) == 0 {
		return nil
	}
	var den, p50, p90 float64
	for _, i := range good {
		w := fs[i].Win(win)
		den += float64(w.N)
		p50 += float64(w.N) * *w.P50
		p90 += float64(w.N) * *w.P90
	}
	out := map[int]Pair{}
	for _, i := range bad {
		out[i] = Pair{P50: m.F(p50 / den), P90: m.F(p90 / den)}
	}
	return out
}

func slowOf(w *m.Window, s m.M) float64 {
	if s.B("timeout_slow", false) && w.SlowRateT != nil {
		return *w.SlowRateT
	}
	return m.Or(w.SlowRate, 0)
}

// ScoreWindow ports score_window. Returns nil when not enough samples.
func ScoreWindow(w *m.Window, ttft float64, s m.M, prior Pair, sub *Pair) *float64 {
	if w == nil || float64(w.N) < s.F("min_n_window", 5) || w.P50 == nil {
		return nil
	}
	bad := math.Min(1.0, m.Or(w.ErrRate, 0)+s.F("empty_weight", 0)*EmptyRateEff(w, s, prior))
	fin := func(p50, p90 float64) *float64 {
		lat := s.F("a_p50", 0)*p50 + s.F("b_p90", 0)*p90 + s.F("c_ttft", 0)*ttft
		return m.F(math.Pow(1-bad, s.F("err_exp", 2)) * (1 - slowOf(w, s)) * s.F("t_ref", 20) / (s.F("t_ref", 20) + lat))
	}
	if sub != nil {
		return fin(*sub.P50, *sub.P90)
	}
	k := s.F("shrink_k", 0)
	p50, p90 := *Shrink(w.P50, w.N, prior.P50, k), *Shrink(w.P90, w.N, prior.P90, k)
	wm := s.Sub("workload_match")
	if wm.B("enabled", false) && w.Mr50 != nil && float64(m.OrI(w.MrN, 0)) >= wm.F("min_n", 5) && prior.P50 != nil {
		mx := wm.F("mix", 1.0)
		p50 = (1-mx)*p50 + mx**w.Mr50**prior.P50
		p90 = (1-mx)*p90 + mx**w.Mr90**prior.P90
	}
	ln := s.Sub("len_norm")
	if ln.B("enabled", false) && float64(w.Nn) >= ln.F("min_n", 5) && w.Np50 != nil {
		mx := ln.F("mix", 1.0)
		p50 = (1-mx)*p50 + mx**w.Np50
		p90 = (1-mx)*p90 + mx**w.Np90
	}
	return fin(p50, p90)
}

// ErrBurstFactor ports err_burst_factor.
func ErrBurstFactor(short *m.Window, s m.M) float64 {
	if short == nil || float64(short.N) < s.F("err_10m_min_n", 2) || short.Err == 0 {
		return 1.0
	}
	return math.Pow(1-*short.ErrRate, s.F("err_10m_exp", 0))
}

// SlowBurstFactor ports slow_burst_factor.
func SlowBurstFactor(fs m.Feats, ids []int, i int, cfg m.M) (float64, string) {
	sb := cfg.Sub("score").Sub("slow_burst")
	if !sb.B("enabled", false) {
		return 1, ""
	}
	wl, ws := m.WindowNames(cfg)
	sh := fs[i].Win(ws)
	if sh == nil || float64(sh.Nn) < sb.F("min_nn", 4) || sh.Np50 == nil {
		return 1, ""
	}
	var peers []float64
	for _, j := range ids {
		w := fs[j].Win(wl)
		if j != i && w != nil && w.Np50 != nil && float64(w.Nn) >= sb.F("peer_min_nn", 5) {
			peers = append(peers, *w.Np50)
		}
	}
	if len(peers) == 0 {
		return 1, ""
	}
	sort.Float64s(peers)
	ref := m.Median(peers)
	r := sb.F("ratio", 2.5)
	if *sh.Np50 <= r*ref {
		return 1, ""
	}
	f := math.Pow(math.Max(sb.F("floor", 0.2), r*ref / *sh.Np50), sb.F("exp", 1.0))
	return f, fmt.Sprintf("10m 归一化 np50 %.1fs > %v×同伴 %.1fs", *sh.Np50, r, ref)
}

// ProbeCalibRatio ports probe_calib_ratio.
func ProbeCalibRatio(cfg m.M, fs m.Feats, ids []int, probes m.Probes) *float64 {
	g := cfg.Sub("score").Sub("log_ttft")
	pc := g.Sub("probe_calib")
	if !g.B("enabled", false) || !pc.B("enabled", false) {
		return nil
	}
	wl, _ := m.WindowNames(cfg)
	var rs []float64
	for _, i := range ids {
		w := fs[i].Win(wl)
		if w == nil || float64(w.LtN) < g.F("min_n", 10) || w.Lt50 == nil {
			continue
		}
		t, st := TtftOf(probes[i])
		if t == nil || *t <= 0 || (st != nil && !*st) {
			continue
		}
		rs = append(rs, *w.Lt50 / *t)
	}
	if float64(len(rs)) < pc.F("min_peers", 2) {
		return nil
	}
	return m.F(m.Round(math.Min(math.Max(m.Median(rs), 1.0), pc.F("max_ratio", 5.0)), 3))
}

// BlendLogTtft ports blend_log_ttft. calib is s["_probe_calib"], pool s["_lt_pool"] (v2.86).
func BlendLogTtft(t float64, probeTtft *float64, w60 *m.Window, s m.M, calib, pool *float64) float64 {
	g := s.Sub("log_ttft")
	if !g.B("enabled", false) {
		return t
	}
	if w60 == nil {
		w60 = &m.Window{}
	}
	n, v := float64(w60.LtN), w60.Lt50
	mn := g.F("min_n", 10)
	if v == nil || n < mn {
		if calib == nil || *calib == 0 || probeTtft == nil || !g.Sub("probe_calib").B("enabled", false) {
			nn := 0.0
			if v != nil {
				nn = n
			}
			return ThinPool(t, nn, s, pool)
		}
		tc := t * *calib
		if v != nil && n > 0 {
			tc = (n**v + (mn-n)*tc) / mn
		}
		return tc
	}
	if probeTtft == nil {
		return *v
	}
	mx := g.F("mix", 0.5)
	fn := g.F("adapt_full_n", 0)
	if fn > g.F("min_n", 10) {
		mx = mx + (1-mx)*math.Min(1.0, (n-g.F("min_n", 10))/(fn-g.F("min_n", 10)))
	}
	return (1-mx)*t + mx**v
}

// TokenGate ports token_gate: copy of w with gated n.
func TokenGate(w *m.Window, f *m.Feat, s m.M) *m.Window {
	g := s.Sub("token_gate")
	if !g.B("enabled", false) || w == nil || w.N == 0 || f.Mix == nil || f.Mix.TopTokShare == nil {
		return w
	}
	sh, mx := *f.Mix.TopTokShare, g.F("max_share", 0.8)
	if sh <= mx || mx >= 1 {
		return w
	}
	c := *w
	c.NGateRaw = m.I(w.N)
	c.N = int(float64(w.N) * math.Max(0, 1-sh) / (1 - mx))
	return &c
}

// Score ports decide.score → (score, ttft, streaming).
func Score(cfg m.M, f *m.Feat, probes []m.Probe, priors map[string]Pair, subs map[string]*Pair, calib, pool *float64) (float64, *float64, *bool) {
	s := cfg.Sub("score")
	wl, ws := m.WindowNames(cfg)
	ttft, streaming := TtftOf(probes)
	t := s.F("ttft_missing_s", 30)
	if ttft != nil {
		t = *ttft
	}
	if features.LogStreamVeto(probes, f, cfg.Sub("log_stream_veto"), wl, "score") {
		streaming = nil
		t = BlendLogTtft(t, nil, f.Win(wl), s, calib, pool)
	} else {
		t = BlendLogTtft(t, ttft, f.Win(wl), s, calib, pool)
	}
	long, short := TokenGate(f.Win(wl), f, s), TokenGate(f.Win(ws), f, s)
	a := ScoreWindow(long, t, s, priors[wl], subs[wl])
	b := ScoreWindow(short, t, s, priors[ws], subs[ws])
	var sc float64
	switch {
	case a == nil && b == nil:
		if ttft != nil {
			sc = s.F("t_ref", 20) / (s.F("t_ref", 20) + s.F("c_ttft", 0)*t) * 0.5
		}
	case b == nil:
		sc = *a
	case a == nil:
		sc = *b
	default:
		w10 := s.F("w_10m", 0)
		if n0 := s.F("w10_n0", 0); n0 > 0 {
			w10 = w10 * float64(short.N) / (float64(short.N) + n0)
		}
		if g0 := s.F("w10_good_n0", 0); g0 > 0 && *b > *a {
			w10 = w10 * float64(short.N) / (float64(short.N) + g0)
		}
		sc = (1-w10)**a + w10**b
	}
	sc *= ErrBurstFactor(short, s)
	if ck := s.F("conf_k", 0); ck > 0 {
		sc *= float64(long.N) / (float64(long.N) + ck)
	}
	if kk := s.F("conc_k", 0); kk > 0 && short != nil {
		if c0 := s.F("conc_c0", 2.0); short.AvgConc > c0 {
			sc /= 1 + kk*(short.AvgConc-c0)
		}
	}
	if streaming != nil && !*streaming {
		sc *= s.F("nonstream_penalty", 1)
	}
	if len(probes) > 0 {
		any := false
		for _, p := range probes {
			any = any || p.OK
		}
		if !any {
			sc *= s.F("probe_fail_penalty", 1.0)
		}
	}
	sc *= features.TpsFactor(f, s)
	return m.Round(sc, 4), ttft, streaming
}
