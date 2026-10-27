#!/usr/bin/env python3
"""Backtest: replay historical logs hour by hour. Hour h is decided from the previous window's features and evaluated on each channel's real performance in hour h:
      weighted metric = sum w_i * metric_i / sum w_i  (assumes new requests follow weights and channel performance does not change with traffic -- an approximation)
Compares strategies + baselines (static: weights currently in the DB / equal: uniform). First token: history has no real first token, so the backtest uses
probe values from rounds.jsonl for the same hour (when present), else the same default for every channel (first token does not discriminate).
Usage: python3 backtest.py --hours 24            # pull read-only from the production DB
            python3 backtest.py --csv logs.csv            # offline (columns channel_id,created_at,type,use_time)
            python3 backtest.py --hours 24 --align-hour --dump data/snap.json   # pull once and save a snapshot
            python3 backtest.py --load data/snap.json      # A/B old vs new versions on the same snapshot, identical windows
"""
import argparse, csv, json, os, sys
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, HERE)
from autoweight import config as _awcfg
from autoweight import db, features, decide, diag, trust as trustmod

def load_rows(a, cfg):
    if a.load:
        d = json.load(open(a.load)); return d["chans"], d["rows"], d["end"]
    if a.csv:
        rows = [dict(ch=int(r["channel_id"]), t=int(r["created_at"]), type=int(r["type"]), ut=int(r["use_time"] or 0), ct=None)
                for r in csv.DictReader(open(a.csv))]
        chans = [dict(id=i, name=str(i), weight=1, priority=10) for i in sorted({r["ch"] for r in rows})]
        return chans, rows, max(r["t"] for r in rows)
    chans = db.channels(cfg)
    now, rows = db.logs(cfg, [c["id"] for c in chans], a.hours * 60 + 60)
    return chans, rows, now

def probes_by_hour(cfg):
    rf = os.path.join(HERE, cfg["rounds_file"]); out = {}
    if os.path.exists(rf):
        for l in open(rf):
            r = json.loads(l); out[r["hour"]] = {int(k): v for k, v in (r.get("probes") or {}).items()}
    return out

def wavg(w, ev, key):
    num = den = 0
    for i, wi in w.items():
        v = ev[i].get(key)
        if v is None or ev[i]["n"] == 0: continue
        num += wi * v; den += wi
    return round(num / den, 3) if den else None

EVAL_EXCLUDE = []   # set by main to probe_token_ids from the config (evaluation definition stays fixed when sweep/--set change cfg)

def run(cfg, chans, rows, end, hours, quiet=False):
    pb = probes_by_hour(cfg)
    eval_cfg = {"probe_token_ids": EVAL_EXCLUDE or cfg.get("probe_token_ids")}
    strats = list(decide.STRATEGIES) + ["static", "equal"]
    agg = {s: {"p50": [], "p90": [], "slow_rate": [], "err_rate": [], "empty_rate": [], "np50": [], "np90": []} for s in strats}
    last = {s: None for s in strats}   # v1.15: each strategy's weights from the previous hour (for hysteresis)
    pev = []                            # v1.31: probe history accumulated hour by hour (only hours before the current one; causal)
    tstate = {}                         # v1.18: trust state rolled hour by hour (strategy-independent, depends only on observations)
    start = min(r["t"] for r in rows) + 3600
    t = end - hours * 3600
    t = max(t, start)
    while t + 3600 <= end:
        hist = [r for r in rows if t - 3600 <= r["t"] < t]
        # Evaluation always uses real user requests (probe tokens removed); decisions follow cfg (for old/new A/B)
        nxt = features.user_rows(eval_cfg, [r for r in rows if t <= r["t"] < t + 3600])
        f = features.build(cfg, chans, hist, t)
        ev = {c["id"]: features.window([r for r in nxt if r["ch"] == c["id"]], t + 3600, 60, cfg["slow_s"]) for c in chans}
        hour = int(((t + 8 * 3600) // 3600) % 24)
        probes = pb.get(hour, {})
        # v1.11: rebuild the "recovery observation period" from probes of rounds before this hour (rounds keyed by hour, only hours with records)
        rc = cfg.get("recovery", {})
        recov = {}
        if rc.get("enabled") and probes:
            hh = [{"probes": {str(k): v for k, v in pb[x].items()}} for x in sorted(pb) if x < hour]
            recov = diag.recovering(hh, probes, [c["id"] for c in chans], rc.get("min_streak", 2))
        bpp = cfg["score"].get("buffer_prob") or {}
        pbuf = diag.buffer_prob(pev, t, probes, bpp) if probes and bpp.get("enabled") else None
        if probes: pev.append((t, {str(k): v for k, v in probes.items()}))
        tmap = None
        if (cfg.get("trust") or {}).get("enabled"):
            tstate = {i: v["trust"] for i, v in trustmod.step(cfg, [c["id"] for c in chans], f, probes, tstate, 60).items()}
            tmap = tstate
        for s in strats:
            if s == "static": w = {c["id"]: max(c["weight"], 1) for c in chans}
            elif s == "equal": w = {c["id"]: 1 for c in chans}
            else: w = decide.decide(cfg, chans, f, probes, s, recov, last[s], tmap, pbuf=pbuf)["target"]
            last[s] = w
            for k in agg[s]:
                v = wavg(w, ev, k)
                if v is not None: agg[s][k].append(v)
        t += 3600
    res = {s: {k: (round(sum(v) / len(v), 3) if v else None) for k, v in m.items()} | {"hours": len(m["p50"])} for s, m in agg.items()}
    if not quiet:
        print(f"{'strategy':<14}{'hours':>6}{'w_p50':>9}{'w_p90':>9}{'w_slow':>9}{'w_err':>9}{'w_empty':>9}{'w_np50':>9}{'w_np90':>9}")
        for s, r in res.items():
            print(f"{s:<14}{r['hours']:>6}{str(r['p50']):>9}{str(r['p90']):>9}{str(r['slow_rate']):>9}{str(r['err_rate']):>9}{str(r['empty_rate']):>9}{str(r.get('np50')):>9}{str(r.get('np90')):>9}")
    return res

def set_path(cfg, kv):
    """Write 'a.b.c=value' into a nested config; the value is parsed as JSON, falling back to a string. For parameter A/B without editing files."""
    k, v = kv.split("=", 1); keys = k.split(".")
    try: v = json.loads(v)
    except ValueError: pass
    d = cfg
    for x in keys[:-1]: d = d.setdefault(x, {})
    d[keys[-1]] = v

if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--config", default=os.path.join(HERE, "config.json"))
    ap.add_argument("--hours", type=int, default=24); ap.add_argument("--csv")
    ap.add_argument("--load", help="load a snapshot saved with --dump (offline, reproducible)")
    ap.add_argument("--dump", help="save the fetched chans/rows/end as a JSON snapshot")
    ap.add_argument("--align-hour", action="store_true", help="align the end time to the full hour")
    ap.add_argument("--set", action="append", default=[], metavar="a.b=v",
                    help="temporary config override (JSON value), repeatable, e.g. --set score.err_10m_exp=2 --set strategies.rank.top_w=14")
    ap.add_argument("--sweep", metavar="a.b=v1,v2,...",
                    help="parameter sweep: run once per value and print one summary line per strategy (same data, directly comparable)")
    a = ap.parse_args(); cfg = _awcfg.load(a.config)
    EVAL_EXCLUDE = list(cfg.get("probe_token_ids") or [])
    for kv in a.set: set_path(cfg, kv)
    chans, rows, end = load_rows(a, cfg)
    if a.align_hour: end -= end % 3600          # align to the hour: repeated backtests within the same hour use the same window
    if a.dump: json.dump({"chans": chans, "rows": rows, "end": end}, open(a.dump, "w"))
    if not a.sweep:
        run(cfg, chans, rows, end, a.hours)
    else:
        import copy
        key, vals = a.sweep.split("=", 1)
        print(f"{key:<24}{'strategy':<14}{'w_p50':>9}{'w_p90':>9}{'w_slow':>9}{'w_err':>9}{'w_empty':>9}{'w_np50':>9}{'w_np90':>9}")
        for v in vals.split(","):
            c2 = copy.deepcopy(cfg); set_path(c2, f"{key}={v}")
            for s_, r in run(c2, chans, rows, end, a.hours, quiet=True).items():
                if s_ in decide.STRATEGIES:
                    print(f"{v:<24}{s_:<14}{str(r['p50']):>9}{str(r['p90']):>9}{str(r['slow_rate']):>9}{str(r['err_rate']):>9}{str(r.get('empty_rate')):>9}{str(r.get('np50')):>9}{str(r.get('np90')):>9}")
