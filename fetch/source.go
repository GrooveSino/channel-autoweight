// Package fetch is the IO layer that reads channels and logs. Everything
// downstream sees only model structs. Adapters: Psql (ssh+docker psql,
// read-only transactions), Snapshot (offline JSON), and Plugin (contract v1,
// aggregate-only; see plugin.go for why it cannot feed the row-level core).
package fetch

import m "autoweight/model"

// Source is the read side the orchestrator needs.
type Source interface {
	// Channels: status=1 channels that serve model_like minus exclude_model_like, ordered by id.
	Channels() ([]m.Channel, error)
	// Disabled: status<>1 channels in the same model scope, ordered by id.
	Disabled() ([]m.Channel, error)
	// Logs: type 2/5 rows for ids in the last minutes, plus DB now (epoch s).
	Logs(ids []int, minutes int) (int64, []m.Row, error)
}

// FilterRows keeps rows of ids with t >= now-minutes*60 (and t <= now).
func FilterRows(rows []m.Row, ids []int, now int64, minutes int) []m.Row {
	in := map[int]bool{}
	for _, i := range ids {
		in[i] = true
	}
	lo := now - int64(minutes)*60
	out := []m.Row{}
	for _, r := range rows {
		if in[r.Ch] && r.T >= lo && r.T <= now {
			out = append(out, r)
		}
	}
	return out
}
