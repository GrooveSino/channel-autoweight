package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// LongTailFlags flags a channel whose 1h real successes (>= minN since `since`) have a
// heavy latency tail: p90 total time >= ratio * p50 and p90 >= minP90 seconds. Users on
// such a channel often wait far longer than the median suggests. rows must already be
// real (non-probe) traffic. Channels with thinN <= n < minN are still checked and marked
// as a small sample, so a thin channel whose few users wait very long is not silent
// (thinN <= 0 or >= minN disables that). Diagnostic only.
func LongTailFlags(rows []m.Row, target map[int]int, since int64, minN, thinN int, ratio, minP90 float64) []string {
	ut := map[int][]int{}
	for _, r := range rows {
		if r.T >= since && r.Type == 2 {
			ut[r.Ch] = append(ut[r.Ch], r.Ut)
		}
	}
	ids := []int{}
	for id := range ut {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	s := []string{}
	for _, id := range ids {
		xs := ut[id]
		thin := len(xs) < minN
		if thin && (thinN <= 0 || len(xs) < thinN) {
			continue
		}
		sort.Ints(xs)
		p50, p90 := float64(xs[len(xs)/2]), float64(xs[len(xs)*9/10])
		if p50 <= 0 || p90 < minP90 || p90 < ratio*p50 {
			continue
		}
		tag := ""
		if thin {
			tag = fmt.Sprintf("（小样本 <%d）", minN)
		}
		s = append(s, fmt.Sprintf("LONGTAIL%s #%d: 1h 真实成功 %d 条，总耗时 p50 %.0fs、p90 %.0fs（%.1f×）——长尾重，系统给 %d；%s；只记录",
			tag, id, len(xs), p50, p90, p90/p50, target[id], TailCause(rows, id, since)))
	}
	return s
}
