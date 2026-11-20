package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// ModelGapFlags compares real-success total time per model across channels. For every
// model with >= minN successes on at least two channels in the window, it flags a
// channel whose p50 on that model is >= ratio x the fastest other channel's p50 on the
// same model. A channel can look fine on its blended p50 (mix of fast models) while
// being slow on one model users actually send it; this surfaces that gap so per-model
// routing or weight choices are not misled by the mix. rows must be real traffic.
// Diagnostic only.
func ModelGapFlags(rows []m.Row, target map[int]int, since int64, minN int, ratio float64) []string {
	ut := map[string]map[int][]float64{}
	spk := map[string]map[int][]float64{} // seconds per 1k completion tokens
	for _, r := range rows {
		if r.Type != 2 || r.T < since || r.Mdl == "" {
			continue
		}
		if ut[r.Mdl] == nil {
			ut[r.Mdl] = map[int][]float64{}
		}
		ut[r.Mdl][r.Ch] = append(ut[r.Mdl][r.Ch], float64(r.Ut))
		if r.Ct > 0 {
			if spk[r.Mdl] == nil {
				spk[r.Mdl] = map[int][]float64{}
			}
			spk[r.Mdl][r.Ch] = append(spk[r.Mdl][r.Ch], float64(r.Ut)*1000/float64(r.Ct))
		}
	}
	models := []string{}
	for k := range ut {
		models = append(models, k)
	}
	sort.Strings(models)
	out := []string{}
	for _, md := range models {
		p50 := map[int]float64{}
		n := map[int]int{}
		for ch, v := range ut[md] {
			if len(v) >= minN {
				p50[ch], n[ch] = median(v), len(v)
			}
		}
		if len(p50) < 2 {
			continue
		}
		ids := []int{}
		for ch := range p50 {
			ids = append(ids, ch)
		}
		sort.Ints(ids)
		for _, ch := range ids {
			best, bid := -1.0, 0
			for _, o := range ids {
				if o != ch && (best < 0 || p50[o] < best) {
					best, bid = p50[o], o
				}
			}
			if best <= 0 || p50[ch] < ratio*best {
				continue
			}
			out = append(out, fmt.Sprintf("MODELGAP #%d %s: 1h 真实成功 %d 条 p50 %.0fs，是同模型最快 #%d（%d 条 p50 %.0fs）的 %.1f×%s，系统给 %d；只记录",
				ch, md, n[ch], p50[ch], bid, n[bid], best, p50[ch]/best, normNote(spk[md][ch], spk[md][bid], minN, ratio), target[ch]))
		}
	}
	return out
}

// normNote re-compares the pair on seconds per 1k completion tokens, so a gap caused by
// one channel getting longer answers (heavier load) is not read as a slow channel.
func normNote(a, b []float64, minN int, ratio float64) string {
	if len(a) < minN || len(b) < minN {
		return "（输出量样本不足，未归一）"
	}
	ma, mb := median(a), median(b)
	if mb <= 0 {
		return ""
	}
	r := ma / mb
	if r < ratio {
		return fmt.Sprintf("，但按每千输出 token 耗时归一后仅 %.1f×（多半是请求负载差异，不是渠道慢）", r)
	}
	return fmt.Sprintf("，按每千输出 token 耗时归一后仍 %.1f×（确实慢）", r)
}
