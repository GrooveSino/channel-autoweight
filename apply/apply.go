// Package apply is the only write path. It builds ONE transaction that
// updates channels and abilities together (priority pinned to cfg.priority,
// which must be 10 for gpt or 0 for claude) plus any toggle statements, and hands it to a Writer.
// Nothing in this task executes it against production.
package apply

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	m "autoweight/model"
	"autoweight/toggle"
)

// Writer executes one complete transaction script.
type Writer interface {
	ExecTx(sql string) error
}

// Plan is what the orchestrator wants written.
type Plan struct {
	Weights    map[int]int // only changed channels
	Toggles    []m.Action
	EnabledIDs []int
}

// Result reports what went into the transaction.
type Result struct {
	SQL           string
	WeightsDone   bool
	TogglesDone   []m.Action
	TogglesReject []m.Action
}

// BuildSQL validates and renders the single transaction. Empty SQL = nothing to do.
func BuildSQL(cfg m.M, chans []m.Channel, p Plan) (*Result, error) {
	prio := cfg.I("priority", 10)
	if prio < 0 || prio > 100 {
		return nil, fmt.Errorf("priority must be within 0..100 (gpt 10, claude 7), config has %d", prio)
	}
	lo, hi := cfg.I("w_min", 0), cfg.I("w_max", 20)
	known := map[int]bool{}
	for _, c := range chans {
		known[c.ID] = true
	}
	ids := make([]int, 0, len(p.Weights))
	for i, w := range p.Weights {
		if !known[i] {
			return nil, fmt.Errorf("#%d is not an enabled in-scope channel", i)
		}
		if w < lo || w > hi {
			return nil, fmt.Errorf("#%d weight %d outside %d..%d", i, w, lo, hi)
		}
		ids = append(ids, i)
	}
	sort.Ints(ids)
	var b strings.Builder
	res := &Result{}
	if len(ids) > 0 {
		ss := make([]string, len(ids))
		for k, i := range ids {
			fmt.Fprintf(&b, "update channels set weight=%d, priority=%d where id=%d and status=1;\n", p.Weights[i], prio, i)
			ss[k] = fmt.Sprint(i)
		}
		fmt.Fprintf(&b, "update abilities a set weight=c.weight, priority=c.priority from channels c where a.channel_id=c.id and c.id in (%s);\n", strings.Join(ss, ","))
		res.WeightsDone = true
	}
	if len(p.Toggles) > 0 {
		tp := toggle.P(cfg)
		sql, ok, rej := toggle.ToggleSQL(p.Toggles, p.EnabledIDs, prio, tp.I("hard_min", 3), tp.I("min_enabled", 4))
		res.TogglesDone, res.TogglesReject = ok, rej
		if sql != "" {
			b.WriteString(sql + "\n")
		}
	}
	if b.Len() == 0 {
		return res, nil
	}
	res.SQL = "BEGIN;\n" + b.String() + "COMMIT;\n"
	return res, nil
}

// Apply builds and executes the transaction.
func Apply(w Writer, cfg m.M, chans []m.Channel, p Plan) (*Result, error) {
	res, err := BuildSQL(cfg, chans, p)
	if err != nil || res.SQL == "" {
		return res, err
	}
	if err := w.ExecTx(res.SQL); err != nil {
		return nil, err
	}
	return res, nil
}

// ErrNotArmed: production writes need AUTOWEIGHT_ALLOW_WRITE=1 in the env.
var ErrNotArmed = errors.New("write refused: set AUTOWEIGHT_ALLOW_WRITE=1 to allow production writes")

// PsqlWriter runs the script through ssh+docker psql (ON_ERROR_STOP=1 so a
// failing statement aborts the whole transaction).
type PsqlWriter struct {
	Argv []string
	Run  func(ctx context.Context, argv []string, stdin string) (string, error)
}

func (w *PsqlWriter) ExecTx(sql string) error {
	if os.Getenv("AUTOWEIGHT_ALLOW_WRITE") != "1" {
		return ErrNotArmed
	}
	if !strings.HasPrefix(sql, "BEGIN;\n") || !strings.HasSuffix(sql, "COMMIT;\n") {
		return errors.New("refusing script that is not a single transaction")
	}
	if w.Run == nil {
		return errors.New("no runner")
	}
	_, err := w.Run(context.Background(), w.Argv, sql)
	return err
}

// MemWriter records scripts (tests, dry runs).
type MemWriter struct{ Scripts []string }

func (w *MemWriter) ExecTx(sql string) error { w.Scripts = append(w.Scripts, sql); return nil }
