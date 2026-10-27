#!/usr/bin/env python3
"""v1.59 diagnostics (reads local rounds.jsonl, not used for scoring): probe "buffered" verdict vs real traffic audit.

For each probe record with streaming=False, compare with the same round's 1h frt_version=2 real-traffic flush-at-once share ls_buf:
- likely_fp: ls_n >= min_n and ls_buf <= max_buf (real traffic streams; the probe is most likely a false positive)
- confirmed: ls_n >= min_n and ls_buf >= 0.5 (real traffic is also largely flushed at once)
- unknown: not enough samples or in between
Accumulates evidence to decide whether log_stream_veto is worth enabling."""
import argparse, json, os, sys
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, os.path.join(HERE, ".."))

def classify(ls_n, ls_buf, min_n=20, max_buf=0.2):
    if ls_n is None or ls_buf is None or ls_n < min_n: return "unknown"
    if ls_buf <= max_buf: return "likely_fp"
    if ls_buf >= 0.5: return "confirmed"
    return "unknown"

def audit(rounds, min_n=20, max_buf=0.2):
    events, seen = [], set()
    for r in rounds:
        for ch, ps in (r.get("probes") or {}).items():
            if not any(p.get("ok") and p.get("streaming") is False for p in ps): continue
            key = (ch, r["ts"][:16])
            if key in seen: continue
            seen.add(key)
            f = ((r.get("features") or {}).get(ch) or {}).get("60m") or {}
            events.append({"ts": r["ts"], "ch": int(ch), "ls_n": f.get("ls_n"), "ls_buf": f.get("ls_buf"),
                           "applied": bool(r.get("applied")),
                           "verdict": classify(f.get("ls_n"), f.get("ls_buf"), min_n, max_buf)})
    summ = {}
    for e in events:
        s = summ.setdefault(e["ch"], {"likely_fp": 0, "confirmed": 0, "unknown": 0})
        s[e["verdict"]] += 1
    return events, summ

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--file", default=os.path.join(HERE, "..", "data", "rounds.jsonl"))
    ap.add_argument("--min-n", type=int, default=20)
    ap.add_argument("--max-buf", type=float, default=0.2)
    a = ap.parse_args()
    rounds = [json.loads(l) for l in open(a.file) if l.strip()]
    ev, summ = audit(rounds, a.min_n, a.max_buf)
    for e in ev:
        if e["ls_n"] is None: continue  # records before v2 logs have no ls features
        print(f"{e['ts'][5:16]} #{e['ch']} ls_n={e['ls_n']} ls_buf={e['ls_buf']} -> {e['verdict']}")
    print("summary (old records without v2 logs count as unknown):", json.dumps(summ, ensure_ascii=False))

if __name__ == "__main__": main()
