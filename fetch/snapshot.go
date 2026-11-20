package fetch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	m "autoweight/model"
)

// Snapshot is one frozen decision point, dumped once from production in
// read-only transactions (awsnap). Rounds/shadow history is stored as
// prefix files next to snap.json so Python and Go read identical bytes.
type Snapshot struct {
	Version  int         `json:"version"`
	Now      int64       `json:"now"`
	Label    string      `json:"label"`
	Source   string      `json:"source"` // "live" or "round:<ts>"
	Channels []m.Channel `json:"channels"`
	Disabled []m.Channel `json:"disabled"`
	LogMin   int         `json:"log_minutes"`
	Rows     []m.Row     `json:"rows"`
	// Probes: site probes for this point (taken from the round record).
	Probes       m.Probes `json:"probes"`
	ProbesReused bool     `json:"probes_reused"`
	// DisabledTests / BottomUpTests: upstream direct tests (from the round's toggle block).
	DisabledTests map[int][]m.Probe `json:"disabled_tests"`
	BottomUpTests m.Probes          `json:"bottom_up_tests"`
	HasToggle     bool              `json:"has_toggle"`
	Dir           string            `json:"-"`
}

const (
	SnapFile   = "snap.json"
	RoundsFile = "rounds.jsonl"
	ShadowFile = "shadow.jsonl"
	ConfigFile = "config.json"
)

func LoadSnapshot(dir string) (*Snapshot, error) {
	b, err := os.ReadFile(filepath.Join(dir, SnapFile))
	if err != nil {
		return nil, err
	}
	s := &Snapshot{}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, err
	}
	s.Dir = dir
	return s, nil
}

func (s *Snapshot) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, SnapFile), b, 0o644)
}

func (s *Snapshot) Path(name string) string { return filepath.Join(s.Dir, name) }

// SnapSource adapts a Snapshot to Source.
type SnapSource struct{ S *Snapshot }

func (x SnapSource) Channels() ([]m.Channel, error) {
	return append([]m.Channel{}, x.S.Channels...), nil
}
func (x SnapSource) Disabled() ([]m.Channel, error) {
	return append([]m.Channel{}, x.S.Disabled...), nil
}

func (x SnapSource) Logs(ids []int, minutes int) (int64, []m.Row, error) {
	s := x.S
	if minutes > s.LogMin {
		return 0, nil, fmt.Errorf("snapshot has %d min of logs, %d requested", s.LogMin, minutes)
	}
	return s.Now, FilterRows(s.Rows, ids, s.Now, minutes), nil
}
