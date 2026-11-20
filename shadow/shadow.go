// Package shadow ports shadow.py + autoweight/shadow.py: every 5 minutes a
// read-only score/suggestion point is appended to data/shadow.jsonl; gaps are
// backfilled by replaying the same logs at clock-aligned points (no probes).
// Trust evolves independently from the previous shadow point.
package shadow

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"time"

	"autoweight/decide"
	"autoweight/features"
	"autoweight/history"
	m "autoweight/model"
	"autoweight/trust"
)

var bj = time.FixedZone("CST", 8*3600)

// Params merges cfg.shadow over the defaults.
func Params(cfg m.M) m.M { return m.Merge(trust.ShadowDefaults, cfg.Sub("shadow")) }

// Record is one shadow.jsonl line (same keys as Python).
type Record struct {
	Ts       string               `json:"ts"`
	Mode     string               `json:"mode"`
	GapMin   *float64             `json:"gap_min"`
	Old      map[int]int          `json:"old"`
	Names    map[int]string       `json:"names"`
	Features m.Feats              `json:"features"`
	RealN    map[int]int          `json:"real_n"`
	Scores   map[int]*decide.Row  `json:"scores"`
	Order    []int                `json:"order"`
	Suggest  map[int]int          `json:"suggest"`
	Notes    []string             `json:"notes"`
	Trust    map[int]*trust.State `json:"trust"`
	Probed   map[int]string       `json:"probed"`
	Probes   m.Probes             `json:"probes"`
	TtftSrc  map[int]string       `json:"ttft_src"`
	Diff     map[int]int          `json:"diff"`
	Backfill bool                 `json:"backfill,omitempty"`
	epoch    float64
}

func ts(r *m.Rec) (float64, bool) {
	if r.TsTime.IsZero() {
		return 0, false
	}
	return m.Epoch(r.TsTime), true
}

// RealCounts: per-channel real (non-probe-token) requests in the last minutes.
func RealCounts(cfg m.M, rows []m.Row, now int64, minutes float64) map[int]int {
	out := map[int]int{}
	for _, x := range features.UserRows(cfg, rows) {
		if float64(x.T) >= float64(now)-minutes*60 {
			out[x.Ch]++
		}
	}
	return out
}

// LastProbe: latest probes for cid within maxAge minutes (recs ascending),
// skipping probes_reused rounds. Returns (nil, nil) when none.
func LastProbe(recs []*m.Rec, cid int, now, maxAge float64) ([]m.Probe, *float64) {
	for k := len(recs) - 1; k >= 0; k-- {
		r := recs[k]
		if r.ProbesReused {
			continue
		}
		ps := r.Probes[cid]
		if len(ps) == 0 {
			continue
		}
		t, ok := ts(r)
		if !ok {
			continue
		}
		age := (now - t) / 60
		if age >= 0 && age <= maxAge {
			return ps, m.F(m.Round(age, 1))
		}
		if age > maxAge {
			return nil, nil
		}
	}
	return nil, nil
}

// PlanProbes: channels whose real requests are below min_real_n and that were
// not probed within the cooldown.
func PlanProbes(cfg m.M, ids []int, counts map[int]int, recs []*m.Rec, now float64) map[int]string {
	p := Params(cfg)
	out := map[int]string{}
	for _, i := range ids {
		n := counts[i]
		if float64(n) >= p.F("min_real_n", 3) {
			continue
		}
		if _, age := LastProbe(recs, i, now, p.F("probe_cooldown_min", 30)); age != nil {
			continue
		}
		out[i] = fmt.Sprintf("%gm 真实请求 %d < %g", p.F("probe_window_min", 10), n, p.F("min_real_n", 3))
	}
	return out
}

var reuseRe = regexp.MustCompile(`^reuse ([0-9.]+)m`)

// ProbeAges: "probe" → 0, "reuse 12.3m" → 12.3, missing → absent.
func ProbeAges(src map[int]string) map[int]float64 {
	out := map[int]float64{}
	for k, v := range src {
		if v == "probe" {
			out[k] = 0
		} else if mm := reuseRe.FindStringSubmatch(v); mm != nil {
			out[k], _ = strconv.ParseFloat(mm[1], 64)
		}
	}
	return out
}

// BackfillSlots: clock-aligned points strictly between lastTs and now (each at
// least interval/2 from both ends), at most maxMin minutes back.
func BackfillSlots(lastTs, now float64, intervalMin, maxMin float64) []int64 {
	if lastTs <= 0 {
		return nil
	}
	step := int64(intervalMin * 60)
	half := step / 2
	start := lastTs
	if v := now - maxMin*60; v > start {
		start = v
	}
	var out []int64
	for t := (int64(start)/step + 1) * step; float64(t) < now-float64(half); t += step {
		if float64(t)-lastTs >= float64(half) {
			out = append(out, t)
		}
	}
	return out
}

// Input is everything Compute needs; Rows may extend past Now (backfill).
type Input struct {
	Cfg    m.M
	Chans  []m.Channel
	Rows   []m.Row
	Now    int64
	Shadow []*m.Rec // previous shadow records (ascending)
	Rounds []*m.Rec // last 10 formal round records
	All    []*m.Rec // full rounds history (newcomer detection)
	Fresh  m.Probes
	Plan   map[int]string
}

// Compute builds one shadow record at in.Now.
func Compute(in Input, backfill bool) (*Record, error) {
	cfg, sp := in.Cfg, Params(in.Cfg)
	now := float64(in.Now)
	var rows []m.Row
	for _, x := range in.Rows {
		if x.T <= in.Now {
			rows = append(rows, x)
		}
	}
	var srecs, rrecs, all []*m.Rec
	for _, r := range in.Shadow {
		if t, ok := ts(r); ok && t < now {
			srecs = append(srecs, r)
		}
	}
	for _, r := range in.Rounds {
		if t, ok := ts(r); ok && t <= now {
			rrecs = append(rrecs, r)
		}
	}
	for _, r := range in.All {
		if t, ok := ts(r); ok && t <= now {
			all = append(all, r)
		}
	}
	ids := make([]int, len(in.Chans))
	for k, c := range in.Chans {
		ids[k] = c.ID
	}
	feats := features.Build(cfg, in.Chans, rows, in.Now)
	counts := RealCounts(cfg, rows, in.Now, sp.F("probe_window_min", 10))
	merged := append(append([]*m.Rec{}, srecs...), rrecs...)
	sort.SliceStable(merged, func(a, b int) bool { return merged[a].TsTime.Before(merged[b].TsTime) })
	probes, src := m.Probes{}, map[int]string{}
	for _, i := range ids {
		if ps, ok := in.Fresh[i]; ok {
			probes[i], src[i] = ps, "probe"
		} else if ps, age := LastProbe(merged, i, now, sp.F("ttft_max_age_min", 90)); ps != nil {
			probes[i], src[i] = ps, fmt.Sprintf("reuse %vm", pyFloat(*age))
		} else {
			src[i] = "missing"
		}
	}
	tprev, tts := history.ShadowTrustPrev(srecs)
	if len(tprev) == 0 {
		tprev, tts = history.LastState(rrecs)
	}
	var gap *float64
	if tts != "" {
		if t, err := m.ParseTs(tts); err == nil {
			gap = m.F((now - m.Epoch(t)) / 60)
		}
	}
	tst := trust.ShadowStep(cfg, ids, feats, probes, tprev, gap, ProbeAges(src))
	old, names, tmap := map[int]int{}, map[int]string{}, map[int]float64{}
	for _, c := range in.Chans {
		old[c.ID], names[c.ID] = c.Weight, c.Name
	}
	for i, v := range tst {
		tmap[i] = v.Trust
	}
	nowT := time.Unix(in.Now, 0).In(bj)
	nc := decide.NewcomerIDs(cfg, in.Chans, all, nowT.Format("2006-01-02T15:04:05-07:00"))
	dec, err := decide.Decide(decide.Input{Cfg: cfg, Chans: in.Chans, Feats: feats, Probes: probes,
		Prev: old, Trust: tmap, TrustIDs: ids, Newcomers: nc, Now: nowT})
	if err != nil {
		return nil, err
	}
	rec := &Record{Ts: nowT.Format("2006-01-02T15:04:05-07:00"), Mode: "shadow", Old: old, Names: names,
		Features: feats, RealN: counts, Scores: dec.Scores, Order: dec.Order, Suggest: dec.Target, Notes: dec.Notes,
		Trust: tst, Probed: in.Plan, Probes: in.Fresh, TtftSrc: src, Diff: map[int]int{}, Backfill: backfill, epoch: now}
	if rec.Probed == nil {
		rec.Probed = map[int]string{}
	}
	if rec.Probes == nil {
		rec.Probes = m.Probes{}
	}
	if len(srecs) > 0 {
		if t, ok := ts(srecs[len(srecs)-1]); ok {
			rec.GapMin = m.F(m.Round((now-t)/60, 1))
		}
	}
	for _, i := range ids {
		if dec.Target[i] != old[i] {
			rec.Diff[i] = dec.Target[i] - old[i]
		}
	}
	return rec, nil
}

// AsRec converts a fresh record into the history shape so later points in the
// same run (backfill) see it.
func (r *Record) AsRec() *m.Rec {
	tr := map[int]m.TrustRec{}
	for i, v := range r.Trust {
		tr[i] = m.TrustRec{Trust: v.Trust}
	}
	return &m.Rec{Ts: r.Ts, Mode: "shadow", Probes: r.Probes, Trust: tr, Old: r.Old,
		TsTime: time.Unix(int64(r.epoch), 0).In(bj)}
}

// pyFloat renders like Python str(float): 2.0, 12.3.
func pyFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	for _, c := range s {
		if c == '.' {
			return s
		}
	}
	return s + ".0"
}
