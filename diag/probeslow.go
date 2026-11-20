package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// ProbeSlowFlags compares channels on the dedicated test token only (same request shape
// everywhere, so a fair same-input comparison even at night when real traffic is sticky
// or absent). A channel whose mean total time on that token (>= minN successes since
// `since`) is >= ratio * the median of the other channels' means is flagged.
// Diagnostic only; it never changes a weight.
func ProbeSlowFlags(rows []m.Row, tok int, target map[int]int, since int64, minN int, ratio float64) []string {
	sum, n := map[int]float64{}, map[int]int{}
	for _, r := range rows {
		if r.Tok == tok && r.Type == 2 && r.T >= since {
			sum[r.Ch] += float64(r.Ut)
			n[r.Ch]++
		}
	}
	mean := map[int]float64{}
	ids := []int{}
	for id, c := range n {
		if c >= minN {
			mean[id] = sum[id] / float64(c)
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	out := []string{}
	if len(ids) < 3 {
		return out
	}
	for _, id := range ids {
		others := []float64{}
		for _, o := range ids {
			if o != id {
				others = append(others, mean[o])
			}
		}
		sort.Float64s(others)
		med := others[len(others)/2]
		if len(others)%2 == 0 {
			med = (others[len(others)/2-1] + others[len(others)/2]) / 2
		}
		if med <= 0 || mean[id] < ratio*med {
			continue
		}
		out = append(out, fmt.Sprintf("PROBESLOW #%d: 测试令牌 %d 同输入 %d 条均耗时 %.1fs，是其他渠道中位 %.1fs 的 %.1f×，系统给 %d；只记录",
			id, tok, n[id], mean[id], med, mean[id]/med, target[id]))
	}
	return out
}
