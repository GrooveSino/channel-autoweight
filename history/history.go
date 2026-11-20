// Package history reads rounds.jsonl / shadow.jsonl records (already parsed
// into model.Rec) and derives the state run.py carries between rounds.
package history

import (
	"math"

	m "autoweight/model"
	"autoweight/trust"
)

// LastState ports trust.last_state: latest formal round's trust map.
func LastState(hist []*m.Rec) (map[int]float64, string) {
	for k := len(hist) - 1; k >= 0; k-- {
		r := hist[k]
		if len(r.Trust) > 0 && !r.IsDryRun(true) {
			out := map[int]float64{}
			for i, v := range r.Trust {
				out[i] = v.Trust
			}
			return out, r.Ts
		}
	}
	return map[int]float64{}, ""
}

// Bootstrap ports trust.bootstrap (replay formal rounds).
func Bootstrap(cfg m.M, hist []*m.Rec) (map[int]float64, string) {
	st := map[int]float64{}
	last := ""
	var lastT float64
	for _, r := range hist {
		if r.IsDryRun(true) {
			continue
		}
		ids := m.SortedIDs(r.Features)
		t := m.Epoch(r.TsTime)
		var gap *float64
		if last != "" {
			gap = m.F((t - lastT) / 60)
		}
		probes := r.Probes
		if probes == nil {
			probes = m.Probes{}
		}
		res := trust.Step(cfg, ids, r.Features, probes, st, gap)
		st = map[int]float64{}
		for i, v := range res {
			st[i] = v.Trust
		}
		last, lastT = r.Ts, t
	}
	return st, last
}

// ShadowTrustPrev ports shadow.trust_prev over mode=shadow records.
func ShadowTrustPrev(recs []*m.Rec) (map[int]float64, string) {
	for k := len(recs) - 1; k >= 0; k-- {
		r := recs[k]
		if r.Mode != "shadow" || len(r.Trust) == 0 {
			continue
		}
		out := map[int]float64{}
		for i, v := range r.Trust {
			out[i] = v.Trust
		}
		return out, r.Ts
	}
	return map[int]float64{}, ""
}

// Reuse ports probe.reuse: latest record with probes within maxAge minutes.
func Reuse(rounds []*m.Rec, ids []int, now, maxAge float64) m.Probes {
	in := map[int]bool{}
	for _, i := range ids {
		in[i] = true
	}
	for k := len(rounds) - 1; k >= 0; k-- {
		r := rounds[k]
		if len(r.Probes) == 0 {
			continue
		}
		age := (now - m.Epoch(r.TsTime)) / 60
		if age < 0 || age > maxAge {
			return m.Probes{}
		}
		out := m.Probes{}
		for i, v := range r.Probes {
			if in[i] {
				out[i] = v
			}
		}
		return out
	}
	return m.Probes{}
}

// ReuseAge ports probe.reuse_age (nil when none).
func ReuseAge(rounds []*m.Rec, now float64) *float64 {
	for k := len(rounds) - 1; k >= 0; k-- {
		r := rounds[k]
		if len(r.Probes) == 0 || r.ProbesReused {
			continue
		}
		return m.F(math.Max(0, (now-m.Epoch(r.TsTime))/60))
	}
	return nil
}

// LastToggle: latest record carrying a toggle block.
func LastToggle(hist []*m.Rec) *m.Rec {
	for k := len(hist) - 1; k >= 0; k-- {
		if hist[k].Toggle != nil {
			return hist[k]
		}
	}
	return nil
}
