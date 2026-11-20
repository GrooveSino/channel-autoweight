package toggle

import (
	"fmt"

	m "autoweight/model"
)

// ExemptIDs ports exempt_ids (v2.88): toggle.exempt ∪ toggle.acct_disable.exempt,
// never disabled on any path (err_streak, last-place, unusable, account-level).
func ExemptIDs(cfg m.M) map[int]bool {
	t := cfg.Sub("toggle")
	out := map[int]bool{}
	for _, x := range t.Ints("exempt") {
		out[x] = true
	}
	for _, x := range t.Sub("acct_disable").Ints("exempt") {
		out[x] = true
	}
	return out
}

// RetiredIDs: toggle.retired, channels the owner took down. They are never
// direct-tested and never considered for re-enable.
func RetiredIDs(cfg m.M) map[int]bool {
	out := map[int]bool{}
	for _, x := range cfg.Sub("toggle").Ints("retired") {
		out[x] = true
	}
	return out
}

// DropRetired filters retired channels out of the disabled list.
func DropRetired(cfg m.M, dis []m.Channel) []m.Channel {
	ret := RetiredIDs(cfg)
	out := dis[:0:0]
	for _, c := range dis {
		if !ret[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// MainGuard: a clean large-sample main (1h real >= main_guard_n, real err rate
// <= main_guard_err) is never disabled for ranking last; small-sample bias
// pushes it to the bottom while it carries most real traffic. Error-based
// disables are unaffected.
func MainGuard(p m.M, cur *Formal, cid int) (bool, string) {
	minN := p.F("main_guard_n", 0)
	if minN <= 0 || cur == nil {
		return false, ""
	}
	n, er := cur.N60[cid], cur.Err60R[cid]
	if n == nil || er == nil || float64(*n) < minN || *er > p.F("main_guard_err", 0.02) {
		return false, ""
	}
	return true, fmt.Sprintf("1h 真实 %d 条、错误率 %.1f%%，属于干净主力", *n, *er*100)
}
