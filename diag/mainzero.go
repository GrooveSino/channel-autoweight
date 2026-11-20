package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// MainZeroFlags flags the known small-sample bias in the opposite direction: the system
// zeroes a clean main channel that carries a large share of real traffic (>= minN real
// requests since `since`, real error rate <= maxErr). rows must already be real
// (non-probe) traffic. Diagnostic only: the reviewer keeps clean mains above 0.
func MainZeroFlags(rows []m.Row, target map[int]int, since int64, minN int, maxErr float64) []string {
	n, e, tot := map[int]int{}, map[int]int{}, 0
	for _, r := range rows {
		if r.T < since {
			continue
		}
		n[r.Ch]++
		tot++
		if r.Type == 5 {
			e[r.Ch]++
		}
	}
	ids := []int{}
	for id, w := range target {
		if w == 0 && n[id] >= minN && float64(e[id]) <= maxErr*float64(n[id]) {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	s := []string{}
	for _, id := range ids {
		s = append(s, fmt.Sprintf("MAINZERO #%d: 系统建议 0，但 1h 真实 %d 条（占 %.0f%%）、错误 %d 条——干净主力不清零；只记录",
			id, n[id], 100*float64(n[id])/float64(tot), e[id]))
	}
	return s
}
