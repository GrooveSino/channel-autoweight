// awtrial: new-channel admission trial through the site with the test token.
//
//	awtrial -ch 28 -ref 2 -model gpt-6-astra -n 50
//
// Sends n interleaved requests to the candidate and the reference channel on
// the same model (every big-every-th one with a ~20k-token context), prints
// per-channel stats and the verdict, and appends a summary to data/trial.jsonl.
// Requests run on the prod host; the test token never leaves it.
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	m "autoweight/model"
	"autoweight/trial"
)

func main() {
	dir := flag.String("dir", "/workspace/reports/ops/channel-autoweight-go", "Go autoweight dir")
	ch := flag.Int("ch", 0, "candidate channel id")
	ref := flag.Int("ref", 2, "reference channel id (same model, same batch)")
	model := flag.String("model", "", "model to trial")
	n := flag.Int("n", 50, "requests per channel")
	bigEvery := flag.Int("big-every", 5, "every k-th request carries a large context")
	conc := flag.Int("conc", 4, "concurrent requests")
	maxOut := flag.Int("max-out", 400, "max_output_tokens")
	rpm := flag.Float64("rpm", 18, "request starts per minute (site limit is 30/min per user, probes share it)")
	flag.Parse()
	if *ch <= 0 || *model == "" {
		log.Fatal("need -ch and -model")
	}
	cfg, err := m.LoadConfig(filepath.Join(*dir, "config.json"))
	if err != nil {
		log.Fatal(err)
	}
	p := cfg.Sub("probe")
	job, _ := json.Marshal(map[string]any{"channels": []int{*ch, *ref}, "model": *model, "n": *n, "big_every": *bigEvery,
		"big_repeat": 150, "conc": *conc, "max_out": *maxOut, "rpm": *rpm, "timeout": 180, "prompt": p.S("prompt", ""),
		"site": p.S("base_url", ""), "key_path": p.S("key_path_remote", "")})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh", "-o", "ConnectTimeout=10", cfg.S("ssh_host", ""),
		"python3 - "+base64.StdEncoding.EncodeToString(job))
	cmd.Stdin = strings.NewReader(trial.Remote)
	cmd.Stderr = nil // dropped: may echo secrets
	out, err := cmd.Output()
	if err != nil {
		log.Printf("remote: %v (keeping %d bytes of output)", err, len(out))
	}
	ts := time.Now().Format("20060102-1504")
	raw := filepath.Join(*dir, "data/trial", fmt.Sprintf("%s-%d-%s.jsonl", ts, *ch, *model))
	_ = os.MkdirAll(filepath.Dir(raw), 0o755)
	_ = os.WriteFile(raw, out, 0o644)
	rs := trial.Parse(string(out))
	c, r := trial.Summarise(rs, *ch), trial.Summarise(rs, *ref)
	for _, s := range []trial.Stats{c, r} {
		fmt.Printf("#%-3d throttled=%d n=%d err=%d empty=%d big=%d/%d ttft50=%.2f p50=%.2f p90=%.2f tps50=%.1f\n",
			s.Ch, s.Throttled, s.N, s.Err, s.Empty, s.BigOK, s.Big, s.Ttft50, s.P50, s.P90, s.TPS50)
	}
	pass, why := trial.Default.Judge(c, r)
	streak := prevStreak(filepath.Join(*dir, "data/trial.jsonl"), *ch, *model)
	if pass {
		streak++
	} else {
		streak = 0
	}
	rec, _ := json.Marshal(map[string]any{"ts": time.Now().Format(time.RFC3339), "ch": *ch, "ref": *ref, "model": *model,
		"pass": pass, "why": why, "streak": streak, "cand": c, "refstats": r, "raw": raw})
	if f, err := os.OpenFile(filepath.Join(*dir, "data/trial.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Write(append(rec, '\n'))
		f.Close()
	}
	fmt.Printf("TRIAL #%d %s vs #%d: pass=%v why=%v streak=%d\n", *ch, *model, *ref, pass, why, streak)
}

func prevStreak(path string, ch int, model string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	s := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r struct {
			Ch     int    `json:"ch"`
			Model  string `json:"model"`
			Streak int    `json:"streak"`
		}
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Ch == ch && r.Model == model {
			s = r.Streak
		}
	}
	return s
}
