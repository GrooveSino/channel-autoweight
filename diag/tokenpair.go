package diag

import (
	"fmt"
	"math"
	"sort"

	m "autoweight/model"
)

// TokenPair is the per-channel same-token normalized-latency comparison (TOKENPAIR, Python v3.34).
type TokenPair struct {
	Ch, N, Tokens, PairedTokens int
	TopShare, NlP50             *float64
	Ratio                       *float64 // weighted geo-mean of token median / token pooled median; <1 = faster for same users
	Verdict                     string   // faster | slower | same | no_pair
}

// TokenPairOpts are the thresholds (Python defaults: 3, 100, 3, 0.15).
type TokenPairOpts struct {
	MinN, CtFloor, MinTokens int
	Band                     float64
}

var DefaultTokenPair = TokenPairOpts{MinN: 3, CtFloor: 100, MinTokens: 3, Band: 0.15}

func median(a []float64) float64 {
	b := append([]float64(nil), a...)
	sort.Float64s(b)
	n := len(b)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return b[n/2]
	}
	return (b[n/2-1] + b[n/2]) / 2
}

// AnalyzeTokenPair compares each channel's normalized latency (s per 1k completion tokens)
// with the same tokens' latency on the other channels they used. rows must be real traffic.
func AnalyzeTokenPair(rows []m.Row, o TokenPairOpts) []TokenPair {
	by := map[int]map[int][]float64{}
	raw := map[int][]float64{}
	toks := map[int]map[int]int{}
	for _, r := range rows {
		if r.Type != 2 || r.Ct <= 0 {
			continue
		}
		nl := float64(r.Ut) / float64(max(r.Ct, o.CtFloor)) * 1000
		if by[r.Tok] == nil {
			by[r.Tok] = map[int][]float64{}
		}
		if toks[r.Ch] == nil {
			toks[r.Ch] = map[int]int{}
		}
		by[r.Tok][r.Ch] = append(by[r.Tok][r.Ch], nl)
		raw[r.Ch] = append(raw[r.Ch], nl)
		toks[r.Ch][r.Tok]++
	}
	type acc struct {
		s, w float64
		k    int
	}
	ac := map[int]*acc{}
	for _, chs := range by {
		ok := map[int][]float64{}
		var pool []float64
		for c, v := range chs {
			if len(v) >= o.MinN {
				ok[c] = v
				pool = append(pool, v...)
			}
		}
		if len(ok) < 2 {
			continue
		}
		pm := median(pool)
		if pm <= 0 {
			continue
		}
		for c, v := range ok {
			if md := median(v); md > 0 {
				a := get(ac, c)
				w := float64(min(len(v), 10))
				a.s += w * math.Log(md/pm)
				a.w += w
				a.k++
			}
		}
	}
	ids := []int{}
	for c := range raw {
		ids = append(ids, c)
	}
	sort.Ints(ids)
	out := []TokenPair{}
	for _, c := range ids {
		n, top := 0, 0
		for _, k := range toks[c] {
			n += k
			top = max(top, k)
		}
		ts := math.Round(float64(top)/float64(n)*100) / 100
		p50 := r1(median(raw[c]))
		p := TokenPair{Ch: c, N: n, Tokens: len(toks[c]), TopShare: &ts, NlP50: &p50}
		a := ac[c]
		if a != nil && a.w > 0 {
			r := math.Round(math.Exp(a.s/a.w)*1000) / 1000
			p.Ratio = &r
			p.PairedTokens = a.k
		}
		switch {
		case p.PairedTokens < o.MinTokens || p.Ratio == nil:
			p.Verdict = "no_pair"
		case *p.Ratio < 1-o.Band:
			p.Verdict = "faster"
		case *p.Ratio > 1+o.Band:
			p.Verdict = "slower"
		default:
			p.Verdict = "same"
		}
		out = append(out, p)
	}
	return out
}

// TokenPairFlags warns when the system's top-weight channel is proven slower for the same
// users (any sample size), is thin (<thinN real) and not proven faster, or is unpaired with
// one token carrying >= ConcentratedShare of its traffic (single-user lead, no same-user check).
// ConcentratedShare is the max-token share above which an unpaired lead counts as single-user.
const ConcentratedShare = 0.7

func TokenPairFlags(ps []TokenPair, system map[int]int, thinN int) []string {
	top, best := 0, -1
	for c, w := range system {
		if w > best || (w == best && c < top) {
			top, best = c, w
		}
	}
	for _, p := range ps {
		if p.Ch == top && (p.Verdict == "slower" || (p.N < thinN && p.Verdict != "faster") ||
			(p.Verdict == "no_pair" && p.TopShare != nil && *p.TopShare >= ConcentratedShare)) {
			r := "-"
			if p.Ratio != nil {
				r = fmt.Sprintf("%.3f", *p.Ratio)
			}
			return []string{fmt.Sprintf("TOKENPAIR #%d: 系统首位，但同令牌配对 %s（配对令牌 %d，比 %s），真实 %d 条 %d 个令牌、最大占 %.2f——领先缺同用户证据或同用户更慢；只记录",
				top, p.Verdict, p.PairedTokens, r, p.N, p.Tokens, *p.TopShare)}
		}
	}
	return nil
}
