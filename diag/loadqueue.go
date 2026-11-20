package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// LoadQueueFlags flags channels whose real-success total time grows with their own
// concurrency (queueing upstream, e.g. a channel that serialises above N in-flight).
// For every real success in the window it counts the channel's other requests in
// flight at its start (start = created_at - use_time) and splits successes into a
// low (< hiConc) and high (>= hiConc) bucket. It flags when both buckets have >= minN
// and the high-bucket p50 is >= ratio x the low-bucket p50: adding weight there buys
// latency, not capacity. rows must already be real traffic. Diagnostic only.
func LoadQueueFlags(rows []m.Row, target map[int]int, since int64, hiConc, minN int, ratio float64) []string {
	type iv struct{ s, e int64 }
	by := map[int][]iv{}
	for _, r := range rows {
		if r.T < since {
			continue
		}
		by[r.Ch] = append(by[r.Ch], iv{r.T - int64(r.Ut), r.T})
	}
	ids := []int{}
	for id := range by {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := []string{}
	for _, id := range ids {
		var lo, hi []float64
		maxC := 0
		for _, r := range rows {
			if r.Ch != id || r.Type != 2 || r.T < since {
				continue
			}
			st := r.T - int64(r.Ut)
			c := 0
			for _, o := range by[id] {
				if o.s <= st && o.e > st {
					c++
				}
			}
			c-- // itself
			if c > maxC {
				maxC = c
			}
			if c >= hiConc {
				hi = append(hi, float64(r.Ut))
			} else {
				lo = append(lo, float64(r.Ut))
			}
		}
		if len(lo) < minN || len(hi) < minN {
			continue
		}
		pl, ph := median(lo), median(hi)
		if pl <= 0 || ph < ratio*pl {
			continue
		}
		out = append(out, fmt.Sprintf("LOADQUEUE #%d: 1h 真实成功按同渠道在途并发分组，<%d 并发 %d 条 p50 %.0fs，≥%d 并发 %d 条 p50 %.0fs（%.1f×，最高并发 %d）——加量换来的是排队，系统给 %d；只记录",
			id, hiConc, len(lo), pl, hiConc, len(hi), ph, ph/pl, maxC, target[id]))
	}
	return out
}
