package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// UnsupportedFlags flags channels whose 1h real errors include >= minN upstream 400
// "feature not supported" rejections (Row.Uf, e.g. user-supplied input item IDs).
// Unlike content-safety errors these are a channel capability gap: any user whose
// client sends that feature fails whenever the weight routes them here. It reports
// how many affected tokens succeeded on another channel in the window (other
// channels accept the same clients). rows must already be real traffic.
// Diagnostic only.
func UnsupportedFlags(rows []m.Row, target map[int]int, since int64, minN int) []string {
	uf, errs := map[int]int{}, map[int]int{}
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
		if r.Uf != 0 {
			uf[r.Ch]++
			if toks[r.Ch] == nil {
				toks[r.Ch] = map[int]bool{}
			}
			toks[r.Ch][r.Tok] = true
		}
	}
	ids := []int{}
	for id, n := range uf {
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
		out = append(out, fmt.Sprintf("UNSUPPORTED #%d: 1h 真实错误 %d 条中 %d 条是上游 400 不支持请求特性（%d 个令牌，其中 %d 个同期在其他渠道成功）——渠道能力缺口，这类客户端路由到这里必失败，系统给 %d；只记录",
			id, errs[id], uf[id], len(toks[id]), elsewhere, target[id]))
	}
	return out
}
