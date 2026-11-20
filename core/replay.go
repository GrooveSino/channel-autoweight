package core

import (
	"fmt"

	m "autoweight/model"
)

// Replay is a Prober that returns recorded results (snapshots / parity).
type Replay struct {
	Probes   m.Probes
	Reused   bool
	Tests    map[int][]m.Probe
	BottomUp m.Probes
}

func (r Replay) Site(ids []int) (m.Probes, bool, error) {
	in := map[int]bool{}
	for _, i := range ids {
		in[i] = true
	}
	out := m.Probes{}
	for k, v := range r.Probes {
		if in[k] {
			out[k] = v
		}
	}
	return out, r.Reused, nil
}

func (r Replay) Upstream(cid, n int) ([]m.Probe, error) {
	if v, ok := r.Tests[cid]; ok {
		return v, nil
	}
	if v, ok := r.BottomUp[cid]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("no recorded upstream test for #%d", cid)
}
