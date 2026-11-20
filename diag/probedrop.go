package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// ProbeDropFlags fires per channel when the system lowers its weight (target < old) but
// the channel's last-hour real (non-probe) traffic is thin (< minReal) and error-free.
// Such a drop is driven by probe latency or tiny samples, not by user evidence, so the
// reviewer should hold the weight (fast de-weighting is for real errors). NOREAL only
// covers the all-zero case; this covers individual channels. Diagnostic only.
func ProbeDropFlags(user []m.Row, target, old map[int]int, since int64, minReal int) []string {
	n, e := map[int]int{}, map[int]int{}
	for _, r := range user {
		if r.T < since {
			continue
		}
		n[r.Ch]++
		if r.Type == 5 {
			e[r.Ch]++
		}
	}
	ids := []int{}
	for id := range target {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := []string{}
	for _, id := range ids {
		o, ok := old[id]
		if !ok || target[id] >= o || e[id] > 0 || n[id] >= minReal {
			continue
		}
		out = append(out, fmt.Sprintf("PROBEDROP #%d: 系统 %d→%d 降权，但 1h 真实只有 %d 条且 0 错——降权只由探测/小样本驱动，没有用户证据，建议保持；只记录",
			id, o, target[id], n[id]))
	}
	return out
}
