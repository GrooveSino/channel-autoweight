package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// RetryCostFlags measures how much user-visible time a channel's real errors cost.
// For every real error (type 5) since `since`, it looks for a success with the same
// token and model within `win` seconds after the error (the site's retry or the
// client's own retry). Such an error was "rescued", but the user still waited the
// error's own duration first. A channel is flagged when it has >= minN rescued
// errors; the flag lists where they were rescued and the median time wasted.
// rows are features.ChanRows (v4.98). Diagnostic only.
func RetryCostFlags(rows []m.Row, target map[int]int, since, win int64, minN int) []string {
	type key struct {
		tok int
		mdl string
	}
	succ := map[key][]m.Row{}
	for _, r := range rows {
		if r.Type == 2 && r.T >= since {
			k := key{r.Tok, r.Mdl}
			succ[k] = append(succ[k], r)
		}
	}
	type agg struct {
		errs, rescued int
		wasted        []float64
		by            map[int]int
	}
	a := map[int]*agg{}
	for _, r := range rows {
		if r.Type != 5 || r.T < since {
			continue
		}
		g := a[r.Ch]
		if g == nil {
			g = &agg{by: map[int]int{}}
			a[r.Ch] = g
		}
		g.errs++
		best := int64(-1)
		bch := 0
		for _, s := range succ[key{r.Tok, r.Mdl}] {
			d := s.T - r.T
			if d >= 0 && d <= win && (best < 0 || d < best) {
				best, bch = d, s.Ch
			}
		}
		if best >= 0 {
			g.rescued++
			g.by[bch]++
			g.wasted = append(g.wasted, float64(r.Ut))
		}
	}
	ids := []int{}
	for ch := range a {
		ids = append(ids, ch)
	}
	sort.Ints(ids)
	out := []string{}
	for _, ch := range ids {
		g := a[ch]
		if g.rescued < minN {
			continue
		}
		bs := []int{}
		for b := range g.by {
			bs = append(bs, b)
		}
		sort.Ints(bs)
		where := ""
		for i, b := range bs {
			if i > 0 {
				where += "、"
			}
			where += fmt.Sprintf("#%d×%d", b, g.by[b])
		}
		out = append(out, fmt.Sprintf("RETRYCOST #%d: 1h 真实错误 %d 条，其中 %d 条在 %ds 内同令牌同模型重试成功（%s），用户先白等 p50 %.0fs，系统给 %d；只记录",
			ch, g.errs, g.rescued, win, where, median(g.wasted), target[ch]))
	}
	return out
}
