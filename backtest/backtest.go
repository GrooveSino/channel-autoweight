// Package backtest replays raw logs: at each step t the decision uses only rows
// before t (no probes, trust off — identical for every variant), and is scored on
// the real (non-probe) requests of the next hour, weighted by the target share.
// This is an approximation: channel behaviour is assumed independent of traffic.
package backtest

import (
	"sort"
	"time"

	"autoweight/decide"
	"autoweight/features"
	m "autoweight/model"
)

type Result struct {
	Steps                   int
	P50, P90, Err, Tps, Bad float64 // share-weighted next-hour metrics
	Emp                     float64 // share-weighted channel-side empty-success rate (Ue rows excluded)
	CErr                    float64 // v4.98: share-weighted channel-error rate (features.ChanRows: client/cross-channel errors out)
	Moved                   int     // sum |Δweight| between steps
}

type ev struct {
	n             int
	p50, p90, err float64
	emp, cerr     float64
	tps           float64
	hasTps        bool
}

func evalWin(cfg m.M, rows []m.Row, t0, t1 int64) map[int]ev {
	cn, ce := map[int]int{}, map[int]int{}
	for _, x := range features.ChanRows(cfg, rows) {
		if x.T >= t0 && x.T < t1 {
			cn[x.Ch]++
			if x.Type == 5 {
				ce[x.Ch]++
			}
		}
	}
	by := map[int][]m.Row{}
	for _, x := range features.UserRows(cfg, rows) {
		if x.T >= t0 && x.T < t1 {
			by[x.Ch] = append(by[x.Ch], x)
		}
	}
	out := map[int]ev{}
	for ch, rs := range by {
		var ut, tp []float64
		e, em := 0, 0
		for _, x := range rs {
			if x.Type == 5 {
				e++
				continue
			}
			if x.Ct == 0 && x.Ue == 0 {
				em++
			}
			ut = append(ut, float64(x.Ut))
			if v, ok := features.TpsSample(x, 50, 3); ok {
				tp = append(tp, v)
			}
		}
		if len(ut) < 3 {
			continue
		}
		r := ev{n: len(rs), p50: *m.Pct(ut, 0.5), p90: *m.Pct(ut, 0.9), err: float64(e) / float64(len(rs)), emp: float64(em) / float64(len(rs))}
		if cn[ch] > 0 {
			r.cerr = float64(ce[ch]) / float64(cn[ch])
		}
		if len(tp) >= 3 {
			r.tps, r.hasTps = m.Median(tp), true
		}
		out[ch] = r
	}
	return out
}

// Next scores target weights on the real requests of [t, t+1h): share-weighted
// p50, p90 and channel-error rate (used by awbacktest -agree).
func Next(cfg m.M, rows []m.Row, t int64, target map[int]int) (w, p50, p90, cerr float64) {
	for id, e := range evalWin(cfg, rows, t, t+3600) {
		if fw := float64(target[id]); fw > 0 {
			w += fw
			p50 += fw * e.p50
			p90 += fw * e.p90
			cerr += fw * e.cerr
		}
	}
	return
}

// Run replays [from, to) every step seconds.
func Run(cfg m.M, chans []m.Channel, rows []m.Row, from, to, step int64) (Result, error) {
	sort.Slice(rows, func(a, b int) bool { return rows[a].T < rows[b].T })
	prev := map[int]int{}
	for _, c := range chans {
		prev[c.ID] = c.Weight
	}
	var r Result
	var sw, s50, s90, se, sem, sce, stw, st float64
	for t := from; t+3600 <= to; t += step {
		var hist []m.Row
		for _, x := range rows {
			if x.T >= t-3600 && x.T < t {
				hist = append(hist, x)
			}
		}
		fs := features.Build(cfg, chans, hist, t-1)
		features.AttachPair(cfg, fs, rows, t-1) // v5.01: windows end at t-1, later rows ignored
		d, err := decide.Decide(decide.Input{Cfg: cfg, Chans: chans, Feats: fs, Probes: m.Probes{},
			Prev: prev, Now: time.Unix(t, 0)})
		if err != nil {
			return r, err
		}
		nx := evalWin(cfg, rows, t, t+3600)
		for id, w := range d.Target {
			e, ok := nx[id]
			if !ok || w == 0 {
				continue
			}
			fw := float64(w)
			sw += fw
			s50 += fw * e.p50
			s90 += fw * e.p90
			se += fw * e.err
			sem += fw * e.emp
			sce += fw * e.cerr
			if e.hasTps {
				stw += fw
				st += fw * e.tps
			}
		}
		for id, w := range d.Target {
			dv := w - prev[id]
			if dv < 0 {
				dv = -dv
			}
			r.Moved += dv
		}
		prev = d.Target
		r.Steps++
	}
	if sw > 0 {
		r.P50, r.P90, r.Err = m.Round(s50/sw, 3), m.Round(s90/sw, 3), m.Round(se/sw, 4)
		r.Emp, r.CErr = m.Round(sem/sw, 4), m.Round(sce/sw, 4)
	}
	if stw > 0 {
		r.Tps = m.Round(st/stw, 2)
	}
	return r, nil
}
