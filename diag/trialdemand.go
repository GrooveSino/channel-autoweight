package diag

import "fmt"

// TrialDemandFlag explains a stalled trial: is the trial channel's model simply not being
// requested (off-peak), or is demand there but routed elsewhere? w/wSum is the channel's
// share of the weight among channels serving those models. Diagnostic only.
// fresh/freshOnCh are the non-affinity (weight-routed) requests; tokens = distinct real tokens.
// Only fresh requests follow weights, so the verdict compares the fresh share, not the total.
func TrialDemandFlag(ch, minutes, total, onCh, fresh, freshOnCh, tokens int, lastAgoMin, w, wSum float64) string {
	share, want := 0.0, 0.0
	if total > 0 {
		share = float64(onCh) / float64(total)
	}
	if wSum > 0 {
		want = w / wSum
	}
	last := "48h 内无真实请求"
	if lastAgoMin >= 0 {
		last = fmt.Sprintf("上一条真实请求在 %.0f 分钟前", lastAgoMin)
	}
	fshare := 0.0
	if fresh > 0 {
		fshare = float64(freshOnCh) / float64(fresh)
	}
	verdict := "分到的比例和权重相当"
	switch {
	case total < 10:
		verdict = "这些模型本身需求很少（低谷期），进度慢属正常"
	case fresh < 5:
		verdict = fmt.Sprintf("需求几乎全是 %d 个令牌的会话亲和命中，按权重分配的新会话只有 %d 条，分不到属正常", tokens, fresh)
	case want > 0 && fshare < want*0.3:
		verdict = "按权重分配的新会话也明显少于权重比例（可能是路由规则或能力过滤），可留意"
	}
	return fmt.Sprintf("TRIALDEMAND #%d: 近 %d 分钟同模型真实请求共 %d 条（%d 个令牌），本渠道 %d 条（%.0f%%，权重占比 %.0f%%）；其中新会话 %d 条、本渠道 %d 条（%.0f%%），%s；%s；只记录",
		ch, minutes, total, tokens, onCh, share*100, want*100, fresh, freshOnCh, fshare*100, last, verdict)
}
