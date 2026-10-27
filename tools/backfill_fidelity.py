#!/usr/bin/env python3
"""v1.21 backfill fidelity check (read-only): recompute existing live shadow records with the v1.21 per-time-point replay and compare weights/scores/trust.
Differences between replay and live only come from late log writes and weight changes in between. Only a high agreement rate means backfilled records can be used for review."""
import json, os, sys
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
import shadow as SH
from autoweight import config as _awcfg
from autoweight import db, shadow as S

def main(n_last=12):
    cfg = _awcfg.load(os.path.join(HERE, "config.json")); sp = S.params(cfg)
    srecs = [r for r in SH.load(os.path.join(HERE, sp["file"])) if not r.get("backfill")]
    rrecs = SH.load(os.path.join(HERE, cfg["rounds_file"]))[-40:]
    live = srecs[-n_last:]
    import time
    span = (time.time() - S._ts(live[0])) / 60 + 61
    chans = db.channels(cfg); ids = [c["id"] for c in chans]
    _, rows = db.logs(cfg, ids, int(span) + 1)
    same_w = same_order = 0; dsc = []
    for r in live:
        t = S._ts(r)
        ch = [dict(c, weight=r["old"].get(str(c["id"]), c["weight"])) for c in chans]
        fresh = {int(k): v for k, v in (r.get("probes") or {}).items()}
        rec, *_ = SH.compute(cfg, sp, ch, rows, int(t), srecs, rrecs, fresh, r.get("probed") or {})
        a = {str(k): v for k, v in rec["suggest"].items()}; b = {str(k): v for k, v in r["suggest"].items()}
        same_w += a == b; same_order += [str(x) for x in rec["order"]] == [str(x) for x in r["order"]]
        for k in b:
            dsc.append(abs(rec["scores"][int(k)]["score"] - r["scores"][k]["score"]))
        print(r["ts"][11:16], "live", b, "replay", a, "OK" if a == b else "DIFF")
    print(f"suggested weights equal {same_w}/{len(live)}, order equal {same_order}/{len(live)}, score abs diff mean {sum(dsc)/len(dsc):.4f} max {max(dsc):.4f}")

if __name__ == "__main__":
    main(int(sys.argv[1]) if len(sys.argv) > 1 else 12)
