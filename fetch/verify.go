package fetch

import "fmt"

// Check is one in-scope channel as stored, with its abilities consistency.
type Check struct {
	ID, Status, Weight, Priority int
	Name                         string
	Abilities, Mismatch, Enabled int
	// WantPriority is the configured priority (gpt 10, claude 7); nil = 10.
	WantPriority *int
}

// OK: abilities follow channels (weight, priority) and priority is the
// configured one (10 unless WantPriority says otherwise).
func (c Check) OK() bool {
	want := 10
	if c.WantPriority != nil {
		want = *c.WantPriority
	}
	return c.Mismatch == 0 && c.Priority == want && c.Abilities > 0
}

// VerifySQL is the read-only post-write check (channels vs abilities).
func (p *Psql) VerifySQL() string {
	return `select c.id, c.name, c.status, c.weight, c.priority, count(*),
 sum(case when a.weight is distinct from c.weight or a.priority is distinct from c.priority then 1 else 0 end),
 sum(case when a.enabled then 1 else 0 end)
 from channels c join abilities a on a.channel_id=c.id where ` + p.scope() + `
 group by c.id, c.name, c.status, c.weight, c.priority order by c.id;`
}

// Verify runs VerifySQL inside BEGIN READ ONLY ... ROLLBACK.
func (p *Psql) Verify() ([]Check, error) {
	rs, err := p.RO(p.VerifySQL())
	if err != nil {
		return nil, err
	}
	cs, err := ParseChecks(rs)
	if err != nil {
		return nil, err
	}
	want := p.Priority
	for i := range cs {
		cs[i].WantPriority = &want
	}
	return cs, nil
}

// ParseChecks decodes VerifySQL rows.
func ParseChecks(rs [][]string) ([]Check, error) {
	out := []Check{}
	for _, r := range rs {
		if len(r) < 8 {
			return nil, fmt.Errorf("bad verify row")
		}
		out = append(out, Check{ID: atoi(r[0]), Name: r[1], Status: atoi(r[2]), Weight: atoi(r[3]), Priority: atoi(r[4]),
			Abilities: atoi(r[5]), Mismatch: atoi(r[6]), Enabled: atoi(r[7])})
	}
	return out, nil
}
