package model

import (
	"fmt"
	"regexp"
)

var pgName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var containerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// PGExec returns a safe docker exec prefix. Overrides affect only this exec process.
func PGExec(container, user, database string) ([]string, error) {
	if !containerName.MatchString(container) {
		return nil, fmt.Errorf("invalid pg_container")
	}
	a := []string{"docker", "exec", "-i"}
	for _, p := range []struct{ k, v string }{{"POSTGRES_USER", user}, {"POSTGRES_DB", database}} {
		if p.v != "" {
			if !pgName.MatchString(p.v) {
				return nil, fmt.Errorf("invalid PG identifier")
			}
			a = append(a, "-e", p.k+"="+p.v)
		}
	}
	return append(a, container), nil
}
