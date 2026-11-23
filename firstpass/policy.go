// Package firstpass evaluates a closed event window. It has no writer or persistent state.
package firstpass

import (
	"fmt"
	"math"
	"sort"
)

// Each stratum is a pre-routing workload class with a complete, same-priority pool.
type Stratum struct {
	Name                        string
	Pool                        []int
	Demand, DeadlineMS, FirstMS int
	Stream                      bool
	Path                        string
}
type Event struct {
	ID, User, Stratum, Route, Outcome string
	Channel, Attempt                  int
	End, Available                    int64
	TotalMS, VisibleMS                *int
}
type Input struct {
	Cutoff                  int64
	WindowSeconds, MinUsers int
	EquivalenceGap          float64
	Current, Caps           map[int]int
	Fixed                   []int
	Strata                  []Stratum
	Events                  []Event
}
type Row struct {
	Old, N, Unknown int
	Strength        *Milli `json:"target_strength"`
	Status          string
	Score           *float64
}
type Result struct {
	Algorithm string
	Cutoff    int64
	Channels  map[int]*Row
}
type count struct {
	n, y, u int
	users   map[string]int
}
type interval struct{ lo, hi float64 }

func Evaluate(in Input) (Result, error) {
	if !(0 < in.EquivalenceGap && in.EquivalenceGap < 1) {
		return Result{}, fmt.Errorf("explicit equivalence gap required")
	}
	r := Result{"first-pass-v1", in.Cutoff, map[int]*Row{}}
	if in.Cutoff <= 0 || in.WindowSeconds <= 0 || in.MinUsers < 2 || len(in.Current) == 0 {
		return r, fmt.Errorf("missing window, preimage or independent-user minimum")
	}
	fixed := map[int]bool{}
	for _, i := range in.Fixed {
		fixed[i] = true
	}
	for i, w := range in.Current {
		cap, ok := in.Caps[i]
		if !ok || cap < 0 || cap > 20 || w < 0 || w > 20 {
			return r, fmt.Errorf("invalid cap/preimage #%d", i)
		}
		r.Channels[i] = &Row{Old: w, Status: "abstain"}
		if fixed[i] {
			r.Channels[i].Status = "excluded"
		}
	}
	cells := map[string]map[int]*count{}
	specs := map[string]Stratum{}
	for _, s := range in.Strata {
		if _, dup := specs[s.Name]; dup || s.Name == "" || s.Demand <= 0 || len(s.Pool) == 0 {
			return r, fmt.Errorf("invalid stratum %q", s.Name)
		}
		specs[s.Name] = s
		cells[s.Name] = map[int]*count{}
		for _, i := range s.Pool {
			if r.Channels[i] == nil || cells[s.Name][i] != nil {
				return r, fmt.Errorf("invalid candidate #%d", i)
			}
			cells[s.Name][i] = &count{users: map[string]int{}}
		}
	}
	seen := make(map[string]int, len(in.Events))
	for idx, e := range in.Events {
		if e.End <= in.Cutoff-int64(in.WindowSeconds) || e.End > in.Cutoff || e.Available > in.Cutoff {
			continue
		}
		if e.Route != "random" || e.Attempt != 1 {
			continue
		}
		s := specs[e.Stratum]
		c := cells[e.Stratum][e.Channel]
		if e.ID == "" || e.User == "" || c == nil || e.Available < e.End {
			return r, fmt.Errorf("unidentifiable first attempt or invalid event time")
		}
		if p, ok := seen[e.ID]; ok {
			if !same(in.Events[p], e) {
				return r, fmt.Errorf("conflicting first attempt %s", e.ID)
			}
			continue
		}
		seen[e.ID] = idx
		c.n++
		c.users[e.User]++
		switch e.Outcome {
		case "ok", "tool":
			if e.TotalMS == nil || *e.TotalMS < 0 || s.Stream && (e.VisibleMS == nil || *e.VisibleMS < 0 || *e.VisibleMS > *e.TotalMS) {
				c.u++
			} else if *e.TotalMS <= s.DeadlineMS && (!s.Stream || *e.VisibleMS <= s.FirstMS) {
				c.y++
			}
		case "empty", "truncated", "timeout", "upstream", "rate_limit":
		case "unknown", "cancel":
			c.u++
		default:
			return r, fmt.Errorf("unrecognised outcome %q", e.Outcome)
		}
	}
	// Abstain when changing a bound weight would benefit one stratum at another's expense.
	votes := map[int][]int{}
	blocked := map[int]bool{}
	sums := map[int]float64{}
	mass := map[int]int{}
	refSum := map[int]float64{}
	ownSum := map[int]float64{}
	for _, s := range in.Strata {
		bounds := map[int]interval{}
		valid := s.DeadlineMS > 0 && (!s.Stream || s.FirstMS > 0) && (s.Path == "cache" || s.Path == "database")
		var best, second float64
		bestID := -1
		var maxLo, maxQ float64
		for _, i := range s.Pool {
			c := cells[s.Name][i]
			row := r.Channels[i]
			row.N += c.n
			row.Unknown += c.u
			if c.n == 0 || len(c.users) < in.MinUsers {
				valid = false
				continue
			}
			n := float64(c.n)
			var squares float64
			for _, k := range c.users {
				squares += float64(k) * float64(k)
			}
			rad := math.Sqrt(math.Log(2*float64(len(s.Pool)*len(in.Strata))/.05) * squares / (2 * n * n))
			bounds[i] = interval{math.Max(0, float64(c.y)/n-rad), math.Min(1, float64(c.y+c.u)/n+rad)}
			b := bounds[i]
			if b.hi > best {
				second, best, bestID = best, b.hi, i
			} else if b.hi > second {
				second = b.hi
			}
			maxLo = max(maxLo, b.lo)
			maxQ = max(maxQ, float64(c.y)/n)
			sums[i] += float64(s.Demand) * float64(c.y) / n
			mass[i] += s.Demand
		}
		if len(s.Pool) == 1 {
			continue
		}
		for _, i := range s.Pool {
			if !valid || maxQ == 0 {
				blocked[i] = true
				continue
			}
			b := bounds[i]
			high := best
			if i == bestID {
				high = second
			}
			lower, upper := maxLo-b.hi, high-b.lo
			w := 1
			switch {
			case lower > in.EquivalenceGap:
				w = 0
			case upper > in.EquivalenceGap:
				blocked[i] = true
				continue
			}
			votes[i] = append(votes[i], w)
			c := cells[s.Name][i]
			ownSum[i] += float64(s.Demand) * float64(c.y) / float64(c.n)
			refSum[i] += float64(s.Demand) * maxQ
		}
	}
	// Keep the entire competing component unchanged if one member has unresolved evidence.
	for i, v := range votes {
		for _, w := range v {
			if w != v[0] {
				blocked[i] = true
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, s := range in.Strata {
			unresolved := false
			for _, i := range s.Pool {
				unresolved = unresolved || blocked[i]
			}
			if unresolved {
				for _, i := range s.Pool {
					if !blocked[i] {
						blocked[i] = true
						changed = true
					}
				}
			}
		}
	}
	for i, row := range r.Channels {
		if mass[i] > 0 {
			q := sums[i] / float64(mass[i])
			row.Score = &q
		}
		if fixed[i] {
			continue
		}
		if v := votes[i]; !blocked[i] && len(v) > 0 {
			strength := Milli(1000)
			if v[0] == 0 {
				strength = Ratio(ownSum[i] / refSum[i])
			}
			row.Strength = ptr(strength)
			row.Status = "target"
			if strength == Milli(50*row.Old) {
				row.Status = "hold"
			}
		}
		cap := Milli(50 * in.Caps[i])
		if row.Strength != nil && *row.Strength > cap || row.Strength == nil && row.Old > in.Caps[i] {
			row.Strength = ptr(cap)
			row.Status = "operator-cap"
		}
	}
	return r, nil
}
func same(a, b Event) bool {
	eq := func(x, y *int) bool { return x == nil && y == nil || x != nil && y != nil && *x == *y }
	if !eq(a.TotalMS, b.TotalMS) || !eq(a.VisibleMS, b.VisibleMS) {
		return false
	}
	a.TotalMS = nil
	b.TotalMS = nil
	a.VisibleMS = nil
	b.VisibleMS = nil
	return a == b
}

// Shares returns local selector probabilities. Database ties retain supplied order.
func Shares(weights map[int]int, pool []int, path string) (map[int]float64, error) {
	if len(pool) == 0 || path != "cache" && path != "database" {
		return nil, fmt.Errorf("unknown pool/path")
	}
	ids := append([]int(nil), pool...)
	out := map[int]float64{}
	total := 0
	for _, id := range ids {
		w, ok := weights[id]
		_, dup := out[id]
		if !ok || dup || w < 0 || w > 1000000 {
			return nil, fmt.Errorf("invalid weight/pool")
		}
		out[id] = float64(w)
		if path == "database" {
			out[id] += 10
		}
		total += int(out[id])
	}
	if path == "database" && len(ids) > 1 {
		sort.SliceStable(ids, func(i, j int) bool { return weights[ids[i]] > weights[ids[j]] })
		out[ids[0]]++
		out[ids[len(ids)-1]]--
	}
	for id := range out {
		if total == 0 {
			out[id] = 1 / float64(len(ids))
		} else {
			out[id] /= float64(total)
		}
	}
	return out, nil
}
