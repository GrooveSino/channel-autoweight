package model

import (
	"fmt"
	"testing"
)

func TestRoundHalfEven(t *testing.T) {
	cases := []struct {
		x    float64
		nd   int
		want float64
	}{{2.5, 0, 2}, {3.5, 0, 4}, {0.125, 2, 0.12}, {0.375, 2, 0.38}, {2.0005, 3, 2.001}, {-1.5, 0, -2}, {1.23456, 4, 1.2346}}
	for _, c := range cases {
		if got := Round(c.x, c.nd); got != c.want {
			t.Errorf("Round(%v,%d)=%v want %v", c.x, c.nd, got, c.want)
		}
	}
}

func TestPySetKnownOrders(t *testing.T) {
	cases := []struct{ add, want []int }{
		{[]int{2, 5}, []int{2, 5}}, {[]int{22, 5}, []int{5, 22}}, {[]int{23, 2}, []int{2, 23}},
		{[]int{26, 20, 5}, []int{26, 20, 5}}, {[]int{1, 2, 3, 4, 5, 6}, []int{1, 2, 3, 4, 5, 6}},
	}
	for _, c := range cases {
		s := NewPySet()
		for _, x := range c.add {
			s.Add(x)
		}
		got := s.Items()
		for k := range got {
			if got[k] != c.want[k] {
				t.Errorf("%v -> %v want %v", c.add, got, c.want)
				break
			}
		}
	}
}

func TestParseRoundAndDryRun(t *testing.T) {
	r, err := ParseRound([]byte(`{"ts":"2026-10-29T21:19:17+08:00","old":{"2":3,"5":null},"applied":true,"trust":{"2":{"trust":0.5}}}`))
	if err != nil || r.Old[2] != 3 || len(r.Old) != 1 || Epoch(r.TsTime) != 1793279957 || !r.IsDryRun(true) || r.IsDryRun(false) {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestProbeTokenIDs(t *testing.T) {
	c, err := LoadConfig("../testdata/config.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(c.Ints("probe_token_ids")); got != "[38 1305]" {
		t.Errorf("probe_token_ids %s", got)
	}
}
