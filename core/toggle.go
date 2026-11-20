package core

import (
	"autoweight/history"
	m "autoweight/model"
	"autoweight/toggle"
)

// stepToggle mirrors run.py's toggle block (decisions only; diagnostics excluded).
func stepToggle(env Env, o Options, r *Result, ids []int, now float64) (*toggle.Plan, error) {
	cfg := env.Cfg
	pt := toggle.P(cfg)
	lastt := history.LastToggle(env.Rounds)
	reuseT := o.ReuseProbe > 0 && lastt != nil && (now-m.Epoch(lastt.TsTime))/60 <= o.ReuseProbe
	dis, err := env.Src.Disabled()
	if err != nil {
		return nil, err
	}
	dis = toggle.DropRetired(cfg, dis)
	var tests []toggle.DisabledTest
	for _, c := range dis {
		var res []m.Probe
		if reuseT {
			if d, ok := lastt.Toggle.Disabled[c.ID]; ok && d.Results != nil {
				res = d.Results
			}
		}
		if res == nil {
			if res, err = env.Probe.Upstream(c.ID, pt.I("n_test", 3)); err != nil {
				return nil, err
			}
		}
		tests = append(tests, toggle.DisabledTest{ID: c.ID, Results: res})
	}
	sc := m.NewOrderedScores()
	for _, c := range r.Channels {
		if row := r.Decision.Scores[c.ID]; row != nil {
			sc.Set(c.ID, m.F(row.Score))
		}
	}
	real := pt.B("real_n60", true)
	fm := append(toggle.FormalFromRounds(env.Rounds, real), toggle.FormalCurrent(now, sc, r.Features, r.Channels, r.Probes, real))
	var bup m.Probes
	if pt.B("upstream_baseline", true) && len(tests) > 0 {
		bot, _ := toggle.BottomForBase(sc, r.Probes, r.Features, pt, nil, nil, nil) // run.py passes no old/n60 here
		var prevb m.Probes
		if reuseT {
			prevb = lastt.Toggle.BottomUpTests
		}
		bup = m.Probes{}
		for _, b := range bot {
			if v := prevb[b]; len(v) > 0 {
				bup[b] = v
				continue
			}
			v, err := env.Probe.Upstream(b, pt.I("n_base_test", 2))
			if err != nil {
				return nil, err
			}
			bup[b] = v
		}
	}
	var hs []*m.ToggleRec = toggle.HistoryFromRounds(env.Rounds, now, 30)
	tg := toggle.MakePlan(cfg, ids, tests, hs, fm, bup)
	if bup != nil {
		tg.BottomUpTests = bup
	}
	tg.ReusedTest = reuseT
	es := cfg.Sub("toggle").Sub("err_source")
	share := es.F("probe_share", 0.5)
	probeIDs := cfg.Ints("probe_token_ids")
	if es.B("enabled", true) {
		mins := es.I("minutes", 60)
		if n2, rows2, err := env.Src.Logs(ids, mins); err == nil {
			tg.ErrSource = toggle.ErrSourceAll(rows2, ids, float64(n2), float64(mins), probeIDs, es.I("min_err", 1), share)
		} else {
			r.Notes = append(r.Notes, "PROBEERR-ERR "+err.Error())
		}
	}
	ac := cfg.Sub("toggle").Sub("acct_disable")
	lb := ac.I("real_err_lookback_min", 0)
	src := tg.ErrSource
	if lb > 60 && len(r.ADStreak) > 0 {
		n3, rows3, err := env.Src.Logs(ids, lb)
		if err != nil {
			r.Notes = append(r.Notes, "TOGGLE-ACCT-ERR "+err.Error())
			return tg, nil
		}
		src = toggle.ErrSourceAll(rows3, ids, float64(n3), float64(lb), probeIDs, 1, share)
	}
	lbm := 60
	if lb > 60 {
		lbm = lb
	}
	tg.AcctAdded = toggle.AcctDisableUpgrade(tg, r.ADStreak, src, cfg, ids, lbm)
	return tg, nil
}
