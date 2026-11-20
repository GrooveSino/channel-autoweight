// Package toggle ports autoweight/toggle.py: autonomous enable/disable
// judgement. Pure functions; SQL is generated here but executed by apply/.
package toggle

import (
	"fmt"
	"math"
	"sort"
	"strconv"

	m "autoweight/model"
)

var Default = m.M{"need_streak": 3.0, "n_test": 3.0, "start_weight": 2.0, "min_n60": 20.0, "last_rounds": 5.0, "last_span_min": 210.0,
	"min_enabled": 4.0, "hard_min": 3.0, "err_rate": 0.2, "err_rounds": 2.0, "min_log_n": 10.0,
	"upstream_baseline": true, "n_base_test": 2.0, "baseline_mode": "site", "real_n60": true,
	"skip_buffered_ttft": true, "min_log_n_buffered": 2.0, "err_probe_fail": true,
	"unusable_ttft_s": 30.0, "unusable_disable": false, "dormant_rounds": 6.0, "dormant_max_w": 2.0, "skip_broken_base": true,
	"broken_n60": 10.0, "recovered_exempt": true,
	"main_guard_n": 200.0, "main_guard_err": 0.02}

// P ports toggle.P.
func P(cfg m.M) m.M { return m.Merge(Default, cfg.Sub("toggle")) }

// PyNum formats a float like Python repr (for human-readable why strings).
func PyNum(x float64) string {
	if x == math.Trunc(x) && math.Abs(x) < 1e16 {
		return strconv.FormatFloat(x, 'f', 1, 64)
	}
	return strconv.FormatFloat(x, 'g', -1, 64)
}

func pyOpt(p *float64) string {
	if p == nil {
		return "None"
	}
	return PyNum(*p)
}

// BottomThird: ids sorted by score asc (stable in key order), first ceil(n/3).
func BottomThird(sc *m.OrderedScores) []int {
	ids := append([]int(nil), sc.Keys...)
	sort.SliceStable(ids, func(a, b int) bool { return m.Or(sc.Vals[ids[a]], 0) < m.Or(sc.Vals[ids[b]], 0) })
	if len(ids) == 0 {
		return []int{}
	}
	k := int(math.Max(1, math.Ceil(float64(len(ids))/3)))
	return ids[:k]
}

func ProbeAllFailed(ps []m.Probe) bool {
	if len(ps) == 0 {
		return false
	}
	for _, x := range ps {
		if x.OK {
			return false
		}
	}
	return true
}

func IsBroken(cid int, probes m.Probes, feats m.Feats, errRate float64, minN float64) bool {
	if ProbeAllFailed(probes[cid]) {
		return true
	}
	f := feats[cid]
	if f == nil || f.Long == nil {
		return false
	}
	return float64(f.Long.N) >= minN && m.Or(f.Long.ErrRate, 0) >= errRate
}

// Med ports toggle._med (even count → round(mean, 2)).
func Med(xs []float64) *float64 {
	if len(xs) == 0 {
		return nil
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return m.F(s[n/2])
	}
	return m.F(m.Round((s[n/2-1]+s[n/2])/2, 2))
}

// Base is a toggle baseline {ttft, total[, n]}.
type Base struct {
	Ttft  *float64 `json:"ttft"`
	Total *float64 `json:"total"`
	N     *int     `json:"n,omitempty"`
}

func nz(p *float64) bool { return p != nil && *p != 0 }

func minF(xs []float64) *float64 {
	if len(xs) == 0 {
		return nil
	}
	v := xs[0]
	for _, x := range xs {
		v = math.Min(v, x)
	}
	return m.F(v)
}

// Baseline ports toggle.baseline.
func Baseline(bottom []int, probes m.Probes, feats m.Feats, minLogN float64, skipBuf bool, minLogNBuf float64, up m.Probes, n60Real map[int]*int) *Base {
	// v3.32 toggle.base_real_log: n60Real != nil → a bottom channel with 1h real
	// requests < minLogN does not use its log p50/lt50 (those rows are probes).
	var tt, to []float64
	for _, i := range bottom {
		var pr []m.Probe
		for _, x := range probes[i] {
			if x.OK {
				pr = append(pr, x)
			}
		}
		f60 := &m.Window{}
		if f := feats[i]; f != nil && f.Long != nil {
			f60 = f.Long
		}
		if n60Real != nil && float64(m.OrI(n60Real[i], 0)) < minLogN {
			f60 = &m.Window{}
		}
		// Python: f60.get("lt_n") is None never happens for windows built by features.
		ltOK := nz(f60.Lt50) && float64(f60.LtN) >= minLogN
		pOK := nz(f60.P50) && float64(f60.N) >= minLogN
		var a []float64
		nPrT := 0
		for _, x := range pr {
			if nz(x.Ttft) && !(skipBuf && x.Streaming != nil && !*x.Streaming) {
				a = append(a, *x.Ttft)
				nPrT++
			}
		}
		if ltOK {
			a = append(a, *f60.Lt50)
		}
		if skipBuf && len(pr) > 0 && nPrT == 0 && len(a) == 0 {
			if nz(f60.Lt50) && float64(f60.LtN) >= minLogNBuf {
				a = []float64{*f60.Lt50}
			} else {
				var ut []float64
				for _, r := range up[i] {
					if r.OK && nz(r.Ttft) {
						ut = append(ut, *r.Ttft)
					}
				}
				if len(ut) > 0 {
					a = []float64{*Med(ut)}
				}
			}
		}
		var b []float64
		for _, x := range pr {
			if nz(x.Total) {
				b = append(b, *x.Total)
			}
		}
		if pOK {
			b = append(b, *f60.P50)
		}
		if len(a) > 0 {
			tt = append(tt, *minF(a))
		}
		if len(b) > 0 {
			to = append(to, *minF(b))
		}
	}
	return &Base{Ttft: minF(tt), Total: minF(to)}
}

// BaselineUpstream ports baseline_upstream (nil when no successful tests).
func BaselineUpstream(bottom []int, up m.Probes) *Base {
	var tt, to []float64
	for _, i := range bottom {
		var ok []m.Probe
		for _, r := range up[i] {
			if r.OK && nz(r.Ttft) {
				ok = append(ok, r)
			}
		}
		if len(ok) == 0 {
			continue
		}
		var ts, tot []float64
		for _, r := range ok {
			ts = append(ts, *r.Ttft)
			if nz(r.Total) {
				tot = append(tot, *r.Total)
			}
		}
		tt = append(tt, *Med(ts))
		if len(tot) > 0 {
			to = append(to, *Med(tot))
		}
	}
	if len(tt) == 0 {
		return nil
	}
	return &Base{Ttft: minF(tt), Total: minF(to), N: m.I(len(tt))}
}

func isTrueStream(r m.Probe) bool {
	return m.OrI(r.Deltas, 0) >= 2 && m.Or(r.Span, 0) > 0
}

func commonFails(results []m.Probe) ([]string, []m.Probe) {
	var why []string
	bad := 0
	var ok []m.Probe
	nst := 0
	for _, r := range results {
		if !(r.OK && r.Status != nil && *r.Status == 200 && nz(r.Ttft)) {
			bad++
		}
		if r.OK && !isTrueStream(r) {
			nst++
		}
		if r.OK && nz(r.Ttft) {
			ok = append(ok, r)
		}
	}
	if bad > 0 {
		why = append(why, fmt.Sprintf("%d/%d 次失败", bad, len(results)))
	}
	if nst > 0 {
		why = append(why, fmt.Sprintf("%d 次非真流式", nst))
	}
	return why, ok
}

// TestPassUpstream ports test_pass_upstream.
func TestPassUpstream(results []m.Probe, base *Base) (bool, []string) {
	if len(results) == 0 {
		return false, []string{"本轮没有测试"}
	}
	why, ok := commonFails(results)
	if base == nil || base.Ttft == nil {
		why = append(why, "没有同口径后 1/3 基准")
	} else if len(ok) > 0 {
		var ts, tot []float64
		for _, r := range ok {
			ts = append(ts, *r.Ttft)
			if nz(r.Total) {
				tot = append(tot, *r.Total)
			}
		}
		mt, mo := Med(ts), Med(tot)
		if *mt >= *base.Ttft {
			why = append(why, fmt.Sprintf("首字中位 %ss 未好过同口径基准 %ss", PyNum(*mt), PyNum(*base.Ttft)))
		}
		if base.Total != nil && (mo == nil || *mo >= *base.Total) {
			why = append(why, fmt.Sprintf("总耗时中位 %ss 未好过同口径基准 %ss", pyOpt(mo), PyNum(*base.Total)))
		}
	}
	if len(why) == 0 {
		return true, []string{"同口径：全部成功、真流式，首字和总耗时中位都好过后 1/3 直连"}
	}
	return false, why
}

// TestPass ports test_pass (site baseline, max of each test).
func TestPass(results []m.Probe, base *Base) (bool, []string) {
	if len(results) == 0 {
		return false, []string{"本轮没有测试"}
	}
	why, ok := commonFails(results)
	if len(ok) > 0 && base != nil {
		mt, mo := math.Inf(-1), math.Inf(-1)
		for _, r := range ok {
			mt = math.Max(mt, *r.Ttft)
			mo = math.Max(mo, m.Or(r.Total, 0))
		}
		if base.Ttft == nil || mt >= *base.Ttft {
			why = append(why, fmt.Sprintf("首字 %ss 未好过后 1/3 基准 %ss", PyNum(mt), pyOpt(base.Ttft)))
		}
		if base.Total == nil || mo >= *base.Total {
			why = append(why, fmt.Sprintf("总耗时 %ss 未好过后 1/3 基准 %ss", PyNum(mo), pyOpt(base.Total)))
		}
	} else if base == nil {
		why = append(why, "没有后 1/3 基准")
	}
	if len(why) == 0 {
		return true, []string{"全部成功、真流式，首字和总耗时都好过后 1/3"}
	}
	return false, why
}
