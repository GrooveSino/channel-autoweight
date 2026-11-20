// Package features computes per-channel window metrics from log rows
// (port of autoweight/features.py). Pure functions: rows in, Feats out.
package features

import (
	"sort"

	m "autoweight/model"
)

func pctF(xs []float64, q float64) *float64 { return m.Pct(xs, q) }

// NormLat ports norm_lat: (n, p50, p90) of ut*ref_ct/ct over ok rows with ct>=min_ct.
func NormLat(ok []m.Row, minCt, refCt float64) (int, *float64, *float64) {
	var v []float64
	for _, x := range ok {
		if float64(x.Ct) >= minCt {
			v = append(v, float64(x.Ut)*refCt/float64(x.Ct))
		}
	}
	if len(v) == 0 {
		return 0, nil, nil
	}
	return len(v), m.F(m.Round(*pctF(v, .5), 2)), m.F(m.Round(*pctF(v, .9), 2))
}

// LogStream ports log_stream (min_chunks=5, buf_span_s=0.3).
func LogStream(ok []m.Row) (int, *float64) {
	n, b := 0, 0
	for _, x := range ok {
		if x.Chunks != nil && *x.Chunks >= 5 && x.Span != nil {
			n++
			if *x.Span < 0.3 {
				b++
			}
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, m.F(m.Round(float64(b)/float64(n), 4))
}

func inInts(v int, xs ...int) bool {
	for _, x := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// IsClientErr: type=5 and 4xx except 408/429.
// v5.00: also content-classified client failures (Row.Cl: adaptive thinking
// required, client connection closed, invalid signature, unsupported parameter),
// which the [ReverixAI] error format reports without a status code.
// v5.01: also Row.Uf / Row.Ec, now matched with or without status_code=400.
func IsClientErr(x m.Row) bool {
	if x.Type != 5 {
		return false
	}
	if x.Cl != 0 {
		return true
	}
	// v5.01: "upstream does not support this feature" and replayed encrypted
	// content are client-side whatever the format; [ReverixAI] has no status_code.
	if x.Uf != 0 || x.Ec != 0 {
		return true
	}
	return x.Sc != nil && *x.Sc >= 400 && *x.Sc < 500 && !inInts(*x.Sc, 408, 429)
}

// IsFollowErr ports is_follow_err.
func IsFollowErr(x m.Row, anySc bool) bool {
	if !anySc {
		return IsClientErr(x)
	}
	if x.Type != 5 {
		return false
	}
	return x.Sc == nil || !inInts(*x.Sc, 408, 429, 504, 524)
}

func errBurst(r []m.Row) (int, *float64, *int) {
	var et []int64
	for _, x := range r {
		if x.Type == 5 {
			et = append(et, x.T)
		}
	}
	if len(et) == 0 {
		return 0, nil, nil
	}
	sort.Slice(et, func(i, j int) bool { return et[i] < et[j] })
	last := et[len(et)-1]
	ok := 0
	for _, x := range r {
		if x.Type == 2 && x.T > last {
			ok++
		}
	}
	return len(et), m.F(float64(last - et[0])), m.I(ok)
}

// Window ports features.window(rows, now, minutes, slow_s, ln).
func Window(rows []m.Row, now int64, minutes int, slowS float64, ln m.M) *m.Window {
	var r, ok []m.Row
	for _, x := range rows {
		if x.T >= now-int64(minutes)*60 {
			r = append(r, x)
		}
	}
	var ut []float64
	n, errN := len(r), 0
	for _, x := range r {
		if x.Type == 2 {
			ok = append(ok, x)
			ut = append(ut, float64(x.Ut))
		} else if x.Type == 5 {
			errN++
		}
	}
	span := int64(minutes) * 60
	sumUt := 0.0
	for _, u := range ut {
		sumUt += u
	}
	avgC := 0.0
	if span != 0 {
		avgC = sumUt / float64(span)
	}
	peak := 0
	for s := now - span; s < now; s += 10 {
		c := 0
		for _, x := range ok {
			if x.T-int64(x.Ut) <= s && s <= x.T {
				c++
			}
		}
		if c > peak {
			peak = c
		}
	}
	var ctok []float64
	empty := 0
	for _, x := range ok {
		ctok = append(ctok, float64(x.Ct))
		if x.Ct == 0 && x.Ue == 0 {
			empty++
		}
	}
	w := &m.Window{N: n, Err: errN, AvgConc: m.Round(avgC, 2), PeakConc: peak}
	if len(ok) > 0 {
		w.Empty = m.I(empty)
	}
	if ln == nil {
		ln = m.M{}
	}
	w.Nn, w.Np50, w.Np90 = NormLat(ok, ln.F("min_ct", 100), ln.F("ref_ct", 500))
	var lt []float64
	for _, x := range ok {
		if x.Frt != nil {
			lt = append(lt, *x.Frt)
		}
	}
	w.LsN, w.LsBuf = LogStream(ok)
	hasSc := errN == 0
	for _, x := range r {
		if x.Type == 5 && x.Sc != nil {
			hasSc = true
		}
	}
	if hasSc {
		c, t := 0, 0
		for _, x := range r {
			if IsClientErr(x) {
				c++
			}
			if x.Type == 5 && x.Sc != nil && inInts(*x.Sc, 524, 504, 408) {
				t++
			}
		}
		w.ErrClient, w.ErrTimeout = m.I(c), m.I(t)
	}
	_, w.EbSpan, w.EbOkAfter = errBurst(r)
	slowOk := 0
	for _, u := range ut {
		if u > slowS {
			slowOk++
		}
	}
	et := m.OrI(w.ErrTimeout, 0)
	if den := len(ut) + et; den > 0 && w.ErrTimeout != nil {
		w.SlowRateT = m.F(m.Round(float64(slowOk+et)/float64(den), 4))
	}
	w.LtN, w.Lt50, w.Lt90 = len(lt), pctF(lt, .5), pctF(lt, .9)
	if n > 0 {
		w.ErrRate = m.F(m.Round(float64(errN)/float64(n), 4))
		if w.Empty != nil {
			w.EmptyRate = m.F(m.Round(float64(*w.Empty)/float64(n), 4))
		}
	}
	w.P50, w.P90 = pctF(ut, .5), pctF(ut, .9)
	if len(ut) > 0 {
		w.SlowRate = m.F(m.Round(float64(slowOk)/float64(len(ut)), 4))
	}
	w.CtokP50 = pctF(ctok, .5)
	return w
}

func bucket(ct int) int {
	for k, e := range []int{150, 400, 900} {
		if ct < e {
			return k
		}
	}
	return 3
}

type MR struct {
	N        int
	R50, R90 float64
}

// MatchedRatios ports matched_ratios (workload-matched latency ratios).
func MatchedRatios(rows []m.Row, t0, t1 int64, minN int) map[int]MR {
	var ok []m.Row
	for _, x := range rows {
		if t0 <= x.T && x.T < t1 && x.Type == 2 && x.Ct > 0 {
			ok = append(ok, x)
		}
	}
	pool := map[int][]float64{}
	for _, x := range ok {
		pool[bucket(x.Ct)] = append(pool[bucket(x.Ct)], float64(x.Ut))
	}
	base := map[int]float64{}
	for b, v := range pool {
		base[b] = *pctF(v, .5)
	}
	by := map[int][]float64{}
	var allr []float64
	for _, x := range ok {
		bb := base[bucket(x.Ct)]
		if bb == 0 {
			continue
		}
		r := float64(x.Ut) / bb
		by[x.Ch] = append(by[x.Ch], r)
		allr = append(allr, r)
	}
	out := map[int]MR{}
	if len(allr) == 0 {
		return out
	}
	g50, g90 := m.OrZ(pctF(allr, .5), 1), m.OrZ(pctF(allr, .9), 1)
	for ch, v := range by {
		if len(v) >= minN {
			out[ch] = MR{len(v), m.Round(*pctF(v, .5)/g50, 4), m.Round(*pctF(v, .9)/g90, 4)}
		}
	}
	return out
}
