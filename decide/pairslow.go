package decide

import (
	"fmt"

	m "autoweight/model"
)

// PairVerdict (v5.01) reads the same-token paired latency ratio: the decision
// window (1h) when it has >= min_tokens paired tokens and >= min_rows rows,
// else the reference window (24h) on the same bar, else "" (no evidence).
// Returns "slow" (>= ratio), "hold" (>= recover) or "ok", and the stat used.
func PairVerdict(pc m.M, f *m.Feat) (string, *m.PairStat) {
	if f == nil {
		return "", nil
	}
	var p *m.PairStat
	for _, x := range []*m.PairStat{f.Pair, f.PairRef} {
		if x != nil && x.Tokens >= pc.I("min_tokens", 3) && x.N >= pc.I("min_rows", 30) {
			p = x
			break
		}
	}
	switch {
	case p == nil:
		return "", nil
	case p.Ratio >= pc.F("ratio", 1.6):
		return "slow", p
	case p.Ratio >= pc.F("recover", 1.3):
		return "hold", p
	}
	return "ok", p
}

// PairSlow (v5.01, cfg pair_slow) runs after Settle: a channel proven slower
// for the same users (PairVerdict "slow") is stepped down by step per round to
// floor even without errors — the one latency-only cut, since the evidence is
// same token, same model, same output length. "hold" (between recover and
// ratio) blocks raises; "ok" leaves Settle's result (normal rules step it
// back up +step per round). Never suggests disabling; caps already applied win.
func PairSlow(cfg m.M, nw, prev map[int]int, fs m.Feats, ids []int, notes *[]string) {
	pc := cfg.Sub("pair_slow")
	if !pc.B("enabled", false) {
		return
	}
	step, floor := pc.I("step", 2), pc.I("floor", 10)
	for _, i := range ids {
		o, ok := prev[i]
		if !ok {
			continue
		}
		v, p := PairVerdict(pc, fs[i])
		w := nw[i]
		switch v {
		case "slow":
			w = min(w, max(o-step, min(floor, o)))
		case "hold":
			w = min(w, o)
		}
		if w != nw[i] && notes != nil {
			*notes = append(*notes, fmt.Sprintf("#%d 同令牌配对偏慢：%d→%d（%dmin 耗时比 %.2f，配对令牌 %d、%d 条；≥%.1f 每轮降 %d 到 %d，%.1f–%.1f 不加）",
				i, nw[i], w, p.Min, p.Ratio, p.Tokens, p.N, pc.F("ratio", 1.6), step, floor, pc.F("recover", 1.3), pc.F("ratio", 1.6)))
		}
		nw[i] = w
	}
}
