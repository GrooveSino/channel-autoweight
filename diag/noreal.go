package diag

import (
	"fmt"
	"sort"
)

// NoRealFlags fires when the last hour carried zero real (non-probe) requests on every
// channel. The ranking is then built purely from probes and test-token traffic, so large
// jumps it proposes (|target-old| > maxStep) have no user evidence behind them; the
// reviewer should hold those weights. Diagnostic only; it never changes a weight.
func NoRealFlags(st []Sticky, target, old map[int]int, maxStep int) []string {
	for _, s := range st {
		if s.N > 0 {
			return nil
		}
	}
	jumps := []string{}
	ids := []int{}
	for id := range target {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		d := target[id] - old[id]
		if d > maxStep || -d > maxStep {
			jumps = append(jumps, fmt.Sprintf("#%d %d→%d", id, old[id], target[id]))
		}
	}
	if len(jumps) == 0 {
		return []string{"NOREAL: 1h 所有渠道真实请求都是 0 条，排名只来自探测；系统建议步长都不大；只记录"}
	}
	return []string{fmt.Sprintf("NOREAL: 1h 所有渠道真实请求都是 0 条，排名只来自探测，%v 这些大跳没有用户证据，建议保持；只记录", jumps)}
}
