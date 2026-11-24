package firstpass

import (
	"math"
	"testing"
)

func TestProduction50AndNonFinite(t *testing.T) {
	for _, q := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := CheckedRatio(q); err == nil {
			t.Fatal("accepted non-finite")
		}
	}
	for _, q := range []float64{0, .347, 1} {
		v, err := CheckedRatio(q)
		if err != nil || v.Weight(50) < 0 || v.Weight(50) > 50 {
			t.Fatal(q)
		}
	}
	in := fixture(t)
	in.WeightScale = 50
	for id := range in.Current {
		in.Current[id] = 50
		in.Caps[id] = 50
	}
	r, err := Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if r.Channels[101].Strength == nil || *r.Channels[101].Strength != 1000 {
		t.Fatal(r)
	}
	in.Caps[101] = 0
	r, err = Evaluate(in)
	if err != nil || r.Channels[101].Strength == nil || *r.Channels[101].Strength != 0 {
		t.Fatal(r, err)
	}
	in.Events = nil
	r, err = Evaluate(in)
	if err != nil || r.Channels[102].Strength != nil {
		t.Fatal("missing must abstain", r, err)
	}
}
