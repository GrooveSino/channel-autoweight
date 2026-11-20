package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// RecoveredFlags flags a channel whose 1h real error rate is high (>= minErr since
// `since`) but whose recent window (last recentMin minutes before now) is clean (0 errors, >= minOK
// successes) while the system still gives it <= 1. It marks a burst outage that has
// ended, so the reviewer can keep a small non-zero weight instead of zeroing it.
// rows must already be real (non-probe) traffic. Diagnostic only.
func RecoveredFlags(rows []m.Row, target map[int]int, since, now int64, recentMin int, minErr float64, minOK int) []string {
	recent := now - int64(recentMin)*60
	n, e, rok, rerr := map[int]int{}, map[int]int{}, map[int]int{}, map[int]int{}
	for _, r := range rows {
		if r.T < since {
			continue
		}
		n[r.Ch]++
		if r.Type == 5 {
			e[r.Ch]++
		}
		if r.T >= recent {
			if r.Type == 5 {
				rerr[r.Ch]++
			} else {
				rok[r.Ch]++
			}
		}
	}
	ids := []int{}
	for id, w := range target {
		if w <= 1 && n[id] > 0 && float64(e[id]) >= minErr*float64(n[id]) && rerr[id] == 0 && rok[id] >= minOK {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	s := []string{}
	for _, id := range ids {
		s = append(s, fmt.Sprintf("RECOVERED #%d: 1h 错误率 %.0f%%（%d/%d），但近 %d 分钟 %d 条成功 0 错误——故障已结束，系统给 %d；只记录",
			id, 100*float64(e[id])/float64(n[id]), e[id], n[id], recentMin, rok[id], target[id]))
	}
	return s
}
