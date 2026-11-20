package decide

import (
	"fmt"

	m "autoweight/model"
)

// Settle (v4.98; replaces v4.66 SoftDown and the error_alert zeroing) turns the
// strategy target into the system proposal using the three rules the operator
// applied by hand every round (Groove 11-12 16:01: the system proposal must be
// usable as is):
//
//  1. Evidence is ChanStat: real requests only, client 4xx and cross-channel
//     "do request failed" excluded; a token holding >= tok_share of the
//     non-capacity errors while others succeed counts at most tok_cap. A single
//     blocked token therefore cannot cut a channel; 429/timeouts always count.
//  2. A cut needs channel errors on a real sample (N >= min_real_n, Eff >=
//     down_min_err, Rate >= down_err). Latency, long outputs, low traffic or a
//     small sample alone never cut. A raise needs N >= min_real_n and Rate < up_max_err
//     (below floor a clean channel may always return to floor, so 0 never starves).
//  3. At most +-step per round, never below floor, unless extreme
//     (Rate >= extreme_err on >= extreme_min_n, or >= 90% of all rows failing):
//     then straight to w_min. Caps (operator soft caps, trust/newcomer) always win.
//
// v5.00 settle.cut_needs_fault (Claude): the extreme path also needs the cut
// evidence (real >= min_real_n, discounted errors >= down_min_err and rate >=
// down_err), and system caps (trust / newcomer / small-sample / capacity) may not
// push a channel below its current weight without it. Operator soft_caps still win.
//
// v5.00 settle.raise_clean: below w_max, real >= min_real_n and discounted
// rate < up_max_err raise +step even when the strategy held or lowered the
// channel (the operator's routine "clean, step it back up"); caps still win.
//
// cfg settle.enabled=false leaves the strategy target untouched.
func Settle(cfg m.M, nw, prev map[int]int, fs m.Feats, ids []int, caps map[int]*Cap, notes *[]string) {
	sc := cfg.Sub("settle")
	if !sc.B("enabled", true) {
		return
	}
	step, floor, minReal := sc.I("step", 2), sc.I("floor", 2), sc.I("min_real_n", 30)
	downErr, downMin, upErr := sc.F("down_err", 0.03), sc.I("down_min_err", 3), sc.F("up_max_err", 0.02)
	extErr, extN := sc.F("extreme_err", 0.2), sc.I("extreme_min_n", 20)
	wmin := cfg.I("w_min", 0)
	strict := sc.B("cut_needs_fault", false) // v5.00: no cut/cap without real faults
	raiseClean, wmax := sc.B("raise_clean", false), cfg.I("w_max", 20)
	for _, i := range ids {
		o, ok := prev[i]
		f := fs[i]
		if !ok || f == nil {
			continue
		}
		s := f.Chan
		if s == nil {
			s = &m.ChanStat{}
		}
		w, why := nw[i], ""
		allFail := f.Long != nil && f.Long.N >= extN && m.Or(f.Long.ErrRate, 0) >= 0.9
		bad := s.N >= minReal && s.Eff >= downMin && s.Rate >= downErr
		extreme := (s.N >= extN && s.Rate >= extErr) || allFail
		if strict && !bad {
			extreme = false // v5.00: tiny samples / client-only failures never zero a channel
		}
		switch {
		case extreme:
			w, why = wmin, fmt.Sprintf("极端：1h 渠道错误 %d/%d（按令牌去集中 %d，%.0f%%），直接降到 %d", s.Err, s.N, s.Eff, s.Rate*100, wmin)
		case bad && w >= o:
			w = max(o-step, min(floor, o))
			why = fmt.Sprintf("渠道错误 %d/%d（去集中 %d，%.1f%%）达降权门槛，策略未降，按步长降 %d、下限 %d", s.Err, s.N, s.Eff, s.Rate*100, step, floor)
		case w <= o && raiseClean && o < wmax && s.N >= minReal && s.Rate < upErr:
			w, why = min(o+step, wmax), fmt.Sprintf("干净回升：1h 真实 %d（≥%d）、去集中错误率 %.1f%%（<%.0f%%），策略未加也按步长加 %d", s.N, minReal, s.Rate*100, upErr*100, step)
		case w < o && !bad:
			w, why = o, fmt.Sprintf("无渠道级错误证据（1h 真实 %d、渠道错误 %d、去集中 %d），延迟/小样本不单独降权", s.N, s.Err, s.Eff)
		case w < o:
			w = max(w, o-step, min(floor, o))
			why = fmt.Sprintf("渠道错误 %d/%d（去集中 %d，%.1f%%），每轮最多降 %d、下限 %d", s.Err, s.N, s.Eff, s.Rate*100, step, floor)
		case w > o && s.Rate < upErr && o < floor:
			w, why = min(w, floor), fmt.Sprintf("低于下限 %d 时只回到 %d（防饿死）", floor, floor)
		case w > o && (s.N < minReal || s.Rate >= upErr):
			w, why = o, fmt.Sprintf("1h 真实 %d（门槛 %d）、去集中错误率 %.1f%%，不加", s.N, minReal, s.Rate*100)
		case w > o+step:
			w, why = o+step, fmt.Sprintf("每轮最多加 %d", step)
		}
		if c, ok := caps[i]; ok && float64(w) > c.Cap {
			if strict && c.Kind != "soft" && !bad && float64(o) > c.Cap {
				if w > o {
					w = o
				}
				why = fmt.Sprintf("系统上限 %v（%s）不执行：1h 真实 %d、渠道错误 %d（去集中 %d，%.1f%%）未达降权门槛（真实≥%d、错误≥%d、≥%.0f%%）",
					c.Cap, c.Reason, s.N, s.Err, s.Eff, s.Rate*100, minReal, downMin, downErr*100)
			} else {
				w, why = int(c.Cap), fmt.Sprintf("上限 %v（%s）", c.Cap, c.Reason)
			}
		}
		if w != nw[i] && notes != nil {
			*notes = append(*notes, fmt.Sprintf("#%d 定档：策略 %d→%d（%s）", i, nw[i], w, why))
		}
		nw[i] = w
	}
}
