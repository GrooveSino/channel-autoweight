// Package diag holds read-only diagnostics: they never touch scoring, toggles or writes.
package diag

import (
	"fmt"
	"math"
	"sort"

	m "autoweight/model"
)

// EmptyPair is the per-channel same-token empty-success comparison (EMPTYPAIR).
type EmptyPair struct {
	Ch, N, Empty, PairedTokens, Obs int
	Exp, Excess                     float64
	Ratio                           *float64
	Verdict                         string // channel | cleaner | user | no_pair
}

// EmptyPairOpts are the thresholds (Python v3.37 defaults: 5, 1, 3, 2).
type EmptyPairOpts struct {
	MinN, MinTokens    int
	MinExcess, RatioHi float64
}

var DefaultEmptyPair = EmptyPairOpts{MinN: 5, MinTokens: 1, MinExcess: 3, RatioHi: 2}

// AnalyzeEmptyPair compares each channel's empty successes (type 2, ct<=0) with the
// count expected from the same tokens' empty rate across all channels they used.
// rows must already be real (non-probe) traffic. Client-side empty requests
// (Ue=1: prompt_tokens=0) are skipped, matching score.empty_skip_user.
func AnalyzeEmptyPair(rows []m.Row, o EmptyPairOpts) []EmptyPair {
	type ne struct{ n, e int }
	by := map[int]map[int]*ne{}
	tot := map[int]*ne{}
	for _, r := range rows {
		if (r.Type != 2 && r.Type != 5) || r.Ue == 1 {
			continue
		}
		e := 0
		if r.Type == 2 && r.Ct <= 0 {
			e = 1
		}
		if by[r.Tok] == nil {
			by[r.Tok] = map[int]*ne{}
		}
		for _, c := range []*ne{get(by[r.Tok], r.Ch), get(tot, r.Ch)} {
			c.n++
			c.e += e
		}
	}
	obs, exp, toks := map[int]int{}, map[int]float64{}, map[int]int{}
	for _, chs := range by {
		n, e, ok := 0, 0, map[int]*ne{}
		for c, v := range chs {
			if v.n >= o.MinN {
				ok[c] = v
				n += v.n
				e += v.e
			}
		}
		if len(ok) < 2 || e == 0 {
			continue
		}
		rate := float64(e) / float64(n)
		for c, v := range ok {
			obs[c] += v.e
			exp[c] += float64(v.n) * rate
			toks[c]++
		}
	}
	ids := []int{}
	for c := range tot {
		ids = append(ids, c)
	}
	sort.Ints(ids)
	out := []EmptyPair{}
	for _, c := range ids {
		p := EmptyPair{Ch: c, N: tot[c].n, Empty: tot[c].e, PairedTokens: toks[c], Obs: obs[c],
			Exp: r1(exp[c]), Excess: r1(float64(obs[c]) - exp[c])}
		x, ob := exp[c], float64(obs[c])
		if x > 0 {
			v := math.Round(ob/x*100) / 100
			p.Ratio = &v
		}
		switch {
		case toks[c] < o.MinTokens || x <= 0:
			p.Verdict = "no_pair"
		case ob-x >= o.MinExcess && ob >= o.RatioHi*x:
			p.Verdict = "channel"
		case ob-x <= -o.MinExcess || (ob*o.RatioHi <= x && x >= o.MinExcess):
			p.Verdict = "cleaner"
		default:
			p.Verdict = "user"
		}
		out = append(out, p)
	}
	return out
}

// EmptyPairFlags are the human notes for channels judged "channel", plus unpaired channels
// whose raw empty-success rate is high (>= hiRate with >= hiN real requests): no same-user
// proof either way, but users there are getting empty replies.
func EmptyPairFlags(ps []EmptyPair, hiN int, hiRate float64) []string {
	s := []string{}
	for _, p := range ps {
		if p.Verdict == "no_pair" && p.N >= hiN && float64(p.Empty) >= hiRate*float64(p.N) {
			s = append(s, fmt.Sprintf("EMPTYPAIR #%d: 无同令牌对照，但真实 %d 条里空成功 %d 条（%.0f%%）——无法判定渠道或用户，用户体验差；只记录",
				p.Ch, p.N, p.Empty, 100*float64(p.Empty)/float64(p.N)))
		}
		if p.Verdict == "channel" {
			s = append(s, fmt.Sprintf("EMPTYPAIR #%d: 同令牌换渠道对照，这里空成功 %d 条、按同批用户在别处的空率只该有 %.1f 条（%.2f×，配对令牌 %d）——空回复是渠道问题；只记录",
				p.Ch, p.Obs, p.Exp, *p.Ratio, p.PairedTokens))
		}
	}
	return s
}

func get[K comparable, V any](mp map[K]*V, k K) *V {
	if mp[k] == nil {
		mp[k] = new(V)
	}
	return mp[k]
}

func r1(x float64) float64 { return math.Round(x*10) / 10 }
