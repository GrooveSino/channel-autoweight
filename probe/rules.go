package probe

import (
	"sort"
	"strings"

	m "autoweight/model"
)

// HistoryTTFT ports history_ttft: per round, min ok ttft of ch, newest first, up to k.
func HistoryTTFT(rounds []*m.Rec, ch, k int) []float64 {
	var vals []float64
	for i := len(rounds) - 1; i >= 0; i-- {
		ps := rounds[i].Probes[ch]
		var ok []float64
		for _, p := range ps {
			if p.OK && p.Ttft != nil {
				ok = append(ok, *p.Ttft)
			}
		}
		if len(ok) > 0 {
			vals = append(vals, minOf(ok))
		}
		if len(vals) >= k {
			break
		}
	}
	return vals
}

func minOf(xs []float64) float64 {
	v := xs[0]
	for _, x := range xs[1:] {
		if x < v {
			v = x
		}
	}
	return v
}

// NeedsRecheck ports needs_recheck.
func NeedsRecheck(ps []m.Probe, hist []float64, rc m.M) bool {
	var ok []float64
	for _, p := range ps {
		if p.OK && p.Ttft != nil {
			ok = append(ok, *p.Ttft)
		}
	}
	if len(ok) == 0 {
		return true
	}
	if float64(len(hist)) < rc.F("min_hist", 2) {
		return false
	}
	h := append([]float64(nil), hist...)
	sort.Float64s(h)
	med := h[len(h)/2]
	t := minOf(ok)
	return t > rc.F("min_s", 0) && t > rc.F("ratio", 0)*med
}

// RecheckIDs returns, in ids order, the channels recheck() would probe again.
func RecheckIDs(cfg m.M, probes m.Probes, ids []int, rounds []*m.Rec) []int {
	rc := cfg.Sub("probe").Sub("recheck")
	if !rc.B("enabled", false) || len(probes) == 0 {
		return []int{}
	}
	out := []int{}
	for _, i := range ids {
		ps, ok := probes[i]
		if !ok {
			continue
		}
		if NeedsRecheck(ps, HistoryTTFT(rounds, i, rc.I("history", 3)), rc) {
			out = append(out, i)
		}
	}
	return out
}

// Anomalies ports anomalies(): only channels whose every probe failed.
func Anomalies(probes m.Probes) map[int][]string {
	out := map[int][]string{}
	for i, ps := range probes {
		if len(ps) == 0 {
			continue
		}
		any := false
		for _, p := range ps {
			any = any || p.OK
		}
		if any {
			continue
		}
		kinds := []string{}
		for _, p := range ps {
			k := "http_err"
			if p.Status != nil && *p.Status == 429 {
				k = "rate_limit"
			} else if p.Status != nil && *p.Status == 200 && m.OrI(p.Deltas, 0) == 0 {
				k = "empty_200"
			}
			if !contains(kinds, k) {
				kinds = append(kinds, k)
			}
		}
		out[i] = kinds
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Classify ports upstream.classify: ok / slow / timeout / http_err / empty.
func Classify(r m.Probe, maxTtft float64) string {
	if r.OK {
		if m.OrZ(r.Ttft, 0) > maxTtft {
			return "slow"
		}
		return "ok"
	}
	e, _ := r.Err.(string)
	st := m.OrI(r.Status, 0)
	if containsFold(e, "timeout") || st == 504 || st == 524 || st == 408 {
		return "timeout"
	}
	if st != 0 && st != 200 {
		return "http_err"
	}
	return "empty"
}

// Verdict ports upstream.verdict.
type VerdictT struct {
	Classes   []string `json:"classes"`
	Recovered bool     `json:"recovered"`
}

func Verdict(rs []m.Probe, needOK int, maxTtft float64) VerdictT {
	v := VerdictT{Classes: []string{}}
	all := true
	for _, r := range rs {
		c := Classify(r, maxTtft)
		v.Classes = append(v.Classes, c)
		all = all && c == "ok"
	}
	v.Recovered = len(v.Classes) >= needOK && all
	return v
}

func containsFold(s, sub string) bool { return strings.Contains(strings.ToLower(s), sub) }
