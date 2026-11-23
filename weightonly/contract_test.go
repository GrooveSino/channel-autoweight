package weightonly

import (
	"fmt"
	"maps"
	"testing"
)

func TestExecutionContract(t *testing.T) {
	s := Scope{map[int]int{}, map[Key]bool{}, map[int]bool{}}
	x := Snapshot{1, map[int]int{}, map[Key]int{}}
	for n := 0; n < 71; n++ {
		id := n%8 + 101
		k := Key{id, "default", fmt.Sprint("demo-", n), 10}
		s.Caps[id] = 20
		s.Keys[k] = true
		x.Channels[id] = 20
		x.Abilities[k] = 20
	}
	p := Proposal{"round1", "first-pass-v1", clone(x), maps.Clone(x.Channels)}
	p.Targets[102] = 6
	var m Model
	bad := clone(x)
	for k := range bad.Abilities {
		delete(bad.Abilities, k)
		break
	}
	if _, e := m.Prepare(s, bad, p); e == nil {
		t.Fatal("key drift accepted")
	}
	p.Targets[999] = 20
	if _, e := m.Prepare(s, x, p); e == nil {
		t.Fatal("candidate drift accepted")
	}
	delete(p.Targets, 999)
	after, e := m.Prepare(s, x, p)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Prepare(s, x, p); e == nil {
		t.Fatal("unknown outcome replay")
	}
	if m.Verify(p.ID, after, 1) == nil {
		t.Fatal("stale cache")
	}
	manual := clone(after)
	manual.Epoch++
	if _, e = m.Rollback(s, p.ID, manual); e == nil {
		t.Fatal("manual change overwritten")
	}
	rb, e := m.Rollback(s, p.ID, after)
	if e != nil || rb.Channels[102] != 20 || m.Verify(p.ID, rb, rb.Epoch) != nil {
		t.Fatal(rb, e)
	}
	if _, e = m.Prepare(s, x, p); e == nil {
		t.Fatal("duplicate round")
	}
}
