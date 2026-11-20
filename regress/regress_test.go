package regress

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Integration: whole rounds (features → trust → decide → toggle) on frozen
// snapshots must reproduce the stored reference outputs.

func load(t *testing.T, path string) *Out {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	o := &Out{}
	if err := json.Unmarshal(b, o); err != nil {
		t.Fatal(err)
	}
	return o
}

func check(t *testing.T, want *Out, dir string, sets ...string) *Out {
	got, _, err := GoEval(dir, sets...)
	if err != nil {
		t.Fatal(err)
	}
	for _, mm := range Compare(want, got) {
		t.Errorf("%s: want=%s go=%s", mm.Field, mm.Py, mm.Go)
	}
	return got
}

// TestGoldenSnapshots: every snapshot in ../snaps (local only, gitignored) per
// config variant; skips snapshots that are not present.
func TestGoldenSnapshots(t *testing.T) {
	for _, v := range []struct {
		sub  string
		sets []string
	}{
		{"golden", nil},
		{"golden_dormant_on", []string{"toggle.skip_dormant_base=true"}},
		{"golden_v286", []string{"score.log_ttft.thin_pool_k=20", "score.empty_weight=2", "toggle.skip_dormant_base=true"}},
		{"golden_v332", nil}, // these snapshot configs carry v3.32 dormant_w_max/base_real_log
	} {
		gs, _ := filepath.Glob("../testdata/" + v.sub + "/*.json")
		for _, g := range gs {
			name := strings.TrimSuffix(filepath.Base(g), ".json")
			t.Run(v.sub+"/"+name, func(t *testing.T) {
				dir := filepath.Join("..", "snaps", name)
				if _, err := os.Stat(dir); err != nil {
					t.Skip("snapshot not present")
				}
				check(t, load(t, g), dir, v.sets...)
			})
		}
	}
}

// TestReplayIncidents: committed replays. 03:18: #26's probe-only error streak
// must not disable it (v2.88 rule + exempt); the v2.87 rule still disables.
// 04:01: bottom-1/3 is #23/#22.
func TestReplayIncidents(t *testing.T) {
	for _, c := range []struct {
		golden, dir string
		sets        []string
		actions     int
	}{
		{"replay_0319", "replay_0319", nil, 0},
		{"replay_0319_legacyrule_exempt", "replay_0319", []string{"toggle.err_real_only=false"}, 0},
		{"replay_0319_v287rule", "replay_0319", []string{"toggle.err_real_only=false", "toggle.acct_disable.exempt=[]"}, 1},
		{"replay_0401", "replay_0401", nil, 0},
	} {
		t.Run(c.golden, func(t *testing.T) {
			got := check(t, load(t, "../testdata/golden_replay/"+c.golden+".json"), "../testdata/"+c.dir, c.sets...)
			if len(got.Toggle.Actions) != c.actions {
				t.Errorf("actions %+v, want %d", got.Toggle.Actions, c.actions)
			}
			if c.dir == "replay_0401" && !reflect.DeepEqual(got.Toggle.Bottom, []int{23, 22}) {
				t.Errorf("bottom %v", got.Toggle.Bottom)
			}
		})
	}
}
