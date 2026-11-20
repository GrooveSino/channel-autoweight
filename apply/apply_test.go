package apply

import (
	"strings"
	"testing"

	m "autoweight/model"
)

var cfg = m.M{"priority": 10.0, "w_min": 0.0, "w_max": 20.0}
var chans = []m.Channel{{ID: 2}, {ID: 5}, {ID: 22}, {ID: 23}}

func TestBuildSQL(t *testing.T) {
	w2 := 2
	cases := []struct {
		name    string
		cfg     m.M
		plan    Plan
		wantErr string
		has     []string
		not     []string
		empty   bool
	}{
		{name: "nothing", plan: Plan{}, empty: true},
		{name: "weights only", plan: Plan{Weights: map[int]int{5: 6, 2: 0}},
			has: []string{"BEGIN;\n", "update channels set weight=0, priority=10 where id=2 and status=1;",
				"update channels set weight=6, priority=10 where id=5 and status=1;",
				"update abilities a set weight=c.weight, priority=c.priority from channels c where a.channel_id=c.id and c.id in (2,5);", "COMMIT;\n"}},
		{name: "weights+toggle one tx", plan: Plan{Weights: map[int]int{22: 10}, EnabledIDs: []int{2, 5, 22, 23},
			Toggles: []m.Action{{Op: "enable", ID: 21, Weight: &w2, Why: "ok"}}},
			has: []string{"id=22 and status=1", "update channels set status=1, priority=10, weight=2 where id=21 and status<>1;", "enabled=(c.status=1)"}},
		{name: "unknown channel", plan: Plan{Weights: map[int]int{99: 1}}, wantErr: "not an enabled"},
		{name: "out of range", plan: Plan{Weights: map[int]int{2: 21}}, wantErr: "outside"},
		{name: "priority out of range", cfg: m.M{"priority": 101.0}, plan: Plan{Weights: map[int]int{2: 1}}, wantErr: "priority must be within"},
		{name: "toggle floor rejects", plan: Plan{EnabledIDs: []int{2, 5, 22}, Toggles: []m.Action{{Op: "disable", ID: 2, Why: "dormant"}}}, empty: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cf := cfg
			if c.cfg != nil {
				cf = c.cfg
			}
			r, err := BuildSQL(cf, chans, c.plan)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("want err %q got %v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.empty != (r.SQL == "") {
				t.Fatalf("empty=%v sql=%q", c.empty, r.SQL)
			}
			if strings.Count(r.SQL, "BEGIN;") > 1 || strings.Count(r.SQL, "COMMIT;") > 1 {
				t.Fatalf("not a single transaction: %q", r.SQL)
			}
			for _, h := range c.has {
				if !strings.Contains(r.SQL, h) {
					t.Errorf("missing %q in %q", h, r.SQL)
				}
			}
		})
	}
}

func TestWriterGuards(t *testing.T) {
	t.Setenv("AUTOWEIGHT_ALLOW_WRITE", "")
	w := &PsqlWriter{}
	if err := w.ExecTx("BEGIN;\nx;\nCOMMIT;\n"); err != ErrNotArmed {
		t.Fatalf("want ErrNotArmed, got %v", err)
	}
	t.Setenv("AUTOWEIGHT_ALLOW_WRITE", "1")
	if err := w.ExecTx("update channels set weight=1;"); err == nil {
		t.Fatal("non-transaction script accepted")
	}
	mw := &MemWriter{}
	r, err := Apply(mw, cfg, chans, Plan{Weights: map[int]int{2: 3}})
	if err != nil || len(mw.Scripts) != 1 || !r.WeightsDone {
		t.Fatalf("apply: %v %v", err, mw.Scripts)
	}
}

func TestClaudePriorityZero(t *testing.T) {
	r, err := BuildSQL(m.M{"priority": 0.0, "w_min": 0.0, "w_max": 20.0}, []m.Channel{{ID: 18}}, Plan{Weights: map[int]int{18: 10}})
	if err != nil || !strings.Contains(r.SQL, "update channels set weight=10, priority=0 where id=18 and status=1;") {
		t.Fatalf("claude priority 0: %v %+v", err, r)
	}
}
