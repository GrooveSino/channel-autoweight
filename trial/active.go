package trial

import (
	"bufio"
	"encoding/json"
	"os"
	"time"
)

// ActivePasses reads data/trial.jsonl and returns, per channel, the time of its latest
// passing admission batch that is still inside the real-traffic trial window (hours).
// A later failing batch for the same channel cancels the pass. Missing file = none.
func ActivePasses(path string, now time.Time, hours float64) map[int]int64 {
	out := map[int]int64{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var r struct {
			Ch   int    `json:"ch"`
			Pass bool   `json:"pass"`
			Ts   string `json:"ts"`
		}
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.Ch == 0 {
			continue
		}
		t, err := time.Parse(time.RFC3339, r.Ts)
		if err != nil {
			continue
		}
		if !r.Pass {
			delete(out, r.Ch)
			continue
		}
		out[r.Ch] = t.Unix()
	}
	cut := now.Unix() - int64(hours*3600)
	for ch, t := range out {
		if t < cut || t > now.Unix() {
			delete(out, ch)
		}
	}
	return out
}
