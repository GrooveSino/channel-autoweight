// Package weightonly models the external adapter contract, not a database writer.
package weightonly

import (
	"errors"
	"maps"
	"sync"
)

type Key struct {
	Channel      int
	Group, Model string
	Priority     int
}
type Snapshot struct {
	Epoch     uint64
	Channels  map[int]int
	Abilities map[Key]int
}
type Proposal struct {
	ID, Algorithm string
	Before        Snapshot
	Targets       map[int]int
}
type Scope struct {
	Caps   map[int]int
	Keys   map[Key]bool
	Frozen map[int]bool
}

func same(a, b Snapshot) bool {
	return a.Epoch == b.Epoch && maps.Equal(a.Channels, b.Channels) && maps.Equal(a.Abilities, b.Abilities)
}
func validate(s Scope, x Snapshot, t map[int]int) error {
	if len(s.Caps) != 8 || len(s.Keys) != 71 || len(x.Channels) != 8 || len(x.Abilities) != 71 || len(t) != 8 {
		return errors.New("scope drift")
	}
	for id, c := range s.Caps {
		w, ok := x.Channels[id]
		v, p := t[id]
		if !ok || !p || c < 0 || c > 20 || w < 0 || w > 20 || v < 0 || v > c || s.Frozen[id] && v != w {
			return errors.New("channel constraint")
		}
	}
	for k, enabled := range s.Keys {
		w, ok := x.Abilities[k]
		cw, exists := x.Channels[k.Channel]
		if !enabled || !ok || !exists || w != cw || k.Group != "default" || k.Model == "" {
			return errors.New("ability drift")
		}
	}
	return nil
}

// Model is an in-memory acceptance harness. Real locking, journaling, CAS and
// cache generation checks must be supplied by the separately authorized adapter.
type Model struct {
	mu            sync.Mutex
	pending       string
	done          map[string]bool
	before, after Snapshot
}

func clone(x Snapshot) Snapshot {
	return Snapshot{x.Epoch, maps.Clone(x.Channels), maps.Clone(x.Abilities)}
}
func (m *Model) Prepare(s Scope, x Snapshot, p Proposal) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending != "" || p.ID == "" || m.done[p.ID] || !same(x, p.Before) || (p.Algorithm != "legacy-v501" && p.Algorithm != "first-pass-v1") {
		return Snapshot{}, errors.New("blocked, replay or preimage mismatch")
	}
	if err := validate(s, x, p.Targets); err != nil {
		return Snapshot{}, err
	}
	m.pending = p.ID
	m.before = clone(x)
	m.after = clone(x)
	m.after.Epoch++
	for i, v := range p.Targets {
		m.after.Channels[i] = v
	}
	for k := range m.after.Abilities {
		m.after.Abilities[k] = p.Targets[k.Channel]
	}
	return clone(m.after), nil
}
func (m *Model) Verify(id string, x Snapshot, cacheEpoch uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" || id != m.pending || !same(x, m.after) || cacheEpoch != x.Epoch {
		return errors.New("unknown or not effective; latch retained")
	}
	if m.done == nil {
		m.done = map[string]bool{}
	}
	m.done[id] = true
	m.pending = ""
	return nil
}
func (m *Model) Rollback(s Scope, id string, current Snapshot) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" || m.pending != id || !same(current, m.after) {
		return Snapshot{}, errors.New("rollback conflict")
	}
	if err := validate(s, current, m.before.Channels); err != nil {
		return Snapshot{}, err
	}
	next := clone(m.before)
	next.Epoch = current.Epoch + 1
	m.after = next
	return clone(next), nil
}
