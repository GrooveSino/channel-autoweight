package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// HangTailFlags counts, per channel, requests in the last hour (success or error, probes
// included) whose total time is >= hangSec. A channel with >= minHang such requests making
// up >= minShare of its traffic is flagged: users sitting minutes on one request is worse UX
// than p90 alone suggests, especially on thin-sample channels. probeTok marks test-token rows
// so the reviewer sees how many hangs were real. Diagnostic only.
func HangTailFlags(rows []m.Row, target map[int]int, now int64, hangSec, minHang int, minShare float64, probeTok int) []string {
	tot, hang, hangProbe, worst := map[int]int{}, map[int]int{}, map[int]int{}, map[int]int{}
	for _, r := range rows {
		if r.T < now-3600 || r.T >= now+60 || (r.Type != 2 && r.Type != 5) {
			continue
		}
		tot[r.Ch]++
		if r.Ut < hangSec {
			continue
		}
		hang[r.Ch]++
		if r.Tok == probeTok {
			hangProbe[r.Ch]++
		}
		if r.Ut > worst[r.Ch] {
			worst[r.Ch] = r.Ut
		}
	}
	ids := []int{}
	for id := range hang {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	s := []string{}
	for _, id := range ids {
		h, n := hang[id], tot[id]
		if h < minHang || float64(h) < minShare*float64(n) {
			continue
		}
		s = append(s, fmt.Sprintf("HANGTAIL #%d: 1h 内 %d/%d 条（%.0f%%）总耗时 ≥%ds（其中探测 %d 条），最长 %ds，系统给 %d；只记录",
			id, h, n, 100*float64(h)/float64(n), hangSec, hangProbe[id], worst[id], target[id]))
	}
	return s
}
