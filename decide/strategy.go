package decide

import (
	"math"

	m "autoweight/model"
)

// RankParams carries strategies.rank plus runtime extras (_trust, _n_user).
type RankParams struct {
	P     m.M
	Trust map[int]float64 // _trust (lead_trust)
	NUser map[int]int     // _n_user (lead_min_n)
}

func absF(x float64) float64 { return math.Abs(x) }

// PrevTiers ports prev_tiers.
func PrevTiers(prev map[int]int, ids []int, p m.M, caps map[int]float64) map[int]string {
	if prev == nil {
		return nil
	}
	vals := []float64{}
	for _, i := range ids {
		v, ok := prev[i]
		if !ok {
			return nil
		}
		vals = append(vals, float64(v))
	}
	if len(vals) == 0 {
		return nil
	}
	mx, mn := vals[0], vals[0]
	for _, v := range vals {
		mx, mn = math.Max(mx, v), math.Min(mn, v)
	}
	out := map[int]string{}
	if len(ids) > 1 && mx-mn <= 1 {
		for _, i := range ids {
			out[i] = "tie"
		}
		return out
	}
	cw := p.F("top_w", 20) * p.F("close_factor", 0.8)
	midW, lowW := p.F("mid_w", 3), p.F("low_w", 0)
	for _, i := range ids {
		w := float64(prev[i])
		if cp, ok := caps[i]; ok && w != mx && absF(w-float64(m.RoundInt(cp))) < 0.5 &&
			float64(m.RoundInt(cp)) > midW && float64(m.RoundInt(cp)) < cw {
			out[i] = "close"
			continue
		}
		switch {
		case w == mx:
			out[i] = "top"
		case w >= (cw+midW)/2:
			out[i] = "close"
		case w > (midW+lowW)/2:
			out[i] = "mid"
		default:
			out[i] = "low"
		}
	}
	return out
}

var rankOf = map[string]int{"top": 3, "close": 2, "mid": 1, "low": 0}

// StratRank ports strat_rank.
func StratRank(order []int, sc map[int]float64, rp RankParams, cfg m.M, prev map[int]int, caps map[int]float64) map[int]float64 {
	p := rp.P
	top := sc[order[0]]
	topFallback := top == 0
	if topFallback {
		top = 1e-9
	}
	band := p.F("band", 0)
	var pt map[int]string
	if band > 0 {
		var cc map[int]float64
		if p.B("band_cap_aware", false) {
			cc = caps
		}
		pt = PrevTiers(prev, order, p, cc)
	}
	wasTie := len(pt) > 0
	for _, v := range pt {
		if v != "tie" {
			wasTie = false
		}
	}
	tieThr := p.F("tie_ratio", 0.95)
	if wasTie {
		tieThr -= band
	} else if len(pt) > 0 {
		tieThr += band
	}
	all := true
	for _, i := range order {
		if !(sc[i] >= top*tieThr) {
			all = false
		}
	}
	w := map[int]float64{}
	if all {
		for _, i := range order {
			w[i] = cfg.F("w_total", 18) / float64(len(order))
		}
		return w
	}
	thr := func(base float64, level, i int) float64 {
		t, ok := pt[i]
		if len(pt) == 0 || !ok || t == "tie" {
			return base
		}
		if rankOf[t] >= level {
			return base - band
		}
		return base + band
	}
	ramp := p.F("ramp", 0)
	topW := p.F("top_w", 20)
	cw := topW * p.F("close_factor", 0.8)
	midW, lowW := p.F("mid_w", 3), p.F("low_w", 0)
	lt := p.F("lead_trust", 0)
	ref, useRef := top, false
	tr := rp.Trust
	trOf := func(j int) float64 {
		if v, ok := tr[j]; ok {
			return v
		}
		return 1.0
	}
	if lt > 0 && len(tr) > 0 && trOf(order[0]) < lt {
		found := false
		mx := 0.0
		for _, j := range order {
			if trOf(j) >= lt {
				if !found || sc[j] > mx {
					mx = sc[j]
				}
				found = true
			}
		}
		if found {
			// Python: `ref is not top` — identical only when both fell back to the 1e-9 literal.
			ref, useRef = mx, true
			if ref == 0 {
				ref = 1e-9
				useRef = !topFallback
			}
		}
	}
	lmn := p.F("lead_min_n", 0)
	nu := rp.NUser
	nuOf := func(j int) float64 {
		if v, ok := nu[j]; ok {
			return float64(v)
		}
		return 1e9
	}
	thinLead, richRef := false, 0.0
	if lmn > 0 && len(nu) > 0 && nuOf(order[0]) < lmn && lt <= 0 {
		var rich []int
		for _, j := range order {
			if nuOf(j) >= lmn {
				rich = append(rich, j)
			}
		}
		nRich := 0
		for _, v := range nu {
			if float64(v) >= lmn {
				nRich++
			}
		}
		need := p.F("lead_min_rich", 2)
		if need == 0 {
			need = 1
		}
		if len(rich) > 0 && float64(nRich) >= need {
			mx := sc[rich[0]]
			for _, j := range rich {
				mx = math.Max(mx, sc[j])
			}
			if mx == 0 {
				mx = 1e-9
			}
			thinLead, richRef = true, mx
		}
	}
	tlw := p.FP("thin_lead_w")
	co := p.F("co_lead_ratio", 0)
	for k, i := range order {
		r := sc[i] / top
		if useRef && trOf(i) >= lt {
			r = math.Min(1.0, sc[i]/ref)
		}
		isThin := thinLead && nuOf(i) < lmn
		if thinLead && !isThin {
			r = math.Min(1.0, sc[i]/richRef)
		}
		switch {
		case thinLead && !isThin && r >= 1.0:
			w[i] = topW
		case k == 0:
			w[i] = topW
		case co > 0 && r >= co:
			w[i] = topW
		case ramp > 0 && len(pt) == 0:
			c, md := p.F("close_ratio", 0.85), p.F("mid_ratio", 0.7)
			switch {
			case r >= c:
				w[i] = cw
			case r >= md:
				w[i] = midW + (cw-midW)*(r-md)/math.Max(c-md, 1e-9)
			case r >= md-ramp:
				w[i] = lowW + (midW-lowW)*(r-(md-ramp))/ramp
			default:
				w[i] = lowW
			}
		case r >= thr(p.F("close_ratio", 0.85), 2, i):
			w[i] = topW * p.F("close_factor", 0.8)
		case r >= thr(p.F("mid_ratio", 0.7), 1, i):
			w[i] = midW
		default:
			w[i] = lowW
		}
		if isThin && tlw != nil {
			w[i] = math.Min(w[i], *tlw)
		}
	}
	return w
}

// StratProportional ports strat_proportional.
func StratProportional(order []int, sc map[int]float64, p m.M, cfg m.M) map[int]float64 {
	g := p.F("gamma", 2)
	tot := 0.0
	for _, i := range order {
		tot += math.Pow(math.Max(sc[i], 1e-6), g)
	}
	w := map[int]float64{}
	for _, i := range order {
		w[i] = cfg.F("w_total", 18) * math.Pow(math.Max(sc[i], 1e-6), g) / tot
	}
	return w
}

// RunStrategy dispatches by name.
func RunStrategy(name string, order []int, sc map[int]float64, rp RankParams, cfg m.M, prev map[int]int, caps map[int]float64) map[int]float64 {
	if name == "proportional" {
		return StratProportional(order, sc, rp.P, cfg)
	}
	return StratRank(order, sc, rp, cfg, prev, caps)
}

// EffectiveRankParams ports effective_rank_params (low_traffic overrides).
func EffectiveRankParams(cfg m.M, name string, n60 map[int]int) (m.M, int, float64) {
	prm := cfg.Sub("strategies").Sub(name)
	if name != "rank" {
		return prm, -1, 0
	}
	lt := prm.Sub("low_traffic")
	mx := lt.F("max_total_n60", 0)
	if mx == 0 {
		return prm, -1, 0
	}
	tot := 0
	for _, v := range n60 {
		tot += v
	}
	if float64(tot) >= mx {
		return prm, -1, 0
	}
	out := prm.With()
	for k, v := range lt.Public() {
		if k != "max_total_n60" {
			out[k] = v
		}
	}
	return out, tot, mx
}

// HoldTop ports hold_top (rank.hysteresis).
func HoldTop(order []int, sc map[int]float64, prev map[int]int, prevOrder []int, h float64) ([]int, int, bool) {
	if h <= 0 || len(prev) == 0 {
		return order, 0, false
	}
	pt, found := 0, false
	for _, i := range prevOrder {
		if _, ok := sc[i]; ok {
			if !found || prev[i] > prev[pt] {
				pt, found = i, true
			}
		}
	}
	if !found || order[0] == pt || sc[pt] < sc[order[0]]*(1-h) {
		return order, 0, false
	}
	out := []int{pt}
	for _, i := range order {
		if i != pt {
			out = append(out, i)
		}
	}
	return out, pt, true
}
