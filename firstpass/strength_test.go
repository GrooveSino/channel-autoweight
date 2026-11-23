package firstpass

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestFixedPointContract(t *testing.T) {
	for _, v := range []struct {
		n Milli
		s string
	}{{0, "0.000"}, {300, "0.300"}, {1000, "1.000"}, {-3, "-0.003"}} {
		b, e := json.Marshal(v.n)
		if e != nil || string(b) != v.s {
			t.Fatal(string(b), e)
		}
	}
	if Milli(347).Weight(10) != 3 || Milli(347).Weight(20) != 7 || Milli(347).Weight(100) != 35 || Milli(350).Weight(10) != 4 {
		t.Fatal("integer rounding")
	}
}
func TestContinuousRatio(t *testing.T) {
	in := fixture(t)
	in.Events = nil
	fast, slow := 1, 6000
	for i := 0; i < 1000; i++ {
		for j, y := range []int{950, 817} {
			ms := &fast
			if i >= y {
				ms = &slow
			}
			in.Events = append(in.Events, Event{ID: fmt.Sprint(i, "/", j), User: fmt.Sprint(i), Stratum: in.Strata[0].Name, Route: "random", Outcome: "ok", Channel: 101 + j, Attempt: 1, End: in.Cutoff, Available: in.Cutoff, TotalMS: ms})
		}
	}
	r, e := Evaluate(in)
	if e != nil || r.Channels[102].Strength == nil || *r.Channels[102].Strength != 860 {
		t.Fatal(r, e)
	}
}
