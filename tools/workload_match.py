#!/usr/bin/env python3
"""v1.49 workload-matched comparison (read-only diagnostics): compare channels' use_time per output-token bucket.

Raw p50/p90 are driven by "job size": sticky sessions keep long conversations on established channels while new channels only get short requests,
so a small use_time does not mean a fast channel. Successful requests (type=2, probe tokens removed, ct>0) are bucketed by completion_tokens,
each bucket gets a per-channel p50, and with "all requests in that bucket" as weights we compute each channel's weighted ratio to the pooled p50
(matched_ratio, <1 means faster for the same workload). A bucket only counts when the channel has >= min_n requests in it.
Usage: python3 tools/workload_match.py [--minutes 120] [--json]
"""
import argparse, json, os, sys
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight import config as _awcfg
from autoweight import db

BUCKETS = [(1, 150), (150, 400), (400, 900), (900, 10**9)]

def pct(xs, q):
    if not xs: return None
    xs = sorted(xs); k = (len(xs) - 1) * q
    lo = int(k); hi = min(lo + 1, len(xs) - 1)
    return xs[lo] + (xs[hi] - xs[lo]) * (k - lo)

def bucket_of(ct):
    for i, (a, b) in enumerate(BUCKETS):
        if a <= ct < b: return i
    return None

def analyze(rows, probe_tokens=(), min_n=5):
    """rows: dict(ch, type, ut, ct, tok). Returns {ch: {buckets:{i:(n,p50)}, matched_ratio, cover}}."""
    ex = set(probe_tokens or [])
    by = {}
    pool = {}
    for r in rows:
        if r["type"] != 2 or r["tok"] in ex or r["ct"] <= 0: continue
        b = bucket_of(r["ct"])
        if b is None: continue
        by.setdefault(r["ch"], {}).setdefault(b, []).append(r["ut"])
        pool.setdefault(b, []).append(r["ut"])
    pp50 = {b: pct(v, .5) for b, v in pool.items()}
    out = {}
    for ch, bs in by.items():
        num = den = 0.0; cover = 0
        bt = {}
        for b, v in bs.items():
            p = pct(v, .5); bt[b] = (len(v), p)
            if len(v) >= min_n and pp50.get(b):
                w = len(pool[b]); num += w * (p / pp50[b]); den += w; cover += len(v)
        tot = sum(len(v) for v in bs.values())
        out[ch] = dict(buckets=bt, matched_ratio=(num / den if den else None),
                       cover=(cover / tot if tot else 0.0), n=tot)
    return out, {b: (len(pool[b]), pp50[b]) for b in pool}

def pool_p50(rows, probe_tokens=()):
    """v1.50: pooled per-bucket p50 (probe removed, ct>0, successful), the baseline for per-request normalisation."""
    ex = set(probe_tokens or []); pool = {}
    for r in rows:
        if r["type"] != 2 or r["tok"] in ex or r["ct"] <= 0: continue
        b = bucket_of(r["ct"])
        if b is not None: pool.setdefault(b, []).append(r["ut"])
    return {b: pct(v, .5) for b, v in pool.items()}

def window_ratios(rows, t0, t1, base, probe_tokens=(), min_n=3):
    """v1.50: per channel within [t0, t1), the median of "per-request use_time / pooled p50 of the same bucket" (relative time for the same workload, <1 is faster).
    Errors (type=5) are excluded (error rate is separate). Channels with < min_n samples get no value. Returns {ch: (n, median_ratio)}."""
    ex = set(probe_tokens or []); by = {}
    for r in rows:
        if not (t0 <= r["t"] < t1) or r["type"] != 2 or r["tok"] in ex or r["ct"] <= 0: continue
        b = bucket_of(r["ct"])
        if b is None or not base.get(b): continue
        by.setdefault(r["ch"], []).append(r["ut"] / base[b])
    return {ch: (len(v), pct(v, .5)) for ch, v in by.items() if len(v) >= min_n}

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--minutes", type=int, default=120)
    ap.add_argument("--min-n", type=int, default=5)
    ap.add_argument("--json", action="store_true")
    a = ap.parse_args()
    cfg = _awcfg.load(os.path.join(os.path.dirname(__file__), "..", "config.json"))
    chs = db.channels(cfg)
    _, rows = db.logs(cfg, [c["id"] for c in chs], a.minutes)
    res, pool = analyze(rows, cfg.get("probe_token_ids"), a.min_n)
    if a.json:
        print(json.dumps(dict(res={str(k): v for k, v in res.items()}, pool=pool), ensure_ascii=False, default=str)); return
    names = {c["id"]: c["name"] for c in chs}
    hdr = " | ".join(f"ct {lo}-{hi if hi < 10**9 else 'inf'}" for lo, hi in BUCKETS)
    print(f"Workload-matched comparison (last {a.minutes} min, cell = n / p50 s)")
    print(f"| Channel | {hdr} | Matched ratio | Coverage |"); print("|---" * (len(BUCKETS) + 3) + "|")
    print("| All | " + " | ".join(f"{pool[b][0]} / {pool[b][1]:.1f}" if b in pool else "-" for b in range(len(BUCKETS))) + " | 1.00 | |")
    for ch in sorted(res):
        r = res[ch]
        cells = " | ".join(f"{r['buckets'][b][0]} / {r['buckets'][b][1]:.1f}" if b in r["buckets"] else "-" for b in range(len(BUCKETS)))
        mr = f"{r['matched_ratio']:.2f}" if r["matched_ratio"] is not None else "-"
        print(f"| #{ch} {names.get(ch, '')} | {cells} | {mr} | {r['cover']:.0%} |")

if __name__ == "__main__":
    main()
