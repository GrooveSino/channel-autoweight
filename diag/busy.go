package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// BusyFlags flags channels whose 1h real errors include >= minN upstream
// "servers are currently busy"/overloaded rejections (Row.Bz). These are a
// capacity signal, not a client gap: the user usually waits a long time before
// the error (often logged with HTTP 200), so each one costs more than a fast 4xx.
// It reports how many fell in the last 30 minutes (still busy now?), the mean
// wait before the error, and how many were followed within 120s by a success for
// the same token and model on another channel. rows must be real traffic.
// Diagnostic only.
func BusyFlags(rows []m.Row, target map[int]int, since, now int64, minN int) []string {
	type agg struct{ n, recent, wait, rescued int }
	a := map[int]*agg{}
	for _, r := range rows {
		if r.T < since || r.Type != 5 || r.Bz == 0 {
			continue
		}
		g := a[r.Ch]
		if g == nil {
			g = &agg{}
			a[r.Ch] = g
		}
		g.n++
		g.wait += r.Ut
		if r.T >= now-1800 {
			g.recent++
		}
		for _, s := range rows {
			if s.T >= r.T && s.T <= r.T+120 && s.Type == 2 && s.Tok == r.Tok && s.Mdl == r.Mdl && s.Ch != r.Ch {
				g.rescued++
				break
			}
		}
	}
	ids := []int{}
	for id, g := range a {
		if g.n >= minN {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	out := []string{}
	for _, id := range ids {
		g := a[id]
		out = append(out, fmt.Sprintf("BUSY #%d: 1h 真实错误中 %d 条是上游繁忙/过载（近 30 分钟 %d 条），用户平均先等 %ds 才报错，其中 %d 条 120s 内同令牌同模型在其他渠道重试成功——上游容量不足，系统给 %d；只记录",
			id, g.n, g.recent, g.wait/g.n, g.rescued, target[id]))
	}
	return out
}
