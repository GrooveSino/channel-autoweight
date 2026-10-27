#!/usr/bin/env python3
"""Shadow mode entry point (v1.19): read-only scoring + suggested weights -> append to data/shadow.jsonl. Never writes the DB.
Usage: python3 shadow.py [--no-probe] [--backfill-only]
Daemon: tools/shadow_loop.sh (every 5 minutes, flock against re-entry, timeout, log in data/shadow.log).
v1.21: after a gap (e.g. the host was frozen), the next run first replays the missing time points from the same read-only log query (backfill=true, no probes),
then computes the current record; backfilled records feed trust evolution and review, so trust is not halved by gap_reset after a gap."""
import argparse, json, os, sys
from datetime import datetime, timezone, timedelta
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, HERE)
from autoweight import config as _awcfg
from autoweight import db, features, probe, decide, shadow as S

def load(path):
    return [json.loads(l) for l in open(path) if l.strip()] if os.path.exists(path) else []

def nc_rounds(hist, now):
    """v1.77: newcomer detection uses all formal rounds before now (consistent with tools/shadow_replay.rounds_before)."""
    return [r for r in hist if (S._ts(r) or 0) <= now]

def compute(cfg, sp, chans, rows, now, srecs, rrecs, fresh, plan, backfill=False, nc_hist=None):
    """Compute one shadow record at time point now. rows may contain logs after now (during replay); they are cut off here.
    v1.77 nc_hist: full formal-round history for newcomer detection (rrecs is only the last 10; with many dry runs, < 4 applied rounds would misclassify old channels as new)."""
    rows = [x for x in rows if x["t"] <= now]
    ids = [c["id"] for c in chans]
    srecs = [r for r in srecs if (S._ts(r) or 0) < now]
    rrecs = [r for r in rrecs if (S._ts(r) or 0) <= now]
    feats = features.build(cfg, chans, rows, now)
    counts = S.real_counts(cfg, rows, now, sp["probe_window_min"])
    allrecs = sorted(srecs + rrecs, key=lambda r: S._ts(r) or 0)
    probes, src = {}, {}
    for i in ids:
        if i in fresh: probes[i], src[i] = fresh[i], "probe"
        else:
            ps, age = S.last_probe(allrecs, i, now, sp["ttft_max_age_min"], sp.get("watch_ttft_fallback", False), sp.get("upstream_ttft_add_s", 1.5))
            if ps: probes[i], src[i] = ps, f"reuse {age}m"
            else: src[i] = "missing"
    tprev, tts = S.trust_prev(srecs)
    if not tprev:   # first run: copy the latest formal-round trust as a read-only starting point, then evolve independently
        from autoweight import trust as T
        tprev, tts = T.last_state(rrecs)
    gap = (now - datetime.fromisoformat(tts).timestamp()) / 60 if tts else None
    tst = S.trust_step(cfg, ids, feats, probes, tprev, gap, S.probe_ages(src))
    old = {c["id"]: c["weight"] for c in chans}
    dec = decide.decide(cfg, chans, feats, probes, None, None, old, {i: v["trust"] for i, v in tst.items()},
                        newcomers=decide.newcomer_ids(cfg, chans, nc_rounds(nc_hist if nc_hist is not None else rrecs, now), datetime.fromtimestamp(now).astimezone().isoformat()))   # v1.47; v1.77 full history
    bj = datetime.fromtimestamp(now, timezone(timedelta(hours=8)))
    prev_ts = S._ts(srecs[-1]) if srecs else None
    rec = dict(ts=bj.isoformat(), mode="shadow", gap_min=round((now - prev_ts) / 60, 1) if prev_ts else None, old=old, names={c["id"]: c["name"] for c in chans},
               features=feats, real_n=counts, scores=dec["scores"], order=dec["order"], suggest=dec["target"],
               notes=dec["notes"], trust=tst, probed=plan, probes={i: fresh[i] for i in fresh}, ttft_src=src,
               diff={i: dec["target"][i] - old[i] for i in ids if dec["target"][i] != old[i]})
    if backfill: rec["backfill"] = True
    return rec, dec, feats, counts, src, tst

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--config", default=os.path.join(HERE, "config.json"))
    ap.add_argument("--no-probe", action="store_true", help="never probe")
    ap.add_argument("--no-backfill", action="store_true", help="v1.21: do not backfill gaps")
    ap.add_argument("--backfill-only", action="store_true", help="v1.21: only backfill gaps; no current record, no probes")
    a = ap.parse_args()
    cfg = _awcfg.load(a.config); sp = S.params(cfg)
    sf = os.path.join(HERE, sp["file"]); rf = os.path.join(HERE, cfg["rounds_file"])
    srecs = load(sf)[-500:]
    rall = load(rf)   # v1.77: full history, used only for newcomer detection
    rrecs = rall[-10:]
    chans = db.channels(cfg)
    ids = [c["id"] for c in chans]
    last_ts = S._ts(srecs[-1]) if srecs else None
    # v1.21: one read-only query covers the gap + the 60m window (backfill at most bf_max_min minutes)
    import time as _t
    gap_now = (_t.time() - last_ts) / 60 if last_ts else 0
    do_bf = sp.get("backfill", True) and not a.no_backfill and gap_now > 1.5 * sp["interval_min"]
    span = max(cfg["windows_min"]) + (min(gap_now, sp.get("backfill_max_min", 360)) if do_bf else 0)
    now, rows = db.logs(cfg, ids, int(span) + 1)
    filled = []
    if do_bf:
        for slot in S.backfill_slots(last_ts, now, sp["interval_min"], sp.get("backfill_max_min", 360)):
            rec, *_ = compute(cfg, sp, chans, rows, slot, srecs, rrecs, {}, {}, backfill=True, nc_hist=rall)
            srecs.append(rec); filled.append(rec)
        if filled:
            with open(sf, "a") as f:
                for rec in filled: f.write(json.dumps(rec, ensure_ascii=False, default=str) + "\n")
            print(f"BACKFILL {len(filled)} records ({filled[0]['ts'][11:16]}-{filled[-1]['ts'][11:16]}, gap {gap_now:.1f}m, replayed per time point, no probes)")
            for r in filled: print(f"  {r['ts'][11:16]} suggest={r['suggest']} trust={ {k: v['trust'] for k, v in r['trust'].items()} }")
    if a.backfill_only: return
    counts = S.real_counts(cfg, rows, now, sp["probe_window_min"])
    plan = {} if (a.no_probe or not cfg["probe"]["enabled"]) else S.plan_probes(cfg, ids, counts, srecs, now)
    fresh = probe.run(cfg, list(plan)) if plan else {}
    rec, dec, feats, counts, src, tst = compute(cfg, sp, chans, rows, now, srecs, rrecs, fresh, plan, nc_hist=rall)
    os.makedirs(os.path.dirname(sf), exist_ok=True)
    with open(sf, "a") as f: f.write(json.dumps(rec, ensure_ascii=False, default=str) + "\n")
    print(f"[{rec['ts']}] SHADOW (no write) probed={list(plan) or '-'}" + (f"  GAP {rec['gap_min']}m (more than {1.5*sp['interval_min']:g} min since the last shadow record; the host may have been frozen or the loop stopped)" if rec["gap_min"] and rec["gap_min"] > 1.5 * sp["interval_min"] else ""))
    old = rec["old"]
    for i in ids:
        s = dec["scores"][i]; f60, f10 = feats[i]["60m"], feats[i]["10m"]
        print(f"#{i:<3} db={old[i]:>2} suggest={dec['target'][i]:>2} score={s['score']:.3f} trust={tst[i]['trust']} "
              f"ttft={s['ttft']}({src[i]}) 1h n={f60['n']} err={f60['err']} p50={f60['p50']} p90={f60['p90']} 10m n={f10['n']} real={counts.get(i,0)}")
    for n in dec["notes"]: print("NOTE:", n)
    for i, r in plan.items(): print(f"PROBE #{i}: {r} -> " + "; ".join(f"ok={p['ok']} ttft={p.get('ttft')} stream={p.get('streaming')} status={p.get('status')}" for p in fresh.get(i, [])))

if __name__ == "__main__":
    main()
