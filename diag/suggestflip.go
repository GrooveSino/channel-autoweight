package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// SuggestFlipFlags scans the shadow points of the last windowMin minutes and flags
// channels whose system suggestion swung between the bottom (<= lo) and the top
// (>= hi) at least minFlips times. Such a suggestion is noise (thin samples, probe
// variance), so the reviewer should not follow it in one step. Diagnostic only.
func SuggestFlipFlags(shadow []*m.Rec, target map[int]int, now, windowMin float64, lo, hi, minFlips int) []string {
	type st struct{ flips, last, n int }
	s := map[int]*st{}
	for _, r := range shadow {
		if r.TsTime.IsZero() {
			continue
		}
		age := (now - m.Epoch(r.TsTime)) / 60
		if age < 0 || age > windowMin {
			continue
		}
		for cid, w := range r.Decision.Target {
			if _, ok := target[cid]; !ok {
				continue
			}
			band := 0
			if w <= lo {
				band = -1
			} else if w >= hi {
				band = 1
			}
			x := s[cid]
			if x == nil {
				x = &st{}
				s[cid] = x
			}
			x.n++
			if band != 0 {
				if x.last != 0 && band != x.last {
					x.flips++
				}
				x.last = band
			}
		}
	}
	ids := []int{}
	for id, x := range s {
		if x.flips >= minFlips {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	out := []string{}
	for _, id := range ids {
		out = append(out, fmt.Sprintf("SUGGESTFLIP #%d: 近 %.0f 分钟 %d 个影子点里系统建议在 ≤%d 和 ≥%d 之间来回翻 %d 次，本轮建议 %d 不稳定，别一步跟；只记录",
			id, windowMin, s[id].n, lo, hi, s[id].flips, target[id]))
	}
	return out
}
