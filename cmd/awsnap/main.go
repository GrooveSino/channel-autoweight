// awsnap dumps an offline snapshot for one decision point. All production
// reads go through fetch.Psql (BEGIN READ ONLY ... ROLLBACK). Nothing is
// written anywhere except the output directory.
//
//	awsnap -round 2026-10-29T21:21:41+08:00 -out snaps/2121
//	awsnap -round last -out snaps/last
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"autoweight/fetch"
	m "autoweight/model"
)

func main() {
	pyDir := flag.String("dir", "/workspace/reports/ops/channel-autoweight-go", "autoweight dir (config.json, data/)")
	round := flag.String("round", "last", "formal round ts to freeze, or 'last' (latest non-dry-run round)")
	out := flag.String("out", "", "output dir")
	flag.Parse()
	if *out == "" {
		log.Fatal("-out required")
	}
	cfgPath := filepath.Join(*pyDir, "config.json")
	cfg, err := m.LoadConfig(cfgPath)
	must(err)
	rf := filepath.Join(*pyDir, cfg.S("rounds_file", "data/rounds.jsonl"))
	sf := filepath.Join(*pyDir, cfg.Sub("shadow").S("file", "data/shadow.jsonl"))
	rounds, err := m.LoadRounds(rf)
	must(err)
	shadow, err := m.LoadRounds(sf)
	must(err)
	idx := -1
	for k := len(rounds) - 1; k >= 0; k-- {
		r := rounds[k]
		if (*round == "last" && !r.IsDryRun(true)) || r.Ts == *round {
			idx = k
			break
		}
	}
	if idx < 0 {
		log.Fatalf("round %s not found", *round)
	}
	rec := rounds[idx]
	now := int64(m.Epoch(rec.TsTime))
	ids := []int{}
	for _, c := range rec.Channels {
		ids = append(ids, c.ID)
	}
	mins := 60
	for _, w := range cfg.Ints("windows_min") {
		mins = max(mins, w)
	}
	mins = max(mins, cfg.Sub("toggle").Sub("err_source").I("minutes", 60), cfg.Sub("toggle").Sub("acct_disable").I("real_err_lookback_min", 0))
	p := fetch.NewPsql(cfg)
	p.At = now
	dbNow, rows, err := p.Logs(ids, mins)
	must(err)
	if dbNow != now {
		log.Fatalf("frozen now mismatch %d != %d", dbNow, now)
	}
	s := &fetch.Snapshot{Version: 1, Now: now, Label: rec.Ts, Source: "round:" + rec.Ts, Channels: rec.Channels,
		LogMin: mins, Rows: rows, Probes: rec.Probes, ProbesReused: rec.ProbesReused,
		DisabledTests: map[int][]m.Probe{}}
	if s.Probes == nil {
		s.Probes = m.Probes{}
	}
	if rec.Toggle != nil {
		s.HasToggle = true
		dis := m.SortedIDs(rec.Toggle.Disabled)
		sort.Ints(dis)
		for _, i := range dis {
			s.Disabled = append(s.Disabled, m.Channel{ID: i, Status: 2})
			s.DisabledTests[i] = rec.Toggle.Disabled[i].Results
		}
		s.BottomUpTests = rec.Toggle.BottomUpTests
	}
	must(s.Save(*out))
	must(writePrefix(filepath.Join(*out, fetch.RoundsFile), rounds[:idx], func(*m.Rec) bool { return true }))
	must(writePrefix(filepath.Join(*out, fetch.ShadowFile), shadow, func(r *m.Rec) bool { return m.Epoch(r.TsTime) < float64(now) }))
	b, err := os.ReadFile(cfgPath)
	must(err)
	must(os.WriteFile(filepath.Join(*out, fetch.ConfigFile), b, 0o644))
	fmt.Printf("snapshot %s: now=%d chans=%d rows=%d (%d min) rounds=%d disabled=%d\n", rec.Ts, now, len(ids), len(rows), mins, idx, len(s.Disabled))
}

func writePrefix(path string, rs []*m.Rec, keep func(*m.Rec) bool) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, r := range rs {
		if keep(r) {
			w.Write(r.Raw)
			w.WriteByte('\n')
		}
	}
	return w.Flush()
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
