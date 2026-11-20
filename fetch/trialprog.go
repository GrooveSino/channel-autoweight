package fetch

import (
	"fmt"
	"strconv"
	"strings"
)

// TrialAgg is a channel's real-traffic aggregate since its trial pass time.
type TrialAgg struct {
	Ch, N, Err, Empty, Tokens int
	P50, P90                  float64
}

// TrialSQL aggregates real (non-probe-token) gpt-* traffic on ch since `since`.
func (p *Psql) TrialSQL(ch int, since int64, probeTok []int) string {
	ts := make([]string, len(probeTok))
	for i, v := range probeTok {
		ts[i] = strconv.Itoa(v)
	}
	excl := ""
	if len(ts) > 0 {
		excl = " and coalesce(token_id,0) not in (" + strings.Join(ts, ",") + ")"
	}
	return fmt.Sprintf(`select %d, count(*), count(*) filter (where type=5),
 count(*) filter (where type=2 and coalesce(completion_tokens,0)=0),
 count(distinct token_id),
 coalesce(percentile_cont(0.5) within group (order by use_time) filter (where type=2),0),
 coalesce(percentile_cont(0.9) within group (order by use_time) filter (where type=2),0)
from logs where channel_id=%d and created_at >= %d and type in (2,5)%s
and model_name like '%s' and model_name not like '%s';`, ch, ch, since, excl, p.ModelLike, p.ExcludeLike)
}

// TrialProgress runs TrialSQL read-only.
func (p *Psql) TrialProgress(ch int, since int64, probeTok []int) (TrialAgg, error) {
	rs, err := p.RO(p.TrialSQL(ch, since, probeTok))
	if err != nil {
		return TrialAgg{}, err
	}
	if len(rs) != 1 || len(rs[0]) < 7 {
		return TrialAgg{}, fmt.Errorf("bad trial agg row")
	}
	r := rs[0]
	f := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
	return TrialAgg{Ch: atoi(r[0]), N: atoi(r[1]), Err: atoi(r[2]), Empty: atoi(r[3]), Tokens: atoi(r[4]), P50: f(r[5]), P90: f(r[6])}, nil
}
