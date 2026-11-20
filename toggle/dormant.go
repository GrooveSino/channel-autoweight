package toggle

import (
	"math"
	"sort"
	"strconv"

	m "autoweight/model"
)

// v2.80 skip_dormant_base (toggle_base.py). Exact port.

// IsDormantOrBuffered ports is_dormant_or_buffered: "buffered" if every site
// probe ok and none truly streaming; "dormant" if old weight known and <= 0
// and 1h real requests < minN60; else "".
//
// v3.09 skip_nodata_base: with skipNodata, "nodata" when no site probe this
// round succeeded and 1h real requests < minN60 (nil n60 counts as 0, like
// Python's `n60 or 0`), e.g. a just re-enabled channel with score 0.
//
// v3.32 toggle.dormant_w_max: wMax (default 0, config 1) — old weight <= wMax
// with 1h real < minN60 is dormant too (weight 1 gets almost no new sessions).
func IsDormantOrBuffered(ps []m.Probe, oldW *int, n60 *int, minN60 float64, skipNodata bool, wMax int) string {
	if probeBufferedOnly(ps) {
		return "buffered"
	}
	if oldW != nil && *oldW <= wMax && float64(m.OrI(n60, 0)) < minN60 {
		return "dormant"
	}
	if skipNodata && float64(m.OrI(n60, 0)) < minN60 {
		anyOK := false
		for _, x := range ps {
			if x.OK {
				anyOK = true
				break
			}
		}
		if !anyOK {
			return "nodata"
		}
	}
	return ""
}

// BottomForBase ports bottom_for_base(scores, probes, feats, p, old, n60, lax_out).
// old/n60 may be nil (run.py's baseline-test selection passes neither, so only
// "buffered" can apply there). laxOut (may be nil) receives {id: reason} only
// when the exclusion is actually used. Returns (bottom, skipped sorted ints).
func BottomForBase(sc *m.OrderedScores, probes m.Probes, feats m.Feats, p m.M,
	old map[int]int, n60 map[int]*int, laxOut map[string]string) ([]int, []int) {
	bot := BottomThird(sc)
	broken := map[int]bool{}
	if p.B("skip_broken_base", false) {
		for _, i := range sc.Keys {
			if IsBroken(i, probes, feats, p.F("err_rate", 0.2), p.F("broken_n60", 10)) {
				broken[i] = true
			}
		}
	}
	lax := map[int]string{}
	if p.B("skip_dormant_base", false) {
		for _, i := range sc.Keys {
			if broken[i] {
				continue
			}
			var ow *int
			if old != nil {
				if w, ok := old[i]; ok {
					ow = m.I(w)
				}
			}
			var nn *int
			if n60 != nil {
				nn = n60[i]
			}
			if why := IsDormantOrBuffered(probes[i], ow, nn, p.F("min_n60", 20), p.B("skip_nodata_base", false), p.I("dormant_w_max", 0)); why != "" {
				lax[i] = why
			}
		}
	}
	if len(broken) == 0 && len(lax) == 0 {
		return bot, []int{}
	}
	pool := func(skipLax bool) []int {
		out := []int{}
		for _, i := range sc.Keys {
			if broken[i] || (skipLax && lax[i] != "") {
				continue
			}
			out = append(out, i)
		}
		return out
	}
	ok := pool(true)
	if len(ok) == 0 {
		ok = pool(false)
		lax = map[int]string{}
		if len(ok) == 0 {
			return bot, []int{}
		}
	}
	k := int(math.Max(1, math.Ceil(float64(len(sc.Keys))/3)))
	if laxOut != nil {
		for i, w := range lax {
			laxOut[strconv.Itoa(i)] = w
		}
	}
	sort.SliceStable(ok, func(a, b int) bool { return m.Or(sc.Vals[ok[a]], 0) < m.Or(sc.Vals[ok[b]], 0) })
	if k > len(ok) {
		k = len(ok)
	}
	sk := []int{}
	for i := range broken {
		sk = append(sk, i)
	}
	for i := range lax {
		if !broken[i] {
			sk = append(sk, i)
		}
	}
	sort.Ints(sk)
	return ok[:k], sk
}
