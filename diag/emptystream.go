package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// EmptyStreamFlags looks at the site probes recorded in recent rounds and flags channels
// that answered HTTP 200 but streamed zero deltas (a hang that ends with an empty
// response). Users see these as a long wait with no output, yet they never show up as
// type-5 errors in logs, so error rates miss them. Diagnostic only.
func EmptyStreamFlags(recs []*m.Rec, target map[int]int, now, windowMin float64) []string {
	empty, total := map[int]int{}, map[int]int{}
	wait := map[int]float64{}
	for _, r := range recs {
		if r.TsTime.IsZero() || r.ProbesReused {
			continue
		}
		age := (now - m.Epoch(r.TsTime)) / 60
		if age < 0 || age > windowMin {
			continue
		}
		for cid, ps := range r.Probes {
			if _, ok := target[cid]; !ok {
				continue
			}
			for _, p := range ps {
				total[cid]++
				if isEmptyStream(p) {
					empty[cid]++
					if p.Total != nil {
						wait[cid] += *p.Total
					}
				}
			}
		}
	}
	ids := []int{}
	for id := range empty {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := []string{}
	for _, id := range ids {
		out = append(out, fmt.Sprintf("EMPTYSTREAM #%d: 近 %.0f 分钟站点探测 %d/%d 次 HTTP 200 但 0 输出，平均卡 %.1fs 才结束（日志不算错误）；只记录",
			id, windowMin, empty[id], total[id], wait[id]/float64(empty[id])))
	}
	return out
}

func isEmptyStream(p m.Probe) bool {
	return !p.OK && p.Status != nil && *p.Status == 200 && (p.Deltas == nil || *p.Deltas == 0)
}
