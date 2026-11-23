package model

import (
	"fmt"
	"strings"
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
	if got := fmt.Sprint(c.Ints("probe_token_ids")); got != "[9001 9002]" {
		t.Errorf("probe_token_ids %s", got)
	}
}

func idsOfChans(cs []Channel) []int {
	out := make([]int, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

// CASE-AW-030: ExcludeChans is an exact ID filter. A test-named or off-priority
// channel stays in the set until its ID is listed in exclude_ids.
func TestExcludeChansKeepsUnlistedTestNamedChannels(t *testing.T) {
	chans := []Channel{
		{ID: 2, Name: "[SWBS]-GPT-0.16", Weight: 20, Priority: 10},
		{ID: 23, Name: "[BL]-GPT-0.13", Weight: 20, Priority: 10},
		{ID: 26, Name: "[KRL]-GPT-0.13", Weight: 20, Priority: 10},
		{ID: 32, Name: "[SJ]-GPT-0.13", Weight: 20, Priority: 10},
		{ID: 73, Name: "[HL]-GPT-0.13", Weight: 20, Priority: 10},
		{ID: 88, Name: "测试-[HL]-GPT-Pro-0.18", Weight: 5, Priority: 10},
		{ID: 89, Name: "测试-[HL]-GPT-Ultrafast-0.20", Weight: 5, Priority: 10},
		{ID: 90, Name: "[OTHER]-GPT", Weight: 8, Priority: 1},
	}
	// Ints() only accepts JSON-style []any of float64.
	old := M{"exclude_ids": []any{28.0, 81.0, 82.0, 83.0, 85.0, 86.0, 87.0}}
	got := ExcludeChans(old, chans)
	if fmt.Sprint(idsOfChans(got)) != "[2 23 26 32 73 88 89 90]" {
		t.Fatalf("unlisted test-named and off-priority channels must remain, got %v", idsOfChans(got))
	}
	for _, c := range got {
		if c.ID == 88 || c.ID == 89 {
			if !strings.HasPrefix(c.Name, "测试") {
				t.Fatalf("test name must be preserved, not used as a filter: %+v", c)
			}
		}
		if c.ID == 90 && c.Priority != 1 {
			t.Fatalf("priority must be preserved and is not a filter: %+v", c)
		}
	}

	now := M{"exclude_ids": []any{28.0, 81.0, 82.0, 83.0, 85.0, 86.0, 87.0, 88.0, 89.0}}
	got = ExcludeChans(now, chans)
	if fmt.Sprint(idsOfChans(got)) != "[2 23 26 32 73 90]" {
		t.Fatalf("listed 88/89 must drop, others remain, got %v", idsOfChans(got))
	}

	// IDs in exclude_ids but absent from the channel list are ignored.
	got = ExcludeChans(M{"exclude_ids": []any{28.0, 99.0}}, []Channel{{ID: 2, Name: "a", Weight: 20, Priority: 10}})
	if fmt.Sprint(idsOfChans(got)) != "[2]" {
		t.Fatalf("missing exclude id should be ignored, got %v", idsOfChans(got))
	}
	got = ExcludeChans(M{}, chans)
	if fmt.Sprint(idsOfChans(got)) != "[2 23 26 32 73 88 89 90]" {
		t.Fatalf("empty exclude keeps all, got %v", idsOfChans(got))
	}
}
