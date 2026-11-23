// Package regress replays frozen snapshots through the Go core and compares
// the result with stored reference outputs (testdata/golden*, originally
// produced by the retired Python prototype). Used by the integration tests.
package regress

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"autoweight/core"
	"autoweight/fetch"
	m "autoweight/model"
)

// Out is the comparable result shape (same keys as py_eval.py).
type Out struct {
	Now          int64              `json:"now"`
	TrustSource  string             `json:"trust_source"`
	Trust        map[string]float64 `json:"trust"`
	Newcomers    []int              `json:"newcomers"`
	Order        []int              `json:"order"`
	Target       map[string]int     `json:"target"`
	Scores       map[string]float64 `json:"scores"`
	Notes        []string           `json:"notes"`
	AccessDenied []int              `json:"access_denied"`
	ADStreak     map[string]bool    `json:"ad_streak"`
	Toggle       *TOut              `json:"toggle"`
	ToggleNote   *string            `json:"toggle_note"`
}

type TAct struct {
	Op     string `json:"op"`
	ID     int    `json:"id"`
	Weight *int   `json:"weight"`
	Why    string `json:"why"`
}

type TOut struct {
	Actions       []TAct            `json:"actions"`
	Notify        bool              `json:"notify"`
	NEnabledAfter int               `json:"n_enabled_after"`
	Passes        map[string]bool   `json:"passes"`
	Streaks       map[string]int    `json:"streaks"`
	Bottom        []int             `json:"bottom"`
	Dormant       []int             `json:"dormant"`
	BaseSkipped   []int             `json:"base_skipped"`
	BaseLax       map[string]string `json:"base_lax"`
	Base          map[string]any    `json:"base"`
	BaseUp        map[string]any    `json:"base_up"`
	BupIDs        []int             `json:"bup_ids"`
	Enabled       any               `json:"enabled"` // full per-channel streak/disable entries (v2.87 gap fix)
}

func k(i int) string { return strconv.Itoa(i) }

// GoEval runs the Go orchestrator on a snapshot directory (dry-run).
func GoEval(dir string, sets ...string) (*Out, *core.Result, error) {
	s, err := fetch.LoadSnapshot(dir)
	if err != nil {
		return nil, nil, err
	}
	return EvalSnapshot(s, sets...)
}
func EvalSnapshot(s *fetch.Snapshot, sets ...string) (*Out, *core.Result, error) {
	cfg, err := m.LoadConfig(s.Path(fetch.ConfigFile))
	if err != nil {
		return nil, nil, err
	}
	if err := ApplySets(cfg, sets); err != nil {
		return nil, nil, err
	}
	rounds, err := m.LoadRounds(s.Path(fetch.RoundsFile))
	if err != nil {
		return nil, nil, err
	}
	shadow, err := m.LoadRounds(s.Path(fetch.ShadowFile))
	if err != nil {
		return nil, nil, err
	}
	env := core.Env{Cfg: cfg, Src: fetch.SnapSource{S: s}, Rounds: rounds, Shadow: shadow,
		Probe: core.Replay{Probes: s.Probes, Reused: s.ProbesReused, Tests: s.DisabledTests, BottomUp: s.BottomUpTests}}
	r, err := core.Run(env, core.Options{})
	if err != nil {
		return nil, nil, err
	}
	o := &Out{Now: r.Now, TrustSource: r.TrustSource, Trust: map[string]float64{}, Newcomers: r.Newcomers,
		Order: r.Decision.Order, Target: map[string]int{}, Scores: map[string]float64{}, Notes: r.Decision.Notes,
		AccessDenied: m.SortedIDs(r.AccessDenied), ADStreak: map[string]bool{}}
	for i, v := range r.Trust {
		o.Trust[k(i)] = v.Trust
	}
	for i, v := range r.Decision.Target {
		o.Target[k(i)] = v
	}
	for i, v := range r.Decision.Scores {
		o.Scores[k(i)] = v.Score
	}
	for i, v := range r.ADStreak {
		o.ADStreak[k(i)] = v.DisableReady
	}
	for _, n := range r.Notes {
		if len(n) > 6 && n[:6] == "TOGGLE" {
			nn := n
			o.ToggleNote = &nn
		}
	}
	if t := r.Toggle; t != nil {
		to := &TOut{Notify: t.Notify, NEnabledAfter: t.NEnabledAfter, Passes: map[string]bool{}, Streaks: map[string]int{},
			Bottom: t.Bottom, Dormant: t.Dormant, Actions: []TAct{}, BaseSkipped: t.BaseSkipped, BaseLax: t.BaseLax,
			Base: asMap(t.Base), BaseUp: asMap(t.BaseUp), BupIDs: m.SortedIDs(t.BottomUpTests), Enabled: norm(t.Enabled)}
		for _, a := range t.Actions {
			to.Actions = append(to.Actions, TAct{a.Op, a.ID, a.Weight, a.Why})
		}
		for i, d := range t.Disabled {
			to.Passes[k(i)], to.Streaks[k(i)] = d.Pass, d.Streak
		}
		o.Toggle = to
	}
	return o, r, nil
}

// Mismatch is one difference.
type Mismatch struct{ Field, Py, Go string }

func js(v any) string { b, _ := json.Marshal(v); return string(b) }

// Compare lists every difference. Scores/trust compare exactly (both sides
// round to 4 dp); notes are compared as text.
func Compare(p, g *Out) []Mismatch {
	var out []Mismatch
	add := func(f string, a, b any) {
		if js(a) != js(b) {
			out = append(out, Mismatch{f, js(a), js(b)})
		}
	}
	keys := map[string]bool{}
	for x := range p.Target {
		keys[x] = true
	}
	for x := range g.Target {
		keys[x] = true
	}
	ks := make([]string, 0, len(keys))
	for x := range keys {
		ks = append(ks, x)
	}
	sort.Strings(ks)
	for _, x := range ks {
		pw, pok := p.Target[x]
		gw, gok := g.Target[x]
		if pw != gw || pok != gok {
			out = append(out, Mismatch{"weight#" + x, js(pw), js(gw)})
		}
		add("score#"+x, p.Scores[x], g.Scores[x])
		add("trust#"+x, p.Trust[x], g.Trust[x])
	}
	add("order", p.Order, g.Order)
	add("trust_source", p.TrustSource, g.TrustSource)
	add("newcomers", nz(p.Newcomers), nz(g.Newcomers))
	add("notes", p.Notes, g.Notes)
	add("access_denied", nz(p.AccessDenied), nz(g.AccessDenied))
	add("ad_streak", p.ADStreak, g.ADStreak)
	add("toggle_skipped", p.ToggleNote != nil, g.ToggleNote != nil)
	if p.Toggle != nil && g.Toggle != nil {
		add("toggle.actions", p.Toggle.Actions, g.Toggle.Actions)
		add("toggle.passes", p.Toggle.Passes, g.Toggle.Passes)
		add("toggle.streaks", p.Toggle.Streaks, g.Toggle.Streaks)
		add("toggle.notify", p.Toggle.Notify, g.Toggle.Notify)
		add("toggle.n_enabled_after", p.Toggle.NEnabledAfter, g.Toggle.NEnabledAfter)
		add("toggle.bottom", nz(p.Toggle.Bottom), nz(g.Toggle.Bottom))
		add("toggle.dormant", nz(p.Toggle.Dormant), nz(g.Toggle.Dormant))
		add("toggle.base_skipped", nz(p.Toggle.BaseSkipped), nz(g.Toggle.BaseSkipped))
		add("toggle.base_lax", p.Toggle.BaseLax, g.Toggle.BaseLax)
		add("toggle.base", p.Toggle.Base, g.Toggle.Base)
		add("toggle.base_up", p.Toggle.BaseUp, g.Toggle.BaseUp)
		add("toggle.bup_ids", nz(p.Toggle.BupIDs), nz(g.Toggle.BupIDs))
		add("toggle.enabled", norm(p.Toggle.Enabled), norm(g.Toggle.Enabled))
	} else {
		add("toggle.present", p.Toggle != nil, g.Toggle != nil)
	}
	return out
}

func nz(x []int) []int {
	if x == nil {
		return []int{}
	}
	return x
}

// ApplySets applies A.B[.C]=JSON overrides (same syntax as py_eval --set).
func ApplySets(cfg m.M, sets []string) error {
	for _, kv := range sets {
		i := strings.Index(kv, "=")
		if i < 0 || !strings.Contains(kv[:i], ".") {
			return fmt.Errorf("bad -set %q", kv)
		}
		var v any
		if err := json.Unmarshal([]byte(kv[i+1:]), &v); err != nil {
			return err
		}
		path := strings.Split(kv[:i], ".") // nested: a.b.c=JSON
		cur := map[string]any(cfg)
		for _, k := range path[:len(path)-1] {
			nx, _ := cur[k].(map[string]any)
			if nx == nil {
				if mm, ok := cur[k].(m.M); ok {
					nx = mm
				} else {
					nx = map[string]any{}
				}
			}
			cur[k] = nx
			cur = nx
		}
		cur[path[len(path)-1]] = v
	}
	return nil
}

func asMap(v any) map[string]any {
	b, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

// norm round-trips v through JSON so int/float spellings (0 vs 0.0) compare equal.
func norm(v any) any {
	b, _ := json.Marshal(v)
	var o any
	_ = json.Unmarshal(b, &o)
	return o
}
