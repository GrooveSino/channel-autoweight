package diag

import (
	"bufio"
	"encoding/json"
	"os"

	m "autoweight/model"
)

// ManualWrite is one weight change the reviewer made by hand outside
// `autoweight -apply` (e.g. a quick cut on Groove's request). Kept in
// data/manual.jsonl so EXTWRITE does not misread our own change as Groove's.
type ManualWrite struct {
	Ts     string `json:"ts"`
	ID     int    `json:"id"`
	W      int    `json:"w"`
	By     string `json:"by"`
	Reason string `json:"reason"`
}

// LoadManual reads data/manual.jsonl (missing file = empty, bad lines skipped).
func LoadManual(path string) []ManualWrite {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	out := []ManualWrite{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var w ManualWrite
		if json.Unmarshal(sc.Bytes(), &w) == nil && w.ID > 0 {
			out = append(out, w)
		}
	}
	return out
}

// manualFor returns the latest manual write for id after `after` whose weight equals w.
func manualFor(ms []ManualWrite, id, w int, after string) *ManualWrite {
	at, err := m.ParseTs(after)
	var hit *ManualWrite
	for i := range ms {
		x := &ms[i]
		if x.ID != id || x.W != w {
			continue
		}
		if t, e := m.ParseTs(x.Ts); err == nil && (e != nil || t.Before(at)) {
			continue
		}
		hit = x
	}
	return hit
}
