#!/usr/bin/env python3
"""v1.50 workload-matched comparison (read-only): on the same shadow time points, compare exp_mr of three weighting schemes in the "next 10m window"
(workload-match ratio weighted by weight share, <1 is better): system = shadow suggestion, db = weights in the DB at the time (manual final), equal = uniform.
Usage: python3 tools/matched_compare.py [--since-hours 6]"""
import argparse, json, os, sys
from datetime import datetime
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import shadow_replay as sr
from autoweight import config as _awcfg

def exp_mr(recs, pick, mr_fn):
    num = den = 0.0
    for k in range(len(recs) - 1):
        r, nx = recs[k], recs[k + 1]
        t0, t1 = (datetime.fromisoformat(x["ts"]).timestamp() for x in (r, nx))
        if (t1 - t0) / 60 > 7: continue
        w = pick(r); tot = sum(w.values()) or 1; m = mr_fn(t1 - 600, t1)
        for i, v in w.items():
            if i in m: num += v / tot * m[i][1]; den += v / tot
    return round(num / den, 4) if den else None

def main():
    ap = argparse.ArgumentParser(); ap.add_argument("--since-hours", type=float, default=6)
    a = ap.parse_args()
    cfg = _awcfg.load(os.path.join(sr.HERE, "config.json"))
    recs = sr.load(os.path.join(sr.HERE, "data", "shadow.jsonl"), a.since_hours)
    fn = sr.make_mr_fn(cfg, recs)
    picks = dict(system=lambda r: {int(k): v for k, v in r["suggest"].items()},
                 db=lambda r: {int(k): v for k, v in r["old"].items()},
                 equal=lambda r: {int(k): 1 for k in r["suggest"]})
    print(json.dumps({"hours": a.since_hours, "n": len(recs), **{k: exp_mr(recs, p, fn) for k, p in picks.items()}}))

if __name__ == "__main__":
    main()
