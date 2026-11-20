package core

import (
	"autoweight/diag"
	m "autoweight/model"
)

// hangTailFlags runs HANGTAIL over the decision rows (cfg hang_tail: hang_sec 180,
// min_hang 2, min_share 0.05, probe_tok 1318). Diagnostic only.
func hangTailFlags(cfg m.M, rows []m.Row, target map[int]int, now int64) []string {
	h := cfg.Sub("hang_tail")
	return diag.HangTailFlags(rows, target, now, int(h.F("hang_sec", 180)), int(h.F("min_hang", 2)), h.F("min_share", 0.05), int(h.F("probe_tok", 1318)))
}
