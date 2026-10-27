#!/usr/bin/env python3
"""Per-round entry point: collect -> probe -> decide -> (optional) write -> record + review.
Usage: python3 run.py [--apply] [--no-probe] [--reuse-probe MIN] [--strategy rank|proportional] [--compare] [--review]
Dry run by default; only --apply writes the DB."""
import argparse, json, os, sys, time
from datetime import datetime, timezone, timedelta
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, HERE)
from autoweight import config as _awcfg
from autoweight import db, features, probe, decide, review, diag, watch, trust

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--config", default=os.path.join(HERE, "config.json"))
    ap.add_argument("--apply", action="store_true", help="actually write the DB (default: dry run)")
    ap.add_argument("--no-probe", action="store_true")
    ap.add_argument("--reuse-probe", type=float, metavar="MIN", help="reuse the latest round's probes from within MIN minutes (v1.12)")
    ap.add_argument("--strategy"); ap.add_argument("--compare", action="store_true", help="also print the results of every strategy")
    ap.add_argument("--review", action="store_true", help="only review the previous round; no collection")
    ap.add_argument("--override", metavar="ID=W[,ID=W]", help="v1.20 final weights after manual review (override the matching channels of the system suggestion); requires --reason")
    ap.add_argument("--final", metavar="ID=W[,ID=W]", help="v1.41 final weights for every adjustable channel at once (mutually exclusive with --override); requires --reason when it differs from the system suggestion")
    ap.add_argument("--expect-system", metavar="ID=W[,ID=W]", help="v1.41 the system suggestion seen during the dry run; abort the write if the recomputation differs")
    ap.add_argument("--apply-toggle", action="store_true", help="v1.92 also execute the autonomous on/off actions (requires --apply)")
    ap.add_argument("--reason", default="", help="reason for --override, recorded in rounds")
    a = ap.parse_args()
    cfg = _awcfg.load(a.config); rf = os.path.join(HERE, cfg["rounds_file"])
    prev, hist, allh = None, [], []
    if os.path.exists(rf):
        allh = [json.loads(l) for l in open(rf) if l.strip()]   # v1.77: full history, used only for newcomer detection
        hist = allh[-24:]
        prev = hist[-1] if hist else None
    if a.review:
        print(json.dumps(prev, ensure_ascii=False, indent=1) if prev else "no history"); return
    chans = db.channels(cfg)
    now, rows = db.logs(cfg, [c["id"] for c in chans], max(cfg["windows_min"]))
    feats = features.build(cfg, chans, rows, now)
    ids = [c["id"] for c in chans]
    reused = probe.reuse(hist, ids, now, a.reuse_probe) if a.reuse_probe else {}
    probes = reused or ({} if a.no_probe else probe.run(cfg, ids))
    rechecked = probe.recheck(cfg, probes, hist) if probes and not reused else []   # v1.4: recheck outlier first tokens
    anom = probe.anomalies(probes)   # v1.7: classify probe anomalies (diagnostics only)
    chset = diag.channel_set_change(prev, chans)          # v1.9 diagnostics
    bstreak = diag.buffered_streak(hist, probes, [c["id"] for c in chans], bool(reused)) if probes else {}
    rc = cfg.get("recovery", {})
    recov = diag.recovering(hist, probes, [c["id"] for c in chans], rc.get("min_streak", 2)) if probes else {}   # always recorded (diagnostics); decide applies the cap only if recovery.enabled
    # v1.18 trust state: continue from the previous formal record (or replay formal history), fast fall / slow rise
    hist_all = [json.loads(l) for l in open(rf) if l.strip()] if os.path.exists(rf) else []
    tprev, tts = trust.last_state(hist_all)
    if not tprev: tprev, tts = trust.bootstrap(cfg, hist_all)
    gap = (now - datetime.fromisoformat(tts).timestamp()) / 60 if tts else None
    tsrc = "rounds"
    tc = cfg.get("trust") or {}
    if tc.get("source") == "shadow":   # v1.43: continue trust from the latest shadow point (evolves every 5 min and sees bursts between rounds); fall back to formal rounds if too old
        from autoweight import shadow as S
        sf = os.path.join(HERE, (cfg.get("shadow") or {}).get("file", "data/shadow.jsonl"))
        srecs = [json.loads(l) for l in open(sf) if l.strip()] if os.path.exists(sf) else []
        st, sts = S.trust_prev([r for r in srecs if r.get("mode") == "shadow"])
        sgap = (now - datetime.fromisoformat(sts).timestamp()) / 60 if sts else None
        if st and sgap is not None and 0 <= sgap <= tc.get("shadow_max_age_min", 15):
            ra = probe.reuse_age(hist_all, now) if reused else 0.0   # v1.68: reused probes are judged as new evidence by their real age
            ages = {i: (999.0 if ra is None else ra) for i in probes}
            tst = S.trust_step(cfg, ids, feats, probes, st, sgap, ages); tsrc = f"shadow {sts[11:16]} +{sgap:.1f}m"
    if tsrc == "rounds":
        tst = trust.step(cfg, ids, feats, probes, tprev, gap)
    tmap = {i: v["trust"] for i, v in tst.items()}
    # v1.31 buffering probability (always computed and recorded; used for scoring only if score.buffer_prob.enabled)
    bpp = cfg["score"].get("buffer_prob") or {}
    pev = [(datetime.fromisoformat(r["ts"]).timestamp(), r.get("probes") or {}) for r in hist_all if r.get("probes") and not r.get("probes_reused")]
    pbuf = diag.buffer_prob(pev, now, probes, bpp) if probes else {}
    dec = decide.decide(cfg, chans, feats, probes, a.strategy, recov, {c["id"]: c["weight"] for c in chans}, tmap, pbuf=pbuf or None, newcomers=decide.newcomer_ids(cfg, chans, allh, __import__("datetime").datetime.now().astimezone().isoformat()))
    alt = {s: decide.decide(cfg, chans, feats, probes, s, recov, {c["id"]: c["weight"] for c in chans}, tmap, pbuf=pbuf or None)["target"] for s in decide.STRATEGIES} if a.compare else None
    wat = watch.run(cfg, db, probe, lambda ids: db.logs(cfg, ids, max(cfg["windows_min"]))) if not a.no_probe else {}   # v1.13 watch list (record only)
    old = {c["id"]: c["weight"] for c in chans}
    zleak = diag.zero_weight_leak(allh, old, feats, now, **(cfg.get("zero_leak") or {}))   # v1.80 diagnostics
    _sf = os.path.join(HERE, (cfg.get("shadow") or {}).get("file", "data/shadow.jsonl"))
    _sr = [json.loads(l) for l in open(_sf) if l.strip()] if os.path.exists(_sf) else []
    flap = diag.shadow_flap([r for r in _sr if r.get("mode") == "shadow"], now, **(cfg.get("flap") or {}))   # v1.82 diagnostics
    pflip = diag.probe_flip(hist_all, None if reused else probes, [c["id"] for c in chans], **(cfg.get("probe_flip") or {}))   # v1.86 diagnostics (not counted again when probes are reused)
    _rent = {k: v for k, v in (cfg.get("reenable") or {}).items() if not k.startswith("_")}
    _up = {}
    for _k, _e in _rent.items():   # v1.83: direct upstream probe (read-only; the key never leaves the production host)
        if (_e or {}).get("upstream_probe") and not a.no_probe:
            from autoweight import upstream as _upm
            _res = _upm.probe(cfg, int(_k), (_e or {}).get("upstream_n", 2))
            _up[int(_k)] = dict(_upm.verdict(_res, (_e or {}).get("upstream_n", 2), (_e or {}).get("upstream_max_ttft", 20.0)), results=_res)
            print(f"UPSTREAM #{_k}", _up[int(_k)]["classes"], [r.get("ttft") for r in _res])
    renb = diag.reenable_check(_rent, chans, feats, now, upstream=_up)   # v1.81 diagnostics
    tgl = None
    tc_ = cfg.get("toggle") or {}
    if tc_.get("enabled") and not a.no_probe:   # v1.90 autonomous on/off: direct tests of disabled channels + streak counting + floors (decide only; writes via --apply-toggle)
        from autoweight import toggle as TG, upstream as _upm
        pt = TG.P(cfg)
        lastt = next((r for r in reversed(hist_all) if r.get("toggle")), None)
        reuse_t = a.reuse_probe and lastt and (now - datetime.fromisoformat(lastt["ts"]).timestamp()) / 60 <= a.reuse_probe
        dis = db.disabled_channels(cfg)
        tests = {}
        for c in dis:
            prevd = ((lastt or {}).get("toggle") or {}).get("disabled", {}).get(str(c["id"])) if reuse_t else None
            tests[c["id"]] = prevd["results"] if prevd else _upm.probe(cfg, c["id"], pt["n_test"])
        # v2.02: this round's record is built by TG.formal_current (fixes err60 being dropped since v1.96, which kept the persistent-error streak at 0)
        fm = TG.formal_from_rounds(hist_all, pt["real_n60"]) + [TG.formal_current(now, dec["scores"], feats, chans, probes, pt["real_n60"])]
        bup = None
        if pt.get("upstream_baseline") and tests:   # v1.95 same-path direct tests of the bottom third
            _bot = TG.bottom_third({k: v.get("score") for k, v in dec["scores"].items() if k in set(ids)})
            prevb = ((lastt or {}).get("toggle") or {}).get("bottom_up_tests") if reuse_t else None
            bup = {}
            for _b in _bot:
                bup[_b] = (prevb or {}).get(str(_b)) or _upm.probe(cfg, _b, pt["n_base_test"])
                print(f"TOGGLE-BASE-UP #{_b} ttft={[r.get('ttft') for r in bup[_b]]} total={[r.get('total') for r in bup[_b]]}")
        tgl = TG.plan(cfg, ids, tests, TG.history_from_rounds(hist_all, now), fm, bottom_up_tests=bup)
        if bup is not None: tgl["bottom_up_tests"] = {str(k): v for k, v in bup.items()}
        tgl["reused_test"] = bool(reuse_t)
        for k, v in tgl["disabled"].items():
            print(f"TOGGLE-TEST #{k} pass={v['pass']} streak={v['streak']} enable={v['enable']} ttft={[r.get('ttft') for r in v['results']]} total={[r.get('total') for r in v['results']]} why={v['why']}")
            print(f"  site pass={v.get('pass_site')} {v.get('why_site')} | same-path pass={v.get('pass_up')} {v.get('why_up')} | decided by {v.get('decided_by')}")
        print(f"TOGGLE base_up={tgl.get('base_up')} mode={tgl.get('baseline_mode')}")
        print(f"TOGGLE bottom1/3={tgl['bottom']} base={tgl['base']} actions={tgl['actions']} notify={tgl['notify']}")
        for k, v in tgl["enabled"].items():
            if v["last_streak"] or v["err_streak"] or v["why"]: print(f"TOGGLE-EN #{k} last_streak={v['last_streak']} span={v['last_span_min']} err_streak={v['err_streak']} disable={v['disable']} {v['why']}")
    system_target = dict(dec["target"])
    if a.override and a.final: sys.exit("--override and --final are mutually exclusive")
    if a.expect_system:
        drift = decide.system_drift(decide.parse_override(a.expect_system, chans, cfg), system_target)
        fin0 = decide.parse_final(a.final, chans, cfg) if a.final else None
        block = decide.blocking_drift(drift, fin0)   # v1.78: drift on manually overridden channels does not block
        if drift and not block: print(f"DRIFT (not blocking; all on manually overridden channels): {drift}")
        drift = block
        if drift and a.apply:
            print(f"ABORT: system suggestion changed {drift} (expected -> recomputed); nothing written; dry-run and review again"); sys.exit(2)
        if drift: print(f"DRIFT: system suggestion differs from expected {drift}")
    if a.final:
        fin = decide.parse_final(a.final, chans, cfg)
        if fin != system_target and not a.reason: sys.exit("--final differs from the system suggestion; --reason is required")
        dec["target"] = dict(fin)
        if fin != system_target: dec["override"] = dict(set=fin, reason=a.reason, system=system_target, final=True)
    if a.override:
        if not a.reason: sys.exit("--override requires --reason")
        ov = decide.parse_override(a.override, chans, cfg)   # only enabled target-model channels, within w_min..w_max
        dec["target"].update(ov)
        dec["override"] = dict(set=ov, reason=a.reason, system=system_target)
    changed = {i: w for i, w in dec["target"].items() if old.get(i) != w}
    applied = False
    if a.apply and changed:
        db.apply_weights(cfg, dec["target"]); applied = True
    if a.apply and a.apply_toggle and tgl and tgl.get("actions"):   # v1.92
        tok, trej = db.apply_toggles(cfg, tgl["actions"], ids)
        tgl["applied"] = tok; tgl["rejected"] = trej
        print(f"TOGGLE-APPLY done={tok} rejected={trej}")
    bj = datetime.fromtimestamp(now, timezone(timedelta(hours=8)))
    rec = dict(ts=bj.isoformat(), hour=bj.hour, channels=chans, old=old, features=feats, probes=probes,
               decision=dec, compare=alt, rechecked=rechecked, probe_anomalies=anom, chanset_change=chset, buffered_streak=bstreak, recovering=recov, changed=changed, applied=applied, dry_run=not a.apply,
               review_prev=review.review(prev, feats), probes_reused=bool(reused), watch=wat, trust=tst, trust_source=tsrc, zero_leak=zleak, reenable=renb, flap=flap, probe_flip=pflip, toggle=tgl)
    os.makedirs(os.path.dirname(rf), exist_ok=True)
    with open(rf, "a") as f: f.write(json.dumps(rec, ensure_ascii=False, default=str) + "\n")
    # human-readable summary
    print(f"[{rec['ts']}] strategy={dec['strategy']} {'APPLY' if a.apply else 'DRY-RUN'} applied={applied}")
    for c in chans:
        i = c["id"]; f60, f10 = feats[i]["60m"], feats[i]["10m"]; s = dec["scores"][i]
        print(f"#{i:<3}{c['name']:<14} w {old[i]:>2} -> {dec['target'][i]:>2}  score={s['score']:.3f}  "
              f"ttft={s['ttft']} stream={s['streaming']}  "
              f"1h n={f60['n']} err={f60['err']} p50={f60['p50']} p90={f60['p90']} slow={f60['slow_rate']} conc={f60['avg_conc']}/{f60['peak_conc']}  "
              f"10m n={f10['n']} err={f10['err']} p50={f10['p50']} p90={f10['p90']} slow={f10['slow_rate']}")
    for i, v in tst.items():
        print(f"TRUST[{rec.get('trust_source','rounds')}] #{i}: {v['prev']} -> {v['trust']} sev={v['sev']} {'; '.join(v['why'])}" + ("" if (cfg.get('trust') or {}).get('enabled') else " (recorded only, not used for scoring)"))
    if reused: print(f"PROBE-REUSE: reusing the previous round's probes from within {a.reuse_probe:g} min")
    for n in dec["notes"]: print("NOTE:", n)
    if dec.get("override"): print(f"OVERRIDE: system={system_target} final={dec['target']} reason={a.reason}")
    if rechecked: print("RECHECK:", rechecked)
    for i, ks in anom.items(): print(f"ANOMALY #{i}: {','.join(ks)}")
    if chset and (chset['added'] or chset['removed']): print(f"CHANSET: added={chset['added']} removed={chset['removed']}")
    for i, z in zleak.items(): print(f"LEAK #{i}: weight 0 for {z['age_min']:.0f} min, still {z['n10']} requests in 10m ({z['err10']} errors), {z['n60']} in 1h ({z['err60']} errors) -- sticky old sessions; lowering weight cannot stop them; the operator decides whether to disable")
    for i, f in pflip.items(): print(f"PROBEFLIP #{i}: {f['flips']} of the last {f['rounds']} rounds had a buffered first probe but a streaming recheck -- buffered verdict unreliable, trust real log first tokens")
    for i, f in flap.items(): print(f"FLAP #{i}: shadow suggestion changed {f['flips']} times in the last 45 min ({f['n_points']} points, values {f['values']}) -- suggestion is jittering; review and pick the stable value")
    for i, r in renb.items(): print(f"REENABLE #{i}: {'can re-enable (start weight ' + str(r['start_weight']) + ')' if r['ready'] else 'keep disabled'}; disabled {r['age_min']:.0f} min, same upstream {r['vendor']} {r['siblings']} 1h {r['sib_n60']} requests, timeouts {r['sib_timeout_60m']} (10m {r['sib_timeout_10m']}); {'; '.join(r['why'])}")
    for i, n in bstreak.items(): print(f"BUFFERED #{i}: probe buffered (non-streaming) for {n} consecutive rounds")
    for i, n in recov.items(): print(f"RECOVERING #{i}: streaming again after {n} buffered rounds" + (f", capped at {rc['cap']} this round" if rc.get("enabled") else " (observation cap disabled; recorded only)"))
    for i, w in wat.items():
        inf = w.get("info") or {}; f = w.get("f60") or {}
        print(f"WATCH #{i} {inf.get('name')} status={inf.get('status')} w={inf.get('weight')} 1h n={f.get('n')} err={f.get('err')} empty={f.get('empty')} p50={f.get('p50')} p90={f.get('p90')}  suggest_enable={w['suggest_enable']} why={w['why']}")
        for p in w["probes"]: print(f"  WATCH-PROBE #{i}: ok={p['ok']} status={p.get('status')} ttft={p.get('ttft')} total={p.get('total')} deltas={p.get('deltas')} streaming={p.get('streaming')} err={p.get('err')}")
    if alt: print("COMPARE:", json.dumps(alt))
    for i, ps in probes.items():
        for p in ps: print(f"PROBE #{i}: ok={p['ok']} status={p.get('status')} ttft={p.get('ttft')}{' (recheck)' if p.get('recheck') else ''} total={p.get('total')} deltas={p.get('deltas')} streaming={p.get('streaming')} err={p.get('err')}")

if __name__ == "__main__":
    main()
