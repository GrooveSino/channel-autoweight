#!/usr/bin/env python3
"""v2.01 diagnostics (reads local rounds.jsonl; no effect on scoring or on/off):
ratio of the site probe first token to the same round's 1h real log first token (lt50 of frt_version=2).
A ratio >= ratio_hi is flagged PROBE_SLOW (probe occasionally slow, may unfairly hurt the score), <= ratio_lo is flagged PROBE_FAST.
Only formal rounds are counted (applied or dry run; dry run + apply within the same minute are de-duplicated, keeping the latter)."""
import json, os, sys, argparse, statistics
from datetime import datetime

HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

def first_probe_ttft(ps):
    for p in ps or []:
        if p.get("ok") and not p.get("recheck") and p.get("ttft") is not None:
            return p["ttft"], bool(p.get("streaming"))
    return None, None

def dedup(rs, gap_min=10):
    out = []
    for r in rs:
        if out:
            try:
                a = datetime.fromisoformat(out[-1]["ts"]); b = datetime.fromisoformat(r["ts"])
                if (b - a).total_seconds() < gap_min * 60:
                    out[-1] = r; continue
            except Exception:
                pass
        out.append(r)
    return out

def audit(rs, min_lt_n=3, ratio_hi=3.0, ratio_lo=0.5, tail=12):
    rows = []
    for r in dedup([x for x in rs if x.get("probes")]):
        for cid, ps in (r.get("probes") or {}).items():
            t, st = first_probe_ttft(ps)
            f = ((r.get("features") or {}).get(cid) or {}).get("60m") or {}
            lt, n = f.get("lt50"), f.get("lt_n") or 0
            if t is None or lt is None or n < min_lt_n or lt <= 0:
                continue
            ratio = round(t / lt, 2)
            flag = "PROBE_SLOW" if ratio >= ratio_hi else ("PROBE_FAST" if ratio <= ratio_lo else "")
            rows.append(dict(ts=r["ts"], cid=cid, probe=t, streaming=st, lt50=lt, lt_n=n, ratio=ratio, flag=flag))
    summ = {}
    for cid in sorted({x["cid"] for x in rows}, key=int):
        xs = [x for x in rows if x["cid"] == cid][-tail:]
        summ[cid] = dict(rounds=len(xs), ratio_med=round(statistics.median(x["ratio"] for x in xs), 2),
                         slow=sum(x["flag"] == "PROBE_SLOW" for x in xs),
                         fast=sum(x["flag"] == "PROBE_FAST" for x in xs),
                         last=xs[-1]["ratio"], last_flag=xs[-1]["flag"])
    return dict(rows=rows, summary=summ)

def main():
    ap = argparse.ArgumentParser(); ap.add_argument("--tail", type=int, default=12)
    ap.add_argument("--json", action="store_true"); a = ap.parse_args()
    rs = [json.loads(l) for l in open(os.path.join(HERE, "data/rounds.jsonl")) if l.strip()]
    out = audit(rs, tail=a.tail)
    if a.json:
        print(json.dumps(out["summary"], ensure_ascii=False)); return
    for cid, s in out["summary"].items():
        print(f"#{cid} last {s['rounds']} rounds: probe/log first-token ratio median {s['ratio_med']}, slow {s['slow']}, fast {s['fast']}, this round {s['last']} {s['last_flag']}")

if __name__ == "__main__":
    main()
