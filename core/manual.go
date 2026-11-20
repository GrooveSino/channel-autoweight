package core

import (
	"errors"
	"maps"

	"autoweight/decide"
	m "autoweight/model"
)

// manual applies --expect-system / --final / --override exactly like run.py.
func manual(cfg m.M, o Options, r *Result) error {
	sys := maps.Clone(r.Decision.Target)
	r.System = sys
	if o.Expect != "" {
		exp, err := decide.ParseOverride(o.Expect, r.Channels, cfg)
		if err != nil {
			return err
		}
		drift := decide.SystemDrift(exp, sys)
		var fin0 map[int]int
		if o.Final != "" {
			if fin0, err = decide.ParseFinal(o.Final, r.Channels, cfg); err != nil {
				return err
			}
		}
		block := decide.BlockingDrift(drift, fin0, cfg.I("apply_drift_tol", 0))
		if len(drift) > 0 && len(block) == 0 {
			r.Notes = append(r.Notes, "DRIFT(不阻断，均为人工覆盖渠道)")
		}
		r.Drift = block
		if len(block) > 0 && o.Apply {
			r.Notes = append(r.Notes, "ABORT: 系统建议已变化，未写库；请重新 dry-run 复核")
			r.ExitCode = 2
			return nil
		}
	}
	if o.Final != "" {
		fin, err := decide.ParseFinal(o.Final, r.Channels, cfg)
		if err != nil {
			return err
		}
		same := maps.Equal(fin, sys)
		if !same && o.Reason == "" {
			return errors.New("--final 与系统建议不同，必须配 --reason")
		}
		r.Decision.Target = fin
		if !same {
			r.Override = map[string]any{"set": fin, "reason": o.Reason, "system": sys, "final": true}
		}
	}
	if o.Override != "" {
		if o.Reason == "" {
			return errors.New("--override 必须配 --reason")
		}
		ov, err := decide.ParseOverride(o.Override, r.Channels, cfg)
		if err != nil {
			return err
		}
		t := maps.Clone(r.Decision.Target)
		maps.Copy(t, ov)
		r.Decision.Target = t
		r.Override = map[string]any{"set": ov, "reason": o.Reason, "system": sys}
	}
	return nil
}
