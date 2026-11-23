package firstpass

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"testing"
)

func fixture(t *testing.T) Input {
	t.Helper()
	var in Input
	b, err := os.ReadFile("../testdata/paired/firstpass.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &in); err != nil {
		t.Fatal(err)
	}
	return in
}
func TestDecisionContract(t *testing.T) {
	for _, v := range []struct {
		name   string
		edit   func(*Input)
		want   int
		status string
	}{
		{"slow without errors", func(*Input) {}, 0, "target"},
		{"missing SLO", func(i *Input) { i.Strata[0].DeadlineMS = 0 }, 20, "abstain"},
		{"fixed operator", func(i *Input) { i.Fixed = []int{102} }, 20, "excluded"},
		{"cap wins", func(i *Input) { i.Caps[101] = 2; i.Events = i.Events[:1]; i.Caps[102] = 2 }, 100, "operator-cap"},
		{"sparse", func(i *Input) { i.Events = i.Events[:129] }, 20, "abstain"},
		{"concentration", func(i *Input) {
			for n := range i.Events {
				i.Events[n].User = "one"
			}
		}, 20, "abstain"},
		{"unknown route", func(i *Input) { i.Strata[0].Path = "unknown" }, 20, "abstain"},
		{"expiry", func(i *Input) { i.Cutoff += 3600 }, 20, "abstain"},
		{"late data", func(i *Input) {
			for n := range i.Events {
				i.Events[n].Available = i.Cutoff + 1
			}
		}, 20, "abstain"},
		{"missing usage-independent time", func(i *Input) {
			for n := range i.Events {
				i.Events[n].TotalMS = nil
			}
		}, 20, "abstain"},
		{"model reversal", func(i *Input) {
			s := i.Strata[0]
			s.Name = "reverse"
			i.Strata = append(i.Strata, s)
			for _, e := range i.Events {
				e.ID += "r"
				e.Stratum = s.Name
				e.Channel = 203 - e.Channel
				i.Events = append(i.Events, e)
			}
		}, 20, "abstain"},
	} {
		t.Run(v.name, func(t *testing.T) {
			in := fixture(t)
			v.edit(&in)
			r, err := Evaluate(in)
			if err != nil {
				t.Fatal(err)
			}
			a := r.Channels[102]
			unknown := v.status == "abstain" || v.status == "excluded"
			if (a.Strength == nil) != unknown || a.Strength != nil && int(*a.Strength) != v.want || a.Status != v.status {
				t.Fatalf("%+v", a)
			}
		})
	}
}
func TestOutcomes(t *testing.T) {
	for _, v := range []struct {
		s       string
		score   float64
		unknown int
	}{{"tool", 1, 0}, {"ok", 1, 0}, {"empty", 0, 0}, {"truncated", 0, 0}, {"upstream", 0, 0}, {"rate_limit", 0, 0}, {"timeout", 0, 0}, {"unknown", 0, 128}, {"cancel", 0, 128}} {
		t.Run(v.s, func(t *testing.T) {
			in := fixture(t)
			for n := range in.Events {
				if in.Events[n].Channel == 101 {
					in.Events[n].Outcome = v.s
				}
			}
			r, err := Evaluate(in)
			if err != nil {
				t.Fatal(err)
			}
			a := r.Channels[101]
			if a.Score == nil || *a.Score != v.score || a.Unknown != v.unknown {
				t.Fatal(a)
			}
		})
	}
}
func TestDuplicateAndClock(t *testing.T) {
	in := fixture(t)
	a, _ := Evaluate(in)
	e := in.Events[0]
	in.Events = append(in.Events, e)
	for _, route := range []string{"affinity", "specified", "probe"} {
		x := e
		x.Route = route
		in.Events = append(in.Events, x)
	}
	x := e
	x.Attempt = 2
	in.Events = append(in.Events, x)
	in.Cutoff += 600
	b, err := Evaluate(in)
	if err != nil || !reflect.DeepEqual(a.Channels, b.Channels) {
		t.Fatal(b, err)
	}
	e.Outcome = "empty"
	in.Events = append(in.Events, e)
	if _, err := Evaluate(in); err == nil {
		t.Fatal("conflict accepted")
	}
}
func TestSelector(t *testing.T) {
	for _, v := range []struct {
		w    []int
		p    string
		want float64
	}{{[]int{20, 0}, "cache", 0}, {[]int{20, 0}, "database", .225}, {[]int{0, 0}, "cache", .5}, {[]int{0, 0}, "database", .45}, {[]int{0}, "cache", 1}} {
		ws := map[int]int{}
		var ids []int
		for i, w := range v.w {
			ids = append(ids, i)
			ws[i] = w
		}
		p, err := Shares(ws, ids, v.p)
		if err != nil || math.Abs(p[len(ids)-1]-v.want) > 1e-10 {
			t.Fatal(p, err)
		}
	}
}

func TestVisibleTime(t *testing.T) {
	in := fixture(t)
	in.Strata[0].Stream = true
	in.Strata[0].FirstMS = 200
	r, err := Evaluate(in)
	if err != nil || r.Channels[101].Unknown != 128 {
		t.Fatal(r, err)
	}
	for n := range in.Events {
		x := 201
		in.Events[n].VisibleMS = &x
	}
	r, err = Evaluate(in)
	if err != nil || *r.Channels[101].Score != 0 || r.Channels[101].Unknown != 0 {
		t.Fatal(r, err)
	}
}
