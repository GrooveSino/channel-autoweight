package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// Sticky is one channel's 1h real traffic split by how the site routed it.
type Sticky struct {
	Ch, N, Aff  int
	RealShare   float64 // share of all 1h real requests
	WeightShare float64 // share of the summed current weights
}

// AnalyzeSticky splits real rows since `since` into channel-affinity hits (sticky
// sessions, routed regardless of weight) and fresh weight-routed requests.
// rows must already be real (non-probe) traffic; weights are the current DB weights.
func AnalyzeSticky(rows []m.Row, weights map[int]int, since int64) []Sticky {
	n, a, tot := map[int]int{}, map[int]int{}, 0
	for _, r := range rows {
		if r.T < since || (r.Type != 2 && r.Type != 5) {
			continue
		}
		n[r.Ch]++
		a[r.Ch] += r.Aff
		tot++
	}
	wsum := 0
	for _, w := range weights {
		wsum += w
	}
	out := []Sticky{}
	for id, w := range weights {
		s := Sticky{Ch: id, N: n[id], Aff: a[id]}
		if tot > 0 {
			s.RealShare = float64(n[id]) / float64(tot)
		}
		if wsum > 0 {
			s.WeightShare = float64(w) / float64(wsum)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ch < out[j].Ch })
	return out
}

// StickyFlags reports channels whose real traffic is mostly sticky-session hits and
// far above their weight share: lowering (or raising) their weight moves only new
// sessions, so a weight change there takes effect slowly. Diagnostic only.
func StickyFlags(st []Sticky, minN int, minAff, ratio float64) []string {
	out := []string{}
	for _, s := range st {
		if s.N < minN || float64(s.Aff) < minAff*float64(s.N) || s.RealShare < ratio*s.WeightShare {
			continue
		}
		out = append(out, fmt.Sprintf("STICKY #%d: 1h 真实 %d 条中 %d 条（%.0f%%）是会话亲和命中，占真实流量 %.0f%%、但权重只占 %.0f%%——改权重只影响新会话，见效慢；只记录",
			s.Ch, s.N, s.Aff, 100*float64(s.Aff)/float64(s.N), 100*s.RealShare, 100*s.WeightShare))
	}
	return out
}

// FreshFlags summarises the weight-routed (non-sticky) part of 1h real traffic.
// When almost all real requests are sticky hits, channels at the top of the weight
// table can show 0 real requests simply because no new session started — that is not
// evidence against them, and weight changes this hour barely move traffic.
func FreshFlags(st []Sticky, minFresh int) []string {
	tot, fresh := 0, 0
	per := []string{}
	for _, s := range st {
		tot += s.N
		f := s.N - s.Aff
		fresh += f
		if f > 0 {
			per = append(per, fmt.Sprintf("#%d=%d(权重占%.0f%%)", s.Ch, f, 100*s.WeightShare))
		}
	}
	if tot == 0 || fresh >= minFresh {
		return nil
	}
	return []string{fmt.Sprintf("FRESH: 1h 真实 %d 条里只有 %d 条是按权重新分配的（其余是会话亲和命中）%v——高权重渠道真实 0 条是因为没有新会话，不是渠道差；本小时改权重几乎不改变流量；只记录",
		tot, fresh, per)}
}
