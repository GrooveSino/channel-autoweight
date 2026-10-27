#!/usr/bin/env python3
"""v1.43 trust source comparison (reads local rounds.jsonl + shadow.jsonl only, no DB).
For each formal round, re-run decide with (A) the formal round's own trust (one step per 45 min) and (B) the trust of the latest shadow point (evolving every 5 min, sees bursts between rounds),
and compare the weight-share-weighted "real 10m p50/p90/error rate over the next 45 minutes" (from the 10m features of shadow points after t).
Usage: python3 tools/trust_source_eval.py [--max-age 15] [--horizon 45]
"""
import argparse, json, os, sys
from datetime import datetime
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, HERE)
from autoweight import config as _awcfg
from autoweight import decide, trust as T
from autoweight import shadow as S

def ts(x): return datetime.fromisoformat(x).timestamp()

def shadow_trust_at(srecs, t, max_age_min):
    best = None
    for r in srecs:
        if r.get("trust") and ts(r["ts"]) <= t: best = r
    if best is None or (t - ts(best["ts"])) / 60 > max_age_min: return None, None
    return {int(k): v["trust"] for k, v in best["trust"].items()}, best["ts"]

def outcome(srecs, t, horizon, w):
    acc = {"p50": [0, 0], "p90": [0, 0], "err": [0, 0]}
    tot = sum(w.values()) or 1
    for r in srecs:
        dt = (ts(r["ts"]) - t) / 60
        if not (5 <= dt <= horizon): continue
        for i, wi in w.items():
            f = (r["features"].get(str(i)) or {}).get("10m") or {}
            if not f.get("n"): continue
            s = wi / tot
            for k, key in (("p50", "p50"), ("p90", "p90"), ("err", "err_rate")):
                if f.get(key) is not None: acc[k][0] += s * f[key]; acc[k][1] += s
    return {k: (round(a / b, 3) if b else None) for k, (a, b) in acc.items()}

def main():
    ap = argparse.ArgumentParser(); ap.add_argument("--max-age", type=float, default=15); ap.add_argument("--horizon", type=float, default=45)
    a = ap.parse_args()
    cfg = _awcfg.load(os.path.join(HERE, "config.json"))
    rounds = [json.loads(l) for l in open(os.path.join(HERE, "data/rounds.jsonl")) if l.strip()]
    srecs = [json.loads(l) for l in open(os.path.join(HERE, "data/shadow.jsonl")) if l.strip()]
    srecs = [r for r in srecs if r.get("mode") == "shadow"]
    tot = {"A": {"p50": [], "p90": [], "err": []}, "B": {"p50": [], "p90": [], "err": []}}; diff = 0; n = 0
    for r in rounds:
        if r.get("dry_run", True) or not r.get("trust"): continue
        t = ts(r["ts"]); st, sts = shadow_trust_at(srecs, t, a.max_age)
        if st is None: continue
        chans = r["channels"]; ids = [c["id"] for c in chans if str(c["id"]) in r["features"] or c["id"] in r["features"]]
        feats = {c["id"]: r["features"].get(str(c["id"]), r["features"].get(c["id"])) for c in chans}
        probes = {int(k): v for k, v in (r.get("probes") or {}).items()}
        old = {int(k): v for k, v in r["old"].items()}
        tA = {int(k): v["trust"] for k, v in r["trust"].items()}
        gap = (t - ts(sts)) / 60
        ages = {i: (999.0 if r.get("probes_reused") else 0.0) for i in probes}
        tB = {i: v["trust"] for i, v in S.trust_step(cfg, [c["id"] for c in chans], feats, probes, st, gap, ages).items()}
        dA = decide.decide(cfg, chans, feats, probes, None, None, old, tA)["target"]
        dB = decide.decide(cfg, chans, feats, probes, None, None, old, tB)["target"]
        oA, oB = outcome(srecs, t, a.horizon, dA), outcome(srecs, t, a.horizon, dB)
        if oA["p50"] is None or oB["p50"] is None: continue
        n += 1; diff += dA != dB
        for k in ("p50", "p90", "err"):
            tot["A"][k].append(oA[k]); tot["B"][k].append(oB[k])
        flag = "*" if dA != dB else " "
        print(f"{flag} {r['ts'][5:16]} trustA={ {i: round(v,2) for i,v in tA.items()} } trustB={ {i: round(v,2) for i,v in tB.items()} } A={dA} B={dB} A p50/p90={oA['p50']}/{oA['p90']} B={oB['p50']}/{oB['p90']}")
    avg = lambda xs: round(sum(xs) / len(xs), 3) if xs else None
    print(f"rounds {n}, decisions differ {diff}")
    for s in "AB": print(s, {k: avg(v) for k, v in tot[s].items()})

if __name__ == "__main__": main()
