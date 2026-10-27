#!/usr/bin/env python3
"""Generate a small, fully synthetic log snapshot for offline backtests (no production data).
Four fake channels with different latency/error profiles over 24h.
Usage: python3 examples/make_synthetic_snapshot.py [--out examples/synthetic_snapshot.json] [--seed 7]"""
import argparse, json, random

PROFILES = {  # id: (name, median use_time s, error rate, requests per hour)
    101: ("[ALPHA]-model", 8, 0.01, 40),
    102: ("[BETA]-model", 12, 0.02, 30),
    103: ("[GAMMA]-model", 20, 0.05, 20),
    104: ("[DELTA]-model", 9, 0.15, 25),
}

def build(seed=7, hours=26, end=1_800_000_000):
    rnd = random.Random(seed); rows = []
    for ch, (_, med, err, rph) in PROFILES.items():
        for _ in range(rph * hours):
            t = end - rnd.randint(0, hours * 3600)
            tok = rnd.randint(1, 50)
            if rnd.random() < err:
                rows.append(dict(ch=ch, t=t, type=5, ut=rnd.randint(1, 100), ct=0, tok=tok, sc=rnd.choice([429, 500, 502, 524])))
            else:
                ct = rnd.randint(50, 1500)
                ut = max(1, int(rnd.lognormvariate(0, 0.4) * med * (0.5 + ct / 1000)))
                rows.append(dict(ch=ch, t=t, type=2, ut=ut, ct=ct, tok=tok, frt=round(ut * 0.2, 3)))
    rows.sort(key=lambda r: r["t"])
    chans = [dict(id=i, name=n, weight=5, priority=10) for i, (n, *_r) in PROFILES.items()]
    return {"chans": chans, "rows": rows, "end": end}

if __name__ == "__main__":
    ap = argparse.ArgumentParser(); ap.add_argument("--out", default="examples/synthetic_snapshot.json"); ap.add_argument("--seed", type=int, default=7)
    a = ap.parse_args(); json.dump(build(a.seed), open(a.out, "w")); print("wrote", a.out)
