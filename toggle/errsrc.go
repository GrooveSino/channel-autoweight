package toggle

// v2.87 err_streak_src (toggle_base.py). Exact port; record only, does not
// change the disable decision.

// StreakSrc splits err_streak into rounds over the 1h log error rate (ByErr)
// and rounds counted only because every site probe failed (ProbeOnly).
type StreakSrc struct {
	Streak      int  `json:"streak"`
	ByErr       int  `json:"by_err"`
	ProbeOnly   int  `json:"probe_only"`
	ProbeDriven bool `json:"probe_driven"`
}

func ErrStreakSrc(formal []*Formal, cid int, errRate float64, probeFail bool) StreakSrc {
	o := StreakSrc{}
	for k := len(formal) - 1; k >= 0; k-- {
		r := formal[k]
		e := r.Err60[cid]
		eb := e != nil && *e >= errRate
		pb := probeFail && ProbeAllFailed(r.Probes[cid])
		if !((eb || pb) && oldIs(r.Old, cid, 0)) {
			break
		}
		o.Streak++
		if eb {
			o.ByErr++
		} else if pb {
			o.ProbeOnly++
		}
	}
	o.ProbeDriven = o.Streak > 0 && o.ProbeOnly == o.Streak
	return o
}
