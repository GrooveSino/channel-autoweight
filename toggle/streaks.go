package toggle

import (
	"math"

	m "autoweight/model"
)

// Formal is one formal (dry_run=False) round as plan() consumes it.
type Formal struct {
	TsEpoch float64
	Scores  *m.OrderedScores
	N60     map[int]*int
	Err60   map[int]*float64
	Err60R  map[int]*float64 // v2.88 err60_real
	Old     map[int]int
	Probes  m.Probes
	Feats   m.Feats
	Up      m.Probes
}

// N60Of ports n60_of.
func N60Of(f *m.Feat, real bool) *int {
	if f == nil {
		return nil
	}
	if real && f.Mix != nil {
		return m.I(f.Mix.NUser)
	}
	if f.Long == nil {
		return nil
	}
	return m.I(f.Long.N)
}

// Err60Real ports err60_real (v2.88): mix_60m.err_rate_real when present; legacy
// records use 60m.err_rate only if the hour had no probe rows, else unknown.
func Err60Real(f *m.Feat) *float64 {
	if f == nil || f.Mix == nil {
		return nil
	}
	if f.Mix.HasErrRateReal {
		return f.Mix.ErrRateReal
	}
	if f.Mix.NProbe == 0 && f.Long != nil {
		return f.Long.ErrRate
	}
	return nil
}

func fromFeats(f m.Feats, real bool) (map[int]*int, map[int]*float64, map[int]*float64) {
	n, e, er := map[int]*int{}, map[int]*float64{}, map[int]*float64{}
	for k, v := range f {
		n[k] = N60Of(v, real)
		er[k] = Err60Real(v)
		if v != nil && v.Long != nil {
			e[k] = v.Long.ErrRate
		} else {
			e[k] = nil
		}
	}
	return n, e, er
}

// FormalFromRounds ports formal_from_rounds.
func FormalFromRounds(rs []*m.Rec, real bool) []*Formal {
	var out []*Formal
	for _, r := range rs {
		if r.IsDryRun(false) {
			continue
		}
		n, e, er := fromFeats(r.Features, real)
		sc := r.Decision.Scores
		var up m.Probes
		if r.Toggle != nil {
			up = r.Toggle.BottomUpTests
		}
		out = append(out, &Formal{TsEpoch: m.Epoch(r.TsTime), Scores: &sc, N60: n, Err60: e, Err60R: er, Old: r.Old,
			Probes: r.Probes, Feats: r.Features, Up: up})
	}
	return out
}

// FormalCurrent ports formal_current. scoreIDs is dec.scores key order.
func FormalCurrent(now float64, scores *m.OrderedScores, feats m.Feats, chans []m.Channel, probes m.Probes, real bool) *Formal {
	n, e, er := fromFeats(feats, real)
	old := map[int]int{}
	for _, c := range chans {
		old[c.ID] = c.Weight
	}
	if probes == nil {
		probes = m.Probes{}
	}
	return &Formal{TsEpoch: now, Scores: scores, N60: n, Err60: e, Err60R: er, Old: old, Probes: probes, Feats: feats}
}

// LastStreak ports last_streak → (rounds, span minutes).
func LastStreak(formal []*Formal, cid int, minN60 float64) (int, float64) {
	n := 0
	var tFirst, tLast float64
	haveLast := false
	for k := len(formal) - 1; k >= 0; k-- {
		r := formal[k]
		var elig []int
		for _, i := range r.Scores.Keys {
			if v := r.N60[i]; v != nil && float64(*v) >= minN60 {
				elig = append(elig, i)
			} else if v == nil && 0 >= minN60 {
				elig = append(elig, i)
			}
		}
		in := false
		for _, i := range elig {
			in = in || i == cid
		}
		if !in || len(elig) < 2 {
			break
		}
		lo := elig[0]
		for _, i := range elig[1:] {
			if m.Or(r.Scores.Vals[i], 0) < m.Or(r.Scores.Vals[lo], 0) {
				lo = i
			}
		}
		if lo != cid {
			break
		}
		n++
		tFirst = r.TsEpoch
		if !haveLast || tLast == 0 {
			tLast, haveLast = r.TsEpoch, true
		}
	}
	if n == 0 {
		return 0, 0.0
	}
	return n, m.Round((tLast-tFirst)/60, 1)
}

func oldIs(old map[int]int, cid int, w int) bool {
	v, ok := old[cid]
	return ok && v == w
}

// ErrStreak ports err_streak; realOnly (v2.88) uses err60_real and ignores probe failures.
func ErrStreak(formal []*Formal, cid int, errRate float64, probeFail, realOnly bool) int {
	n := 0
	for k := len(formal) - 1; k >= 0; k-- {
		r := formal[k]
		e := r.Err60[cid]
		if realOnly && r.Err60R != nil {
			e = r.Err60R[cid]
		}
		bad := (e != nil && *e >= errRate) || (probeFail && !realOnly && ProbeAllFailed(r.Probes[cid]))
		if bad && oldIs(r.Old, cid, 0) {
			n++
		} else {
			break
		}
	}
	return n
}

func ProbeRecovered(ps []m.Probe) bool {
	if len(ps) == 0 {
		return false
	}
	any := false
	for _, x := range ps {
		if !x.OK {
			return false
		}
		any = any || (x.Streaming != nil && *x.Streaming)
	}
	return any
}

// allUnusable: every probe failed or ttft >= s. Empty → nil.
func allUnusable(ps []m.Probe, s float64) *bool {
	if len(ps) == 0 {
		return nil
	}
	for _, x := range ps {
		if x.OK && x.Ttft != nil && *x.Ttft < s {
			return m.B(false)
		}
	}
	return m.B(true)
}

func UnusableStreak(formal []*Formal, cid int, s float64) int {
	n := 0
	for k := len(formal) - 1; k >= 0; k-- {
		r := formal[k]
		u := allUnusable(r.Probes[cid], s)
		if oldIs(r.Old, cid, 0) && u != nil && *u {
			n++
		} else {
			break
		}
	}
	return n
}

func UpUnusableStreak(formal []*Formal, cid int, s float64) int {
	n := 0
	for k := len(formal) - 1; k >= 0; k-- {
		r := formal[k]
		u := allUnusable(r.Up[cid], s)
		if oldIs(r.Old, cid, 0) && u != nil && *u {
			n++
		} else {
			break
		}
	}
	return n
}

func probeBufferedOnly(ps []m.Probe) bool {
	if len(ps) == 0 {
		return false
	}
	for _, x := range ps {
		if !x.OK || (x.Streaming != nil && *x.Streaming) {
			return false
		}
	}
	return true
}

func DormantStreak(formal []*Formal, cid int, minN60, maxW float64) int {
	n := 0
	for k := len(formal) - 1; k >= 0; k-- {
		r := formal[k]
		w, ok := r.Old[cid]
		nn := 0
		if v := r.N60[cid]; v != nil {
			nn = *v
		}
		if ok && float64(w) <= maxW && float64(nn) < minN60 && probeBufferedOnly(r.Probes[cid]) {
			n++
		} else {
			break
		}
	}
	return n
}

// PassStreak ports pass_streak over toggle history.
func PassStreak(hist []*m.ToggleRec, cid int) int {
	n := 0
	for k := len(hist) - 1; k >= 0; k-- {
		d, ok := hist[k].Disabled[cid]
		if !ok {
			continue
		}
		if d.Pass {
			n++
		} else {
			break
		}
	}
	return n
}

// HistoryFromRounds ports history_from_rounds (dedupe within gap minutes).
func HistoryFromRounds(rs []*m.Rec, now float64, gapMin float64) []*m.ToggleRec {
	type x struct {
		t  float64
		tg *m.ToggleRec
	}
	var out []x
	for _, r := range rs {
		if r.Toggle == nil {
			continue
		}
		t := m.Epoch(r.TsTime)
		if now-t < gapMin*60 {
			continue
		}
		if len(out) > 0 && t-out[len(out)-1].t < gapMin*60 {
			out[len(out)-1] = x{t, r.Toggle}
		} else {
			out = append(out, x{t, r.Toggle})
		}
	}
	res := make([]*m.ToggleRec, len(out))
	for k, v := range out {
		res[k] = v.tg
	}
	return res
}

var _ = math.Inf
