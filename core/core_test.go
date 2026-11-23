package core

import (
	"strconv"
	"strings"
	"testing"

	"autoweight/apply"
	"autoweight/fetch"
	m "autoweight/model"
)

const snapDir = "../testdata/plumbing" // synthetic legacy writer exercise, never used for live writes

func env(t *testing.T) Env {
	s, err := fetch.LoadSnapshot(snapDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := m.LoadConfig(s.Path(fetch.ConfigFile))
	cfg["settle"] = m.M{"enabled": false} // apply/toggle plumbing fixtures predate v4.66 SoftDown / v4.98 Settle
	rs, _ := m.LoadRounds(s.Path(fetch.RoundsFile))
	sh, _ := m.LoadRounds(s.Path(fetch.ShadowFile))
	return Env{Cfg: cfg, Src: fetch.SnapSource{S: s}, Rounds: rs, Shadow: sh,
		Probe: Replay{Probes: s.Probes, Reused: s.ProbesReused, Tests: s.DisabledTests, BottomUp: s.BottomUpTests}}
}

func TestFlows(t *testing.T) {
	e := env(t)
	base, err := Run(e, Options{})
	if err != nil {
		t.Fatal(err)
	}
	sys := base.Decision.Target
	expOK, expBad := "", ""
	for i, w := range sys {
		if expOK != "" {
			expOK += ","
			expBad += ","
		}
		expOK += itoa(i) + "=" + itoa(w)
		expBad += itoa(i) + "=" + itoa((w+1)%20)
	}
	cases := []struct {
		name     string
		o        Options
		wantErr  string
		exit     int
		sqlHas   []string
		noWriter bool
		sqlNot   []string
		legacy   bool // v2.88: #26 is exempt now; the v2.87 rule keeps the single-tx disable path covered
	}{
		{name: "dry-run", o: Options{}},
		{name: "apply weights+toggle single tx", o: Options{Apply: true, ApplyToggle: true, Expect: expOK}, legacy: true,
			sqlHas: []string{"BEGIN;", "update channels set weight=0, priority=10 where id=23 and status=1;", "update channels set status=2, weight=0 where id=26 and status=1;", "COMMIT;"}},
		{name: "v2.88 exempt #26 not disabled", o: Options{Apply: true, ApplyToggle: true, Expect: expOK},
			sqlHas: []string{"BEGIN;", "id=23 and status=1;", "COMMIT;"}, sqlNot: []string{"status=2"}},
		{name: "apply without toggle", o: Options{Apply: true}, sqlHas: []string{"id=23 and status=1"}},
		{name: "drift aborts apply", o: Options{Apply: true, Expect: expBad}, exit: 2},
		{name: "drift on dry-run only reported", o: Options{Expect: expBad}},
		{name: "override needs reason", o: Options{Override: "2=1"}, wantErr: "必须配 --reason"},
		{name: "override+final exclusive", o: Options{Override: "2=1", Final: "2=1", Reason: "x"}, wantErr: "互斥"},
		{name: "final must be complete", o: Options{Final: "2=1", Reason: "x"}, wantErr: "缺少"},
		{name: "apply without writer", o: Options{Apply: true}, wantErr: "no writer", noWriter: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ee := e
			w := &apply.MemWriter{}
			if !c.noWriter {
				ee.Writer = w
			}
			if c.legacy {
				ee.Cfg = legacyCfg(e.Cfg)
			}
			r, err := Run(ee, c.o)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("want %q got %v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.ExitCode != c.exit {
				t.Fatalf("exit %d", r.ExitCode)
			}
			if c.exit != 0 || !c.o.Apply {
				if len(w.Scripts) != 0 {
					t.Fatalf("wrote on dry-run/abort: %v", w.Scripts)
				}
				return
			}
			if len(w.Scripts) != 1 {
				t.Fatalf("want exactly one transaction, got %d", len(w.Scripts))
			}
			for _, h := range c.sqlNot {
				if strings.Contains(w.Scripts[0], h) {
					t.Errorf("unexpected %q in\n%s", h, w.Scripts[0])
				}
			}
			for _, h := range c.sqlHas {
				if !strings.Contains(w.Scripts[0], h) {
					t.Errorf("missing %q in\n%s", h, w.Scripts[0])
				}
			}
		})
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

// legacyCfg: the v2.87 toggle rule (probe failures count, no exempt) on a copy of cfg.
func legacyCfg(c m.M) m.M {
	o := m.M{}
	for k, v := range c {
		o[k] = v
	}
	tg := m.M{}
	for k, v := range c.Sub("toggle") {
		tg[k] = v
	}
	ad := m.M{}
	for k, v := range tg.Sub("acct_disable") {
		ad[k] = v
	}
	ad["exempt"] = []any{}
	tg["acct_disable"] = map[string]any(ad)
	tg["err_real_only"] = false
	o["toggle"] = map[string]any(tg)
	return o
}
