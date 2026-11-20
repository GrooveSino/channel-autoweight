package shadow

import (
	"fmt"
	"testing"

	m "autoweight/model"
)

func TestBackfillSlots(t *testing.T) {
	const base = 1_700_000_000 - 1_700_000_000%300
	for _, c := range []struct {
		name string
		last float64
		now  float64
		max  float64
		want string
	}{
		{"no history", 0, base + 3000, 120, "[]"},
		{"fills missing 5m slots", base, base + 1000, 120, fmt.Sprint([]int64{base + 300, base + 600})},
		{"skips slot too close to now", base, base + 700, 120, fmt.Sprint([]int64{base + 300})},
		{"capped by max_min", base, base + 3700, 10, fmt.Sprint([]int64{base + 3300})},
	} {
		got := BackfillSlots(c.last, c.now, 5, c.max)
		if s := fmt.Sprint(got); s != c.want && !(c.want == "[]" && len(got) == 0) {
			t.Errorf("%s: got %s want %s", c.name, s, c.want)
		}
	}
}

// Channels with few real requests get a probe unless one ran within the cooldown.
func TestPlanProbesAndAges(t *testing.T) {
	now := 1_700_000_000.0
	rec, _ := m.ParseRound([]byte(`{"ts":"2023-11-14T22:00:00+00:00","probes":{"5":[{"ok":true,"ttft":2}]}}`))
	recs := []*m.Rec{rec} // 13:20 before now ⇒ 13.3 min old
	plan := PlanProbes(m.M{}, []int{2, 5, 9}, map[int]int{2: 10, 5: 0, 9: 1}, recs, now)
	if _, ok := plan[9]; !ok || len(plan) != 1 {
		t.Errorf("plan %v (2 busy, 5 probed recently)", plan)
	}
	if p, age := LastProbe(recs, 5, now, 30); len(p) != 1 || age == nil || *age < 13 || *age > 14 {
		t.Errorf("last probe %v %v", p, age)
	}
	ages := ProbeAges(map[int]string{2: "probe", 5: "reuse 12.5m", 9: "none"})
	if ages[2] != 0 || ages[5] != 12.5 || len(ages) != 2 {
		t.Errorf("ages %v", ages)
	}
}
