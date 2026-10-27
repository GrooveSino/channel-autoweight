#!/usr/bin/env python3
"""v1.23 shadow replay evaluation (reads local data/shadow.jsonl, no DB).
Feed each shadow record's features, first token and trust at the time back into decide, change parameters with --set, and simulate "write the suggestion every 5 minutes":
prev = suggestion at the previous time point (the first uses DB weights). Output:
    - fidelity: share of recomputed suggestions equal to the record when parameters are unchanged
    - changes / moved: number of suggested-weight changes and total weight movement (smaller is more stable)
    - exp_p50 / exp_p90 / exp_err: real p50/p90/error rate of the "next time point's 10m window" weighted by suggested weight share (only when adjacent points are <= 7 min apart)
    - exp_mr (v1.50, --matched, one read-only log pull): the same window weighted by "workload-match ratio" (median of per-request use_time / pooled p50 of the same output-token bucket),
        no longer biased towards channels that only get short jobs; <1 means the suggestion steers traffic to channels that are faster for the same workload
Usage: python3 tools/shadow_replay.py [--since-hours 6] [--set strategies.rank.band=0.05 ...] [--sweep strategies.rank.band=0,0.03,0.05,0.08]
"""
import argparse, copy, json, os, sys
from datetime import datetime
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, HERE)
from autoweight import config as _awcfg
from autoweight import decide

def set_path(cfg, kv):
    k, v = kv.split("=", 1); d = cfg; ks = k.split(".")
    for x in ks[:-1]: d = d[x]
    try: v = json.loads(v)
    except Exception: pass
    d[ks[-1]] = v

def load(path, since_h=None, start=None, until=None):
    recs = [json.loads(l) for l in open(path) if l.strip()]
    recs = [r for r in recs if r.get("mode") == "shadow" and r.get("scores")]
    # v1.63: cut by absolute time (replay records before and after a parameter change separately)
    if start: recs = [r for r in recs if datetime.fromisoformat(r["ts"]) >= datetime.fromisoformat(start)]
    if until: recs = [r for r in recs if datetime.fromisoformat(r["ts"]) < datetime.fromisoformat(until)]
    if since_h:
        end = datetime.fromisoformat(recs[-1]["ts"]).timestamp()
        recs = [r for r in recs if datetime.fromisoformat(r["ts"]).timestamp() >= end - since_h * 3600]
    return recs

def rec_probes(r):
    """Recover the first token / probes used at the time from the record (including reused ones)."""
    probes = {}
    for k, s in r["scores"].items():
        i = int(k)
        if s.get("ttft") is not None and s.get("probe_ok", 0) > 0:
            probes[i] = [{"ok": True, "ttft": s["ttft"], "streaming": s.get("streaming"), "deltas": 100}]
    return probes

_ROUNDS = None
ROUNDS_TAIL = 0   # v1.77: >0 keeps only the latest N formal rounds before ts (simulates the pre-v1.77 live shadow that only read the last 10 rounds)
def rounds_before(ts):
    """v1.49: during replay, restore the formal-round history available at the record's time (for newcomer_ids), consistent with the live shadow."""
    global _ROUNDS
    if _ROUNDS is None:
        f = os.path.join(HERE, "data", "rounds.jsonl")
        _ROUNDS = [json.loads(l) for l in open(f) if l.strip()] if os.path.exists(f) else []
    t = datetime.fromisoformat(ts).timestamp()
    out = [x for x in _ROUNDS if datetime.fromisoformat(x["ts"]).timestamp() <= t]
    return out[-ROUNDS_TAIL:] if ROUNDS_TAIL > 0 else out

def redo(cfg, r, prev, smooth=None, trust_state=None):
    ids = [int(k) for k in r["scores"]]
    chans = [{"id": i, "name": r["names"].get(str(i), ""), "weight": prev.get(i, cfg.get("w_min", 0))} for i in ids]
    feats = {i: r["features"][str(i)] for i in ids}
    if (cfg.get("score", {}).get("client_err") or {}).get("enabled"):   # v1.58: decisions use the client-error-free definition; evaluation still uses raw records
        from autoweight import features as F
        _ce = cfg["score"]["client_err"]
        feats = F.drop_client_errors(copy.deepcopy(feats), "err_user" if _ce.get("mode") == "cross_channel" else "err_client")
    if (cfg.get("score", {}).get("err_recover") or {}).get("enabled"):   # v1.72
        from autoweight import features as F
        feats = F.recover_errors(copy.deepcopy(feats), cfg["score"]["err_recover"])
    probes = {}
    for i in ids:
        s = r["scores"][str(i)]
        if s.get("ttft") is not None and s.get("probe_ok", 0) > 0:
            probes[i] = [{"ok": True, "ttft": s["ttft"], "streaming": s.get("streaming"), "deltas": 100}]
    trust = {i: r["scores"][str(i)]["trust"] for i in ids if "trust" in r["scores"][str(i)]}
    if trust_state is not None:   # v1.28 --evolve-trust: re-evolve trust with the current parameters instead of using recorded values
        from autoweight import shadow as S
        if not trust_state.get("t"):
            trust_state["t"] = {int(k): v["prev"] if v.get("prev") is not None else v["trust"] for k, v in r["trust"].items()}
        tst = S.trust_step(cfg, ids, feats, probes, trust_state["t"], r.get("gap_min"), S.probe_ages(r.get("ttft_src")))
        trust_state["t"] = {i: v["trust"] for i, v in tst.items()}
        trust = dict(trust_state["t"])
    nc = decide.newcomer_ids(cfg, chans, rounds_before(r["ts"]), r["ts"])   # v1.49: replay applies the newcomer cap too
    d = decide.decide(cfg, chans, feats, probes, None, None, prev, trust or None, prev_smooth=smooth, newcomers=nc or None)
    if smooth is not None:
        smooth.clear(); smooth.update({i: row["score"] for i, row in d["scores"].items()})
    return d["target"]

def evaluate(cfg, recs, evolve_trust=False, mr_fn=None):
    """mr_fn(t0, t1) -> {ch: (n, ratio)}: v1.50 workload-matched evaluation (None = no exp_mr)."""
    prev = {int(k): v for k, v in recs[0]["old"].items()}
    same = changes = moved = 0; ev = {"p50": [0, 0], "p90": [0, 0], "err": [0, 0]}; seq = []
    mr = [0.0, 0.0]
    smooth = {}; ts = {} if evolve_trust else None
    rc = cfg.get("rise_confirm") or {}; rn = rc.get("n", 1) if rc.get("enabled") else 1; raws = []
    for k, r in enumerate(recs):
        tgt = redo(cfg, r, prev, smooth, ts)
        raws.append(tgt); raws = raws[-max(rn, 1):]
        if rn > 1 and k > 0:   # v1.34; v1.35 with max_n60>0 only channels with 1h samples < max_n60 need confirmation
            mx = rc.get("max_n60", 0) or 0
            only = {i for i in tgt if (r["features"][str(i)]["60m"]["n"] or 0) < mx} if mx > 0 else None
            tgt = decide.confirm_rise(raws, prev, rn, only)
        same += tgt == {int(a): b for a, b in r["suggest"].items()}
        if k > 0:
            d = sum(abs(tgt.get(i, 0) - prev.get(i, 0)) for i in set(tgt) | set(prev))
            changes += d > 0; moved += d
        seq.append(tgt)
        if k + 1 < len(recs):
            nx = recs[k + 1]
            gap = (datetime.fromisoformat(nx["ts"]) - datetime.fromisoformat(r["ts"])).total_seconds() / 60
            if gap <= 7:
                tot = sum(tgt.values()) or 1
                for i, w in tgt.items():
                    f = (nx["features"].get(str(i)) or {}).get("10m")   # v1.51: a channel disabled in between is missing at the next point
                    if f and f["n"] >= 3 and f["p50"] is not None:
                        for key in ev:
                            v = f["err_rate"] if key == "err" else f[key]
                            ev[key][0] += w / tot * v; ev[key][1] += w / tot
                if mr_fn:
                    t1 = datetime.fromisoformat(nx["ts"]).timestamp()
                    m = mr_fn(t1 - 600, t1)
                    for i, w in tgt.items():
                        if i in m:
                            mr[0] += w / tot * m[i][1]; mr[1] += w / tot
        prev = tgt
    out = dict(n=len(recs), fidelity=round(same / len(recs), 3), changes=changes, moved=moved)
    for key, (a, b) in ev.items(): out[f"exp_{key}"] = round(a / b, 4) if b else None
    if mr_fn: out["exp_mr"] = round(mr[0] / mr[1], 4) if mr[1] else None
    return out, seq

def make_mr_fn(cfg, recs):
    """v1.50: one read-only pull of logs covering the replay interval; baseline = pooled per-bucket p50 over the whole interval."""
    from autoweight import db
    sys.path.insert(0, os.path.join(HERE, "tools"))
    import workload_match as wm
    t_first = datetime.fromisoformat(recs[0]["ts"]).timestamp()
    ids = sorted({int(i) for r in recs for i in r["features"]})
    now, rows = db.logs(cfg, ids, int((datetime.now().timestamp() - t_first) / 60) + 20)
    base = wm.pool_p50(rows, cfg.get("probe_token_ids"))
    cache = {}
    def fn(t0, t1):
        k = (int(t0), int(t1))
        if k not in cache: cache[k] = wm.window_ratios(rows, t0, t1, base, cfg.get("probe_token_ids"))
        return cache[k]
    return fn

def inject_mr(cfg, recs):
    """v1.52: recompute 60m/10m workload-match ratios from logs for each shadow time point and inject them into features (not in records), to replay score.workload_match."""
    from autoweight import db, features
    t_first = datetime.fromisoformat(recs[0]["ts"]).timestamp()
    ids = sorted({int(i) for r in recs for i in r["features"]})
    _, rows = db.logs(cfg, ids, int((datetime.now().timestamp() - t_first) / 60) + 80)
    if cfg.get("probe_token_ids") and cfg.get("score", {}).get("exclude_probe_rows"):
        rows = features.user_rows(cfg, rows)
    mn = (cfg["score"].get("workload_match") or {}).get("min_n", 5)
    out = copy.deepcopy(recs)
    for r in out:
        t = int(datetime.fromisoformat(r["ts"]).timestamp())
        xt = features.cross_channel_tokens(rows, t - 3600, t + 1, (cfg["score"].get("client_err") or {}).get("min_ch", 2))  # v1.71
        for m in cfg["windows_min"]:
            mr = features.matched_ratios(rows, t - m * 60, t + 1, mn)
            for k, f in r["features"].items():
                v = mr.get(int(k)); w = f.get(f"{m}m")
                if v and w is not None: w.update(mr_n=v[0], mr50=v[1], mr90=v[2])
            # v1.53: also inject log first token (frt_version=2) lt_n/lt50/lt90 to replay score.log_ttft
            for k, f in r["features"].items():
                w = f.get(f"{m}m")
                if w is None: continue
                lt = [x["frt"] for x in rows if x["ch"] == int(k) and x["type"] == 2 and t - m * 60 <= x["t"] <= t and x.get("frt") is not None]
                w.update(lt_n=len(lt), lt50=features.pct(lt, .5), lt90=features.pct(lt, .9))
                # v1.58: inject client error counts (4xx except 408/429) to replay score.client_err
                if "err_client" not in w:
                    w["err_client"] = sum(1 for x in rows if x["ch"] == int(k) and t - m * 60 <= x["t"] <= t and features.is_client_err(x))
                if "eb_span" not in w:   # v1.72
                    _, w["eb_span"], w["eb_ok_after"] = features.err_burst([x for x in rows if x["ch"] == int(k) and t - m * 60 <= x["t"] <= t])
                if "err_user" not in w:   # v1.71
                    w["err_user"] = sum(1 for x in rows if x["ch"] == int(k) and t - m * 60 <= x["t"] <= t and x.get("tok") in xt and features.is_client_err(x))
    return out

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--config", default=os.path.join(HERE, "config.json"))
    ap.add_argument("--file", default=os.path.join(HERE, "data", "shadow.jsonl"))
    ap.add_argument("--since-hours", type=float)
    ap.add_argument("--set", action="append", default=[])
    ap.add_argument("--sweep")
    ap.add_argument("--start", help="v1.63: only replay records with ts >= START (ISO time)")
    ap.add_argument("--until", help="v1.63: only replay records with ts < UNTIL (ISO time)")
    ap.add_argument("--evolve-trust", action="store_true", help="v1.28: re-evolve trust with the current parameters (evaluate trust parameter changes)")
    ap.add_argument("--inject-mr", action="store_true", help="v1.52: recompute workload-match ratios from logs and inject them into features")
    ap.add_argument("--matched", action="store_true", help="v1.50: also compute workload-matched exp_mr (one read-only log pull)")
    ap.add_argument("--rounds-tail", type=int, default=0, help="v1.77: newcomer detection only sees the latest N formal rounds before ts (simulates an old bug; 0 = full history)")
    a = ap.parse_args()
    global ROUNDS_TAIL
    ROUNDS_TAIL = a.rounds_tail
    base = _awcfg.load(a.config)
    for kv in a.set: set_path(base, kv)
    recs = load(a.file, a.since_hours, a.start, a.until)
    if a.inject_mr: recs = inject_mr(base, recs)
    mr_fn = make_mr_fn(base, recs) if a.matched else None
    vals = [None]
    if a.sweep:
        k, vs = a.sweep.split("="); vals = [f"{k}={v}" for v in vs.split(",")]
    for v in vals:
        cfg = copy.deepcopy(base)
        if v: set_path(cfg, v)
        out, _ = evaluate(cfg, recs, a.evolve_trust, mr_fn)
        print(json.dumps({"set": v or a.set, **out}, ensure_ascii=False))

if __name__ == "__main__":
    main()
