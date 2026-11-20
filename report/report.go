// Package report renders a core.Result for humans and appends the Go round
// record (never to the Python data files).
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"autoweight/core"
	"autoweight/fetch"
)

func opt(p *float64) string {
	if p == nil {
		return "None"
	}
	return fmt.Sprint(*p)
}

// Text prints a run.py-like summary.
func Text(w io.Writer, r *core.Result) {
	mode := "DRY-RUN"
	if !r.DryRun {
		mode = "APPLY"
	}
	fmt.Fprintf(w, "[%s] strategy=%s %s applied=%v trust_source=%s\n", r.Ts, r.Decision.Strategy, mode, r.Applied, r.TrustSource)
	for _, c := range r.Channels {
		i := c.ID
		s := r.Decision.Scores[i]
		f := r.Features[i]
		l := ""
		if f != nil && f.Long != nil {
			l = fmt.Sprintf("1h n=%d err=%v p50=%s p90=%s", f.Long.N, f.Long.Err, opt(f.Long.P50), opt(f.Long.P90))
		}
		if c := f.Chan; c != nil { // v4.98: the one 1h count for the report table and flags
			l += fmt.Sprintf(" | 真实 %d/%d 错(去集中 %d, %.1f%%) 排除客户端/跨渠道 %d", c.Err, c.N, c.Eff, c.Rate*100, c.Client)
		}
		tr := ""
		if t := r.Trust[i]; t != nil {
			tr = fmt.Sprintf("trust=%v", t.Trust)
		}
		fmt.Fprintf(w, "#%-3d%-14s w %2d -> %2d  score=%.3f ttft=%s %s %s\n", i, c.Name, r.Old[i], r.Decision.Target[i], s.Score, opt(s.Ttft), tr, l)
	}
	for _, n := range r.Decision.Notes {
		fmt.Fprintln(w, "NOTE:", n)
	}
	if r.Override != nil {
		fmt.Fprintf(w, "OVERRIDE: system=%v final=%v reason=%v\n", r.System, r.Decision.Target, r.Override["reason"])
	}
	if len(r.Drift) > 0 {
		fmt.Fprintf(w, "DRIFT: %d channel(s) differ from --expect-system\n", len(r.Drift))
	}
	if t := r.Toggle; t != nil {
		fmt.Fprintf(w, "TOGGLE bottom1/3=%v actions=%d notify=%v n_enabled_after=%d\n", t.Bottom, len(t.Actions), t.Notify, t.NEnabledAfter)
		for _, a := range t.Actions {
			fmt.Fprintf(w, "  %s #%d %s\n", a.Op, a.ID, a.Why)
		}
		for k, d := range t.Disabled {
			fmt.Fprintf(w, "TOGGLE-TEST #%d pass=%v streak=%d enable=%v\n", k, d.Pass, d.Streak, d.Enable)
		}
	}
	for _, n := range r.PairFlags {
		fmt.Fprintln(w, "FLAG:", n)
	}
	for _, n := range r.Notes {
		fmt.Fprintln(w, n)
	}
	if len(r.Changed) > 0 {
		fmt.Fprintf(w, "CHANGED %v\n", r.Changed)
	} else {
		fmt.Fprintln(w, "CHANGED none")
	}
}

// Append writes one JSON line to path, refusing anything inside pyDir.
func Append(path, pyDir string, r *core.Result) error {
	ap, _ := filepath.Abs(path)
	pd, _ := filepath.Abs(pyDir)
	if pd != "" && strings.HasPrefix(ap, pd+string(filepath.Separator)) {
		return fmt.Errorf("refusing to write inside the Python prototype dir")
	}
	if err := os.MkdirAll(filepath.Dir(ap), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(ap, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// Verify prints the read-only post-write check; returns the number of bad
// channels (abilities out of sync, priority != 10, or weight != target).
func Verify(w io.Writer, cs []fetch.Check, target map[int]int) int {
	bad := 0
	for _, c := range cs {
		st := "OK"
		if !c.OK() {
			st = "MISMATCH"
		} else if t, ok := target[c.ID]; ok && c.Status == 1 && t != c.Weight {
			st = fmt.Sprintf("WANT %d", t)
		}
		if st != "OK" {
			bad++
		}
		fmt.Fprintf(w, "VERIFY #%d status=%d weight=%d priority=%d abilities=%d diff=%d %s\n", c.ID, c.Status, c.Weight, c.Priority, c.Abilities, c.Mismatch, st)
	}
	return bad
}
