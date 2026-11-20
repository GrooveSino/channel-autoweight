package diag

import (
	"fmt"
	"sort"

	m "autoweight/model"
)

// ExtWriteFlags compares the current DB weights (cur) with the final weights of
// the last applied round in rounds. A difference means someone else (Groove or
// another bot) changed the weight since our last write; the reviewer should take
// the current value as intentional instead of silently reverting it. Channels
// absent from the last write (new channels) are skipped. Diagnostic only.
// A matching entry in manual (same channel and weight, after the last write)
// attributes the change to the bot's own hand edit instead.
func ExtWriteFlags(rounds []*m.Rec, cur map[int]int, manual []ManualWrite) []string {
	var last *m.Rec
	for i := len(rounds) - 1; i >= 0; i-- {
		if rounds[i].Applied && len(rounds[i].Decision.Target) > 0 {
			last = rounds[i]
			break
		}
	}
	if last == nil {
		return nil
	}
	ids := []int{}
	for id, w := range cur {
		if lw, ok := last.Decision.Target[id]; ok && lw != w {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	out := []string{}
	for _, id := range ids {
		if mw := manualFor(manual, id, cur[id], last.Ts); mw != nil {
			out = append(out, fmt.Sprintf("EXTWRITE #%d: 上次写库（%s）是 %d，现在库里是 %d——是 %s 在 %s 手动改的（%s），不是外部改动；只记录",
				id, last.Ts, last.Decision.Target[id], cur[id], mw.By, mw.Ts, mw.Reason))
			continue
		}
		out = append(out, fmt.Sprintf("EXTWRITE #%d: 上次写库（%s）是 %d，现在库里是 %d——有人在本系统之外改过权重，按有意调整对待、不要直接改回；只记录",
			id, last.Ts, last.Decision.Target[id], cur[id]))
	}
	return out
}
