// Package probe is the probing layer: site probes (probe.py), direct upstream
// tests (upstream.py), recheck/anomaly rules and the streaming classifier.
// It exchanges plain model.Probe structs only. Keys live in memory only and are
// never logged; error texts have the key replaced by "***".
package probe

import (
	"encoding/json"
	"errors"
	"strings"

	m "autoweight/model"
)

// Stream reproduces probe_once's SSE loop. Feed each raw line with its arrival
// time (seconds since request start); Feed reports stop=true on [DONE] /
// response.completed / response.failed. A non-nil error mirrors a Python
// exception raised inside the loop (it ends the probe with err set).
type Stream struct {
	Chunks int
	Events []float64 // arrival times of non-empty text deltas
}

// ErrAttr mirrors Python's AttributeError/TypeError/IndexError when an SSE
// payload has an unexpected shape (e.g. `data: 5`).
var ErrAttr = errors.New("AttributeError: unexpected SSE payload shape")

func (s *Stream) Feed(line string, t float64) (bool, error) {
	if line == "" || !strings.HasPrefix(line, "data:") {
		return false, nil
	}
	d := strings.TrimSpace(line[5:])
	if d == "[DONE]" {
		return true, nil
	}
	var raw any
	if err := json.Unmarshal([]byte(d), &raw); err != nil {
		return false, nil
	}
	j, ok := raw.(map[string]any)
	if !ok {
		return false, ErrAttr // j.get on a non-dict
	}
	s.Chunks++
	var delta any
	hasDelta := false
	if typ, _ := j["type"].(string); typ == "response.output_text.delta" {
		delta, hasDelta = j["delta"], true
	}
	if (!hasDelta || delta == nil) && hasKey(j, "choices") {
		ch := j["choices"]
		if !Truthy(ch) {
			ch = []any{map[string]any{}}
		}
		arr, ok := ch.([]any)
		if !ok || len(arr) == 0 {
			if mp, ok2 := ch.(map[string]any); ok2 {
				// dict[0] → KeyError in Python
				_ = mp
			}
			return false, ErrAttr
		}
		c0, ok := arr[0].(map[string]any)
		if !ok {
			return false, ErrAttr
		}
		dd := c0["delta"]
		if !Truthy(dd) {
			dd = map[string]any{}
		}
		dm, ok := dd.(map[string]any)
		if !ok {
			return false, ErrAttr
		}
		delta = dm["content"]
	}
	if Truthy(delta) {
		s.Events = append(s.Events, t)
	}
	if typ, _ := j["type"].(string); typ == "response.completed" || typ == "response.failed" {
		return true, nil
	}
	return false, nil
}

func hasKey(j map[string]any, k string) bool { _, ok := j[k]; return ok }

// Truthy is Python truthiness for decoded JSON values.
func Truthy(v any) bool {
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

// Streaming is the true-streaming classifier: ≥20 text deltas spread ≥1s →
// true, <0.3s → false (whole answer buffered then flushed), else unknown (nil).
func Streaming(events []float64) *bool {
	if len(events) < 20 {
		return nil
	}
	spread := events[len(events)-1] - events[0]
	if spread >= 1.0 {
		return m.B(true)
	}
	if spread < 0.3 {
		return m.B(false)
	}
	return nil
}

// Finish builds the site-probe conclusion exactly like probe_once's return.
// errText nil = no error; key (may be "") is redacted from errText.
func (s *Stream) Finish(status *int, errText *string, total float64, key string) m.Probe {
	var first *float64
	if len(s.Events) > 0 {
		first = &s.Events[0]
	}
	p := m.Probe{OK: first != nil, Status: status, Total: m.F(m.Round(total, 2)),
		Deltas: m.I(len(s.Events)), Streaming: Streaming(s.Events)}
	if first != nil && *first != 0 { // Python: round(first,2) if first else None
		p.Ttft = m.F(m.Round(*first, 2))
	}
	if errText != nil {
		e := *errText
		if e != "" && key != "" && strings.Contains(e, key) {
			e = strings.ReplaceAll(e, key, "***")
		}
		p.Err = e
	}
	return p
}

// Runes truncates to n characters like Python s[:n].
func Runes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
