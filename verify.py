#!/usr/bin/env python3
"""Read-only check after a write: python3 verify.py [--expect-last] [--wait 65]
--expect-last uses the target of the last applied round in data/rounds.jsonl as the expected weights."""
import argparse, json, os, sys, time
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, HERE)
from autoweight import config as _awcfg
from autoweight import verify

ap = argparse.ArgumentParser()
ap.add_argument("--config", default=os.path.join(HERE, "config.json"))
ap.add_argument("--wait", type=int, default=0, help="seconds to wait first (the gateway cache syncs in ~60s)")
ap.add_argument("--expect-last", action="store_true")
a = ap.parse_args()
cfg = _awcfg.load(a.config)
exp = None
if a.expect_last:
    rf = os.path.join(HERE, cfg["rounds_file"])
    for l in reversed([l for l in open(rf) if l.strip()]):
        r = json.loads(l)
        if r.get("applied"):
            exp = {int(k): v for k, v in r["decision"]["target"].items()}; break
if a.wait: time.sleep(a.wait)
rows = verify.fetch(cfg)
for r in rows:
    st = "enabled" if r["status"] == 1 else f"disabled(status={r['status']})"
    print(f"#{r['id']:<3} {r['name']:<14} {st:<14} priority={r['priority']} weight={r['weight']} abilities={r['abilities']}")
probs = verify.check(rows, cfg["priority"], exp)
print("OK: channels/abilities consistent" if not probs else "PROBLEM:\n" + "\n".join(probs))
sys.exit(1 if probs else 0)
