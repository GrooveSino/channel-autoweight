package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// ContentSafeFlags flags channels whose 1h real errors include >= minN upstream
// content-safety rejections (Row.Cs). These errors depend on request content, so the
// reviewer should not read them as an outage; it also reports how many of the
// affected tokens succeeded on another channel in the same window (other channels
// accept the same users). rows must already be real traffic. Diagnostic only.
func ContentSafeFlags(rows []m.Row, target map[int]int, since int64, minN int) []string {
	cs, errs := map[int]int{}, map[int]int{}
	toks := map[int]map[int]bool{}
	okOn := map[int]map[int]bool{} // token -> channels with a success
	for _, r := range rows {
		if r.T < since {
			continue
		}
		if r.Type == 2 {
			if okOn[r.Tok] == nil {
				okOn[r.Tok] = map[int]bool{}
			}
			okOn[r.Tok][r.Ch] = true
			continue
		}
		errs[r.Ch]++
		if r.Cs != 0 {
			cs[r.Ch]++
			if toks[r.Ch] == nil {
				toks[r.Ch] = map[int]bool{}
			}
			toks[r.Ch][r.Tok] = true
		}
	}
	ids := []int{}
	for id, n := range cs {
		if n >= minN {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	out := []string{}
	for _, id := range ids {
		elsewhere := 0
		for tok := range toks[id] {
			for ch := range okOn[tok] {
				if ch != id {
					elsewhere++
					break
				}
			}
		}
		out = append(out, fmt.Sprintf("CONTENTSAFE #%d: 1h 真实错误 %d 条中 %d 条是上游内容安全拦截（%d 个令牌，其中 %d 个同期在其他渠道成功）——随请求内容而非渠道故障，系统给 %d；只记录",
			id, errs[id], cs[id], len(toks[id]), elsewhere, target[id]))
	}
	return out
}
