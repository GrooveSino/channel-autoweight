package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"autoweight/diag"
	m "autoweight/model"
)

func runTokenPair(rows []m.Row, dir string, minutes int, asJSON bool) {
	res := diag.AnalyzeTokenPair(rows, diag.DefaultTokenPair)
	if asJSON {
		json.NewEncoder(os.Stdout).Encode(res)
		return
	}
	fmt.Printf("TOKENPAIR 最近 %dm 真实成功请求，同令牌跨渠道配对（归一延迟 秒/千 token，<1 = 同一批用户在该渠道更快）\n", minutes)
	fmt.Println("| 渠道 | n | 令牌数 | 最大令牌占比 | 归一 p50 | 配对令牌 | 配对比 | 判定 |")
	fmt.Println("|---|---|---|---|---|---|---|---|")
	for _, p := range res {
		r := "-"
		if p.Ratio != nil {
			r = fmt.Sprintf("%.3f", *p.Ratio)
		}
		fmt.Printf("| #%d | %d | %d | %.2f | %.1f | %d | %s | %s |\n", p.Ch, p.N, p.Tokens, *p.TopShare, *p.NlP50, p.PairedTokens, r, p.Verdict)
	}
	for _, s := range diag.TokenPairFlags(res, lastShadowSuggest(filepath.Join(dir, "data", "shadow.jsonl")), 30) {
		fmt.Println(s)
	}
}

// lastShadowSuggest reads the suggest map of the last shadow point (nil if unavailable).
func lastShadowSuggest(path string) map[int]int {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	last := ""
	for sc.Scan() {
		last = sc.Text()
	}
	var d struct {
		Suggest map[string]int `json:"suggest"`
	}
	if json.Unmarshal([]byte(last), &d) != nil {
		return nil
	}
	out := map[int]int{}
	for k, v := range d.Suggest {
		if i, err := strconv.Atoi(k); err == nil {
			out[i] = v
		}
	}
	return out
}
