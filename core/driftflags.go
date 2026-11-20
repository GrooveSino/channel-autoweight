package core

import (
	"autoweight/diag"
	"autoweight/features"
	m "autoweight/model"
)

// slowDriftFlags runs SLOWDRIFT over a 120-minute fetch (cfg slow_drift: min_n 20,
// ratio 2.5, min_p50 8). Falls back to the decision rows if the fetch fails. Diagnostic only.
func slowDriftFlags(env Env, cfg m.M, ids []int, rows []m.Row, target map[int]int, now int64) []string {
	if _, long, err := env.Src.Logs(ids, 120); err == nil {
		rows = long
	}
	sd := cfg.Sub("slow_drift")
	return diag.SlowDriftFlags(features.UserRows(cfg, rows), target, now, int(sd.F("min_n", 20)), sd.F("ratio", 2.5), sd.F("min_p50", 8))
}
