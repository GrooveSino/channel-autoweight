package fetch

import (
	"fmt"
	"strconv"
	"strings"
)

// TrialDemand is the last-window real demand for the models a trial channel serves.
type TrialDemand struct {
	Ch, Total, OnCh  int
	LastAgoMin       float64 // minutes since the last real request on ch; -1 if none in 48h
	W, WSum          float64 // ch's weight and the weight sum of enabled channels serving those models
	Fresh, FreshOnCh int     // non-affinity (weight-routed) requests in the window, total and on ch
	Tokens           int     // distinct real tokens behind Total
}

// TrialDemandSQL counts real (non-probe) requests in the last `minutes` for the gpt-*
// models enabled on ch, across all channels and on ch itself, plus the age of ch's
// last real request. Read-only aggregate.
func (p *Psql) TrialDemandSQL(ch, minutes int, probeTok []int) string {
	ts := make([]string, len(probeTok))
	for i, v := range probeTok {
		ts[i] = strconv.Itoa(v)
	}
	excl := ""
	if len(ts) > 0 {
		excl = " and coalesce(l.token_id,0) not in (" + strings.Join(ts, ",") + ")"
	}
	return fmt.Sprintf(`with ms as (select distinct model from abilities where channel_id=%d and enabled
 and model like '%s' and model not like '%s'),
 r as (select l.channel_id, l.created_at, l.token_id,
 case when l.other like '%%"channel_affinity":{"channel_id":%%' then 1 else 0 end aff from logs l where l.type in (2,5) and l.model_name in (select model from ms)
 and l.created_at >= extract(epoch from now())::bigint - 172800%s)
select %d, count(*) filter (where created_at >= extract(epoch from now())::bigint - %d),
 count(*) filter (where channel_id=%d and created_at >= extract(epoch from now())::bigint - %d),
 coalesce(round((extract(epoch from now()) - max(created_at) filter (where channel_id=%d))/60.0, 1), -1),
 (select weight from channels where id=%d),
 (select coalesce(sum(c.weight),0) from channels c where c.status=1 and exists (select 1 from abilities a
  where a.channel_id=c.id and a.enabled and a.model in (select model from ms))),
 count(*) filter (where aff=0 and created_at >= extract(epoch from now())::bigint - %d),
 count(*) filter (where aff=0 and channel_id=%d and created_at >= extract(epoch from now())::bigint - %d),
 count(distinct token_id) filter (where created_at >= extract(epoch from now())::bigint - %d)
from r;`, ch, p.ModelLike, p.ExcludeLike, excl, ch, minutes*60, ch, minutes*60, ch, ch, minutes*60, ch, minutes*60, minutes*60)
}

// TrialDemandOf runs TrialDemandSQL read-only.
func (p *Psql) TrialDemandOf(ch, minutes int, probeTok []int) (TrialDemand, error) {
	rs, err := p.RO(p.TrialDemandSQL(ch, minutes, probeTok))
	if err != nil {
		return TrialDemand{}, err
	}
	if len(rs) != 1 || len(rs[0]) < 6 {
		return TrialDemand{}, fmt.Errorf("bad trial demand row")
	}
	f := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
	r := rs[0]
	d := TrialDemand{Ch: atoi(r[0]), Total: atoi(r[1]), OnCh: atoi(r[2]), LastAgoMin: f(r[3]), W: f(r[4]), WSum: f(r[5])}
	if len(r) >= 9 {
		d.Fresh, d.FreshOnCh, d.Tokens = atoi(r[6]), atoi(r[7]), atoi(r[8])
	}
	return d, nil
}
