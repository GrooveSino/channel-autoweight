package features

import (
	"fmt"
	"math"

	m "autoweight/model"
)

// TpsSample is one eligible request's end-to-end TPS.
//
// Definition: completion_tokens / (use_time + 0.5). use_time is truncated to whole
// seconds (logs show frt > use_time on ~7% of rows), so +0.5 is the unbiased
// midpoint. Generation-phase TPS (ct/(use_time-frt)) is not used: completion_tokens
// include reasoning tokens produced before frt, and frt is missing on ~37% of rows.
func TpsSample(x m.Row, minCt, minUt int) (float64, bool) {
	if x.Type != 2 || x.Ct < minCt || x.Ut < minUt || x.St == 2 {
		return 0, false
	}
	return float64(x.Ct) / (float64(x.Ut) + 0.5), true
}

// AttachTps sets Feat.Tps from real (non-probe) rows in [now-minutes*60, now].
// Model mix is normalised: each request is divided by its model's pooled median
// across all channels (models with < minModelN samples use the global median).
func AttachTps(cfg m.M, feats m.Feats, rows []m.Row, now int64) {
	g := cfg.Sub("score").Sub("tps")
	minCt, minUt := g.I("min_ct", 50), g.I("min_ut", 3)
	t0 := now - int64(g.I("window_min", 60))*60
	type smp struct {
		ch  int
		mdl string
		v   float64
	}
	var ss []smp
	byM := map[string][]float64{}
	var all []float64
	for _, x := range UserRows(cfg, rows) {
		if x.T < t0 || x.T > now {
			continue
		}
		if v, ok := TpsSample(x, minCt, minUt); ok {
			ss = append(ss, smp{x.Ch, x.Mdl, v})
			byM[x.Mdl] = append(byM[x.Mdl], v)
			all = append(all, v)
		}
	}
	if len(all) == 0 {
		return
	}
	gm := m.Median(all)
	pool := map[string]float64{}
	for k, v := range byM {
		pool[k] = gm
		if len(v) >= g.I("min_model_n", 10) {
			pool[k] = m.Median(v)
		}
	}
	raw, nr := map[int][]float64{}, map[int][]float64{}
	for _, s := range ss {
		raw[s.ch] = append(raw[s.ch], s.v)
		nr[s.ch] = append(nr[s.ch], s.v/pool[s.mdl])
	}
	for ch, f := range feats {
		if len(raw[ch]) == 0 {
			continue
		}
		f.Tps = &m.Tps{N: len(raw[ch]), P25: m.Round(*m.Pct(raw[ch], 0.25), 2),
			P50: m.Round(m.Median(raw[ch]), 2), Idx: m.Round(m.Median(nr[ch]), 3)}
	}
}

// TpsFactor is the score multiplier clamp(idx, lo, hi)^exp (1 when off or thin).
func TpsFactor(f *m.Feat, s m.M) float64 {
	g := s.Sub("tps")
	if !g.B("enabled", false) || f == nil || f.Tps == nil || f.Tps.N < g.I("min_n", 10) {
		return 1
	}
	return math.Pow(math.Min(math.Max(f.Tps.Idx, g.F("lo", 0.5)), g.F("hi", 1.5)), g.F("exp", 0.5))
}

// TpsFlags is the diagnostic TPSLOW line: channels whose model-normalised TPS
// index is below thr with at least minN samples (does not affect scoring).
func TpsFlags(feats m.Feats, minN int, thr float64) []string {
	var out []string
	for _, id := range m.SortedIDs(feats) {
		t := feats[id].Tps
		if t != nil && t.N >= minN && t.Idx < thr {
			out = append(out, fmt.Sprintf("TPSLOW #%d 1h 输出速度 p50 %.1f tok/s（同模型标准化 %.2f×，n=%d）；只诊断", id, t.P50, t.Idx, t.N))
		}
	}
	return out
}
