package model

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
)

// M is a JSON object with Python dict.get-style typed accessors. Config is kept
// dynamic on purpose: the prototype's config.json has ~150 knobs read with
// .get(key, default) and this mirrors that exactly (missing key → default).
type M map[string]any

func LoadConfig(path string) (M, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m M
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (m M) Has(k string) bool { _, ok := m[k]; return ok }

// Sub returns a nested object ("(cfg.get(k) or {})").
func (m M) Sub(k string) M {
	if m == nil {
		return M{}
	}
	if v, ok := m[k].(map[string]any); ok {
		return M(v)
	}
	if v, ok := m[k].(M); ok {
		return v
	}
	return M{}
}

// F: m.get(k, d) as float; JSON null → d.
func (m M) F(k string, d float64) float64 {
	if p := m.FP(k); p != nil {
		return *p
	}
	return d
}

// FP: nullable float.
func (m M) FP(k string) *float64 {
	switch v := m[k].(type) {
	case float64:
		return &v
	case int:
		f := float64(v)
		return &f
	case bool:
		if v {
			return F(1)
		}
		return F(0)
	}
	return nil
}

func (m M) I(k string, d int) int { return int(m.F(k, float64(d))) }

// B: truthiness of m.get(k, d).
func (m M) B(k string, d bool) bool {
	v, ok := m[k]
	if !ok {
		return d
	}
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

func (m M) S(k, d string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return d
}

func (m M) Ints(k string) []int {
	a, _ := m[k].([]any)
	out := []int{}
	for _, x := range a {
		if f, ok := x.(float64); ok {
			out = append(out, int(f))
		}
	}
	return out
}

// Public returns keys not starting with "_" (Python's {k: v ... if not k.startswith("_")}).
func (m M) Public() M {
	o := M{}
	for k, v := range m {
		if !strings.HasPrefix(k, "_") {
			o[k] = v
		}
	}
	return o
}

// With returns a shallow copy with overrides (dict(cfg, **kw)).
func (m M) With(kv ...any) M {
	o := M{}
	for k, v := range m {
		o[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		o[kv[i].(string)] = kv[i+1]
	}
	return o
}

// Merge overlays b's public keys on a copy of defaults (p = dict(DEFAULT); p.update(...)).
func Merge(defaults M, b M) M {
	o := M{}
	for k, v := range defaults {
		o[k] = v
	}
	for k, v := range b.Public() {
		o[k] = v
	}
	return o
}

// WindowNames returns ("60m","10m") from windows_min.
func WindowNames(cfg M) (string, string) {
	w := cfg.Ints("windows_min")
	if len(w) < 2 {
		return "60m", "10m"
	}
	return strconv.Itoa(w[0]) + "m", strconv.Itoa(w[1]) + "m"
}

// Keys returns the map keys sorted (callers only use it where order is irrelevant).
func (m M) Keys() []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ExcludeChans drops cfg exclude_ids (v4.66, e.g. retired #28); ids missing from chans are ignored.
func ExcludeChans(cfg M, chans []Channel) []Channel {
	ex := map[int]bool{}
	for _, i := range cfg.Ints("exclude_ids") {
		ex[i] = true
	}
	out := chans[:0:0]
	for _, c := range chans {
		if !ex[c.ID] {
			out = append(out, c)
		}
	}
	return out
}
