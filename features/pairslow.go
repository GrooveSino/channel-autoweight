package features

import (
	"math"

	m "autoweight/model"
)

// v5.01 same-token paired latency (pair_slow). For each channel, real
// successes are grouped into cells (token, model, output-length bucket); a cell
// counts when this channel and the pooled other channels each have >= min_cell
// rows. The cell ratio is median(s per 1k output tokens, floor ct_floor) here /
// the same on the other channels; the channel ratio is the weighted geometric
// mean (weight min(n_here, n_other, 10)). Same users, same model, similar
// output length: what is left is the channel's own speed.

// PairOf computes PairStat per channel over rows in [now-minutes*60, now].
func PairOf(cfg m.M, rows []m.Row, now int64, minutes int) map[int]*m.PairStat {
	pc := cfg.Sub("pair_slow")
	minCell, floor := pc.I("min_cell", 3), pc.F("ct_floor", 100)
	type key struct {
		tok, b int
		mdl    string
	}
	cells := map[key]map[int][]float64{}
	for _, x := range UserRows(cfg, rows) {
		if x.Type != 2 || x.Ct <= 0 || x.T < now-int64(minutes)*60 || x.T > now {
			continue
		}
		k := key{x.Tok, bucket(x.Ct), x.Mdl}
		if cells[k] == nil {
			cells[k] = map[int][]float64{}
		}
		cells[k][x.Ch] = append(cells[k][x.Ch], float64(x.Ut)*1000/math.Max(float64(x.Ct), floor))
	}
	type acc struct {
		s, w float64
		n    int
		toks map[int]bool
	}
	ac := map[int]*acc{}
	for k, by := range cells {
		for c, v := range by {
			if len(v) < minCell {
				continue
			}
			var o []float64
			for c2, v2 := range by {
				if c2 != c {
					o = append(o, v2...)
				}
			}
			if len(o) < minCell {
				continue
			}
			mh, mo := m.Median(v), m.Median(o)
			if mh <= 0 || mo <= 0 {
				continue
			}
			a := ac[c]
			if a == nil {
				a = &acc{toks: map[int]bool{}}
				ac[c] = a
			}
			w := float64(min(len(v), len(o), 10))
			a.s += w * math.Log(mh/mo)
			a.w += w
			a.n += len(v)
			a.toks[k.tok] = true
		}
	}
	out := map[int]*m.PairStat{}
	for c, a := range ac {
		out[c] = &m.PairStat{Ratio: m.Round(math.Exp(a.s/a.w), 3), Tokens: len(a.toks), N: a.n, Min: minutes}
	}
	return out
}

// AttachPair sets Feat.Pair (decision window, default 60 min) and Feat.PairRef
// (reference window, default 1440 min). rows must cover the reference window;
// with fewer rows the reference is simply thinner.
func AttachPair(cfg m.M, fs m.Feats, rows []m.Row, now int64) {
	pc := cfg.Sub("pair_slow")
	if !pc.B("enabled", false) {
		return
	}
	a, b := PairOf(cfg, rows, now, pc.I("minutes", 60)), PairOf(cfg, rows, now, pc.I("ref_minutes", 1440))
	for i, f := range fs {
		if f != nil {
			f.Pair, f.PairRef = a[i], b[i]
		}
	}
}
