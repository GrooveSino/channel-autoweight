// awdiag: read-only diagnostics over recent real logs (never writes, never affects scoring).
//
//	awdiag -emptypair -minutes 360   # same-token cross-channel empty-success comparison
//	awdiag -tokenpair -minutes 360   # same-token cross-channel normalized-latency comparison
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"autoweight/diag"
	"autoweight/features"
	"autoweight/fetch"
	m "autoweight/model"
)

func main() {
	dir := flag.String("dir", "/workspace/reports/ops/channel-autoweight-go", "Go autoweight dir")
	minutes := flag.Int("minutes", 360, "log window")
	minN := flag.Int("min-n", 5, "min requests per token-channel pair")
	asJSON := flag.Bool("json", false, "print JSON")
	flag.Bool("emptypair", true, "EMPTYPAIR diagnostic (default)")
	tokenpair := flag.Bool("tokenpair", false, "TOKENPAIR diagnostic instead of EMPTYPAIR")
	flag.Parse()
	cfg, err := m.LoadConfig(filepath.Join(*dir, "config.json"))
	if err != nil {
		log.Fatal(err)
	}
	p := fetch.NewPsql(cfg)
	chs, err := p.Channels()
	if err != nil {
		log.Fatal(err)
	}
	ids := []int{}
	for _, c := range chs {
		ids = append(ids, c.ID)
	}
	_, rs, err := p.Logs(ids, *minutes)
	if err != nil {
		log.Fatal(err)
	}
	if *tokenpair {
		runTokenPair(features.UserRows(cfg, rs), *dir, *minutes, *asJSON)
		return
	}
	o := diag.DefaultEmptyPair
	o.MinN = *minN
	res := diag.AnalyzeEmptyPair(features.UserRows(cfg, rs), o)
	if *asJSON {
		json.NewEncoder(os.Stdout).Encode(res)
		return
	}
	fmt.Printf("EMPTYPAIR 最近 %dm 真实请求，同令牌跨渠道空成功对照（观察/期望>1 = 同一批用户在该渠道更容易空）\n", *minutes)
	fmt.Println("| 渠道 | n | 空成功 | 空率 | 配对令牌 | 观察 | 期望 | 超出 | 比值 | 判定 |")
	fmt.Println("|---|---|---|---|---|---|---|---|---|---|")
	for _, x := range res {
		rt, ra := "-", "-"
		if x.N > 0 {
			rt = fmt.Sprintf("%.3f", float64(x.Empty)/float64(x.N))
		}
		if x.Ratio != nil {
			ra = fmt.Sprintf("%.2f", *x.Ratio)
		}
		fmt.Printf("| #%d | %d | %d | %s | %d | %d | %.1f | %.1f | %s | %s |\n", x.Ch, x.N, x.Empty, rt, x.PairedTokens, x.Obs, x.Exp, x.Excess, ra, x.Verdict)
	}
	for _, s := range diag.EmptyPairFlags(res, 10, 0.25) {
		fmt.Println(s)
	}
}
