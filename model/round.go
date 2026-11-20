package model

import (
	"bufio"
	"encoding/json"
	"os"
	"time"
)

// Action is a toggle action (enable/disable one channel).
type Action struct {
	Op     string `json:"op"`
	ID     int    `json:"id"`
	Weight *int   `json:"weight,omitempty"`
	Why    string `json:"why"`
	Reject string `json:"reject,omitempty"`
}

// DisRec is toggle.disabled[id] as recorded in a round.
type DisRec struct {
	Results []Probe `json:"results"`
	Pass    bool    `json:"pass"`
}

// ToggleRec is the subset of a round's toggle record the core path reads.
type ToggleRec struct {
	Disabled      map[int]DisRec `json:"disabled"`
	BottomUpTests Probes         `json:"bottom_up_tests"`
	Applied       []Action       `json:"applied"`
}

type TrustRec struct {
	Trust float64 `json:"trust"`
}

type ADRec struct {
	Old *int `json:"old"`
}

// Rec is the subset of one rounds.jsonl / shadow.jsonl record the core path reads.
type Rec struct {
	Ts       string    `json:"ts"`
	Mode     string    `json:"mode,omitempty"`
	Channels []Channel `json:"channels"`
	Old      map[int]int
	OldRaw   map[string]*int `json:"old"`
	Features Feats           `json:"features"`
	Probes   Probes          `json:"probes"`
	Decision struct {
		Scores OrderedScores `json:"scores"`
		Target map[int]int   `json:"target"`
	} `json:"decision"`
	Applied      bool             `json:"applied"`
	DryRun       *bool            `json:"dry_run"`
	ProbesReused bool             `json:"probes_reused"`
	Trust        map[int]TrustRec `json:"trust"`
	TrustSource  string           `json:"trust_source"`
	Toggle       *ToggleRec       `json:"toggle"`
	AccessDenied map[int]ADRec    `json:"access_denied"`
	TsTime       time.Time        `json:"-"`
	Raw          json.RawMessage  `json:"-"`
}

// IsDryRun: r.get("dry_run", default).
func (r *Rec) IsDryRun(def bool) bool {
	if r.DryRun == nil {
		return def
	}
	return *r.DryRun
}

// ParseTs parses Python isoformat timestamps.
func ParseTs(s string) (time.Time, error) {
	for _, l := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999-07:00"} {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.ParseInLocation("2006-01-02T15:04:05", s, time.Local)
}

// Epoch returns ts as float seconds.
func Epoch(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

// LoadRounds reads a jsonl file (missing file = empty).
func LoadRounds(path string) ([]*Rec, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []*Rec
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<28)
	for sc.Scan() {
		b := sc.Bytes()
		if len(b) == 0 {
			continue
		}
		r, err := ParseRound(b)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

func ParseRound(b []byte) (*Rec, error) {
	r := &Rec{}
	if err := json.Unmarshal(b, r); err != nil {
		return nil, err
	}
	r.Raw = append(json.RawMessage(nil), b...)
	r.Old = map[int]int{}
	for k, v := range r.OldRaw {
		var i int
		if _, err := fmtSscan(k, &i); err == nil && v != nil {
			r.Old[i] = *v
		}
	}
	if r.Ts != "" {
		t, err := ParseTs(r.Ts)
		if err == nil {
			r.TsTime = t
		}
	}
	return r, nil
}
