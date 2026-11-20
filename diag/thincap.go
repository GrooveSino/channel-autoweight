package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// PeakN returns each channel's max 60m request count across recs (probes included).
func PeakN(recs []*m.Rec) map[int]int {
	out := map[int]int{}
	for _, r := range recs {
		for id, f := range r.Features {
			if f != nil && f.Long != nil && f.Long.N > out[id] {
				out[id] = f.Long.N
			}
		}
	}
	return out
}

// ThinCapFlags ports THINCAPGAP (Python v2.46): the system suggests a weight above cap
// for a channel with fewer than minN real requests in the last hour (since >= since).
// rows must already be real (non-probe) traffic. Diagnostic only.
// peak maps channel -> max 60m n seen in recent rounds (probes included); a channel whose
// peak >= estN is an established channel in an off-peak lull, not a newcomer, and is tagged
// so the slow-ramp cap is not misapplied to it. Pass nil to disable the tag.
func ThinCapFlags(rows []m.Row, target map[int]int, since int64, minN, cap int, peak map[int]int, estN int, estH float64) []string {
	n := map[int]int{}
	for _, r := range rows {
		if r.T >= since {
			n[r.Ch]++
		}
	}
	ids := []int{}
	for id, w := range target {
		if w > cap && n[id] < minN {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	s := []string{}
	for _, id := range ids {
		if p, ok := peak[id]; ok && p >= estN {
			s = append(s, fmt.Sprintf("THINCAPGAP #%d: 系统建议 %d，1h 真实只有 %d 条（<%d），但近 %.0fh 峰值 %d 条——老渠道低峰期，慢加量上限 %d 不适用，按步长与表现复核；只记录",
				id, target[id], n[id], minN, estH, p, cap))
			continue
		}
		s = append(s, fmt.Sprintf("THINCAPGAP #%d: 系统建议 %d，但 1h 真实请求只有 %d 条（<%d）——按慢慢加量规则人工上限 %d；只记录",
			id, target[id], n[id], minN, cap))
	}
	return s
}
