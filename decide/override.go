package decide

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	m "autoweight/model"
)

// ParseOverride ports parse_override ("5=3,2=12").
func ParseOverride(spec string, chans []m.Channel, cfg m.M) (map[int]int, error) {
	ok := map[int]bool{}
	for _, c := range chans {
		ok[c.ID] = true
	}
	lo, hi := cfg.I("w_min", 0), cfg.I("w_max", 20)
	out := map[int]int{}
	for _, part := range strings.Split(spec, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		kv := strings.Split(part, "=")
		if len(kv) != 2 {
			return nil, fmt.Errorf("bad override %q", part)
		}
		i, err := strconv.Atoi(strings.TrimLeft(strings.TrimSpace(kv[0]), "#"))
		if err != nil {
			return nil, err
		}
		w, err := strconv.Atoi(strings.TrimSpace(kv[1]))
		if err != nil {
			return nil, err
		}
		if !ok[i] {
			return nil, fmt.Errorf("#%d 不在本轮可调渠道内（禁用或不在本组模型范围的渠道不可改）", i)
		}
		if w < lo || w > hi {
			return nil, fmt.Errorf("#%d 权重 %d 超出 %d..%d", i, w, lo, hi)
		}
		out[i] = w
	}
	return out, nil
}

// ParseFinal ports parse_final (must cover every candidate channel).
func ParseFinal(spec string, chans []m.Channel, cfg m.M) (map[int]int, error) {
	out, err := ParseOverride(spec, chans, cfg)
	if err != nil {
		return nil, err
	}
	var miss []int
	for _, c := range chans {
		if _, ok := out[c.ID]; !ok {
			miss = append(miss, c.ID)
		}
	}
	sort.Ints(miss)
	if len(miss) > 0 {
		return nil, fmt.Errorf("--final 必须给全本轮所有可调渠道，缺少 %v", miss)
	}
	return out, nil
}

// Drift is {id: [expect, now]} (now nil when missing).
type Drift map[int][2]*int

func SystemDrift(expect, system map[int]int) Drift {
	d := Drift{}
	for i, w := range expect {
		s, ok := system[i]
		if !ok || s != w {
			var sp *int
			if ok {
				sp = m.I(s)
			}
			d[i] = [2]*int{m.I(w), sp}
		}
	}
	return d
}

// BlockingDrift ports blocking_drift(drift, final, tol). v3.18: with --final,
// a follow channel whose recompute only jitters (|new-expected| <= tol, both
// non-zero) no longer blocks; crossing 0 still blocks.
func BlockingDrift(d Drift, final map[int]int, tol int) Drift {
	if len(final) == 0 {
		return d
	}
	out := Drift{}
	for i, v := range d {
		if v[0] != nil && v[1] != nil && tol > 0 && *v[0] != 0 && *v[1] != 0 && abs(*v[1]-*v[0]) <= tol {
			continue
		}
		if f, ok := final[i]; !ok || f == *v[0] {
			out[i] = v
		}
	}
	return out
}

// NewcomerIDs ports newcomer_ids. nowTs is the ISO string Python passes.
func NewcomerIDs(cfg m.M, chans []m.Channel, rounds []*m.Rec, nowTs string) map[int]bool {
	nc := cfg.Sub("newcomer")
	out := map[int]bool{}
	if !nc.B("enabled", false) {
		return out
	}
	seen, first, miss := map[int]int{}, map[int]string{}, map[int]int{}
	gap := nc.I("reset_gap_rounds", 0)
	for _, r := range rounds {
		ids := map[int]bool{}
		for _, c := range r.Channels {
			ids[c.ID] = true
		}
		if gap > 0 {
			for k := range seen {
				if !ids[k] {
					miss[k]++
				}
			}
			for k := range ids {
				if miss[k] >= gap {
					seen[k] = 0
				}
				miss[k] = 0
			}
		}
		if !r.Applied {
			continue
		}
		for k := range ids {
			if seen[k] == 0 {
				first[k] = r.Ts
			}
			seen[k]++
		}
		if nc.B("reenable_from_toggle", true) && r.Toggle != nil {
			for _, a := range r.Toggle.Applied {
				if a.Op == "enable" {
					seen[a.ID], first[a.ID] = 1, r.Ts
				}
			}
		}
	}
	ageH := nc.F("max_age_h", 0)
	now := nowTs
	if now == "" && len(rounds) > 0 {
		now = rounds[len(rounds)-1].Ts
	}
	young := func(k int) bool {
		if ageH <= 0 || first[k] == "" || now == "" {
			return false
		}
		a, e1 := m.ParseTs(now)
		b, e2 := m.ParseTs(first[k])
		if e1 != nil || e2 != nil {
			return false
		}
		return a.Sub(b).Hours() < ageH
	}
	grad := nc.I("graduate_real_n60", 0)
	proven := map[int]bool{}
	if grad > 0 {
		for _, r := range rounds {
			if !r.Applied {
				continue
			}
			for _, c := range r.Channels {
				k := c.ID
				if first[k] != "" && r.Ts != "" && r.Ts < first[k] {
					continue
				}
				if f := r.Features[k]; f != nil && f.Mix != nil && f.Mix.NUser >= grad {
					proven[k] = true
				}
			}
		}
	}
	for _, c := range chans {
		k := c.ID
		if float64(seen[k]) < nc.F("min_rounds", 4) || young(k) || (grad > 0 && !proven[k]) {
			out[k] = true
		}
	}
	return out
}

var _ = time.Now

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
