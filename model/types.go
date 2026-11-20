package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// Channel is an enabled (or disabled) gpt channel as read from the DB.
type Channel struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Weight   int    `json:"weight"`
	Priority int    `json:"priority"`
	Status   int    `json:"status,omitempty"`
}

// Row is one consume(type=2)/error(type=5) log row, same keys as db.logs().
type Row struct {
	Ch     int      `json:"ch"`
	T      int64    `json:"t"`
	Type   int      `json:"type"`
	Ut     int      `json:"ut"`
	Ct     int      `json:"ct"`
	Tok    int      `json:"tok"`
	Frt    *float64 `json:"frt,omitempty"`
	Span   *float64 `json:"span,omitempty"`
	Chunks *int     `json:"chunks,omitempty"`
	Sc     *int     `json:"sc,omitempty"`
	Sb     int      `json:"sb,omitempty"`  // v3.11 session-block 403 flag
	Cs     int      `json:"cs,omitempty"`  // upstream content-safety rejection flag
	Mdl    string   `json:"m,omitempty"`   // model_name (TPS stratification)
	St     int      `json:"st,omitempty"`  // is_stream: 1 stream, 2 non-stream, 0 unknown
	Aff    int      `json:"aff,omitempty"` // 1: routed by a channel-affinity (sticky session) hit, not by weight
	Ue     int      `json:"ue,omitempty"`  // 1: empty success whose request had prompt_tokens=0 (client-side empty request)
	Uf     int      `json:"uf,omitempty"`  // 1: 400 (or [ReverixAI], no status code; v5.01) error saying the upstream does not support a request feature
	Mm     int      `json:"mm,omitempty"`  // 1: 404 "model ... is not available for this group" (model not served by the channel)
	Ec     int      `json:"ec,omitempty"`  // 1: 400 (or [ReverixAI]; v5.01) "encrypted content" / "Encrypted context cannot be read" error (reasoning blob minted by another upstream replayed here)
	Bz     int      `json:"bz,omitempty"`  // 1: upstream "servers are currently busy"/overloaded error (capacity, often after a long wait)
	Df     int      `json:"df,omitempty"`  // 1: "do request failed" / v5.01 "Model request could not be transmitted" (the upstream call could not be made)
	// v5.00 content classes
	Cl  int `json:"cl,omitempty"`  // 1: client-side failure (adaptive thinking required, client connection closed, invalid signature, unsupported parameter ...)
	Nf  int `json:"nf,omitempty"`  // 1: 404 model not found (client error only when concentrated on one token)
	To  int `json:"to,omitempty"`  // 1: timeout (408/504/524 or "timed out")
	Cap int `json:"cap,omitempty"` // 1: capacity (429 / "Concurrency limit exceeded"); never discounted
}

// Probe is a site probe or an upstream direct test result.
type Probe struct {
	OK        bool     `json:"ok"`
	Status    *int     `json:"status"`
	Ttft      *float64 `json:"ttft"`
	Total     *float64 `json:"total"`
	Deltas    *int     `json:"deltas"`
	Streaming *bool    `json:"streaming,omitempty"`
	Span      *float64 `json:"span,omitempty"`
	Err       any      `json:"err"`
	Recheck   bool     `json:"recheck,omitempty"`
	Source    string   `json:"source,omitempty"`
	Route     string   `json:"route,omitempty"` // v3.01: "site" | "direct" (remote upstream probe)
}

type Probes map[int][]Probe

// Window is features.window() output (plus fields added by build()).
type Window struct {
	ErrTimeout *int     `json:"err_timeout"`
	SlowRateT  *float64 `json:"slow_rate_t"`
	EbSpan     *float64 `json:"eb_span"`
	EbOkAfter  *int     `json:"eb_ok_after"`
	ErrClient  *int     `json:"err_client"`
	LtN        int      `json:"lt_n"`
	Lt50       *float64 `json:"lt50"`
	Lt90       *float64 `json:"lt90"`
	LsN        int      `json:"ls_n"`
	LsBuf      *float64 `json:"ls_buf"`
	N          int      `json:"n"`
	Err        int      `json:"err"`
	ErrRate    *float64 `json:"err_rate"`
	Empty      *int     `json:"empty"`
	EmptyRate  *float64 `json:"empty_rate"`
	P50        *float64 `json:"p50"`
	P90        *float64 `json:"p90"`
	SlowRate   *float64 `json:"slow_rate"`
	AvgConc    float64  `json:"avg_conc"`
	PeakConc   int      `json:"peak_conc"`
	CtokP50    *float64 `json:"ctok_p50"`
	Nn         int      `json:"nn"`
	Np50       *float64 `json:"np50"`
	Np90       *float64 `json:"np90"`
	MrN        *int     `json:"mr_n,omitempty"`
	Mr50       *float64 `json:"mr50,omitempty"`
	Mr90       *float64 `json:"mr90,omitempty"`
	ErrUser    *int     `json:"err_user"`
	NRaw       *int     `json:"n_raw,omitempty"`
	ErrRaw     *int     `json:"err_raw,omitempty"`
	ErrRateRaw *float64 `json:"err_rate_raw,omitempty"`
	ErrPreRec  *int     `json:"err_pre_rec,omitempty"`
	NGateRaw   *int     `json:"n_gate_raw,omitempty"`
}

// Mix is traffic_mix() (mix_60m).
type Mix struct {
	NUser       int      `json:"n_user"`
	NProbe      int      `json:"n_probe"`
	Tokens      int      `json:"tokens"`
	NErrReal    int      `json:"n_err_real"`    // v2.88
	ErrRateReal *float64 `json:"err_rate_real"` // v2.88 (null when no user rows)
	TopTokShare *float64 `json:"top_tok_share"`
	// HasErrRateReal: the key was present (legacy records before v2.88 lack it).
	HasErrRateReal bool `json:"-"`
}

// UnmarshalJSON records whether err_rate_real was present (v2.88 legacy rule).
func (x *Mix) UnmarshalJSON(b []byte) error {
	type plain Mix
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if err := json.Unmarshal(b, (*plain)(x)); err != nil {
		return err
	}
	_, x.HasErrRateReal = raw["err_rate_real"]
	return nil
}

// Feat is one channel's features: {"60m": w, "10m": w, "mix_60m": mix}.
type Feat struct {
	Long  *Window   `json:"60m"`
	Short *Window   `json:"10m"`
	Mix   *Mix      `json:"mix_60m"`
	Tps   *Tps      `json:"tps,omitempty"`
	Chan  *ChanStat `json:"chan_60m,omitempty"`
	// v5.01 same-token paired latency: decision window and reference window.
	Pair    *PairStat `json:"pair,omitempty"`
	PairRef *PairStat `json:"pair_ref,omitempty"`
}

// PairStat (v5.01) is a channel's same-token latency ratio against the other
// channels (features.PairOf): >1 = slower for the same users, model and output length.
type PairStat struct {
	Ratio  float64 `json:"ratio"`
	Tokens int     `json:"tokens"` // paired tokens
	N      int     `json:"n"`      // this channel's rows in paired cells
	Min    int     `json:"min"`    // window minutes
}

// ChanStat is the single 1h error measure (v4.98) used by the settle rules, the
// report table and the TOKENBAN/RETRYCOST flags: real rows only (probe tokens
// dropped), client errors (4xx except 408/429) and cross-channel "do request
// failed" errors excluded. Eff caps a dominant token's non-capacity errors (one
// blocked token is not a channel outage; 429/timeouts always count).
type ChanStat struct {
	N      int     `json:"n"`      // real requests minus excluded client errors
	Err    int     `json:"err"`    // channel errors (5xx/timeout/429/unknown)
	Client int     `json:"client"` // excluded client / cross-channel errors
	Eff    int     `json:"eff"`    // Err with a dominant token capped at tok_cap
	Rate   float64 `json:"rate"`   // Eff/(N-Err+Eff)
	ErrTok int     `json:"err_tok"`
	Toks   int     `json:"toks"`
	TopTok int     `json:"top_tok,omitempty"`
	TopErr int     `json:"top_err,omitempty"`
}

// Tps is the per-channel end-to-end output speed (completion_tokens/(use_time+0.5)),
// real streaming successes only. Idx = median of per-request tps / same-model pooled median.
type Tps struct {
	N   int     `json:"n"`
	P25 float64 `json:"p25"`
	P50 float64 `json:"p50"`
	Idx float64 `json:"idx"`
}

func (f *Feat) Win(name string) *Window {
	if f == nil {
		return nil
	}
	switch name {
	case "60m":
		return f.Long
	case "10m":
		return f.Short
	}
	return nil
}

type Feats map[int]*Feat

// OrderedScores keeps Python dict insertion order (ties in min()/sorted()
// resolve by that order, so it matters for parity).
type OrderedScores struct {
	Keys []int
	Vals map[int]*float64
}

func NewOrderedScores() *OrderedScores { return &OrderedScores{Vals: map[int]*float64{}} }

func (o *OrderedScores) Set(k int, v *float64) {
	if _, ok := o.Vals[k]; !ok {
		o.Keys = append(o.Keys, k)
	}
	o.Vals[k] = v
}

// UnmarshalJSON reads {"id": {"score": x, ...}} or {"id": x} keeping key order.
func (o *OrderedScores) UnmarshalJSON(b []byte) error {
	o.Vals = map[int]*float64{}
	o.Keys = nil
	dec := json.NewDecoder(bytes.NewReader(b))
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if t == nil {
		return nil
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("scores: want object")
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		k, err := strconv.Atoi(kt.(string))
		if err != nil {
			return err
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		var v *float64
		var obj struct {
			Score *float64 `json:"score"`
		}
		if len(raw) > 0 && raw[0] == '{' {
			if err := json.Unmarshal(raw, &obj); err != nil {
				return err
			}
			v = obj.Score
		} else {
			_ = json.Unmarshal(raw, &v)
		}
		o.Set(k, v)
	}
	return nil
}

func (o *OrderedScores) MarshalJSON() ([]byte, error) {
	m := map[string]*float64{}
	for _, k := range o.Keys {
		m[strconv.Itoa(k)] = o.Vals[k]
	}
	return json.Marshal(m)
}

// SortedIDs returns sorted keys of an int-keyed map.
func SortedIDs[V any](m map[int]V) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
