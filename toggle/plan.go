package toggle

import (
	"fmt"
	"sort"
	"strings"

	m "autoweight/model"
)

// DisOut is plan().disabled[id].
type DisOut struct {
	Results   []m.Probe `json:"results"`
	Pass      bool      `json:"pass"`
	Why       []string  `json:"why"`
	Streak    int       `json:"streak"`
	Enable    bool      `json:"enable"`
	PassSite  bool      `json:"pass_site"`
	WhySite   []string  `json:"why_site"`
	PassUp    *bool     `json:"pass_up"`
	WhyUp     []string  `json:"why_up"`
	DecidedBy string    `json:"decided_by"`
}

// EnOut is plan().enabled[id].
type EnOut struct {
	LastStreak       int        `json:"last_streak"`
	LastSpanMin      float64    `json:"last_span_min"`
	ErrStreak        int        `json:"err_streak"`
	ErrStreakSrc     *StreakSrc `json:"err_streak_src,omitempty"`
	UnusableStreak   int        `json:"unusable_streak"`
	UpUnusableStreak int        `json:"up_unusable_streak"`
	DormantStreak    int        `json:"dormant_streak"`
	Disable          bool       `json:"disable"`
	Why              []string   `json:"why"`
	AcctDisable      bool       `json:"acct_disable,omitempty"`
}

// Plan is toggle.plan() output (core keys).
type Plan struct {
	Bottom        []int             `json:"bottom"`
	BaseSkipped   []int             `json:"base_skipped"`
	BaseLax       map[string]string `json:"base_lax"` // v2.80 {id: dormant|buffered}
	Base          *Base             `json:"base"`
	BaseUp        *Base             `json:"base_up"`
	BaselineMode  string            `json:"baseline_mode"`
	Disabled      map[int]*DisOut   `json:"disabled"`
	Enabled       map[int]*EnOut    `json:"enabled"`
	Actions       []m.Action        `json:"actions"`
	Notify        bool              `json:"notify"`
	NEnabledAfter int               `json:"n_enabled_after"`
	Dormant       []int             `json:"dormant"`
	NLiveAfter    int               `json:"n_live_after"`
	BottomUpTests m.Probes          `json:"bottom_up_tests,omitempty"`
	ReusedTest    bool              `json:"reused_test"`
	ErrSource     map[int]ErrSrc    `json:"err_source,omitempty"`
	AcctAdded     []m.Action        `json:"acct_added,omitempty"`
	Applied       []m.Action        `json:"applied,omitempty"`
	Rejected      []m.Action        `json:"rejected,omitempty"`
}

// DisabledTest is one disabled channel's upstream test results, in DB order.
type DisabledTest struct {
	ID      int
	Results []m.Probe
}

// MakePlan ports toggle.plan. formal's last element is the current round.
func MakePlan(cfg m.M, enabledIDs []int, tests []DisabledTest, hist []*m.ToggleRec, formal []*Formal, bottomUp m.Probes) *Plan {
	p := P(cfg)
	cur := &Formal{Scores: m.NewOrderedScores()}
	if len(formal) > 0 {
		cur = formal[len(formal)-1]
		if bottomUp != nil {
			cur.Up = bottomUp
		}
	}
	en := map[int]bool{}
	for _, i := range enabledIDs {
		en[i] = true
	}
	sc := m.NewOrderedScores()
	for _, k := range cur.Scores.Keys {
		if en[k] {
			sc.Set(k, cur.Scores.Vals[k])
		}
	}
	lax := map[string]string{}
	bot, skipped := BottomForBase(sc, cur.Probes, cur.Feats, p, cur.Old, cur.N60, lax)
	base := Baseline(bot, cur.Probes, cur.Feats, p.F("min_log_n", 10), p.B("skip_buffered_ttft", false), p.F("min_log_n_buffered", 2), bottomUp, baseN60(p, cur))
	var baseUp *Base
	if bottomUp != nil {
		baseUp = BaselineUpstream(bot, bottomUp)
	}
	mode := p.S("baseline_mode", "site")
	out := &Plan{Bottom: bot, BaseSkipped: skipped, BaseLax: lax, Base: base, BaseUp: baseUp, BaselineMode: mode,
		Disabled: map[int]*DisOut{}, Enabled: map[int]*EnOut{}, Actions: []m.Action{}, Dormant: []int{}}
	nEn := len(enabledIDs)
	for _, t := range tests {
		okS, whyS := TestPass(t.Results, base)
		var okU *bool
		whyU := []string{"未做同口径测试"}
		if bottomUp != nil {
			o, w := TestPassUpstream(t.Results, baseUp)
			okU, whyU = m.B(o), w
		}
		useUp := mode == "upstream" && baseUp != nil
		ok, why := okS, whyS
		by := "site"
		if useUp {
			ok, why, by = *okU, whyU, "upstream"
		}
		st := 0
		if ok {
			st = PassStreak(hist, t.ID) + 1
		}
		e := ok && float64(st) >= p.F("need_streak", 3)
		out.Disabled[t.ID] = &DisOut{Results: t.Results, Pass: ok, Why: why, Streak: st, Enable: e,
			PassSite: okS, WhySite: whyS, PassUp: okU, WhyUp: whyU, DecidedBy: by}
		if e {
			w := p.I("start_weight", 2)
			out.Actions = append(out.Actions, m.Action{Op: "enable", ID: t.ID, Weight: &w,
				Why: fmt.Sprintf("连续 %d 轮直连测试达标：", st) + strings.Join(why, "；")})
			nEn++
		}
	}
	errRounds := p.F("err_rounds", 2)
	exempt := ExemptIDs(cfg)
	for _, cid := range enabledIDs {
		ls, span := LastStreak(formal, cid, p.F("min_n60", 20))
		es := ErrStreak(formal, cid, p.F("err_rate", 0.2), p.B("err_probe_fail", false), p.B("err_real_only", true))
		us := UnusableStreak(formal, cid, p.F("unusable_ttft_s", 30))
		uus := UpUnusableStreak(formal, cid, p.F("unusable_ttft_s", 30))
		ds := DormantStreak(formal, cid, p.F("min_n60", 20), p.F("dormant_max_w", 2))
		d, why, nt := false, []string{}, false
		if p.B("unusable_disable", false) && float64(es) < errRounds && float64(us) >= errRounds {
			es = us
			why = append(why, fmt.Sprintf("站点探测连续 %d 轮全部失败或首字 ≥%.0fs", us, p.F("unusable_ttft_s", 30)))
		}
		hardMin, minEn := p.F("hard_min", 3), p.F("min_enabled", 4)
		switch {
		case float64(es) >= errRounds && p.B("recovered_exempt", false) && ProbeRecovered(cur.Probes[cid]):
			why = append(why, fmt.Sprintf("权重 0 后连续 %d 轮 1h 错误率超标，但本轮站点探测全部成功且真流式（已恢复，错误是滞后窗口），暂不禁用、继续观察", es))
		case float64(es) >= errRounds:
			if float64(nEn-1) >= hardMin {
				d = true
				why = append(why, fmt.Sprintf("权重 0 后连续 %d 轮持续报错（1h 错误率 ≥%.0f%% 或站点探测全部失败）", es, p.F("err_rate", 0.2)*100))
				nt = float64(nEn-1) < minEn
			} else {
				why = append(why, fmt.Sprintf("持续报错但启用数已到底线 %v，不禁用", hardMin))
			}
		case float64(ls) >= p.F("last_rounds", 5) && span >= p.F("last_span_min", 210):
			if ok, rw := rankFault(p, cur, cid); !ok {
				why = append(why, fmt.Sprintf("连续 %d 轮（%.0f 分钟）倒数第一，但", ls, span)+rw+"，只是更慢，不建议停用，只记诊断")
			} else if g, gw := MainGuard(p, cur, cid); g {
				why = append(why, fmt.Sprintf("连续 %d 轮（%.0f 分钟）倒数第一，但", ls, span)+gw+"，排名禁用跳过")
			} else if float64(nEn-1) >= minEn {
				d = true
				why = append(why, fmt.Sprintf("连续 %d 轮（%.0f 分钟）倒数第一", ls, span))
			} else {
				why = append(why, fmt.Sprintf("连续 %d 轮倒数第一，但启用数 %d 已到正常底线 %v，不因排名禁用", ls, nEn, minEn))
			}
		}
		if d && exempt[cid] {
			d, nt = false, false
			why = append(why, "已满足禁用条件，但在 exempt 名单（Groove 硬性规则：永不禁用，只调权重），只记录，也不通知")
		}
		if d && nt { // v2.89: notify only after the exempt check
			out.Notify = true
			why = append(why, fmt.Sprintf("启用数降到 %d，必须马上告诉 Groove", nEn-1))
		}
		esrc := ErrStreakSrc(formal, cid, p.F("err_rate", 0.2), p.B("err_probe_fail", false)) // v2.87
		if esrc.Streak != 0 && es == 0 {
			why = append(why, fmt.Sprintf("日志/探测口径持续报错 %d 轮，但真实用户 1h 错误率未超标（v2.88 只算真实用户），不计入禁用", esrc.Streak))
		}
		if esrc.ProbeDriven && es != 0 {
			why = append(why, fmt.Sprintf("持续报错 %d 轮全部来自站点探测失败，1h 日志错误率未超标（真实用户口径没有持续报错）", esrc.Streak))
		}
		out.Enabled[cid] = &EnOut{LastStreak: ls, LastSpanMin: span, ErrStreak: es, ErrStreakSrc: &esrc, UnusableStreak: us,
			UpUnusableStreak: uus, DormantStreak: ds, Disable: d, Why: why}
		if float64(ds) >= p.F("dormant_rounds", 6) && !d {
			out.Dormant = append(out.Dormant, cid)
		}
		if d {
			out.Actions = append(out.Actions, m.Action{Op: "disable", ID: cid, Why: strings.Join(why, "；")})
			nEn--
		}
	}
	out.NEnabledAfter = nEn
	sort.Ints(out.Dormant)
	live := nEn
	for _, c := range out.Dormant {
		if en[c] {
			live--
		}
	}
	out.NLiveAfter = live
	if bottomUp != nil {
		out.BottomUpTests = bottomUp
	}
	return out
}

// rankFault (v5.00): a latency-rank disable also needs real channel faults in
// the current hour (features.ChanStat, same discounting as settle): real >=
// rank_fault_min_n, discounted errors >= rank_fault_min_err and rate >=
// rank_fault_err. toggle.rank_needs_fault=false restores the rank-only rule.
func rankFault(p m.M, cur *Formal, cid int) (bool, string) {
	if !p.B("rank_needs_fault", true) {
		return true, ""
	}
	var s *m.ChanStat
	if cur != nil && cur.Feats[cid] != nil {
		s = cur.Feats[cid].Chan
	}
	if s == nil {
		return false, "本轮没有真实请求错误口径数据"
	}
	if float64(s.N) >= p.F("rank_fault_min_n", 30) && float64(s.Eff) >= p.F("rank_fault_min_err", 3) && s.Rate >= p.F("rank_fault_err", 0.03) {
		return true, ""
	}
	return false, fmt.Sprintf("1h 真实 %d 条、渠道错误 %d（去集中 %d，%.1f%%），没有真实故障", s.N, s.Err, s.Eff, s.Rate*100)
}

// baseN60 is v3.32's n60_real argument: the round's real 1h counts when
// toggle.base_real_log is on (an empty map, not nil, when none are known).
func baseN60(p m.M, cur *Formal) map[int]*int {
	if !p.B("base_real_log", false) {
		return nil
	}
	if cur.N60 == nil {
		return map[int]*int{}
	}
	return cur.N60
}
