// autoweight: one weight round (the only production weight tool since 10-31).
//
//	autoweight -live-probe                  # live dry-run with real probes
//	autoweight -no-probe                    # live dry-run (read-only DB)
//	autoweight -reuse-probe 15              # live dry-run reusing the last round's probes
//	autoweight -snap snaps/r10292119        # offline dry-run on a snapshot
//	AUTOWEIGHT_ALLOW_WRITE=1 autoweight -apply -reuse-probe 15 -expect-system .. -final .. -reason ..
//
// Reads <dir>/config.json, <dir>/data/{rounds,shadow}.jsonl and appends the
// round record to <dir>/data/rounds.jsonl. After -apply it re-reads channels
// and abilities in a read-only transaction and exits 3 on mismatch.
package main

import (
	"autoweight/diag"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"autoweight/apply"
	"autoweight/core"
	"autoweight/fetch"
	m "autoweight/model"
	"autoweight/probe"
	"autoweight/report"
	"autoweight/trial"
)

// pyDir is the retired Python prototype; nothing is ever written there.
const pyDir = "/workspace/reports/ops/channel-autoweight"

func main() {
	dir := flag.String("dir", "/workspace/reports/ops/channel-autoweight-go", "autoweight dir (config.json, data/)")
	cfgPath := flag.String("config", "", "config.json (default <dir>/config.json)")
	snap := flag.String("snap", "", "offline snapshot dir (replays recorded probes)")
	out := flag.String("out", "", "round record file (default <dir>/<rounds_file>; '-' = none)")
	asJSON := flag.Bool("json", false, "print the full record as JSON")
	var o core.Options
	flag.BoolVar(&o.Apply, "apply", false, "write weights (single transaction; needs AUTOWEIGHT_ALLOW_WRITE=1)")
	flag.BoolVar(&o.ApplyToggle, "apply-toggle", false, "also execute toggle actions (with -apply)")
	flag.BoolVar(&o.NoProbe, "no-probe", false, "skip probing (and toggle)")
	flag.Float64Var(&o.ReuseProbe, "reuse-probe", 0, "reuse probes from a round within MIN minutes")
	flag.StringVar(&o.Strategy, "strategy", "", "rank|proportional")
	flag.StringVar(&o.Override, "override", "", "ID=W[,ID=W] (needs -reason)")
	flag.StringVar(&o.Final, "final", "", "ID=W for every channel")
	flag.StringVar(&o.Expect, "expect-system", "", "ID=W[,...] system target seen at dry-run")
	flag.StringVar(&o.Reason, "reason", "", "reason for -override/-final")
	live := flag.Bool("live-probe", false, "run real site probes/recheck and upstream direct tests (same calls as run.py; costs requests)")
	flag.Parse()

	env := core.Env{Probe: core.NoProber{}}
	var err error
	if *snap != "" {
		s, err := fetch.LoadSnapshot(*snap)
		must(err)
		if o.Apply {
			log.Fatal("-apply is not allowed on a snapshot")
		}
		env.Src = fetch.SnapSource{S: s}
		env.Probe = core.Replay{Probes: s.Probes, Reused: s.ProbesReused, Tests: s.DisabledTests, BottomUp: s.BottomUpTests}
		*cfgPath = s.Path(fetch.ConfigFile)
		env.Rounds, err = m.LoadRounds(s.Path(fetch.RoundsFile))
		must(err)
		env.Shadow, err = m.LoadRounds(s.Path(fetch.ShadowFile))
		must(err)
	}
	if *cfgPath == "" {
		*cfgPath = filepath.Join(*dir, "config.json")
	}
	env.Cfg, err = m.LoadConfig(*cfgPath)
	must(err)
	if *snap == "" {
		p := fetch.NewPsql(env.Cfg)
		env.Src = p
		env.Rounds, err = m.LoadRounds(filepath.Join(*dir, env.Cfg.S("rounds_file", "data/rounds.jsonl")))
		must(err)
		env.Shadow, err = m.LoadRounds(filepath.Join(*dir, env.Cfg.Sub("shadow").S("file", "data/shadow.jsonl")))
		must(err)
		env.Manual = diag.LoadManual(filepath.Join(*dir, "data/manual.jsonl"))
		env.Trials = trial.ActivePasses(filepath.Join(*dir, "data/trial.jsonl"), time.Now(), 48)
		if *live {
			h := env.Rounds
			if len(h) > 24 {
				h = h[len(h)-24:]
			}
			pc := probe.NewClient(env.Cfg)
			pc.CountPath = filepath.Join(*dir, "data/direct_probe_count.jsonl")
			env.Probe = &probe.Live{C: pc, Rounds: h}
		}
		if o.Apply {
			env.Writer = &apply.PsqlWriter{Argv: p.Argv(), Run: fetch.DefaultRun}
		}
	}
	r, err := core.Run(env, o)
	must(err)
	if *asJSON {
		b, _ := json.MarshalIndent(r, "", " ")
		fmt.Println(string(b))
	} else {
		report.Text(os.Stdout, r)
		if *snap == "" {
			trialProg(env)
		}
	}
	if *out == "" && *snap == "" {
		*out = filepath.Join(*dir, env.Cfg.S("rounds_file", "data/rounds.jsonl"))
	}
	if *out != "" && *out != "-" {
		must(report.Append(*out, pyDir, r))
	}
	if r.Applied || (r.Toggle != nil && len(r.Toggle.Applied) > 0) {
		cs, err := fetch.NewPsql(env.Cfg).Verify()
		must(err)
		if bad := report.Verify(os.Stdout, cs, r.Decision.Target); bad > 0 {
			os.Exit(3)
		}
	}
	os.Exit(r.ExitCode)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// trialProg prints TRIALPROG for channels in an active 48h trial (read-only, diag only).
func trialProg(env core.Env) {
	p, ok := env.Src.(*fetch.Psql)
	if !ok {
		return
	}
	tc := env.Cfg.Sub("trial")
	hours, minN := tc.F("hours", 48), int(tc.F("min_real", 300))
	for _, ch := range m.SortedIDs(env.Trials) {
		since := env.Trials[ch]
		a, err := p.TrialProgress(ch, since, env.Cfg.Ints("probe_token_ids"))
		if err != nil {
			fmt.Printf("TRIALPROG #%d: 查询失败 %v\n", ch, err)
			continue
		}
		t := diag.TrialProg{Ch: ch, N: a.N, Err: a.Err, Empty: a.Empty, Tokens: a.Tokens, P50: a.P50, P90: a.P90,
			ElapsedH: float64(time.Now().Unix()-since) / 3600}
		fmt.Println(diag.TrialProgFlag(t, hours, minN, tc.F("max_err", 0.01)))
		if d, err := p.TrialDemandOf(ch, 60, env.Cfg.Ints("probe_token_ids")); err == nil {
			fmt.Println(diag.TrialDemandFlag(ch, 60, d.Total, d.OnCh, d.Fresh, d.FreshOnCh, d.Tokens, d.LastAgoMin, d.W, d.WSum))
		} else {
			fmt.Printf("TRIALDEMAND #%d: 查询失败 %v\n", ch, err)
		}
	}
}
