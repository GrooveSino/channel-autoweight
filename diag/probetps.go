package diag

import (
	"fmt"
	"sort"
	"strings"

	m "autoweight/model"
)

// ProbeTpsFlags complements PROBESLOW: the dedicated test token's requests can produce
// very different output lengths per channel, so mean total time alone punishes a channel
// that simply wrote more. Here each channel's output speed on that token is
// sum(completion tokens) / sum(total seconds) over successful requests with output,
// computed per model and compared with the median of the other channels on the same
// model. A channel slower than 1/ratio of that median on its main model is flagged, and
// one line ranks all channels by their speed index. Diagnostic only.
func ProbeTpsFlags(rows []m.Row, tok int, target map[int]int, since int64, minN int, ratio float64) []string {
	type acc struct {
		ct, ut float64
		n      int
	}
	by := map[string]map[int]*acc{}
	for _, r := range rows {
		if r.Tok != tok || r.Type != 2 || r.T < since || r.Ct <= 0 || r.Ut <= 0 {
			continue
		}
		if by[r.Mdl] == nil {
			by[r.Mdl] = map[int]*acc{}
		}
		a := by[r.Mdl][r.Ch]
		if a == nil {
			a = &acc{}
			by[r.Mdl][r.Ch] = a
		}
		a.ct += float64(r.Ct)
		a.ut += float64(r.Ut)
		a.n++
	}
	// per channel: weighted mean of index (tps / others' median) across models
	isum, iw := map[int]float64{}, map[int]float64{}
	for _, chs := range by {
		tps := map[int]float64{}
		for ch, a := range chs {
			if a.n >= minN {
				tps[ch] = a.ct / a.ut
			}
		}
		if len(tps) < 3 {
			continue
		}
		for ch, v := range tps {
			others := []float64{}
			for o, ov := range tps {
				if o != ch {
					others = append(others, ov)
				}
			}
			med := median(others)
			if med <= 0 {
				continue
			}
			w := float64(chs[ch].n)
			isum[ch] += w * v / med
			iw[ch] += w
		}
	}
	out := []string{}
	if len(iw) < 3 {
		return out
	}
	ids := []int{}
	for id := range iw {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return isum[ids[i]]/iw[ids[i]] > isum[ids[j]]/iw[ids[j]] })
	parts := []string{}
	for _, id := range ids {
		idx := isum[id] / iw[id]
		parts = append(parts, fmt.Sprintf("#%d %.2f×", id, idx))
		if ratio > 0 && idx <= 1/ratio {
			out = append(out, fmt.Sprintf("PROBETPS #%d: 测试令牌 %d 输出速度只有同模型其他渠道中位的 %.2f×，系统给 %d；只记录", id, tok, idx, target[id]))
		}
	}
	return append(out, "PROBETPS 排名（测试令牌按模型的输出速度指数）: "+strings.Join(parts, " > ")+"；只记录")
}
