package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"autoweight/backtest"
	"autoweight/decide"
	"autoweight/features"
	m "autoweight/model"
)

// agree replays every recorded dry-run round in the last `hours` of the
// snapshot: the current code's proposal (same probes/trust/prev as recorded)
// is compared with the operator's final weights (the apply record that followed
// within 10 min, else the unchanged DB weights) and with the old recorded
// system proposal. Channels in skip are left out.
func agree(cfg m.M, rounds []*m.Rec, rows []m.Row, end int64, hours int, skip map[int]bool, verbose bool) {
	sort.Slice(rows, func(a, b int) bool { return rows[a].T < rows[b].T })
	from := end - int64(hours)*3600
	nNew, nOld, n, dNew, dOld := 0, 0, 0, 0, 0
	var q [3][4]float64 // manual/new/old: weight, p50, p90, cerr sums over the next hour
	for k, r := range rounds {
		ex := extra(r)
		if !r.IsDryRun(false) || ex.Now < from || ex.Now > end || len(r.Decision.Target) == 0 {
			continue
		}
		final := r.Old
		for _, a := range rounds[k+1:] {
			if extra(a).Now-ex.Now > 600 {
				break
			}
			if a.Applied {
				final = a.Decision.Target
				break
			}
		}
		var hist []m.Row
		for _, x := range rows {
			if x.T >= ex.Now-3600 && x.T <= ex.Now {
				hist = append(hist, x)
			}
		}
		chans := r.Channels
		ids := make([]int, len(chans))
		for i, c := range chans {
			ids[i] = c.ID
		}
		tm := map[int]float64{}
		for i, s := range r.Trust {
			tm[i] = s.Trust
		}
		now := time.Unix(ex.Now, 0).In(time.FixedZone("CST", 8*3600))
		nc := decide.NewcomerIDs(cfg, chans, rounds[:k], now.Format("2006-01-02T15:04:05.000000-07:00"))
		fs := features.Build(cfg, chans, hist, ex.Now)
		features.AttachPair(cfg, fs, rows, ex.Now) // v5.01: rows after ex.Now are ignored
		d, err := decide.Decide(decide.Input{Cfg: cfg, Chans: chans, Feats: fs,
			Probes: r.Probes, Prev: r.Old, Trust: tm, TrustIDs: ids, Newcomers: nc, Now: now})
		must(err)
		line := ""
		for j, tg := range []map[int]int{final, d.Target, ex.System} {
			kept := map[int]int{}
			for i, v := range tg {
				if !skip[i] {
					kept[i] = v
				}
			}
			w, a, b, c := backtest.Next(cfg, rows, ex.Now, kept)
			q[j][0], q[j][1], q[j][2], q[j][3] = q[j][0]+w, q[j][1]+a, q[j][2]+b, q[j][3]+c
		}
		for _, i := range ids {
			if skip[i] {
				continue
			}
			f, nw, old := final[i], d.Target[i], ex.System[i]
			n++
			if nw == f {
				nNew++
			}
			if old == f {
				nOld++
			}
			dNew += iabs(nw - f)
			dOld += iabs(old - f)
			mark := ""
			if nw != f {
				mark = "≠"
			}
			line += fmt.Sprintf(" #%d %d→人工%d/新%d/旧%d%s", i, r.Old[i], f, nw, old, mark)
		}
		if verbose {
			fmt.Printf("  %s%s\n", now.Format("01-02 15:04"), line)
			for _, no := range d.Notes {
				fmt.Printf("      %s\n", no)
			}
		}
	}
	if n == 0 {
		fmt.Printf("%3dh: no rounds\n", hours)
		return
	}
	fmt.Printf("%3dh: 渠道轮次 %d  一致率 新 %.1f%%（%d） 旧 %.1f%%（%d）  平均|差| 新 %.2f 旧 %.2f\n", hours, n,
		100*float64(nNew)/float64(n), nNew, 100*float64(nOld)/float64(n), nOld, float64(dNew)/float64(n), float64(dOld)/float64(n))
	for j, nm := range []string{"人工", "新", "旧"} {
		if w := q[j][0]; w > 0 {
			fmt.Printf("       下一小时(按权重) %s p50=%.2f p90=%.2f 渠道错误率=%.4f\n", nm, q[j][1]/w, q[j][2]/w, q[j][3]/w)
		}
	}
}

type recExtra struct {
	Now    int64       `json:"now"`
	System map[int]int `json:"system_target"`
}

func extra(r *m.Rec) recExtra {
	var e recExtra
	_ = json.Unmarshal(r.Raw, &e)
	return e
}

func iabs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
