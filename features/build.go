package features

import (
	"sort"

	m "autoweight/model"
)

func probeSet(cfg m.M) map[int]bool {
	s := map[int]bool{}
	for _, i := range cfg.Ints("probe_token_ids") {
		s[i] = true
	}
	return s
}

// UserRows drops rows from probe tokens (features.user_rows).
func UserRows(cfg m.M, rows []m.Row) []m.Row {
	ex := probeSet(cfg)
	if len(ex) == 0 {
		return rows
	}
	var out []m.Row
	for _, x := range rows {
		if !ex[x.Tok] {
			out = append(out, x)
		}
	}
	return out
}

func dropProbeHung(cfg m.M, rows []m.Row) []m.Row {
	o := cfg.Sub("score").Sub("drop_probe_hung")
	pid := probeSet(cfg)
	if !o.B("enabled", false) || len(pid) == 0 {
		return rows
	}
	ss := o.F("slow_s", cfg.F("slow_s", 60))
	var out []m.Row
	for _, x := range rows {
		if pid[x.Tok] && x.Type == 2 && x.Ct == 0 && float64(x.Ut) >= ss {
			continue
		}
		out = append(out, x)
	}
	return out
}

// dropProbeMismatch removes probe-token errors that only say the probed model is not
// served by the channel (Row.Mm, 404 "not available for this group"), e.g. the site
// auto-test sending gpt-6.1-sol to #28 which only lists gpt-6-astra. They say nothing
// about the channel's health; real-user mismatches are kept.
func dropProbeMismatch(cfg m.M, rows []m.Row) []m.Row {
	pid := probeSet(cfg)
	if !cfg.Sub("score").Sub("drop_probe_mismatch").B("enabled", false) || len(pid) == 0 {
		return rows
	}
	out := rows[:0:0]
	for _, x := range rows {
		if pid[x.Tok] && x.Type == 5 && x.Mm != 0 {
			continue
		}
		out = append(out, x)
	}
	return out
}

// TrafficMix ports traffic_mix.
func TrafficMix(rows []m.Row, now int64, minutes int, probe map[int]bool) *m.Mix {
	cnt := map[int]int{}
	mx := &m.Mix{}
	for _, x := range rows {
		if x.T < now-int64(minutes)*60 {
			continue
		}
		if probe[x.Tok] {
			mx.NProbe++
			continue
		}
		mx.NUser++
		cnt[x.Tok]++
		if x.Type == 5 {
			mx.NErrReal++ // v2.88 real-user errors
		}
	}
	mx.Tokens = len(cnt)
	mx.HasErrRateReal = true
	if mx.NUser > 0 {
		mx.ErrRateReal = m.F(m.Round(float64(mx.NErrReal)/float64(mx.NUser), 4))
		top := 0
		for _, c := range cnt {
			if c > top {
				top = c
			}
		}
		mx.TopTokShare = m.F(m.Round(float64(top)/float64(mx.NUser), 3))
	}
	return mx
}

// CrossChannelTokens ports cross_channel_tokens.
func CrossChannelTokens(rows []m.Row, t0, t1 int64, minCh int, anySc bool) map[int]bool {
	by := map[int]map[int]bool{}
	for _, x := range rows {
		if t0 <= x.T && x.T <= t1 && IsFollowErr(x, anySc) {
			if by[x.Tok] == nil {
				by[x.Tok] = map[int]bool{}
			}
			by[x.Tok][x.Ch] = true
		}
	}
	out := map[int]bool{}
	for t, chs := range by {
		if len(chs) >= minCh {
			out[t] = true
		}
	}
	return out
}

func eachWin(f *m.Feat, fn func(*m.Window)) {
	for _, w := range []*m.Window{f.Long, f.Short} {
		if w != nil {
			fn(w)
		}
	}
}

// DropClientErrors ports drop_client_errors (field: "err_user" or "err_client").
func DropClientErrors(feats m.Feats, field string) {
	for _, f := range feats {
		eachWin(f, func(w *m.Window) {
			v := w.ErrClient
			if field == "err_user" {
				v = w.ErrUser
			}
			if v == nil || w.ErrRaw != nil {
				return
			}
			c := *v
			if w.Err < c {
				c = w.Err
			}
			w.NRaw, w.ErrRaw, w.ErrRateRaw = m.I(w.N), m.I(w.Err), w.ErrRate
			w.N -= c
			w.Err -= c
			w.ErrRate = nil
			if w.N != 0 {
				w.ErrRate = m.F(m.Round(float64(w.Err)/float64(w.N), 4))
			}
		})
	}
}

// RecoverErrors ports recover_errors (score.err_recover).
func RecoverErrors(feats m.Feats, rc m.M) {
	if !rc.B("enabled", false) {
		return
	}
	for _, f := range feats {
		eachWin(f, func(w *m.Window) {
			if w.ErrPreRec != nil || w.Err == 0 || w.EbSpan == nil || w.EbOkAfter == nil {
				return
			}
			if *w.EbSpan > rc.F("span_s", 60) || float64(*w.EbOkAfter) < rc.F("min_ok", 10) {
				return
			}
			keep := m.RoundInt(float64(w.Err) * rc.F("keep", 0.3))
			w.ErrPreRec = m.I(w.Err)
			w.Err = keep
			w.ErrRate = nil
			if w.N != 0 {
				w.ErrRate = m.F(m.Round(float64(keep)/float64(w.N), 4))
			}
		})
	}
}

// Build ports features.build(cfg, chans, rows, now).
func Build(cfg m.M, chans []m.Channel, rows []m.Row, now int64) m.Feats {
	sc := cfg.Sub("score")
	orig := rows
	rrAll := map[int][]m.Row{}
	for _, x := range rows {
		rrAll[x.Ch] = append(rrAll[x.Ch], x)
	}
	if sc.B("exclude_probe_rows", false) {
		rows = UserRows(cfg, rows)
	}
	rows = dropProbeHung(cfg, rows)
	rows = dropProbeMismatch(cfg, rows)
	if !sc.B("empty_skip_user", false) {
		rows = clearUserEmpty(rows, 0)
	} else if hs := sc.F("empty_skip_user_max_s", 0); hs > 0 {
		rows = clearUserEmpty(rows, hs)
	}
	wins := cfg.Ints("windows_min")
	ln := sc.Sub("len_norm")
	if _, ok := sc["len_norm"]; !ok {
		ln = nil
	}
	pid := probeSet(cfg)
	out := m.Feats{}
	byCh := map[int][]m.Row{}
	for _, x := range rows {
		byCh[x.Ch] = append(byCh[x.Ch], x)
	}
	for _, c := range chans {
		f := &m.Feat{
			Long:  Window(byCh[c.ID], now, wins[0], cfg.F("slow_s", 60), ln),
			Short: Window(byCh[c.ID], now, wins[1], cfg.F("slow_s", 60), ln),
			Mix:   TrafficMix(rrAll[c.ID], now, 60, pid),
		}
		out[c.ID] = f
	}
	wmMin := sc.Sub("workload_match").I("min_n", 5)
	for k, mn := range wins {
		mr := MatchedRatios(rows, now-int64(mn)*60, now+1, wmMin)
		for cid, v := range mr {
			if f, ok := out[cid]; ok {
				w := f.Long
				if k == 1 {
					w = f.Short
				}
				w.MrN, w.Mr50, w.Mr90 = m.I(v.N), m.F(v.R50), m.F(v.R90)
			}
		}
	}
	ce := sc.Sub("client_err")
	anySc := ce.B("follow_5xx", false)
	sbx := ce.B("session_block", false) // v3.12: session-block 403s (sb=1) also follow the user
	xt := CrossChannelTokens(rows, now-3600, now+1, ce.I("min_ch", 2), anySc)
	for _, c := range chans {
		for k, mn := range wins {
			w := out[c.ID].Long
			if k == 1 {
				w = out[c.ID].Short
			}
			if w.ErrClient == nil {
				w.ErrUser = nil
				continue
			}
			n := 0
			for _, x := range byCh[c.ID] {
				if x.T >= now-int64(mn)*60 && ((xt[x.Tok] && IsFollowErr(x, anySc)) || (sbx && x.Type == 5 && x.Sb != 0)) {
					n++
				}
			}
			w.ErrUser = m.I(n)
		}
	}
	if ce.B("enabled", false) {
		field := "err_client"
		if ce.S("mode", "") == "cross_channel" {
			field = "err_user"
		}
		DropClientErrors(out, field)
	}
	RecoverErrors(out, sc.Sub("err_recover"))
	AttachTps(cfg, out, rows, now)
	for id, st := range ChanStats(cfg, orig, now-3600, cfg.Sub("settle").I("tok_cap", 2), cfg.Sub("settle").F("tok_share", 0.8)) {
		if f, ok := out[id]; ok {
			f.Chan = st
		}
	}
	return out
}

// LogStreamVeto ports features.log_stream_veto.
func LogStreamVeto(probes []m.Probe, f *m.Feat, lv m.M, win, use string) bool {
	if !lv.B("enabled", false) || len(probes) == 0 {
		return false
	}
	scope := lv.S("scope", "both")
	if use != "" && scope != "both" && scope != use {
		return false
	}
	anyT, anyF, n := false, false, 0
	for _, p := range probes {
		if p.OK {
			n++
			if p.Streaming != nil && *p.Streaming {
				anyT = true
			} else if p.Streaming != nil {
				anyF = true
			}
		}
	}
	if n == 0 || anyT || !anyF {
		return false
	}
	w := f.Win(win)
	if w == nil {
		return false
	}
	return float64(w.LsN) >= lv.F("min_n", 20) && w.LsBuf != nil && *w.LsBuf <= lv.F("max_buf", 0.2)
}

var _ = sort.Ints

// clearUserEmpty drops the Ue mark (copying rows) so prompt_tokens=0 empty
// successes count against the channel again. minUt=0 clears every row
// (score.empty_skip_user=false); minUt>0 clears only hung ones (use_time >=
// minUt, score.empty_skip_user_max_s): a client-side empty request returns at
// once, an empty reply that hung for a minute is the channel's fault.
func clearUserEmpty(rows []m.Row, minUt float64) []m.Row {
	out := make([]m.Row, len(rows))
	for i, x := range rows {
		if minUt <= 0 || float64(x.Ut) >= minUt {
			x.Ue = 0
		}
		out[i] = x
	}
	return out
}
