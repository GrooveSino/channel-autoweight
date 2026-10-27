#!/usr/bin/env python3
"""v1.51 prototype shadow vs the gateway's built-in autoweight shadow module (read-only).

The gateway module records each run in auto_weight_runs (body JSON: rows[].channel.id / suggested / trust / features.60m.n / ttft).
Each gateway record is matched by time to the nearest prototype shadow point (default <= 4 minutes), and we report:
    - per-channel, overall and first-place agreement of suggested weights
    - trust difference and 1h sample ratio (the gateway keeps samples in memory and starts from zero after a restart)
Also log_ttft(): real first-token p50/p90 from frt_version=2 logs (ms->s; non-streaming frt is null and excluded automatically).
"""
import argparse, json, os, sys
from datetime import datetime, timezone, timedelta
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight import config as _awcfg
from autoweight import db

TZ = timezone(timedelta(hours=8))

def site_runs(cfg, hours=3):
    sql = f"""select id, created_at, operation, applied, body from auto_weight_runs
where created_at >= extract(epoch from now())::bigint - {int(hours*3600)} order by created_at;"""
    out = []
    for r in db.ro(cfg, sql):
        try:
            body = json.loads("|".join(r[4:]))
        except Exception:
            continue
        out.append(parse_run(int(r[1]), r[2], r[3] == "t", body))
    return out

def parse_run(t, op, applied, body):
    rows = {}
    for x in body.get("rows") or []:
        ch = x.get("channel") or {}
        f60 = (x.get("features") or {}).get("60m") or {}
        rows[int(ch["id"])] = dict(suggested=x.get("suggested"), trust=x.get("trust"), score=x.get("score"),
                                   n60=f60.get("n"), ttft=x.get("ttft"), weight=ch.get("weight"))
    q = body.get("quality") or {}
    reasons = body.get("reasons") or []
    # v1.70: in shadow mode the gateway marks every run blocked=True (no writes); only runs with a reason (e.g. insufficient coverage, keep current weights) are invalid
    return dict(t=t, op=op, applied=applied, rows=rows, blocked=bool(body.get("blocked")) and bool(reasons),
                reasons=reasons, started=q.get("started"), cold=bool(q.get("cold")))

def load_shadow(path):
    pts = []
    for l in open(path):
        try:
            d = json.loads(l)
        except Exception:
            continue
        pts.append((datetime.fromisoformat(d["ts"]).timestamp(), d))
    return pts

def match(site, shadow, tol_s=240):
    """Match each gateway record to the nearest prototype shadow point; no match beyond tol."""
    res = []
    for s in site:
        best = min(shadow, key=lambda p: abs(p[0] - s["t"]), default=None)
        if best is None or abs(best[0] - s["t"]) > tol_s:
            continue
        res.append((s, best[1]))
    return res

def compare(pairs):
    ch_tot, ch_eq, all_eq, top_eq, n = {}, {}, 0, 0, 0
    tdiff, nratio, lines = {}, {}, []
    for s, p in pairs:
        sug = {int(k): v for k, v in (p.get("suggest") or {}).items()}
        ids = sorted(set(sug) & set(s["rows"]))
        if not ids:
            continue
        n += 1
        ss = {i: s["rows"][i]["suggested"] for i in ids}
        eq = all(ss[i] == sug[i] for i in ids)
        all_eq += eq
        top_s = max(ids, key=lambda i: (ss[i], -i)); top_p = max(ids, key=lambda i: (sug[i], -i))
        top_eq += top_s == top_p
        for i in ids:
            ch_tot[i] = ch_tot.get(i, 0) + 1
            ch_eq[i] = ch_eq.get(i, 0) + (ss[i] == sug[i])
            pt = ((p.get("trust") or {}).get(str(i)) or {}).get("trust")
            st = s["rows"][i]["trust"]
            if pt is not None and st is not None:
                tdiff.setdefault(i, []).append(st - pt)
            pn = ((p.get("scores") or {}).get(str(i)) or {}).get("n60")
            sn = s["rows"][i]["n60"]
            if pn and sn is not None:
                nratio.setdefault(i, []).append(sn / pn)
        lines.append(dict(site_t=datetime.fromtimestamp(s["t"], TZ).strftime("%H:%M"), proto_ts=p["ts"][11:16],
                          site=ss, proto={i: sug[i] for i in ids}, same=eq))
    avg = lambda v: round(sum(v) / len(v), 3) if v else None
    return dict(n=n, all_agree=round(all_eq / n, 3) if n else None, top_agree=round(top_eq / n, 3) if n else None,
                per_channel={i: round(ch_eq[i] / ch_tot[i], 3) for i in ch_tot},
                trust_diff={i: avg(v) for i, v in tdiff.items()},
                n60_ratio={i: avg(v) for i, v in nratio.items()}, lines=lines)

def log_ttft(cfg, ids, minutes=60):
    """Log first token for frt_version=2 (streaming records with frt only); returns {id: (n, p50_s, p90_s)}."""
    sql = f"""select channel_id, count(*),
 percentile_cont(0.5) within group (order by (other::jsonb->>'frt')::float),
 percentile_cont(0.9) within group (order by (other::jsonb->>'frt')::float)
from logs where created_at >= extract(epoch from now())::bigint - {int(minutes)*60} and type=2
 and channel_id in ({",".join(map(str, ids))}) and other like '%"frt_version":2%'
 and (other::jsonb->>'frt') is not null group by 1 order by 1;"""
    return {int(r[0]): (int(r[1]), round(float(r[2]) / 1000, 2), round(float(r[3]) / 1000, 2)) for r in db.ro(cfg, sql)}

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--config", default=os.path.join(os.path.dirname(__file__), "..", "config.json"))
    ap.add_argument("--hours", type=float, default=3)
    ap.add_argument("--tol", type=int, default=240)
    ap.add_argument("--json", action="store_true")
    a = ap.parse_args()
    cfg = _awcfg.load(a.config)
    base = os.path.join(os.path.dirname(__file__), "..")
    allr = [r for r in site_runs(cfg, a.hours) if r["op"] == "shadow"]
    site = [r for r in allr if not (r["cold"] or r["blocked"])]   # cold-start / insufficient-coverage runs keep current weights and are not suggestions
    pairs = match(site, load_shadow(os.path.join(base, "data", "shadow.jsonl")), a.tol)
    res = compare(pairs)
    ids = sorted({i for s in allr for i in s["rows"]} | {2, 5, 20, 21, 22})
    res["log_ttft_60m"] = log_ttft(cfg, ids)
    if site:
        res["site_started"] = site[-1]["started"]
    if a.json:
        print(json.dumps(res, ensure_ascii=False, default=str)); return
    print(f"gateway shadow {len(allr)} records ({len(allr)-len(site)} cold-start skipped), {len(site)} valid, {res['n']} matched; overall agreement {res['all_agree']}, first-place agreement {res['top_agree']}; gateway process started {res.get('site_started')}")
    for l in res["lines"]:
        print(f"  gateway {l['site_t']} <-> prototype {l['proto_ts']} {'=' if l['same'] else '!='} gateway {l['site']} prototype {l['proto']}")
    print("per-channel agreement", res["per_channel"]); print("trust diff (gateway - prototype)", res["trust_diff"]); print("1h sample ratio (gateway/prototype)", res["n60_ratio"])
    print("log first token v2 60m (n, p50s, p90s)", res["log_ttft_60m"])

if __name__ == "__main__":
    main()
