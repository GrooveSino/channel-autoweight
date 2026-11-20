// Package core is the thin orchestrator: it wires fetch → features → trust →
// decide → toggle → apply using plain structs only. It mirrors run.py's
// core path (diagnostics excluded).
package core

import (
	"errors"
	"fmt"
	"time"

	"autoweight/apply"
	"autoweight/decide"
	"autoweight/diag"
	"autoweight/features"
	"autoweight/fetch"
	"autoweight/history"
	m "autoweight/model"
	"autoweight/probe"
	"autoweight/toggle"
	"autoweight/trust"
)

// Prober supplies probe results: probe.Live (real calls), Replay (snapshots)
// or NoProber (refuses; the default unless -live-probe is given).
type Prober interface {
	// Site returns site probes for ids and whether they are reused copies.
	Site(ids []int) (m.Probes, bool, error)
	// Upstream returns n direct upstream tests for one channel.
	Upstream(cid, n int) ([]m.Probe, error)
}

var ErrNoProbe = errors.New("live probing disabled; pass -live-probe, or use -no-probe / -reuse-probe")

// NoProber is the live-mode prober.
type NoProber struct{}

func (NoProber) Site([]int) (m.Probes, bool, error)   { return nil, false, ErrNoProbe }
func (NoProber) Upstream(int, int) ([]m.Probe, error) { return nil, ErrNoProbe }

type Options struct {
	Apply, ApplyToggle, NoProbe bool
	ReuseProbe                  float64
	Strategy                    string
	Override, Final, Expect     string
	Reason                      string
}

type Env struct {
	Cfg    m.M
	Src    fetch.Source
	Probe  Prober
	Rounds []*m.Rec           // full rounds.jsonl (read-only)
	Shadow []*m.Rec           // full shadow.jsonl (read-only)
	Manual []diag.ManualWrite // hand edits outside -apply (data/manual.jsonl, diag only)
	Trials map[int]int64      // channels in an active admission trial -> pass time (diag only)
	Writer apply.Writer
}

// Result is the round record (Go side). Exit 2 = drift abort.
type Result struct {
	Version      string                   `json:"version,omitempty"`
	Ts           string                   `json:"ts"`
	Now          int64                    `json:"now"`
	Channels     []m.Channel              `json:"channels"`
	Old          map[int]int              `json:"old"`
	Features     m.Feats                  `json:"features"`
	Probes       m.Probes                 `json:"probes"`
	ProbesReused bool                     `json:"probes_reused"`
	Rechecked    []int                    `json:"rechecked"`
	ProbeAnom    map[int][]string         `json:"probe_anomalies"`
	Trust        map[int]*trust.State     `json:"trust"`
	TrustSource  string                   `json:"trust_source"`
	Newcomers    []int                    `json:"newcomers"`
	Decision     *decide.Decision         `json:"decision"`
	System       map[int]int              `json:"system_target"`
	Override     map[string]any           `json:"override,omitempty"`
	Drift        decide.Drift             `json:"drift,omitempty"`
	Changed      map[int]int              `json:"changed"`
	Toggle       *toggle.Plan             `json:"toggle"`
	AccessDenied map[int]*toggle.AD       `json:"access_denied"`
	ADStreak     map[int]*toggle.ADStreak `json:"access_denied_streak"`
	Applied      bool                     `json:"applied"`
	ApplySQL     string                   `json:"apply_sql,omitempty"`
	DryRun       bool                     `json:"dry_run"`
	Notes        []string                 `json:"notes"`
	PairFlags    []string                 `json:"tokenpair_flags,omitempty"`
	ExitCode     int                      `json:"-"`
}

func idsOf(cs []m.Channel) []int {
	out := make([]int, len(cs))
	for k, c := range cs {
		out[k] = c.ID
	}
	return out
}

// within returns rounds stamped within d before now. rounds.jsonl holds 2-3 records per
// round (dry-run, apply, verify), so a count-based tail(8) covered only ~2h, not ~6h;
// a time window keeps the established-channel peak honest. Unstamped records are kept.
func within(rs []*m.Rec, now time.Time, d time.Duration) []*m.Rec {
	out := []*m.Rec{}
	for _, r := range rs {
		if r.TsTime.IsZero() || !r.TsTime.Before(now.Add(-d)) {
			out = append(out, r)
		}
	}
	return out
}

func tail(rs []*m.Rec, n int) []*m.Rec {
	if len(rs) > n {
		return rs[len(rs)-n:]
	}
	return rs
}

// Run executes one round.
func Run(env Env, o Options) (*Result, error) {
	cfg := env.Cfg
	if err := decide.Guard(cfg); err != nil {
		return nil, err
	}
	if o.Override != "" && o.Final != "" {
		return nil, errors.New("--override 与 --final 互斥")
	}
	chans, err := env.Src.Channels()
	if err != nil {
		return nil, err
	}
	chans = m.ExcludeChans(cfg, chans)
	ids := idsOf(chans)
	wmax := 0
	for _, w := range cfg.Ints("windows_min") {
		wmax = max(wmax, w)
	}
	now, rows, err := env.Src.Logs(ids, wmax)
	if err != nil {
		return nil, err
	}
	nowF := float64(now)
	nowT := time.Unix(now, 0).In(time.FixedZone("CST", 8*3600))
	feats := features.Build(cfg, chans, rows, now)
	hist := tail(env.Rounds, 24)
	r := &Result{Version: m.Version, Now: now, Ts: nowT.Format("2006-01-02T15:04:05-07:00"), Channels: chans, Features: feats,
		Old: map[int]int{}, DryRun: !o.Apply, Notes: []string{}}
	for _, c := range chans {
		r.Old[c.ID] = c.Weight
	}
	// v5.01 same-token paired latency over the decision and reference windows.
	ref := rows
	if pc := cfg.Sub("pair_slow"); pc.B("enabled", false) {
		if mins := pc.I("ref_minutes", 1440); mins > wmax {
			if _, long, err := env.Src.Logs(ids, mins); err == nil {
				ref = long
			} else {
				r.Notes = append(r.Notes, fmt.Sprintf("同令牌配对参考窗口 %dmin 取数失败，只用决策窗口：%v", mins, err))
			}
		}
		features.AttachPair(cfg, feats, ref, now)
	}
	// probes
	var probes m.Probes
	reused := false
	if o.ReuseProbe > 0 {
		probes = history.Reuse(hist, ids, nowF, o.ReuseProbe)
		reused = len(probes) > 0
	}
	if !reused && !o.NoProbe {
		probes, reused, err = env.Probe.Site(ids)
		if err != nil {
			return nil, err
		}
	}
	if probes == nil {
		probes = m.Probes{}
	}
	r.Probes, r.ProbesReused = probes, reused
	r.Rechecked = []int{}
	if rr, ok := env.Probe.(interface{ RecheckedIDs() []int }); ok && !reused {
		r.Rechecked = rr.RecheckedIDs()
	}
	r.ProbeAnom = probe.Anomalies(probes)
	// trust
	r.Trust, r.TrustSource = stepTrust(cfg, env, ids, feats, probes, reused, nowF)
	tmap := map[int]float64{}
	for i, v := range r.Trust {
		tmap[i] = v.Trust
	}
	// decide
	nc := decide.NewcomerIDs(cfg, chans, env.Rounds, nowT.Format("2006-01-02T15:04:05.000000-07:00"))
	r.Newcomers = m.SortedIDs(nc)
	dec, err := decide.Decide(decide.Input{Cfg: cfg, Chans: chans, Feats: feats, Probes: probes, Strategy: o.Strategy,
		Prev: r.Old, Trust: tmap, TrustIDs: ids, Newcomers: nc, Now: nowT})
	if err != nil {
		return nil, err
	}
	r.Decision = dec
	// TOKENPAIR (diagnostic only): does the system top have same-user evidence?
	r.PairFlags = pairFlags(env, cfg, ids, rows, ref, now, wmax, dec.Target)
	r.PairFlags = append(r.PairFlags, emptyFlags(cfg, rows)...)
	r.PairFlags = append(r.PairFlags, thinCapFlags(cfg, rows, dec.Target, nowT.Unix(), within(env.Rounds, nowT, estWindow(cfg)))...)
	r.PairFlags = append(r.PairFlags, mainZeroFlags(cfg, rows, dec.Target, nowT.Unix())...)
	r.PairFlags = append(r.PairFlags, recoveredFlags(cfg, rows, dec.Target, nowT.Unix())...)
	tg := cfg.Sub("score").Sub("tps")
	r.PairFlags = append(r.PairFlags, features.TpsFlags(feats, tg.I("min_n", 20), tg.F("flag_idx", 0.7))...)
	r.PairFlags = append(r.PairFlags, longTailFlags(cfg, rows, dec.Target, nowT.Unix())...)
	r.PairFlags = append(r.PairFlags, slowDriftFlags(env, cfg, ids, rows, dec.Target, nowT.Unix())...)
	r.PairFlags = append(r.PairFlags, hangTailFlags(cfg, rows, dec.Target, nowT.Unix())...)
	r.PairFlags = append(r.PairFlags, diag.ContentSafeFlags(features.UserRows(cfg, rows), dec.Target, nowT.Unix()-3600, int(cfg.Sub("content_safe").F("min_n", 3)))...)
	r.PairFlags = append(r.PairFlags, diag.UnsupportedFlags(features.UserRows(cfg, rows), dec.Target, nowT.Unix()-3600, int(cfg.Sub("unsupported").F("min_n", 1)))...)
	r.PairFlags = append(r.PairFlags, diag.ExtWriteFlags(env.Rounds, r.Old, env.Manual)...)
	r.PairFlags = append(r.PairFlags, diag.BusyFlags(features.UserRows(cfg, rows), dec.Target, nowT.Unix()-3600, nowT.Unix(), int(cfg.Sub("busy").F("min_n", 1)))...)
	r.PairFlags = append(r.PairFlags, diag.CrossEncFlags(features.UserRows(cfg, rows), dec.Target, nowT.Unix()-3600, int64(cfg.Sub("crossenc").F("lookback_s", 1800)))...)
	lq := cfg.Sub("load_queue")
	r.PairFlags = append(r.PairFlags, diag.LoadQueueFlags(features.UserRows(cfg, rows), dec.Target, nowT.Unix()-3600, int(lq.F("hi_conc", 5)), int(lq.F("min_n", 10)), lq.F("ratio", 2))...)
	mg := cfg.Sub("model_gap")
	r.PairFlags = append(r.PairFlags, diag.ModelGapFlags(features.UserRows(cfg, rows), dec.Target, nowT.Unix()-3600, int(mg.F("min_n", 8)), mg.F("ratio", 2.5))...)
	rc := cfg.Sub("retry_cost")
	r.PairFlags = append(r.PairFlags, diag.RetryCostFlags(features.ChanRows(cfg, rows), dec.Target, nowT.Unix()-3600, int64(rc.F("win", 120)), int(rc.F("min_n", 2)))...)
	tb := cfg.Sub("token_ban")
	r.PairFlags = append(r.PairFlags, diag.TokenBanFlags(features.ChanRows(cfg, rows), dec.Target, nowT.Unix()-3600, int(tb.F("min_n", 5)), tb.F("share", 0.8))...)
	sk := cfg.Sub("sticky")
	st := diag.AnalyzeSticky(features.UserRows(cfg, rows), r.Old, nowT.Unix()-3600)
	r.PairFlags = append(r.PairFlags, diag.StickyFlags(st, int(sk.F("min_n", 30)), sk.F("min_aff", 0.7), sk.F("ratio", 2))...)
	r.PairFlags = append(r.PairFlags, diag.FreshFlags(st, int(sk.F("min_fresh", 10)))...)
	r.PairFlags = append(r.PairFlags, diag.NoRealFlags(st, dec.Target, r.Old, int(sk.F("noreal_step", 2)))...)
	ps := cfg.Sub("probe_slow")
	r.PairFlags = append(r.PairFlags, diag.ProbeSlowFlags(rows, int(ps.F("token", 1318)), dec.Target, nowT.Unix()-3600, int(ps.F("min_n", 5)), ps.F("ratio", 2.5))...)
	r.PairFlags = append(r.PairFlags, diag.ProbeDropFlags(features.UserRows(cfg, rows), dec.Target, r.Old, nowT.Unix()-3600, int(ps.F("drop_min_real", 30)))...)
	r.PairFlags = append(r.PairFlags, diag.ProbeTpsFlags(rows, int(ps.F("token", 1318)), dec.Target, nowT.Unix()-3600, int(ps.F("min_n", 5)), ps.F("ratio", 2.5))...)
	r.PairFlags = append(r.PairFlags, diag.EmptyStreamFlags(env.Rounds, dec.Target, nowF, ps.F("empty_window_min", 120))...)
	r.PairFlags = append(r.PairFlags, diag.SuggestFlipFlags(env.Shadow, dec.Target, nowF, 60, 3, 10, 2)...)
	tgc := cfg.Sub("toggle")
	r.PairFlags = append(r.PairFlags, diag.StaleCfgFlags(cfg, ids, []string{"capacity", "reenable", "channels", "overrides"})...)
	r.PairFlags = append(r.PairFlags, diag.FloorFlags(len(ids), int(tgc.F("min_enabled", 4)), int(tgc.F("hard_min", 3)))...)
	gr := diag.DefaultGuard
	gr.Trial = map[int]bool{}
	for ch := range env.Trials {
		gr.Trial[ch] = true
	}
	gr.TpsIdx = map[int]float64{}
	for id, f := range feats {
		if f != nil && f.Tps != nil && f.Tps.N >= tg.I("min_n", 20) {
			gr.TpsIdx[id] = f.Tps.Idx
		}
	}
	tcg := cfg.Sub("thin_cap_gap")
	gr.Est = map[int]bool{}
	for id, pk := range diag.PeakN(within(env.Rounds, nowT, estWindow(cfg))) {
		gr.Est[id] = pk >= int(tcg.F("est_n", 60))
	}
	gp, gw := diag.GuardedProposal(features.UserRows(cfg, rows), dec.Target, r.Old, nowT.Unix(), gr)
	r.PairFlags = append(r.PairFlags, diag.GuardedFlag(gp, gw))
	// access-denied (feeds toggle acct upgrade)
	r.AccessDenied, r.ADStreak = map[int]*toggle.AD{}, map[int]*toggle.ADStreak{}
	if cfg.Sub("access_denied").B("enabled", true) {
		r.AccessDenied = toggle.AccessDenied(probes, dec.Target, r.Old)
		if len(r.AccessDenied) > 0 {
			r.ADStreak = toggle.AccessDeniedStreak(env.Rounds, r.AccessDenied, nowF)
		}
	}
	// toggle
	if cfg.Sub("toggle").B("enabled", false) && !o.NoProbe {
		r.Toggle, err = stepToggle(env, o, r, ids, nowF)
		if err != nil {
			r.Notes = append(r.Notes, "TOGGLE skipped: "+err.Error())
			r.Toggle = nil
		}
	}
	// manual override / final / drift
	if err := manual(cfg, o, r); err != nil {
		return nil, err
	}
	if r.ExitCode != 0 {
		return r, nil
	}
	r.Changed = map[int]int{}
	for i, w := range dec.Target {
		if old, ok := r.Old[i]; !ok || old != w {
			r.Changed[i] = w
		}
	}
	return r, doApply(env, o, r, ids)
}

func stepTrust(cfg m.M, env Env, ids []int, feats m.Feats, probes m.Probes, reused bool, now float64) (map[int]*trust.State, string) {
	tc := cfg.Sub("trust")
	if tc.S("source", "") == "shadow" {
		var sh []*m.Rec
		for _, x := range env.Shadow {
			if x.Mode == "shadow" {
				sh = append(sh, x)
			}
		}
		st, sts := history.ShadowTrustPrev(sh)
		if len(st) > 0 && sts != "" {
			if t, err := m.ParseTs(sts); err == nil {
				sgap := (now - m.Epoch(t)) / 60
				if sgap >= 0 && sgap <= tc.F("shadow_max_age_min", 15) {
					ra := m.F(0)
					if reused {
						ra = history.ReuseAge(env.Rounds, now)
					}
					ages := map[int]float64{}
					for i := range probes {
						if ra == nil {
							ages[i] = 999
						} else {
							ages[i] = *ra
						}
					}
					src := fmt.Sprintf("shadow %s +%.1fm", sts[11:16], sgap)
					return trust.ShadowStep(cfg, ids, feats, probes, st, &sgap, ages), src
				}
			}
		}
	}
	prev, ts := history.LastState(env.Rounds)
	if len(prev) == 0 {
		prev, ts = history.Bootstrap(cfg, env.Rounds)
	}
	var gap *float64
	if ts != "" {
		if t, err := m.ParseTs(ts); err == nil {
			gap = m.F((now - m.Epoch(t)) / 60)
		}
	}
	return trust.Step(cfg, ids, feats, probes, prev, gap), "rounds"
}

func doApply(env Env, o Options, r *Result, ids []int) error {
	if !o.Apply {
		return nil
	}
	p := apply.Plan{EnabledIDs: ids}
	if len(r.Changed) > 0 {
		p.Weights = r.Changed
	}
	if o.ApplyToggle && r.Toggle != nil && len(r.Toggle.Actions) > 0 {
		p.Toggles = r.Toggle.Actions
	}
	if env.Writer == nil {
		return errors.New("apply requested but no writer configured")
	}
	res, err := apply.Apply(env.Writer, env.Cfg, r.Channels, p)
	if err != nil {
		return err
	}
	r.ApplySQL, r.Applied = res.SQL, res.WeightsDone
	if r.Toggle != nil && p.Toggles != nil {
		r.Toggle.Applied, r.Toggle.Rejected = res.TogglesDone, res.TogglesReject
	}
	return nil
}

// pairFlags runs TOKENPAIR over a longer window (cfg tokenpair_minutes, default 180; 1h is
// too thin to pair tokens). Falls back to the decision rows if the longer fetch fails.
func pairFlags(env Env, cfg m.M, ids []int, rows, ref []m.Row, now int64, wmax int, target map[int]int) []string {
	if mins := int(cfg.F("tokenpair_minutes", 180)); mins > wmax {
		if len(ref) > len(rows) && cfg.Sub("pair_slow").I("ref_minutes", 1440) >= mins {
			// v5.01: the pair_slow reference fetch already covers this window
			rows = nil
			for _, x := range ref {
				if x.T >= now-int64(mins)*60 {
					rows = append(rows, x)
				}
			}
		} else if _, long, err := env.Src.Logs(ids, mins); err == nil {
			rows = long
		}
	}
	return diag.TokenPairFlags(diag.AnalyzeTokenPair(features.UserRows(cfg, rows), diag.DefaultTokenPair), target, 30)
}

// emptyFlags runs EMPTYPAIR over the decision window rows (diagnostic only).
func emptyFlags(cfg m.M, rows []m.Row) []string {
	return diag.EmptyPairFlags(diag.AnalyzeEmptyPair(features.UserRows(cfg, rows), diag.DefaultEmptyPair), 10, 0.25)
}

// estWindow is thin_cap_gap.est_hours (default 24h) of round history for the peak.
func estWindow(cfg m.M) time.Duration {
	return time.Duration(cfg.Sub("thin_cap_gap").F("est_hours", 24) * float64(time.Hour))
}

// thinCapFlags runs THINCAPGAP (cfg thin_cap_gap: min_n 30, cap 6; diagnostic only).
// Peak 60m n over the last est_hours (~24h) of rounds >= est_n marks an established channel in a lull.
func thinCapFlags(cfg m.M, rows []m.Row, target map[int]int, now int64, recent []*m.Rec) []string {
	tc := cfg.Sub("thin_cap_gap")
	return diag.ThinCapFlags(features.UserRows(cfg, rows), target, now-3600, int(tc.F("min_n", 30)), int(tc.F("cap", 6)),
		diag.PeakN(recent), int(tc.F("est_n", 60)), tc.F("est_hours", 24))
}

// mainZeroFlags runs MAINZERO (cfg main_zero: min_n 200, max_err 0.02; diagnostic only).
func mainZeroFlags(cfg m.M, rows []m.Row, target map[int]int, now int64) []string {
	mz := cfg.Sub("main_zero")
	return diag.MainZeroFlags(features.UserRows(cfg, rows), target, now-3600, int(mz.F("min_n", 200)), mz.F("max_err", 0.02))
}

// recoveredFlags runs RECOVERED (cfg recovered: recent_min 15, min_err 0.2, min_ok 5; diagnostic only).
func recoveredFlags(cfg m.M, rows []m.Row, target map[int]int, now int64) []string {
	rc := cfg.Sub("recovered")
	return diag.RecoveredFlags(features.UserRows(cfg, rows), target, now-3600, now, int(rc.F("recent_min", 15)), rc.F("min_err", 0.2), int(rc.F("min_ok", 5)))
}

// longTailFlags runs LONGTAIL (cfg long_tail: min_n 50, thin_n 20, ratio 3.5, min_p90 30; diagnostic only).
func longTailFlags(cfg m.M, rows []m.Row, target map[int]int, now int64) []string {
	lt := cfg.Sub("long_tail")
	return diag.LongTailFlags(features.UserRows(cfg, rows), target, now-3600, int(lt.F("min_n", 50)), int(lt.F("thin_n", 20)), lt.F("ratio", 3.5), lt.F("min_p90", 30))
}
