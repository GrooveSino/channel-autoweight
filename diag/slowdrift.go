package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// SlowDriftFlags compares each channel's real-success total-time p50 in the last hour
// [now-3600, now) with the hour before [now-7200, now-3600). It flags a channel whose p50
// rose by >= ratio and is now >= minP50 seconds, with >= minN successes in both hours,
// so a main that suddenly got slower is noticed even when it has zero errors. rows must
// already be real (non-probe) traffic. Diagnostic only.
func SlowDriftFlags(rows []m.Row, target map[int]int, now int64, minN int, ratio, minP50 float64) []string {
	cur, prev := map[int][]int{}, map[int][]int{}
	for _, r := range rows {
		if r.Type != 2 {
			continue
		}
		switch {
		case r.T >= now-3600 && r.T < now+60:
			cur[r.Ch] = append(cur[r.Ch], r.Ut)
		case r.T >= now-7200 && r.T < now-3600:
			prev[r.Ch] = append(prev[r.Ch], r.Ut)
		}
	}
	ids := []int{}
	for id := range cur {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	s := []string{}
	for _, id := range ids {
		a, b := cur[id], prev[id]
		if len(a) < minN || len(b) < minN {
			continue
		}
		pa, pb := med(a), med(b)
		if pa < minP50 || pb <= 0 || pa < ratio*pb {
			continue
		}
		s = append(s, fmt.Sprintf("SLOWDRIFT #%d: 真实成功总耗时 p50 从上一小时 %.0fs（%d 条）升到 %.0fs（%d 条，%.1f×），系统给 %d；只记录",
			id, pb, len(b), pa, len(a), pa/pb, target[id]))
	}
	return s
}

func med(xs []int) float64 {
	ys := append([]int(nil), xs...)
	sort.Ints(ys)
	return float64(ys[len(ys)/2])
}
