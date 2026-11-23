package main

import (
	"autoweight/fetch"
	"autoweight/firstpass"
	m "autoweight/model"
	"autoweight/regress"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// The review path deliberately has no live source, history appender or writer.
func reviewAlgorithm(name, dir string, compare bool, out io.Writer, scales ...int) error {
	if name != "first-pass-v1" && name != "legacy-v501" {
		return fmt.Errorf("unknown algorithm %q", name)
	}
	if dir == "" {
		return fmt.Errorf("review requires -snap with firstpass.json")
	}
	f, err := os.Open(filepath.Join(dir, "firstpass.json"))
	if err != nil {
		return err
	}
	defer f.Close()
	var in firstpass.Input
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err = dec.Decode(&in); err != nil {
		return err
	}
	if dec.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing snapshot data")
	}
	snap, err := fetch.LoadSnapshot(dir)
	if err != nil {
		return err
	}
	if snap.Now != in.Cutoff || len(snap.Channels) != len(in.Current) {
		return fmt.Errorf("snapshot scope/time mismatch")
	}
	seen := map[int]bool{}
	for _, c := range snap.Channels {
		if w, ok := in.Current[c.ID]; !ok || seen[c.ID] || w != c.Weight {
			return fmt.Errorf("snapshot preimage mismatch")
		}
		seen[c.ID] = true
	}
	next, err := firstpass.Evaluate(in)
	if err != nil {
		return err
	}
	result := map[string]any{"first-pass-v1": next, "schema": "strength-v1", "mode": "read-only"}
	var oldRows map[int]*firstpass.Row
	if compare {
		_, old, err := regress.EvalSnapshot(snap)
		if err != nil {
			return err
		}
		cfg, err := m.LoadConfig(snap.Path(fetch.ConfigFile))
		if err != nil {
			return err
		}
		legacy, err := normalized(old.Decision.Target, old.Old, cfg.I("w_max", 20), old.Now)
		if err != nil {
			return err
		}
		oldRows = legacy.Channels
		result["legacy-v501"] = legacy
		diff := map[int]*firstpass.Milli{}
		for id, row := range next.Channels {
			v, ok := oldRows[id]
			if !ok {
				return fmt.Errorf("legacy candidate mismatch")
			}
			diff[id] = nil
			if row.Strength != nil {
				d := *row.Strength - *v.Strength
				diff[id] = &d
			}
		}
		result["target_delta"] = diff
	}
	if len(scales) > 0 && scales[0] > 0 {
		scale := scales[0]
		p := preview(next.Channels, in, scale)
		previews := map[string]any{"first-pass-v1": p}
		if oldRows != nil {
			previews["legacy-v501"] = preview(oldRows, in, scale)
		}
		result["preview"] = previews
	}
	return json.NewEncoder(out).Encode(result)
}

func normalized(weights, before map[int]int, full int, now int64) (firstpass.Result, error) {
	r := firstpass.Result{Algorithm: "legacy-v501", Cutoff: now, Channels: map[int]*firstpass.Row{}}
	for id, w := range weights {
		if full <= 0 || w < 0 || w > full {
			return r, fmt.Errorf("invalid full scale or weight #%d", id)
		}
		v := firstpass.Ratio(float64(w) / float64(full))
		status := "target"
		if before[id] == w {
			status = "hold"
		}
		r.Channels[id] = &firstpass.Row{Old: before[id], Strength: &v, Status: status}
	}
	return r, nil
}
func preview(rows map[int]*firstpass.Row, in firstpass.Input, scale int) map[string]any {
	weights := map[int]int{}
	raw := map[int]int{}
	for id, r := range rows {
		if r.Strength == nil {
			weights[id] = in.Current[id]
			continue
		}
		raw[id] = r.Strength.Weight(scale)
		weights[id] = min(raw[id], in.Caps[id])
	}
	shares := map[string]any{}
	for _, s := range in.Strata {
		p, err := firstpass.Shares(weights, s.Pool, s.Path)
		if err != nil {
			shares[s.Name] = nil
			continue
		}
		shares[s.Name] = p
	}
	return map[string]any{"scale": scale, "integer_weights": raw, "weights_after_original_caps": weights, "pool_shares_after_caps": shares}
}
