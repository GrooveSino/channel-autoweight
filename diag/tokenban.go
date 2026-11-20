package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// TokenBanFlags flags channels whose 1h real errors are dominated by a single token
// (>= minN errors and >= share of the channel's errors) while other tokens still
// succeed on the same channel in the window. That pattern looks like the upstream
// blocking one user/token, not a whole-channel outage, so the reviewer should not
// cut the channel hard on error rate alone. rows are features.ChanRows (v4.98: same
// count as ChanStat and the report table).
// Diagnostic only.
func TokenBanFlags(rows []m.Row, target map[int]int, since int64, minN int, share float64) []string {
	errs := map[int]int{}
	byTok := map[int]map[int]int{} // ch -> tok -> errors
	okTok := map[int]map[int]int{} // ch -> tok -> successes
	for _, r := range rows {
		if r.T < since {
			continue
		}
		if r.Type == 2 {
			if okTok[r.Ch] == nil {
				okTok[r.Ch] = map[int]int{}
			}
			okTok[r.Ch][r.Tok]++
			continue
		}
		if r.Type != 5 {
			continue
		}
		errs[r.Ch]++
		if byTok[r.Ch] == nil {
			byTok[r.Ch] = map[int]int{}
		}
		byTok[r.Ch][r.Tok]++
	}
	ids := []int{}
	for id := range errs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := []string{}
	for _, id := range ids {
		top, topN := 0, 0
		for tok, n := range byTok[id] {
			if n > topN || (n == topN && tok < top) {
				top, topN = tok, n
			}
		}
		if topN < minN || float64(topN) < share*float64(errs[id]) {
			continue
		}
		otherOK, otherToks := 0, 0
		for tok, n := range okTok[id] {
			if tok != top {
				otherOK += n
				otherToks++
			}
		}
		if otherOK == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("TOKENBAN #%d: 1h 真实错误 %d 条中 %d 条来自令牌 %d（该令牌同期成功 %d 条），其他 %d 个令牌同期在该渠道成功 %d 条——像上游封单个令牌而非整渠道故障，系统给 %d；只记录",
			id, errs[id], topN, top, okTok[id][top], otherToks, otherOK, target[id]))
	}
	return out
}
