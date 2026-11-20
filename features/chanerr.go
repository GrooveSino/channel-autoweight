package features

import (
	m "autoweight/model"
)

// crossDfWin: a transmission error (Row.Df: "do request failed", v5.01 also
// [ReverixAI] "Model request could not be transmitted") counts as cross-channel when the same
// token has another error on a different channel within this many seconds.
const crossDfWin = 60

// nfShare: share of a channel's 404 model-not-found rows one token must hold
// for them to count as that client's error (v5.00).
const nfShare = 0.8

// nonChanErr marks errors that are not the channel's fault (v4.98): client
// errors (4xx except 408/429: 400 bad request, 413 too large ...) and
// "do request failed" errors the same token hit on another channel at the same
// moment (new-api retrying a request that fails everywhere).
func nonChanErr(rows []m.Row) func(int) bool {
	type ev struct {
		t  int64
		ch int
	}
	byTok := map[int][]ev{}
	nf := map[int]map[int]int{} // ch -> tok -> 404 model-not-found rows
	for _, x := range rows {
		if x.Type == 5 {
			byTok[x.Tok] = append(byTok[x.Tok], ev{x.T, x.Ch})
			if x.Nf != 0 {
				if nf[x.Ch] == nil {
					nf[x.Ch] = map[int]int{}
				}
				nf[x.Ch][x.Tok]++
			}
		}
	}
	// v5.00: 404 model_not_found held (>= nfShare) by one token is that client's
	// model choice, not the channel's fault; spread over tokens it still counts.
	nfTok := map[int]int{}
	for ch, bt := range nf {
		sum, top, topN := 0, 0, 0
		for t, n := range bt {
			sum += n
			if n > topN || (n == topN && t < top) {
				top, topN = t, n
			}
		}
		if float64(topN) >= nfShare*float64(sum) {
			nfTok[ch] = top
		}
	}
	return func(k int) bool {
		x := rows[k]
		if x.Type != 5 {
			return false
		}
		if IsClientErr(x) {
			return true
		}
		if x.Nf != 0 {
			if t, ok := nfTok[x.Ch]; ok && t == x.Tok {
				return true
			}
		}
		if x.Df == 0 {
			return false
		}
		for _, e := range byTok[x.Tok] {
			if e.ch != x.Ch && e.t >= x.T-crossDfWin && e.t <= x.T+crossDfWin {
				return true
			}
		}
		return false
	}
}

// ChanRows returns real rows with non-channel errors removed: the one row set
// behind ChanStat and the TOKENBAN/RETRYCOST flags.
func ChanRows(cfg m.M, rows []m.Row) []m.Row {
	rows = UserRows(cfg, rows)
	nc := nonChanErr(rows)
	out := make([]m.Row, 0, len(rows))
	for k, x := range rows {
		if !nc(k) {
			out = append(out, x)
		}
	}
	return out
}

// capacityErr: rate limits (429, "Concurrency limit exceeded") are the channel's
// capacity, whoever hit them, and always count. v5.00: timeouts are no longer
// here — when one token holds them (e.g. its huge prompts) they are discounted
// like any other single-token error.
func capacityErr(x m.Row) bool {
	return x.Cap != 0 || (x.Sc != nil && *x.Sc == 429)
}

// ChanStats computes ChanStat per channel over [since, now]. Token
// de-concentration (the TOKENBAN pattern): when one token holds >= tokShare of
// the channel's non-capacity errors while other tokens still succeed there, that
// token's errors count at most tokCap. Capacity errors (429/concurrency) always count.
func ChanStats(cfg m.M, rows []m.Row, since int64, tokCap int, tokShare float64) map[int]*m.ChanStat {
	rows = UserRows(cfg, rows)
	nc := nonChanErr(rows)
	out := map[int]*m.ChanStat{}
	disc := map[int]map[int]int{} // ch -> tok -> non-capacity errors
	okBy := map[int]map[int]int{} // ch -> tok -> successes
	errToks := map[int]map[int]bool{}
	for k, x := range rows {
		if x.T < since {
			continue
		}
		s := out[x.Ch]
		if s == nil {
			s = &m.ChanStat{}
			out[x.Ch], disc[x.Ch], okBy[x.Ch], errToks[x.Ch] = s, map[int]int{}, map[int]int{}, map[int]bool{}
		}
		if nc(k) {
			s.Client++
			continue
		}
		s.N++
		switch {
		case x.Type != 5:
			okBy[x.Ch][x.Tok]++
		case capacityErr(x):
			s.Err++
			errToks[x.Ch][x.Tok] = true
		default:
			s.Err++
			errToks[x.Ch][x.Tok] = true
			disc[x.Ch][x.Tok]++
		}
	}
	for ch, s := range out {
		toks := map[int]bool{}
		for t := range okBy[ch] {
			toks[t] = true
		}
		for t := range errToks[ch] {
			toks[t] = true
		}
		s.Toks, s.ErrTok = len(toks), len(errToks[ch])
		sum := 0
		for t, e := range disc[ch] {
			sum += e
			if e > s.TopErr || (e == s.TopErr && t < s.TopTok) {
				s.TopTok, s.TopErr = t, e
			}
		}
		otherOK := 0
		for t, n := range okBy[ch] {
			if t != s.TopTok {
				otherOK += n
			}
		}
		s.Eff = s.Err
		if s.TopErr > tokCap && float64(s.TopErr) >= tokShare*float64(sum) && otherOK > 0 {
			s.Eff = s.Err - s.TopErr + tokCap
		}
		if d := s.N - s.Err + s.Eff; d > 0 {
			s.Rate = m.Round(float64(s.Eff)/float64(d), 4)
		}
	}
	return out
}
