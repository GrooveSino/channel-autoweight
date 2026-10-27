#!/usr/bin/env python3
"""v1.55 diagnostics (read-only, not used for scoring): per-channel error timeline in 5-minute buckets + error classification.

Motivation: a channel showed an 11% 1h error rate but only 1/31 in 10m; it was unclear whether it had "already recovered" or had "persistent sporadic timeouts".
This tool classifies errors by status code (timeout=524/504/408, upstream=5xx, ratelimit=429, client=4xx, other),
and counts consecutive erroring buckets (streak) and "wait seconds wasted on errors" (sum of use_time of failed requests; a 524 often wastes 100s).
Output never contains secrets: only the status_code digits from content are read."""
import argparse, json, os, re, sys, time
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, os.path.join(HERE, ".."))
from autoweight import config as _awcfg

def classify(content):
    m = re.search(r"status_code=(\d{3})", content or "")
    if not m: return "other"
    c = int(m.group(1))
    if c in (524, 504, 408): return "timeout"
    if c == 429: return "ratelimit"
    if c >= 500: return "upstream"
    if c >= 400: return "client"
    return "other"

def timeline(rows, t0, t1, bucket=300):
    """rows: dict(ch,t,type,ut,cls). Returns {ch: {buckets:[(start,n,err)], err_by_cls, wasted_s, streak, err_buckets, n_buckets}}"""
    out = {}
    nb = max(1, int((t1 - t0 + bucket - 1) // bucket))
    for x in rows:
        if not (t0 <= x["t"] < t1): continue
        d = out.setdefault(x["ch"], {"b": [[0, 0] for _ in range(nb)], "cls": {}, "wasted_s": 0})
        i = int((x["t"] - t0) // bucket)
        d["b"][i][0] += 1
        if x["type"] == 5:
            d["b"][i][1] += 1
            d["cls"][x.get("cls", "other")] = d["cls"].get(x.get("cls", "other"), 0) + 1
            d["wasted_s"] += x.get("ut", 0)
    for ch, d in out.items():
        streak = 0
        for n, e in reversed(d["b"]):
            if n == 0: continue
            if e > 0: streak += 1
            else: break
        d["streak"] = streak  # counting back from the latest, consecutive buckets (with traffic) that had errors
        d["err_buckets"] = sum(1 for n, e in d["b"] if e > 0)
        d["n_buckets"] = sum(1 for n, e in d["b"] if n > 0)
    return out

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--config", default=os.path.join(HERE, "..", "config.json"))
    ap.add_argument("--minutes", type=int, default=60)
    ap.add_argument("--json", action="store_true")
    a = ap.parse_args()
    from autoweight import db
    cfg = _awcfg.load(a.config)
    ids = [int(i) for i in cfg.get("channel_ids") or [2, 20, 21, 22]]
    sql = f"""select channel_id, created_at, type, coalesce(use_time,0),
 case when type=5 then coalesce(substring(content from 'status_code=([0-9]{{3}})'),'') else '' end
from logs where created_at >= extract(epoch from now())::bigint - {int(a.minutes)*60}
and type in (2,5) and channel_id in ({",".join(map(str, ids))})
and model_name like '{cfg["model_like"]}' and model_name not like '{cfg["exclude_model_like"]}';
select extract(epoch from now())::bigint;"""
    r = db.ro(cfg, sql)
    now = int(r[-1][0])
    rows = [dict(ch=int(x[0]), t=int(x[1]), type=int(x[2]), ut=int(x[3]),
                 cls=classify(f"status_code={x[4]}" if len(x) > 4 and x[4] else "")) for x in r[:-1]]
    t0 = now - a.minutes * 60
    tl = timeline(rows, t0, now + 1)
    if a.json:
        print(json.dumps({str(k): v for k, v in tl.items()}, ensure_ascii=False)); return
    for ch in sorted(tl):
        d = tl[ch]
        n = sum(b[0] for b in d["b"]); e = sum(b[1] for b in d["b"])
        line = " ".join(f"{b[1]}/{b[0]}" for b in d["b"])
        print(f"#{ch} n={n} err={e} error buckets {d['err_buckets']}/{d['n_buckets']} streak {d['streak']} wasted {d['wasted_s']}s classes {d['cls']}\n   5-min buckets (err/n): {line}")

if __name__ == "__main__":
    main()
