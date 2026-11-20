// Package trial runs and judges new-channel trial batches (Groove's admission
// rule): >= N requests per main model incl. large-context ones, error rate
// <= 1%, no empty replies, TPS >= 0.8x and p90 <= 1.3x of a reference channel
// measured in the same interleaved batch on the same model.
package trial

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
)

// Rec is one request result printed by Remote.
type Rec struct {
	Ch     int      `json:"ch"`
	Big    bool     `json:"big"`
	K      int      `json:"k"`
	Status *int     `json:"status"`
	Ttft   *float64 `json:"ttft"`
	Total  float64  `json:"total"`
	Deltas int      `json:"deltas"`
	Out    *int     `json:"out"`
	In     *int     `json:"in"`
	Err    *string  `json:"err"`
}

// OK: HTTP 200, no error, some streamed text.
func (r Rec) OK() bool { return r.Status != nil && *r.Status == 200 && r.Err == nil && r.Deltas > 0 }

// Empty: HTTP 200 without error but no text (the EMPTYSTREAM failure mode).
func (r Rec) Empty() bool {
	return r.Status != nil && *r.Status == 200 && r.Err == nil && (r.Deltas == 0 || (r.Out != nil && *r.Out == 0))
}

// Throttled: the site's own per-user rate limit (429 "请求数限制"); the
// request never reached the channel, so it is excluded from its stats.
func (r Rec) Throttled() bool {
	return r.Status != nil && *r.Status == 429 && r.Err != nil && strings.Contains(*r.Err, "请求数限制")
}

// TPS: output tokens per second after the first token (nil if not measurable).
func (r Rec) TPS() *float64 {
	if !r.OK() || r.Ttft == nil || r.Total-*r.Ttft < 0.3 {
		return nil
	}
	n := r.Deltas // stream chunks ~ tokens when usage is not reported
	if r.Out != nil {
		n = *r.Out
	}
	if n < 20 {
		return nil
	}
	v := float64(n) / (r.Total - *r.Ttft)
	return &v
}

// Parse reads Remote's JSON lines, skipping anything else.
func Parse(stdout string) []Rec {
	var out []Rec
	for _, l := range strings.Split(stdout, "\n") {
		var r Rec
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "{") && json.Unmarshal([]byte(l), &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

// Stats summarises one channel's batch.
type Stats struct {
	Ch, Throttled             int
	N, Err, Empty, Big, BigOK int
	P50, P90, Ttft50, TPS50   float64
}

func quant(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	pos := q * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return s[lo] + (s[hi]-s[lo])*(pos-float64(lo))
}

// Summarise: latency quantiles over successful requests only.
func Summarise(rs []Rec, ch int) Stats {
	st := Stats{Ch: ch}
	var tot, tt, tps []float64
	for _, r := range rs {
		if r.Ch != ch {
			continue
		}
		if r.Throttled() {
			st.Throttled++
			continue
		}
		st.N++
		if r.Big {
			st.Big++
		}
		switch {
		case r.Empty():
			st.Empty++
		case !r.OK():
			st.Err++
		default:
			if r.Big {
				st.BigOK++
			}
			tot = append(tot, r.Total)
			tt = append(tt, *r.Ttft)
			if v := r.TPS(); v != nil {
				tps = append(tps, *v)
			}
		}
	}
	st.P50, st.P90, st.Ttft50, st.TPS50 = quant(tot, .5), quant(tot, .9), quant(tt, .5), quant(tps, .5)
	return st
}

// Rule holds the admission thresholds.
type Rule struct {
	MinN                   int
	MaxErr, MinTPS, MaxP90 float64
}

var Default = Rule{MinN: 50, MaxErr: 0.01, MinTPS: 0.8, MaxP90: 1.3}

// Judge returns pass and the failed reasons for cand against ref.
func (u Rule) Judge(cand, ref Stats) (bool, []string) {
	var why []string
	if cand.N < u.MinN {
		why = append(why, "样本不足")
	}
	if cand.N > 0 && float64(cand.Err+cand.Empty)/float64(cand.N) > u.MaxErr {
		why = append(why, "错误率超 1%")
	}
	if cand.Empty > 0 {
		why = append(why, "有空回复")
	}
	if cand.Big == 0 || cand.BigOK < cand.Big {
		why = append(why, "大上下文未全部成功")
	}
	if !(cand.TPS50 >= u.MinTPS*ref.TPS50) {
		why = append(why, "输出速度低于 0.8×")
	}
	if !(cand.P90 <= u.MaxP90*ref.P90) {
		why = append(why, "p90 超过 1.3×")
	}
	return len(why) == 0, why
}
