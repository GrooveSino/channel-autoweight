package decide

import (
	"fmt"
	"math"
	"strconv"
	"time"

	m "autoweight/model"
)

// Cap is one soft cap (soft_caps / trust_cap / newcomer / capacity ...).
type Cap struct {
	Cap    float64 `json:"cap"`
	Reason string  `json:"reason"`
	// Kind (v5.00): "soft" = operator soft_caps (always win); trust / newcomer /
	// small / capacity are system caps that settle.cut_needs_fault can veto.
	Kind string `json:"kind,omitempty"`
}

// ActiveCaps ports active_caps; now replaces datetime.now() so replays are deterministic.
func ActiveCaps(cfg m.M, now time.Time) (map[int]*Cap, error) {
	caps := map[int]*Cap{}
	sc := cfg.Sub("soft_caps")
	for _, k := range sc.Keys() {
		if len(k) > 0 && k[0] == '_' {
			continue
		}
		v := sc.Sub(k)
		if !v.B("enabled", true) {
			continue
		}
		if u := v.S("until", ""); u != "" {
			t, err := time.Parse(time.RFC3339, u)
			if err != nil {
				return nil, fmt.Errorf("soft_caps.%s.until %q: %w", k, u, err)
			}
			if !now.Before(t) {
				continue
			}
		}
		if v.Sub("relax").B("enabled", false) {
			return nil, fmt.Errorf("soft_caps.%s.relax.enabled not ported", k)
		}
		i, err := strconv.Atoi(k)
		if err != nil {
			return nil, err
		}
		caps[i] = &Cap{Cap: v.F("cap", 0), Reason: v.S("reason", ""), Kind: "soft"}
	}
	return caps, nil
}

func lower(caps map[int]*Cap, i int, c float64) bool {
	old, ok := caps[i]
	return !ok || old.Cap > c
}

// applyTrustCap ports the v1.46/v1.61/v1.66 trust_cap block.
func applyTrustCap(cfg m.M, caps map[int]*Cap, rows map[int]*Row, trust map[int]float64, tids []int) {
	tc := cfg.Sub("trust_cap")
	if !tc.B("enabled", false) || len(trust) == 0 {
		return
	}
	for _, i := range tids {
		t := trust[i]
		mx := tc.F("max_n60", 0)
		r, in := rows[i]
		if mx != 0 && in && float64(r.N60) >= mx {
			continue
		}
		thr, cp := tc.F("thr", 0.6), tc.F("cap", 3)
		if in && t < thr && lower(caps, i, cp) {
			why := fmt.Sprintf("信任 %.2f < %v", t, thr)
			if mx != 0 {
				why += fmt.Sprintf("且 1h 样本 %d < %v", r.N60, mx)
			}
			caps[i] = &Cap{cp, why + "，恢复期限幅", "trust"}
		}
		if rp := tc.F("ramp", 0); rp > 0 && in && thr <= t && t < thr+rp {
			top := cfg.Sub("strategies").Sub("rank").F("top_w", 20)
			ce := float64(m.RoundInt(cp + (top-cp)*(t-thr)/rp))
			if ce < top && lower(caps, i, ce) {
				caps[i] = &Cap{ce, fmt.Sprintf("信任 %.2f 刚越过 %v（爬坡区 %v），限幅 %v", t, thr, rp, ce), "trust"}
			}
		}
	}
}

func nUser(f *m.Feat) *int {
	if f == nil || f.Mix == nil {
		return nil
	}
	return m.I(f.Mix.NUser)
}

// applyNewcomer ports the v1.47 newcomer cap and v1.76 small_sample cap.
func applyNewcomer(cfg m.M, caps map[int]*Cap, rows map[int]*Row, ids []int, fs m.Feats, newcomers map[int]bool) {
	nc := cfg.Sub("newcomer")
	if nc.B("enabled", false) && len(newcomers) > 0 {
		for _, i := range ids {
			if !newcomers[i] {
				continue
			}
			nu := nUser(fs[i])
			if nu == nil {
				continue
			}
			mn := nc.F("min_real_n60", 30)
			if float64(*nu) < mn {
				rows[i].NewcomerN = m.I(*nu)
				if lower(caps, i, nc.F("cap", 6)) {
					caps[i] = &Cap{nc.F("cap", 6), fmt.Sprintf("新渠道/真实流量不足：1h 真实请求 %d < %v", *nu, mn), "newcomer"}
					rows[i].Newcomer = m.I(*nu)
				}
			}
		}
	}
	ss := cfg.Sub("small_sample")
	if ss.B("enabled", false) && ss.F("max_n60", 0) > 0 {
		for _, i := range ids {
			nu := rows[i].N60
			if p := nUser(fs[i]); p != nil {
				nu = *p
			}
			if float64(nu) < ss.F("max_n60", 0) && lower(caps, i, ss.F("cap", 3)) {
				caps[i] = &Cap{ss.F("cap", 3), fmt.Sprintf("小样本：1h 真实请求 %d < %v，限幅 %v", nu, ss.F("max_n60", 0), ss.F("cap", 3)), "small"}
				rows[i].SmallSample = m.I(nu)
			}
		}
	}
}

// sumVals sums w over keys in order (Python dict order) skipping one id.
func sumVals(w map[int]float64, keys []int, skip int) float64 {
	s := 0.0
	for _, k := range keys {
		if k != skip {
			s += w[k]
		}
	}
	return s
}

// applyCapacity ports the v1.48/v1.57 concurrency capacity cap.
func applyCapacity(cfg m.M, caps map[int]*Cap, rows map[int]*Row, ids, order []int, fs m.Feats, sc map[int]float64,
	name string, rp RankParams, prev map[int]int) {
	cc := cfg.Sub("capacity")
	if !cc.B("enabled", false) {
		return
	}
	wl, _ := m.WindowNames(cfg)
	tot := 0.0
	for _, i := range ids {
		if w := fs[i].Win(wl); w != nil {
			tot += w.AvgConc
		}
	}
	C := tot * cc.F("peak_factor", 2.0)
	if C <= 0 {
		return
	}
	cm := capMap(caps)
	t0 := RunStrategy(name, order, sc, rp, cfg, prev, cm)
	for _, k := range cc.Keys() {
		if len(k) > 0 && k[0] == '_' {
			continue
		}
		v, ok := cc[k].(map[string]any)
		if !ok {
			continue
		}
		i, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		if _, in := rows[i]; !in {
			continue
		}
		vm := m.M(v)
		s := math.Min(1.0, vm.F("max_conc", 0)/C)
		if s >= 1.0 {
			continue
		}
		var R float64
		if cc.S("r_mode", "first") == "rest" {
			var rest []int
			for _, j := range order {
				if j != i {
					rest = append(rest, j)
				}
			}
			if len(rest) > 0 {
				R = sumVals(RunStrategy(name, rest, sc, rp, cfg, prev, cm), rest, -1<<31)
			}
		} else {
			R = sumVals(t0, order, i)
		}
		cp := math.Max(cfg.F("w_min", 0), m.Round(s*R/(1-s), 1))
		if lower(caps, i, cp) {
			caps[i] = &Cap{cp, fmt.Sprintf("并发容量 %v（%s），估计高峰总并发 %.1f，份额≤%.0f%%", vm.F("max_conc", 0), vm.S("reason", ""), C, s*100), "capacity"}
		}
		rows[i].CapacityCap = m.F(cp)
	}
}

func capMap(caps map[int]*Cap) map[int]float64 {
	out := map[int]float64{}
	for i, c := range caps {
		out[i] = c.Cap
	}
	return out
}
