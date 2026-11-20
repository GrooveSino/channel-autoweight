package decide

import (
	m "autoweight/model"
)

// v2.82 / v2.86 small-sample knobs (scoring.py pooled_empty, empty_rate_eff,
// lt_pool, thin_pool). Exact ports.

// PooledEmpty ports pooled_empty: total empty / total n over channels with a
// non-empty window and a known empty count; nil when no samples.
func PooledEmpty(fs m.Feats, ids []int, win string) *float64 {
	var num, den float64
	for _, i := range ids {
		w := fs[i].Win(win)
		if w == nil || w.N == 0 || w.Empty == nil {
			continue
		}
		num += float64(*w.Empty)
		den += float64(w.N)
	}
	if den == 0 {
		return nil
	}
	return m.F(num / den)
}

// EmptyRateEff ports empty_rate_eff (score.empty_prior_k): (empty+k*pool)/(n+k);
// k=0 (default) returns the raw empty_rate (or 0).
func EmptyRateEff(w *m.Window, s m.M, prior Pair) float64 {
	k := s.F("empty_prior_k", 0)
	if k == 0 || prior.Empty == nil || w.EmptyRate == nil || w.Empty == nil {
		return m.Or(w.EmptyRate, 0)
	}
	return (float64(*w.Empty) + k**prior.Empty) / (float64(w.N) + k)
}

// LtPool ports lt_pool: median 1h log-TTFT p50 of channels with lt_n >= min_n;
// nil when log_ttft is off, thin_pool_k is 0, or fewer than 2 such channels.
func LtPool(cfg m.M, fs m.Feats, ids []int) *float64 {
	g := cfg.Sub("score").Sub("log_ttft")
	if !g.B("enabled", false) || g.F("thin_pool_k", 0) == 0 {
		return nil
	}
	wl, _ := m.WindowNames(cfg)
	var vs []float64
	for _, i := range ids {
		w := fs[i].Win(wl)
		if w == nil || float64(w.LtN) < g.F("min_n", 10) || w.Lt50 == nil {
			continue
		}
		vs = append(vs, *w.Lt50)
	}
	if len(vs) < 2 {
		return nil
	}
	return m.F(m.Median(vs))
}

// ThinPool ports thin_pool: t' = (n*t + k*pool)/(n+k); identity when k=0 or no pool.
func ThinPool(t float64, n float64, s m.M, pool *float64) float64 {
	k := s.Sub("log_ttft").F("thin_pool_k", 0)
	if k == 0 || pool == nil {
		return t
	}
	return (n*t + k**pool) / (n + k)
}
