package decide

import (
	"fmt"
	"math"
	"sort"
	"time"

	m "autoweight/model"
	"autoweight/trust"
)

// Row is decision.scores[id].
type Row struct {
	Score         float64              `json:"score"`
	Ttft          *float64             `json:"ttft"`
	Streaming     *bool                `json:"streaming"`
	N60           int                  `json:"n60"`
	ProbeOK       int                  `json:"probe_ok"`
	EligibleRank1 bool                 `json:"eligible_rank1"`
	CmpSub        map[string][]float64 `json:"cmp_sub,omitempty"`
	SlowBurst     *float64             `json:"slow_burst,omitempty"`
	RawScore      *float64             `json:"raw_score,omitempty"`
	Trust         *float64             `json:"trust,omitempty"`
	NewcomerN     *int                 `json:"newcomer_n,omitempty"`
	Newcomer      *int                 `json:"newcomer,omitempty"`
	SmallSample   *int                 `json:"small_sample,omitempty"`
	CapacityCap   *float64             `json:"capacity_cap,omitempty"`
	ExploreFloor  *int                 `json:"explore_floor,omitempty"`
}

// Decision is decide()'s output.
type Decision struct {
	Strategy string       `json:"strategy"`
	Order    []int        `json:"order"`
	Scores   map[int]*Row `json:"scores"`
	Target   map[int]int  `json:"target"`
	Notes    []string     `json:"notes"`
	Caps     map[int]*Cap `json:"caps,omitempty"` // Go-only, for reports
}

// Input bundles decide() arguments. Prev = DB weights (hysteresis/band),
// Trust = trust map (nil when trust disabled), Newcomers = newcomer_ids().
type Input struct {
	Cfg       m.M
	Chans     []m.Channel
	Feats     m.Feats
	Probes    m.Probes
	Strategy  string
	Prev      map[int]int
	Trust     map[int]float64
	TrustIDs  []int // iteration order of Trust (Python dict order)
	Newcomers map[int]bool
	Now       time.Time // replaces datetime.now() in active_caps
}

// Guard rejects config features that are not ported (fail closed).
func Guard(cfg m.M) error {
	if cfg.Sub("score").Sub("buffer_prob").B("enabled", false) {
		return fmt.Errorf("score.buffer_prob.enabled not ported")
	}
	if cfg.Sub("recovery").B("enabled", false) {
		return fmt.Errorf("recovery.enabled not ported")
	}
	if cfg.Sub("score_smooth").B("enabled", false) {
		return fmt.Errorf("score_smooth.enabled not ported")
	}
	if a, b := m.WindowNames(cfg); a != "60m" || b != "10m" {
		return fmt.Errorf("windows_min must be [60,10], got %s,%s", a, b)
	}
	return nil
}

// Decide ports decide.decide.
func Decide(in Input) (*Decision, error) {
	cfg := in.Cfg
	if err := Guard(cfg); err != nil {
		return nil, err
	}
	name := in.Strategy
	if name == "" {
		name = cfg.S("strategy", "rank")
	}
	wl, ws := m.WindowNames(cfg)
	ids := make([]int, len(in.Chans))
	for k, c := range in.Chans {
		ids[k] = c.ID
	}
	fs := in.Feats
	priors := map[string]Pair{}
	for _, w := range []string{wl, ws} { // v2.82: third item is the pooled empty rate
		pr := Pooled(fs, ids, w)
		pr.Empty = PooledEmpty(fs, ids, w)
		priors[w] = pr
	}
	cs := map[string]map[int]Pair{wl: CmpSubs(fs, ids, wl, cfg.Sub("score")), ws: CmpSubs(fs, ids, ws, cfg.Sub("score"))}
	notes := []string{}
	pc := ProbeCalibRatio(cfg, fs, ids, in.Probes)
	if pc != nil {
		notes = append(notes, fmt.Sprintf("首字探测校准：样本充足渠道日志/探测首字比中位 %.2f，日志首字不足的渠道探测首字×%.2f", *pc, *pc))
	}
	lp := LtPool(cfg, fs, ids) // v2.86 thin_pool_k
	rows := map[int]*Row{}
	for _, i := range ids {
		subs := map[string]*Pair{}
		cmp := map[string][]float64{}
		for _, w := range []string{wl, ws} {
			if p, ok := cs[w][i]; ok {
				pp := p
				subs[w] = &pp
				cmp[w] = []float64{m.Round(*p.P50, 1), m.Round(*p.P90, 1)}
			}
		}
		sc, ttft, st := Score(cfg, fs[i], in.Probes[i], priors, subs, pc, lp)
		n60 := fs[i].Long.N
		pok := 0
		for _, p := range in.Probes[i] {
			if p.OK {
				pok++
			}
		}
		r := &Row{Score: sc, Ttft: ttft, Streaming: st, N60: n60, ProbeOK: pok,
			EligibleRank1: !(float64(n60) < cfg.F("min_n_rank1", 0) && pok < 1)}
		if len(cmp) > 0 {
			r.CmpSub = cmp
		}
		if bf, why := SlowBurstFactor(fs, ids, i, cfg); why != "" {
			r.SlowBurst = m.F(m.Round(bf, 3))
			r.Score = m.Round(sc*bf, 4)
			notes = append(notes, fmt.Sprintf("#%d 突发变慢：%s，分数×%.2f", i, why, bf))
		}
		rows[i] = r
	}
	if len(in.Trust) > 0 && cfg.Sub("trust").B("enabled", false) {
		tp := trust.Params(cfg)
		for _, i := range ids {
			if t, ok := in.Trust[i]; ok {
				r := rows[i]
				r.RawScore, r.Trust = m.F(r.Score), m.F(t)
				r.Score = m.Round(r.Score*trust.Multiplier(t, tp), 4)
			}
		}
	}
	sc := map[int]float64{}
	for _, i := range ids {
		sc[i] = rows[i].Score
	}
	order := append([]int(nil), ids...)
	sort.SliceStable(order, func(a, b int) bool { return -sc[order[a]] < -sc[order[b]] })
	hy := cfg.Sub("strategies").Sub(name).F("hysteresis", 0)
	prevOrder := sortedPrevKeys(in.Chans, in.Prev)
	var held bool
	var hid int
	order, hid, held = HoldTop(order, sc, in.Prev, prevOrder, hy)
	if held {
		notes = append(notes, fmt.Sprintf("迟滞：#%d 分数在第一名 %.0f%% 以内，保留第一", hid, hy*100))
	}
	if !rows[order[0]].EligibleRank1 {
		for j, i := range order {
			if rows[i].EligibleRank1 {
				notes = append(notes, fmt.Sprintf("#%d 样本不足，不得排第一，由 #%d 顶上", order[0], i))
				o := append([]int{i}, order[:j]...)
				order = append(o, order[j+1:]...)
				break
			}
		}
	}
	caps, err := ActiveCaps(cfg, in.Now)
	if err != nil {
		return nil, err
	}
	applyTrustCap(cfg, caps, rows, in.Trust, in.TrustIDs)
	applyNewcomer(cfg, caps, rows, ids, fs, in.Newcomers)
	prm, ltTot, ltMax := EffectiveRankParams(cfg, name, n60s(rows))
	if ltTot >= 0 {
		notes = append(notes, fmt.Sprintf("低流量档位：1h 总样本 %d < %v，close_ratio=%v", ltTot, ltMax, prm["close_ratio"]))
	}
	rp := RankParams{P: prm}
	applyCapacity(cfg, caps, rows, ids, order, fs, sc, name, rp, in.Prev)
	if name == "rank" && prm.F("lead_trust", 0) > 0 && len(in.Trust) > 0 {
		rp.Trust = in.Trust
	}
	if name == "rank" && prm.F("lead_min_n", 0) > 0 {
		rp.NUser = map[int]int{}
		for _, i := range ids {
			rp.NUser[i] = rows[i].N60
			if p := nUser(fs[i]); p != nil {
				rp.NUser[i] = *p
			}
		}
	}
	cm := capMap(caps)
	tgt := RunStrategy(name, order, sc, rp, cfg, in.Prev, cm)
	if len(rp.NUser) > 0 {
		l := prm.F("lead_min_n", 0)
		if v, ok := rp.NUser[order[0]]; ok && float64(v) < l {
			notes = append(notes, fmt.Sprintf("小样本领跑参照：第一名 #%d 1h 真实请求 %d < %v，样本充足渠道按其中最高分定档", order[0], v, l))
		}
	}
	capped := map[int]bool{}
	cset := m.NewPySet() // Python iterates the capped set in hash order
	for _, i := range order {
		if c, ok := caps[i]; ok && tgt[i] > c.Cap {
			capped[i] = true
			cset.Add(i)
		}
	}
	for _, i := range cset.Items() {
		c := caps[i]
		notes = append(notes, fmt.Sprintf("#%d 软上限 %v（%s），目标 %.1f 被截断，已标记质量风险", i, c.Cap, c.Reason, tgt[i]))
	}
	var rest []int
	for _, i := range order {
		if !capped[i] {
			rest = append(rest, i)
		}
	}
	if len(capped) > 0 && len(rest) > 0 {
		for k, v := range RunStrategy(name, rest, sc, rp, cfg, in.Prev, cm) {
			tgt[k] = v
		}
		for range order {
			mset := m.NewPySet()
			for _, i := range rest {
				if c, ok := caps[i]; ok && tgt[i] > c.Cap {
					mset.Add(i)
				}
			}
			more := mset.Items()
			for _, i := range more {
				c := caps[i]
				notes = append(notes, fmt.Sprintf("#%d 软上限 %v（%s），重排后目标 %.1f 被截断", i, c.Cap, c.Reason, tgt[i]))
			}
			if len(more) == 0 {
				break
			}
			var r2 []int
			for _, i := range more {
				capped[i] = true
			}
			for _, i := range rest {
				if !capped[i] {
					r2 = append(r2, i)
				}
			}
			rest = r2
			if len(rest) > 0 {
				for k, v := range RunStrategy(name, rest, sc, rp, cfg, in.Prev, cm) {
					tgt[k] = v
				}
			}
		}
	}
	for i := range capped {
		tgt[i] = caps[i].Cap
	}
	wmin, wmax := cfg.F("w_min", 0), cfg.F("w_max", 20)
	nw := map[int]int{}
	for _, i := range order {
		nw[i] = int(math.Max(wmin, math.Min(wmax, float64(m.RoundInt(tgt[i])))))
	}
	Settle(cfg, nw, in.Prev, fs, ids, caps, &notes)
	PairSlow(cfg, nw, in.Prev, fs, ids, &notes)
	nc := cfg.Sub("newcomer")
	ew := 0
	if nc.B("enabled", false) {
		ew = nc.I("explore_w", 0)
	}
	if ew > 0 {
		for _, i := range order {
			r := rows[i]
			if r.NewcomerN == nil {
				continue
			}
			if fs[i].Long.Err == 0 && nw[i] < ew {
				notes = append(notes, fmt.Sprintf("#%d 新渠道探索底线：%d→%d（1h 真实请求 %d，0 错误）", i, nw[i], ew, *r.NewcomerN))
				nw[i] = ew
				r.ExploreFloor = m.I(ew)
			}
		}
	}
	return &Decision{Strategy: name, Order: order, Scores: rows, Target: nw, Notes: notes, Caps: caps}, nil
}

func n60s(rows map[int]*Row) map[int]int {
	out := map[int]int{}
	for i, r := range rows {
		out[i] = r.N60
	}
	return out
}

// sortedPrevKeys: prev dict in run.py is built from chans order.
func sortedPrevKeys(chans []m.Channel, prev map[int]int) []int {
	var out []int
	for _, c := range chans {
		if _, ok := prev[c.ID]; ok {
			out = append(out, c.ID)
		}
	}
	return out
}
