package diag

import "fmt"

// TrialProg judges a channel's 48h real-traffic trial (Groove's rule: weight 2 for 48h,
// >= minN real requests, error rate <= maxErr, no empty replies) from aggregates since
// its admission pass. Diagnostic only; it never changes a weight.
type TrialProg struct {
	Ch, N, Err, Empty, Tokens int
	P50, P90, ElapsedH        float64
}

// TrialProgFlag returns one TRIALPROG line with progress, pace projection and verdict.
func TrialProgFlag(t TrialProg, hours float64, minN int, maxErr float64) string {
	er := 0.0
	if t.N > 0 {
		er = float64(t.Err) / float64(t.N)
	}
	proj := 0
	if t.ElapsedH > 0 {
		proj = int(float64(t.N) / t.ElapsedH * hours)
	}
	verdict := "进行中"
	switch {
	case t.N >= 20 && er > maxErr:
		verdict = fmt.Sprintf("错误率 %.1f%% 超过 %.0f%%，不应加档", er*100, maxErr*100)
	case t.Empty > 0:
		verdict = fmt.Sprintf("出现 %d 条空回复，不应加档", t.Empty)
	case t.ElapsedH >= hours && t.N >= minN:
		verdict = "已达标，可每轮最多加两档"
	case t.ElapsedH >= hours:
		verdict = fmt.Sprintf("已满 %.0fh 但真实请求不足 %d 条，继续保持", hours, minN)
	case proj < minN:
		verdict = fmt.Sprintf("按当前速度 %.0fh 约 %d 条，可能不足 %d 条", hours, proj, minN)
	}
	return fmt.Sprintf("TRIALPROG #%d: 试跑 %.1f/%.0fh，真实 %d/%d 条（%d 个令牌），错误 %d（%.1f%%），空回复 %d，p50/p90 %.0f/%.0fs；%s；只记录",
		t.Ch, t.ElapsedH, hours, t.N, minN, t.Tokens, t.Err, er*100, t.Empty, t.P50, t.P90, verdict)
}
