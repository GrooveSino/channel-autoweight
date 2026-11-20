// awcheck: read-only post-write verification and disabled-channel direct tests.
//
//	awcheck                      # channels vs abilities (BEGIN READ ONLY), exit 1 on mismatch
//	awcheck -expect 2=9,5=2      # also require these weights
//	awcheck -direct 26 -n 3      # n direct tests of #26 (runs on the prod host; the key never leaves it)
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"autoweight/fetch"
	m "autoweight/model"
	"autoweight/probe"
)

func main() {
	dir := flag.String("dir", "/workspace/reports/ops/channel-autoweight-go", "Go autoweight dir (config.json, data/)")
	expect := flag.String("expect", "", "ID=W[,ID=W] weights that must be in the DB")
	direct := flag.Int("direct", 0, "channel id to test directly (upstream test via the prod host)")
	n := flag.Int("n", 3, "number of direct tests")
	flag.Parse()
	cfg, err := m.LoadConfig(filepath.Join(*dir, "config.json"))
	if err != nil {
		log.Fatal(err)
	}
	if *direct > 0 {
		c := probe.NewClient(cfg)
		c.CountPath = filepath.Join(*dir, "data/direct_probe_count.jsonl")
		ok := 0
		for k, r := range c.Upstream(*direct, *n) {
			if r.OK {
				ok++
			}
			fmt.Printf("#%d test %d: ok=%v status=%s ttft=%s total=%s deltas=%s route=%s err=%v\n", *direct, k+1, r.OK,
				pi(r.Status), pf(r.Ttft), pf(r.Total), pi(r.Deltas), r.Route, r.Err)
		}
		fmt.Printf("DIRECT #%d %d/%d ok\n", *direct, ok, *n)
		if ok*2 <= *n {
			os.Exit(1)
		}
		return
	}
	want := map[int]int{}
	for _, kv := range strings.Split(*expect, ",") {
		if a, b, ok := strings.Cut(strings.TrimSpace(kv), "="); ok {
			i, e1 := strconv.Atoi(strings.TrimPrefix(a, "#"))
			w, e2 := strconv.Atoi(b)
			if e1 != nil || e2 != nil {
				log.Fatalf("bad -expect item %q", kv)
			}
			want[i] = w
		}
	}
	cs, err := fetch.NewPsql(cfg).Verify()
	if err != nil {
		log.Fatal(err)
	}
	bad := 0
	for _, c := range cs {
		st := "OK"
		if !c.OK() {
			st = "MISMATCH"
		}
		if w, ok := want[c.ID]; ok && w != c.Weight {
			st = fmt.Sprintf("WANT %d", w)
		}
		if st != "OK" {
			bad++
		}
		fmt.Printf("#%-3d %-22s status=%d weight=%-2d priority=%d abilities=%d enabled=%d diff=%d %s\n",
			c.ID, c.Name, c.Status, c.Weight, c.Priority, c.Abilities, c.Enabled, c.Mismatch, st)
	}
	if bad > 0 {
		fmt.Printf("VERIFY FAIL %d\n", bad)
		os.Exit(1)
	}
	fmt.Println("VERIFY OK")
}

func pi(p *int) string {
	if p == nil {
		return "-"
	}
	return strconv.Itoa(*p)
}

func pf(p *float64) string {
	if p == nil {
		return "-"
	}
	return strconv.FormatFloat(*p, 'f', 2, 64)
}
