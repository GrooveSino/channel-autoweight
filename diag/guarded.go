package diag

import (
	"fmt"
	"sort"
	"strings"

	m "autoweight/model"
)

// GuardRule holds the reviewer's standing manual rules (see routine prompt).
type GuardRule struct {
	ThinN, ThinCap, ThinFloor int     // <ThinN real/1h: cap ThinCap, floor ThinFloor when 0 errors
	MainN                     int     // clean main: >=MainN real/1h and err<=MainErr is never lowered
	MainErr                   float64 //
	MainShare                 float64 // quiet hours: >=3*ThinN real and >=MainShare of all real/1h also counts as main
	Step                      int     // max change per round otherwise (errors in last 10m bypass)
	TailRatio, TailP90        float64 // clean main with success p90 >= TailRatio*p50 and >= TailP90 s may drop one Step
	TrialN                    int     // channels in an active 48h admission trial hold their trial weight until TrialN real/1h
	Trial                     map[int]bool
	TpsIdx                    map[int]float64 // model-normalised 1h TPS index (only channels with enough samples)
	TpsHold                   float64         // index below this: never raised this round (users wait on slow output)
	Est                       map[int]bool    // established channels (recent 60m peak >= est_n): a thin hour is a lull, not a newcomer
}

var DefaultGuard = GuardRule{ThinN: 30, ThinCap: 6, ThinFloor: 2, MainN: 200, MainErr: 0.02, MainShare: 0.35, Step: 2, TailRatio: 5, TailP90: 45, TrialN: 300, TpsHold: 0.5}

// GuardedProposal codifies the manual review the reviewer applies on top of the system
// target: thin channels ramp slowly (cap, min floor when clean), clean large-sample mains
// are not lowered, and other moves are limited to Step per round unless the channel had
// real errors in the last 10 minutes (fast drop). When no channel had any real request
// in the hour, every weight is held. rows must be real traffic.
// Diagnostic only: it prints a proposal next to the manual final, never writes.
func GuardedProposal(rows []m.Row, target, old map[int]int, now int64, g GuardRule) (map[int]int, map[int]string) {
	n, e, e10, eh := map[int]int{}, map[int]int{}, map[int]int{}, map[int]int{}
	ut := map[int][]int{}
	for _, r := range rows {
		if r.T < now-3600 {
			continue
		}
		n[r.Ch]++
		if r.Type == 2 {
			ut[r.Ch] = append(ut[r.Ch], r.Ut)
		}
		if r.Type == 5 {
			e[r.Ch]++
			if r.Uf == 0 {
				eh[r.Ch]++ // reliability errors (excludes 400 capability gaps)
			}
			// a 400 "feature not supported" (Row.Uf) is a capability gap for one client
			// type, not a reliability incident: it stays in the 1h error count but does
			// not trigger the 10-minute fast drop (step rule applies instead).
			if r.T >= now-600 && r.Uf == 0 {
				e10[r.Ch]++
			}
		}
	}
	total := 0
	for _, c := range n {
		total += c
	}
	isMain := func(id int) bool {
		if n[id] >= g.MainN {
			return true
		}
		return g.MainShare > 0 && n[id] >= 3*g.ThinN && float64(n[id]) >= g.MainShare*float64(total)
	}
	heavyTail := func(id int) (float64, float64, bool) {
		xs := append([]int(nil), ut[id]...)
		if g.TailRatio <= 0 || len(xs) < 10 {
			return 0, 0, false
		}
		sort.Ints(xs)
		p50, p90 := float64(xs[len(xs)/2]), float64(xs[len(xs)*9/10])
		return p50, p90, p50 > 0 && p90 >= g.TailP90 && p90 >= g.TailRatio*p50
	}
	out, why := map[int]int{}, map[int]string{}
	for id, t := range target {
		o, w := old[id], t
		switch {
		case total == 0:
			// quiet hour: no real request on any channel, so the ranking is probe-only;
			// hold every weight instead of letting the thin cap lower established ones.
			w = o
			if w != t {
				why[id] = "noreal hold"
			}
		case g.Trial[id] && e[id] == 0 && n[id] < g.TrialN && t < o:
			// 48h real-traffic trial after passing admission: a clean channel keeps its
			// trial weight; low trust from the short history must not zero it mid-trial.
			w, why[id] = o, fmt.Sprintf("trial hold n=%d", n[id])
		case g.Est[id] && n[id] < g.ThinN && eh[id] == 0:
			// off-peak lull on an established channel: the few real requests cannot rank
			// it, so keep the weight instead of applying the newcomer cap (THINCAPGAP).
			// 400 "feature not supported" rejections (Uf) are a client capability gap
			// that hits every channel alike, not a reliability signal, so they do not
			// break the hold.
			w = o
			if w != t {
				why[id] = fmt.Sprintf("lull hold n=%d", n[id])
			}
		case n[id] < g.ThinN:
			w = min(t, g.ThinCap, o+g.Step)
			if e[id] == 0 && w < g.ThinFloor {
				w = g.ThinFloor
			}
			why[id] = fmt.Sprintf("thin n=%d", n[id])
		case isMain(id) && float64(e[id]) <= g.MainErr*float64(n[id]) && t < o:
			if p50, p90, heavy := heavyTail(id); heavy {
				// clean but users wait p90>>p50: one step down instead of a hold
				w, why[id] = max(t, o-g.Step), fmt.Sprintf("clean main n=%d tail p90 %.0fs/%.0fs", n[id], p90, p50)
			} else {
				w, why[id] = o, fmt.Sprintf("clean main n=%d", n[id])
			}
		case e10[id] > 0 && t < o:
			why[id] = fmt.Sprintf("err10m=%d fast drop", e10[id])
		case t > o && g.TpsHold > 0 && g.TpsIdx[id] > 0 && g.TpsIdx[id] < g.TpsHold:
			// output speed is far below the same models on other channels: more traffic
			// here makes users wait longer per answer, so hold instead of ramping up.
			w, why[id] = o, fmt.Sprintf("tps hold %.2fx", g.TpsIdx[id])
		default:
			if w > o+g.Step {
				w = o + g.Step
			} else if w < o-g.Step {
				w = o - g.Step
			}
			if w != t {
				why[id] = fmt.Sprintf("step±%d", g.Step)
			}
		}
		out[id] = max(w, 0)
	}
	return out, why
}

// GuardedFlag renders the proposal as one diagnostic line.
func GuardedFlag(p map[int]int, why map[int]string) string {
	ids := []int{}
	for id := range p {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	parts := []string{}
	for _, id := range ids {
		s := fmt.Sprintf("%d=%d", id, p[id])
		if why[id] != "" {
			s += "(" + why[id] + ")"
		}
		parts = append(parts, s)
	}
	return "GUARDED 按人工复核规则（慢加量/主力不降/每轮步长）给的参考权重: " + strings.Join(parts, " ") + "；只记录"
}
