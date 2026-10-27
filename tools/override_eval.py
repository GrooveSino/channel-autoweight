#!/usr/bin/env python3
"""v1.45 scorecard: manual review vs system suggestion (reads local rounds.jsonl + shadow.jsonl only, no DB).
For every formal write round where "final weight != system suggestion", weight the real 10m p50/p90/error rate of shadow points in the following horizon minutes by weight share,
and compare whether the manual final weights (F) or the system suggestion (S) did better. Used to judge whether manual review can be dropped.
Usage: python3 tools/override_eval.py [--horizons 20,45,90]
"""
import argparse, json, os, sys
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(HERE, "tools"))
from trust_source_eval import outcome, ts

def pairs(rounds):
    out = []
    for r in rounds:
        if r.get("dry_run", True) or not r.get("applied"): continue
        ov = (r.get("decision") or {}).get("override")
        if not ov or not ov.get("system"): continue
        sysw = {int(k): v for k, v in ov["system"].items()}
        fin = {int(k): v for k, v in r["decision"]["target"].items()}
        if sysw != fin: out.append((r["ts"], sysw, fin, ov.get("reason", "")))
    return out

def score(srecs, pr, horizon):
    rows = []
    for t0, sysw, fin, why in pr:
        t = ts(t0); oS, oF = outcome(srecs, t, horizon, sysw), outcome(srecs, t, horizon, fin)
        if oS["p50"] is None or oF["p50"] is None: continue
        rows.append((t0, sysw, fin, why, oS, oF))
    return rows

def verdict(oS, oF, tol=0.02):
    """p90 first (tail experience), then p50; a relative difference < tol is a tie. Returns 'F'/'S'/'='."""
    for k in ("p90", "p50"):
        a, b = oS[k], oF[k]
        if a is None or b is None: continue
        if abs(a - b) > tol * max(a, b, 1e-9): return "F" if b < a else "S"
    return "="

def main():
    ap = argparse.ArgumentParser(); ap.add_argument("--horizons", default="20,45,90"); a = ap.parse_args()
    rounds = [json.loads(l) for l in open(os.path.join(HERE, "data/rounds.jsonl")) if l.strip()]
    srecs = [r for r in (json.loads(l) for l in open(os.path.join(HERE, "data/shadow.jsonl")) if l.strip()) if r.get("mode") == "shadow"]
    pr = pairs(rounds); print(f"{len(pr)} write rounds where the system suggestion was manually changed")
    for h in [float(x) for x in a.horizons.split(",")]:
        rows = score(srecs, pr, h); w = {"F": 0, "S": 0, "=": 0}; sp = {"S": [0, 0], "F": [0, 0]}
        print(f"\n== next {h:g} minutes ==")
        for t0, sysw, fin, why, oS, oF in rows:
            v = verdict(oS, oF); w[v] += 1
            for k, o in (("S", oS), ("F", oF)): sp[k][0] += o["p50"]; sp[k][1] += o["p90"]
            print(f"{v} {t0[5:16]} system={sysw} manual={fin} system p50/p90={oS['p50']}/{oS['p90']} manual={oF['p50']}/{oF['p90']}  {why[:40]}")
        n = len(rows)
        if n: print(f"manual wins {w['F']} / system wins {w['S']} / ties {w['=']}; means system {sp['S'][0]/n:.2f}/{sp['S'][1]/n:.2f} manual {sp['F'][0]/n:.2f}/{sp['F'][1]/n:.2f}")

if __name__ == "__main__": main()
