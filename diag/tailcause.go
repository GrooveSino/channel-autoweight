package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// TailCause explains a channel's heavy latency tail from its real successes: it takes
// the slowest 10% (the tail) and compares their median completion tokens and seconds
// per 1k completion tokens with the other 90%. If the tail answers are >= 2x longer
// but not slower per token, the tail is request load (long outputs), not the channel;
// if per-token time is >= 2x, the channel itself is slow/queueing on those requests.
// It also names the model most of the tail came from. Diagnostic only.
func TailCause(rows []m.Row, ch int, since int64) string {
	xs := []m.Row{}
	for _, r := range rows {
		if r.Ch == ch && r.T >= since && r.Type == 2 {
			xs = append(xs, r)
		}
	}
	if len(xs) < 20 {
		return ""
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i].Ut < xs[j].Ut })
	cut := len(xs) * 9 / 10
	head, tail := xs[:cut], xs[cut:]
	ctH, ctT := medCt(head), medCt(tail)
	spH, spT := medSpk(head), medSpk(tail)
	mc := map[string]int{}
	for _, r := range tail {
		mc[r.Mdl]++
	}
	top, topN := "", 0
	for k, v := range mc {
		if v > topN || (v == topN && k < top) {
			top, topN = k, v
		}
	}
	zero := 0
	for _, r := range tail {
		if r.Ct == 0 {
			zero++
		}
	}
	verdict := "原因不明"
	switch {
	case zero*2 > len(tail):
		verdict = fmt.Sprintf("尾部 %d 条 0 输出却等了很久（疑似卡住后空回复，用户体验差）", zero)
	case spH > 0 && spT >= 2*spH:
		verdict = "同样长度也慢（渠道排队或上游慢）"
	case ctH > 0 && ctT >= 2*ctH:
		verdict = "主要是输出更长（请求负载，不是渠道慢）"
	}
	return fmt.Sprintf("尾部 %d 条中 %s 占 %d；输出 token 中位 %.0f vs 其余 %.0f，每千 token 耗时 %.0fs vs %.0fs——%s",
		len(tail), top, topN, ctT, ctH, spT, spH, verdict)
}

func medCt(rs []m.Row) float64 {
	v := []float64{}
	for _, r := range rs {
		v = append(v, float64(r.Ct))
	}
	return median(v)
}

func medSpk(rs []m.Row) float64 {
	v := []float64{}
	for _, r := range rs {
		if r.Ct > 0 {
			v = append(v, float64(r.Ut)*1000/float64(r.Ct))
		}
	}
	return median(v)
}
