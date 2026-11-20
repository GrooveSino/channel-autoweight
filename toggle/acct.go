package toggle

import (
	"fmt"
	"sort"
	"strings"

	m "autoweight/model"
)

// ToggleSQL ports toggle_sql → (sql or "", done, rejected).
func ToggleSQL(actions []m.Action, enabledIDs []int, priority, hardMin, minEnabled int) (string, []m.Action, []m.Action) {
	en := map[int]bool{}
	for _, i := range enabledIDs {
		en[i] = true
	}
	acts := append([]m.Action(nil), actions...)
	sort.SliceStable(acts, func(a, b int) bool { return acts[a].Op == "enable" && acts[b].Op != "enable" })
	var ok, rej []m.Action
	var stmts []string
	for _, a := range acts {
		cid := a.ID
		switch a.Op {
		case "enable":
			if en[cid] {
				a.Reject = "已启用"
				rej = append(rej, a)
				continue
			}
			w := 2
			if a.Weight != nil {
				w = *a.Weight
			}
			if w < 0 {
				w = 0
			}
			stmts = append(stmts, fmt.Sprintf("update channels set status=1, priority=%d, weight=%d where id=%d and status<>1;", priority, w, cid))
			en[cid] = true
			ok = append(ok, a)
		case "disable":
			if !en[cid] {
				a.Reject = "未启用"
				rej = append(rej, a)
				continue
			}
			floor := minEnabled
			if strings.Contains(a.Why, "报错") || strings.Contains(a.Why, "错误") {
				floor = hardMin
			}
			if len(en)-1 < floor {
				a.Reject = fmt.Sprintf("启用数会降到 %d，低于底线 %d", len(en)-1, floor)
				rej = append(rej, a)
				continue
			}
			stmts = append(stmts, fmt.Sprintf("update channels set status=2, weight=0 where id=%d and status=1;", cid))
			delete(en, cid)
			ok = append(ok, a)
		}
	}
	if len(stmts) == 0 {
		return "", ok, rej
	}
	var ids []string
	for _, a := range ok {
		ids = append(ids, fmt.Sprint(a.ID))
	}
	return strings.Join(stmts, "\n") + fmt.Sprintf("\nupdate abilities a set weight=c.weight, priority=c.priority, enabled=(c.status=1) from channels c where a.channel_id=c.id and c.id in (%s);", strings.Join(ids, ",")), ok, rej
}

// ErrSrc is err_source() output.
type ErrSrc struct {
	N           int      `json:"n"`
	NReal       int      `json:"n_real"`
	Err         int      `json:"err"`
	ErrProbe    int      `json:"err_probe"`
	ErrReal     int      `json:"err_real"`
	ErrRate     *float64 `json:"err_rate"`
	ErrRateReal *float64 `json:"err_rate_real"`
	ProbeShare  *float64 `json:"probe_share"`
	Flag        bool     `json:"flag"`
}

// ErrSource ports err_source.
func ErrSource(rows []m.Row, cid int, now float64, minutes float64, probeIDs []int, minErr int, share float64) ErrSrc {
	ex := map[int]bool{}
	for _, i := range probeIDs {
		ex[i] = true
	}
	var o ErrSrc
	for _, x := range rows {
		if x.Ch != cid || float64(x.T) < now-minutes*60 {
			continue
		}
		o.N++
		if !ex[x.Tok] {
			o.NReal++
		}
		if x.Type == 5 {
			o.Err++
			if ex[x.Tok] {
				o.ErrProbe++
			}
		}
	}
	o.ErrReal = o.Err - o.ErrProbe
	if o.Err > 0 {
		o.ProbeShare = m.F(m.Round(float64(o.ErrProbe)/float64(o.Err), 4))
	}
	if o.N > 0 {
		o.ErrRate = m.F(m.Round(float64(o.Err)/float64(o.N), 4))
	}
	if o.NReal > 0 {
		o.ErrRateReal = m.F(m.Round(float64(o.ErrReal)/float64(o.NReal), 4))
	}
	o.Flag = o.Err > 0 && o.Err >= minErr && o.ProbeShare != nil && *o.ProbeShare >= share
	return o
}

func ErrSourceAll(rows []m.Row, ids []int, now, minutes float64, probeIDs []int, minErr int, share float64) map[int]ErrSrc {
	out := map[int]ErrSrc{}
	for _, c := range ids {
		if v := ErrSource(rows, c, now, minutes, probeIDs, minErr, share); v.Err > 0 {
			out[c] = v
		}
	}
	return out
}

var AccessDeniedPatterns = []string{"forbidden", "unauthorized", "invalid api key", "invalid_api_key", "incorrect api key",
	"insufficient_quota", "quota", "balance", "余额", "access denied", "suspended", "deactivated", "banned", "account"}

// AD is diag.access_denied()[id] (core fields).
type AD struct {
	NProbe     int    `json:"n_probe"`
	NDenied    int    `json:"n_denied"`
	Statuses   []int  `json:"statuses"`
	Sample     string `json:"sample"`
	Old        *int   `json:"old"`
	Target     *int   `json:"target"`
	TargetZero bool   `json:"target_zero"`
}

func errStr(e any) string {
	switch v := e.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		if !v {
			return ""
		}
		return "True"
	}
	return fmt.Sprint(e)
}

// AccessDenied ports diag.access_denied.
func AccessDenied(probes m.Probes, target, old map[int]int) map[int]*AD {
	out := map[int]*AD{}
	for i, lst := range probes {
		var hit []m.Probe
		for _, p := range lst {
			if p.OK {
				continue
			}
			msg := strings.ToLower(errStr(p.Err))
			h := p.Status != nil && (*p.Status == 401 || *p.Status == 402 || *p.Status == 403)
			for _, s := range AccessDeniedPatterns {
				h = h || strings.Contains(msg, s)
			}
			if h {
				hit = append(hit, p)
			}
		}
		if len(hit) == 0 {
			continue
		}
		st := map[int]bool{}
		for _, p := range hit {
			if p.Status != nil {
				st[*p.Status] = true
			}
		}
		ss := []int{}
		for k := range st {
			ss = append(ss, k)
		}
		sort.Ints(ss)
		smp := []rune(errStr(hit[0].Err))
		if len(smp) > 120 {
			smp = smp[:120]
		}
		a := &AD{NProbe: len(lst), NDenied: len(hit), Statuses: ss, Sample: string(smp)}
		if v, ok := old[i]; ok {
			a.Old = m.I(v)
		}
		if v, ok := target[i]; ok {
			a.Target = m.I(v)
		}
		a.TargetZero = a.Target == nil || *a.Target == 0
		out[i] = a
	}
	return out
}

// ADStreak is access_denied_streak()[id].
type ADStreak struct {
	Streak       int     `json:"streak"`
	ZeroStreak   int     `json:"zero_streak"`
	FirstTs      *string `json:"first_ts"`
	DisableReady bool    `json:"disable_ready"`
}

// AccessDeniedStreak ports diag.access_denied_streak (max_gap 90, zero_rounds 2).
func AccessDeniedStreak(hist []*m.Rec, cur map[int]*AD, now float64) map[int]*ADStreak {
	out := map[int]*ADStreak{}
	if len(cur) == 0 {
		return out
	}
	var formal []*m.Rec
	for _, r := range hist {
		if !r.IsDryRun(false) && r.Applied && r.Ts != "" {
			formal = append(formal, r)
		}
	}
	sort.SliceStable(formal, func(a, b int) bool { return formal[a].Ts < formal[b].Ts })
	for ch, d := range cur {
		streak, zero := 1, 0
		if d.Old == nil || *d.Old == 0 {
			zero = 1
		}
		var first *string
		last := now
		for k := len(formal) - 1; k >= 0; k-- {
			r := formal[k]
			t := m.Epoch(r.TsTime)
			if last-t > 90*60 {
				break
			}
			if last-t < 60 && t-last < 60 {
				continue
			}
			ad, ok := r.AccessDenied[ch]
			if !ok {
				break
			}
			streak++
			if ad.Old == nil || *ad.Old == 0 {
				zero++
			}
			ts := r.Ts
			first, last = &ts, t
		}
		out[ch] = &ADStreak{Streak: streak, ZeroStreak: zero, FirstTs: first, DisableReady: zero >= 2}
	}
	return out
}

// AcctDisableUpgrade ports acct_disable_upgrade (mutates plan, returns added actions).
func AcctDisableUpgrade(t *Plan, ads map[int]*ADStreak, errSrc map[int]ErrSrc, cfg m.M, enabledIDs []int, lookbackMin int) []m.Action {
	c := cfg.Sub("toggle").Sub("acct_disable")
	if t == nil || !c.B("enabled", false) || len(ads) == 0 {
		return nil
	}
	p := P(cfg)
	exempt := ExemptIDs(cfg)
	en := map[int]bool{}
	for _, i := range enabledIDs {
		en[i] = true
	}
	already := map[int]bool{}
	for _, a := range t.Actions {
		if a.Op == "disable" {
			already[a.ID] = true
		}
	}
	nEn := t.NEnabledAfter
	var added []m.Action
	for _, cid := range m.SortedIDs(ads) {
		s := ads[cid]
		if !en[cid] || already[cid] || !s.DisableReady {
			continue
		}
		ent := t.Enabled[cid]
		if ent == nil {
			ent = &EnOut{Why: []string{}}
			t.Enabled[cid] = ent
		}
		if exempt[cid] {
			ent.Why = append(ent.Why, "账号级错误已满足禁用条件，但在 exempt 名单（Groove 要求不禁用），只记录")
			continue
		}
		er := errSrc[cid].ErrReal
		h := lookbackMin / 60
		if float64(er) < c.F("min_real_err", 1) {
			ent.Why = append(ent.Why, fmt.Sprintf("账号级错误连续 %d 轮（降 0 后 %d 轮），但 %dh 真实用户错误 %d 条 < %v，只算探测不禁用", s.Streak, s.ZeroStreak, h, er, c.F("min_real_err", 1)))
			continue
		}
		if float64(nEn-1) < p.F("hard_min", 3) {
			ent.Why = append(ent.Why, fmt.Sprintf("账号级持续报错但启用数已到底线 %v，不禁用", p.F("hard_min", 3)))
			continue
		}
		why := fmt.Sprintf("探测连续 %d 个正式轮上游账号/授权级错误（降 0 后 %d 轮仍如此），%dh 真实用户也报错 %d 条", s.Streak, s.ZeroStreak, h, er)
		ent.Disable, ent.AcctDisable = true, true
		ent.Why = append(ent.Why, why)
		if float64(nEn-1) < p.F("min_enabled", 4) {
			t.Notify = true
			ent.Why = append(ent.Why, fmt.Sprintf("启用数降到 %d，必须马上告诉 Groove", nEn-1))
		}
		a := m.Action{Op: "disable", ID: cid, Why: why + "（v2.58 ACCTSTREAK）"}
		t.Actions = append(t.Actions, a)
		added = append(added, a)
		nEn--
	}
	t.NEnabledAfter = nEn
	return added
}
