#!/usr/bin/env python3
"""v1.54 diagnostics (read-only, not used for scoring): bootstrap estimate of each channel's probability P(best) of "really being the best right now".

Method: successful requests in the window get workload-matched ratios per output-token bucket (same baseline as features.matched_ratios); errors get the penalty err_pen;
each channel is resampled with replacement B times, the "experience score" (1-w90)*p50 + w90*p90 of matched ratios is computed, and the smallest counts as best once.
A high P(best) means the leader has solid evidence; similar P(best) across channels means shadow suggestions jumping between them is noise and weights should not flip."""
import argparse, json, os, random, sys, time
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, os.path.join(HERE, ".."))
from autoweight import config as _awcfg
from autoweight import features

def per_request(rows, t0, t1, err_pen=4.0):
    ok = [x for x in rows if t0 <= x["t"] < t1 and x["type"] == 2 and (x.get("ct") or 0) > 0]
    pool = {}
    for x in ok: pool.setdefault(features._bucket(x["ct"]), []).append(x["ut"])
    base = {b: features.pct(v, .5) for b, v in pool.items()}
    by = {}
    for x in ok:
        bb = base.get(features._bucket(x["ct"]))
        if bb: by.setdefault(x["ch"], []).append(x["ut"] / bb)
    for x in rows:
        if t0 <= x["t"] < t1 and x["type"] == 5: by.setdefault(x["ch"], []).append(err_pen)
    return by

def pbest(by, B=2000, w90=0.3, min_n=10, seed=0):
    rng = random.Random(seed)
    chs = [c for c, v in by.items() if len(v) >= min_n]
    if not chs: return {}
    win = {c: 0 for c in chs}
    for _ in range(B):
        best, bv = None, None
        for c in chs:
            s = sorted(rng.choices(by[c], k=len(by[c])))
            v = (1 - w90) * s[len(s) // 2] + w90 * s[min(len(s) - 1, int(len(s) * .9))]
            if bv is None or v < bv: best, bv = c, v
        win[best] += 1
    return {c: round(win[c] / B, 3) for c in chs}

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--config", default=os.path.join(HERE, "..", "config.json"))
    ap.add_argument("--minutes", type=int, nargs="+", default=[10, 30, 60])
    ap.add_argument("--B", type=int, default=2000)
    a = ap.parse_args()
    from autoweight import db
    cfg = _awcfg.load(a.config)
    ids = [int(i) for i in cfg.get("channel_ids") or [2, 20, 21, 22]]
    _, rows = db.logs(cfg, ids, max(a.minutes) + 5)
    rows = features.user_rows(cfg, rows)
    now = time.time()
    for m in a.minutes:
        by = per_request(rows, now - m * 60, now + 1)
        print(f"{m}m n={ {c: len(v) for c, v in sorted(by.items())} } P(best)={pbest(by, a.B)}")

if __name__ == "__main__":
    main()
