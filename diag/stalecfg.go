package diag

import (
	"fmt"
	"sort"
	"strconv"
)

// StaleCfgFlags lists per-channel config blocks (numeric keys inside the given config
// sections, e.g. capacity."21") whose channel is not among the currently enabled gpt
// channels: deleted or disabled channels whose overrides would silently re-apply if the
// id came back. Diagnostic only; it never edits config or weights.
func StaleCfgFlags(cfg map[string]any, enabled []int, sections []string) []string {
	live := map[int]bool{}
	for _, id := range enabled {
		live[id] = true
	}
	out := []string{}
	for _, s := range sections {
		sub, ok := cfg[s].(map[string]any)
		if !ok {
			continue
		}
		stale := []int{}
		for k := range sub {
			if id, err := strconv.Atoi(k); err == nil && !live[id] {
				stale = append(stale, id)
			}
		}
		sort.Ints(stale)
		if len(stale) > 0 {
			out = append(out, fmt.Sprintf("STALECFG: 配置 %s 里还有 %v 的专属参数，但它们不在当前启用渠道里（已删除或禁用）；只记录", s, stale))
		}
	}
	return out
}

// FloorFlags notes when the enabled count sits at (or below) the minimum, so the
// "bottom for ~4h" disable rule is blocked and only persistent errors may disable,
// down to hardMin. Diagnostic only.
func FloorFlags(nEnabled, minEnabled, hardMin int) []string {
	if nEnabled > minEnabled {
		return nil
	}
	return []string{fmt.Sprintf("FLOOR: 当前只有 %d 个启用渠道（底线 %d、硬底线 %d）：排名垫底规则不能禁用，只有持续报错才能降到 %d 并须马上告知 Groove；只记录", nEnabled, minEnabled, hardMin, hardMin)}
}
