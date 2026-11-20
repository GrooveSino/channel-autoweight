// awbacktest: read-only A/B replay of score variants over raw production logs.
//
//	awbacktest -hours 24 -dump data/bt.json          # fetch once (BEGIN READ ONLY) and save
//	awbacktest -load data/bt.json -set score.tps.enabled=true -set score.tps.exp=0.5
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"autoweight/backtest"
	"autoweight/fetch"
	m "autoweight/model"
	"autoweight/regress"
)

type snap struct {
	Chans []m.Channel `json:"chans"`
	Rows  []m.Row     `json:"rows"`
	End   int64       `json:"end"`
}

type sets []string

func (s *sets) String() string     { return strings.Join(*s, ",") }
func (s *sets) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	dir := flag.String("dir", "/workspace/reports/ops/channel-autoweight-go", "autoweight dir")
	hours := flag.Int("hours", 24, "replay hours (fetches hours+1)")
	dump := flag.String("dump", "", "save fetched logs here")
	load := flag.String("load", "", "load logs from here instead of production")
	stepMin := flag.Int("step", 15, "decision step minutes")
	agreeH := flag.String("agree", "", "comma hours: compare proposals with operator finals from data/rounds.jsonl instead of replay")
	skipS := flag.String("skip", "", "comma channel ids left out of -agree")
	verbose := flag.Bool("v", false, "print every -agree round")
	ids := flag.String("ids", "", "comma channel ids to fetch (default: enabled channels)")
	var ss sets
	flag.Var(&ss, "set", "config override a.b=JSON (repeatable)")
	flag.Parse()
	cfg, err := m.LoadConfig(filepath.Join(*dir, "config.json"))
	must(err)
	var s snap
	if *load != "" {
		b, err := os.ReadFile(*load)
		must(err)
		must(json.Unmarshal(b, &s))
	} else {
		p := fetch.NewPsql(cfg)
		s.Chans, err = p.Channels()
		must(err)
		fids := csvInts(*ids)
		if len(fids) == 0 {
			for _, c := range s.Chans {
				fids = append(fids, c.ID)
			}
		}
		s.End, s.Rows, err = p.Logs(fids, (*hours+1)*60)
		must(err)
		if *dump != "" {
			b, _ := json.Marshal(s)
			must(os.WriteFile(*dump, b, 0o644))
		}
	}
	must(regress.ApplySets(cfg, ss))
	if *agreeH != "" {
		rounds, err := m.LoadRounds(filepath.Join(*dir, cfg.S("rounds_file", "data/rounds.jsonl")))
		must(err)
		skip := map[int]bool{}
		for _, i := range csvInts(*skipS) {
			skip[i] = true
		}
		for _, h := range csvInts(*agreeH) {
			agree(cfg, rounds, s.Rows, s.End, h, skip, *verbose)
		}
		return
	}
	for _, h := range []int{*hours, *hours / 2, *hours / 4} {
		if h < 2 {
			continue
		}
		r, err := backtest.Run(cfg, s.Chans, s.Rows, s.End-int64(h+1)*3600+3600, s.End, int64(*stepMin)*60)
		must(err)
		fmt.Printf("%2dh steps=%d p50=%.3f p90=%.3f err=%.4f cerr=%.4f emp=%.4f tps=%.2f moved=%d\n", h, r.Steps, r.P50, r.P90, r.Err, r.CErr, r.Emp, r.Tps, r.Moved)
	}
}

func csvInts(s string) []int {
	var out []int
	for _, p := range strings.Split(s, ",") {
		if v, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
