package diag

import (
	"fmt"
	"sort"
	"strings"

	m "autoweight/model"
)

// CrossEncFlags flags channels whose real errors in [since, ...) include upstream 400
// "encrypted content" rejections (Row.Ec). These usually mean the client replayed a
// reasoning blob minted by a different upstream: the same token's previous success
// (within lookback seconds) landed on another channel, so the failure is caused by a
// session switching channels, not by this channel misbehaving. It reports how many
// were preceded by a cross-channel switch. rows must be real traffic sorted or not.
// Diagnostic only.
func CrossEncFlags(rows []m.Row, target map[int]int, since, lookback int64) []string {
	rs := append([]m.Row(nil), rows...)
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].T < rs[j].T })
	last := map[int]m.Row{} // token -> last success
	ec, sw, from := map[int]int{}, map[int]int{}, map[int]map[int]int{}
	far := map[int][]string{} // no switch within lookback: where the token last succeeded
	for _, r := range rs {
		if r.Type == 2 {
			last[r.Tok] = r
			continue
		}
		if r.Ec == 0 || r.T < since {
			continue
		}
		ec[r.Ch]++
		if p, ok := last[r.Tok]; ok && p.Ch != r.Ch && r.T-p.T <= lookback {
			sw[r.Ch]++
			if from[r.Ch] == nil {
				from[r.Ch] = map[int]int{}
			}
			from[r.Ch][p.Ch]++
		} else {
			far[r.Ch] = append(far[r.Ch], lastDesc(p, ok, r))
		}
	}
	ids := []int{}
	for id := range ec {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := []string{}
	for _, id := range ids {
		src := []int{}
		for c := range from[id] {
			src = append(src, c)
		}
		sort.Ints(src)
		s := ""
		for _, c := range src {
			s += fmt.Sprintf(" #%d×%d", c, from[id][c])
		}
		if sw[id] == 0 {
			out = append(out, fmt.Sprintf("CROSSENC #%d: 真实 400 加密内容错误 %d 条，%ds 内均无跨渠道切换（%s），按普通错误计，系统给 %d；只记录",
				id, ec[id], lookback, strings.Join(far[id], "；"), target[id]))
			continue
		}
		out = append(out, fmt.Sprintf("CROSSENC #%d: 真实 400 加密内容错误 %d 条，其中 %d 条同令牌上一次成功在其他渠道（%ds 内：%s）——会话跨渠道切换导致推理密文不认，不是渠道本身故障，系统给 %d；只记录",
			id, ec[id], sw[id], lookback, s, target[id]))
	}
	return out
}

// lastDesc explains a no-switch encrypted-content error by the token's last success in
// the fetched window: none (old resumed session), same channel (likely this channel's
// own fault), or another channel beyond the lookback (long-gap cross-channel resume).
func lastDesc(p m.Row, ok bool, r m.Row) string {
	switch {
	case !ok:
		return fmt.Sprintf("令牌 %d 在取数窗口内没有成功记录，多半是续用很早以前的会话", r.Tok)
	case p.Ch == r.Ch:
		return fmt.Sprintf("令牌 %d 上一次成功就在本渠道（%d 分钟前），更像本渠道问题", r.Tok, (r.T-p.T)/60)
	default:
		return fmt.Sprintf("令牌 %d 上一次成功在 #%d（%d 分钟前，超出回看），可能是长间隔会话跨渠道续用", r.Tok, p.Ch, (r.T-p.T)/60)
	}
}
