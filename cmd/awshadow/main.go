// awshadow: read-only shadow point (port of shadow.py). Never writes the DB.
//
//	awshadow                 # backfill gaps, then probe thin channels and append the current point
//	awshadow -backfill-only  # only replay missing points (no probes), as each round does first
//	awshadow -no-probe       # current point without probes
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"autoweight/fetch"
	m "autoweight/model"
	"autoweight/probe"
	"autoweight/shadow"
)

func main() {
	dir := flag.String("dir", "/workspace/reports/ops/channel-autoweight-go", "Go autoweight dir (config.json, data/)")
	noProbe := flag.Bool("no-probe", false, "no probes")
	noBF := flag.Bool("no-backfill", false, "do not backfill gaps")
	bfOnly := flag.Bool("backfill-only", false, "only backfill gaps")
	flag.Parse()
	cfg, err := m.LoadConfig(filepath.Join(*dir, "config.json"))
	must(err)
	sp := shadow.Params(cfg)
	sf := filepath.Join(*dir, sp.S("file", "data/shadow.jsonl"))
	srecs, err := m.LoadRounds(sf)
	must(err)
	if len(srecs) > 500 {
		srecs = srecs[len(srecs)-500:]
	}
	rall, err := m.LoadRounds(filepath.Join(*dir, cfg.S("rounds_file", "data/rounds.jsonl")))
	must(err)
	rrecs := rall
	if len(rrecs) > 10 {
		rrecs = rrecs[len(rrecs)-10:]
	}
	db := fetch.NewPsql(cfg)
	chans, err := db.Channels()
	must(err)
	chans = m.ExcludeChans(cfg, chans)
	ids := make([]int, len(chans))
	for k, c := range chans {
		ids[k] = c.ID
	}
	var lastTs float64
	if len(srecs) > 0 && !srecs[len(srecs)-1].TsTime.IsZero() {
		lastTs = m.Epoch(srecs[len(srecs)-1].TsTime)
	}
	gapNow := 0.0
	if lastTs > 0 {
		gapNow = (float64(time.Now().Unix()) - lastTs) / 60
	}
	doBF := sp.B("backfill", true) && !*noBF && gapNow > 1.5*sp.F("interval_min", 5)
	span := 0
	for _, w := range cfg.Ints("windows_min") {
		span = max(span, w)
	}
	if doBF {
		span += int(min(gapNow, sp.F("backfill_max_min", 360)))
	}
	now, rows, err := db.Logs(ids, span+1)
	must(err)
	in := shadow.Input{Cfg: cfg, Chans: chans, Rows: rows, Shadow: srecs, Rounds: rrecs, All: rall}
	if doBF {
		var filled []*shadow.Record
		for _, slot := range shadow.BackfillSlots(lastTs, float64(now), sp.F("interval_min", 5), sp.F("backfill_max_min", 360)) {
			in.Now = slot
			rec, err := shadow.Compute(in, true)
			must(err)
			in.Shadow = append(in.Shadow, rec.AsRec())
			filled = append(filled, rec)
		}
		if len(filled) > 0 {
			must(appendAll(sf, filled))
			fmt.Printf("BACKFILL %d 条（%s–%s，空档 %.1fm，按时间点回放，不探测）\n", len(filled), filled[0].Ts[11:16], filled[len(filled)-1].Ts[11:16], gapNow)
			for _, r := range filled {
				fmt.Printf("  %s suggest=%v trust=%v\n", r.Ts[11:16], r.Suggest, trustMap(r))
			}
		}
	}
	if *bfOnly {
		return
	}
	in.Now = now
	counts := shadow.RealCounts(cfg, rows, now, sp.F("probe_window_min", 10))
	plan := map[int]string{}
	if !*noProbe && cfg.Sub("probe").B("enabled", false) {
		plan = shadow.PlanProbes(cfg, ids, counts, in.Shadow, float64(now))
	}
	in.Plan, in.Fresh = plan, m.Probes{}
	if len(plan) > 0 {
		in.Fresh = probe.NewClient(cfg).Site(m.SortedIDs(plan))
	}
	rec, err := shadow.Compute(in, false)
	must(err)
	must(appendAll(sf, []*shadow.Record{rec}))
	fmt.Printf("[%s] SHADOW (no write) probed=%v\n", rec.Ts, m.SortedIDs(plan))
	for _, c := range chans {
		i := c.ID
		s, f := rec.Scores[i], rec.Features[i]
		fmt.Printf("#%-3d db=%2d suggest=%2d score=%.3f trust=%v ttft=%s(%s) 1h n=%d real=%d\n", i, rec.Old[i], rec.Suggest[i],
			s.Score, rec.Trust[i].Trust, ff(s.Ttft), rec.TtftSrc[i], f.Long.N, counts[i])
	}
	for _, n := range rec.Notes {
		fmt.Println("NOTE:", n)
	}
}

func trustMap(r *shadow.Record) map[int]float64 {
	out := map[int]float64{}
	for i, v := range r.Trust {
		out[i] = v.Trust
	}
	return out
}

func ff(p *float64) string {
	if p == nil {
		return "None"
	}
	return fmt.Sprint(*p)
}

func appendAll(path string, recs []*shadow.Record) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
