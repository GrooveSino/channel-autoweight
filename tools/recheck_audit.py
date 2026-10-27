#!/usr/bin/env python3
"""v1.63 read-only diagnostics: probe recheck audit.

When the first probe is an outlier (slow / buffered / failed) but the recheck is fine, the system takes the smaller value, i.e. discards the first probe as noise.
This checks whether, after such "bad first probe, good recheck" events, the channel really performed worse (a queueing/capacity signal or pure noise):
for each channel in each formal round (not reused probes), take its 10m/60m features in the next round (20-90 minutes later),
and compare p90, error rate and slow rate afterwards between the "recheck event" group and the "first probe fine" group. Reads rounds.jsonl only; no production access.
Usage: python3 tools/recheck_audit.py [--file data/rounds.jsonl] [--json]
"""
import argparse, json, statistics
from datetime import datetime

def _ts(s): return datetime.fromisoformat(s)

def classify(ps):
    """None = no probe; 'flaky' = bad first probe, good recheck; 'bad' = recheck also bad; 'ok' = first probe fine."""
    if not ps: return None
    first = ps[0]; re = [p for p in ps[1:] if p.get("recheck")]
    if not re: return "ok"
    good = lambda p: p.get("ok") and p.get("streaming") is not False
    return "flaky" if good(re[-1]) else "bad"

def after(rounds, i, cid, lo=20, hi=90):
    t0 = _ts(rounds[i]["ts"])
    for r in rounds[i+1:]:
        dt = (_ts(r["ts"]) - t0).total_seconds() / 60
        if dt < lo: continue
        if dt > hi: return None
        f = (r.get("features") or {}).get(str(cid)) or {}
        w = f.get("60m") or {}
        return w if w.get("n") else None
    return None

def audit(rounds):
    groups = {}
    for i, r in enumerate(rounds):
        if r.get("probes_reused"): continue
        for cid, ps in (r.get("probes") or {}).items():
            c = classify(ps)
            if c is None: continue
            w = after(rounds, i, cid)
            groups.setdefault(c, []).append(dict(ts=r["ts"], cid=int(cid), after=w))
    out = {}
    for c, evs in groups.items():
        ws = [e["after"] for e in evs if e["after"]]
        med = lambda k: round(statistics.median([w[k] for w in ws if w.get(k) is not None]), 3) if ws else None
        out[c] = dict(events=len(evs), with_after=len(ws), p90_med=med("p90"), err_med=med("err_rate"),
                      slow_med=med("slow_rate"), by_ch={str(k): sum(1 for e in evs if e["cid"] == k) for k in sorted({e["cid"] for e in evs})})
    return out

def main():
    ap = argparse.ArgumentParser(); ap.add_argument("--file", default="data/rounds.jsonl"); ap.add_argument("--json", action="store_true")
    a = ap.parse_args()
    rounds = [json.loads(l) for l in open(a.file) if l.strip()]
    res = audit(rounds)
    if a.json: print(json.dumps(res, ensure_ascii=False)); return
    for c in ("ok", "flaky", "bad"):
        if c in res: print(c, res[c])

if __name__ == "__main__": main()
