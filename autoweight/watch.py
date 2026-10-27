"""v1.13 watch list: for disabled / non-scheduled channels, run read-only log stats + real probes every round.
Record only: no scoring, no DB writes. Used to decide whether to suggest re-enabling (the operator always decides)."""
from . import features

def channel_info(cfg, db, ids):
    """Read-only: name / status / weight / priority of watched channels."""
    if not ids: return []
    sql = f"select id, name, status, weight, priority from channels where id in ({','.join(str(int(i)) for i in ids)}) order by id;"
    return [dict(id=int(r[0]), name=r[1], status=int(r[2]), weight=int(r[3] or 0), priority=int(r[4] or 0)) for r in db.ro(cfg, sql)]

def verdict(probes, feat60, crit):
    """Whether re-enabling can be suggested: all probes succeed, real streaming, first token <= max_ttft, and if there is 1h traffic, error/empty rates within limits.
    Returns (bool, reasons). Failed probes (including rejections after disabling) honestly yield False."""
    why = []
    if not probes: return False, ["no probes"]
    for p in probes:
        if not p.get("ok"): why.append(f"probe failed status={p.get('status')} deltas={p.get('deltas')}")
        elif p.get("streaming") is False: why.append("buffered (non-streaming)")
        elif p.get("ttft") is not None and p["ttft"] > crit["max_ttft"]: why.append(f"first token {p['ttft']}s > {crit['max_ttft']}s")
    n = (feat60 or {}).get("n") or 0
    if n >= crit["min_n"]:
        if (feat60.get("err_rate") or 0) > crit["max_err"]: why.append(f"1h error rate {feat60['err_rate']}")
        if (feat60.get("empty_rate") or 0) > crit["max_empty"]: why.append(f"1h empty-success rate {feat60['empty_rate']}")
    ok_streak = all(p.get("ok") and p.get("streaming") is True for p in probes)
    if not ok_streak and not why: why.append("streaming undecidable")
    return (not why), why

def run(cfg, db, probe_mod, now_rows_fn):
    """v1.60: channels no longer in the DB are not probed (recorded as missing). For disabled channels (status!=1) the site returns 403
    "channel disabled", so probing wastes requests and is always False; disabled channels are not probed by default and suggest_enable=None
    (undecidable), only 1h logs are read. watch.probe_disabled=true restores the old behaviour."""
    w = cfg.get("watch", {})
    ids = w.get("ids", [])
    if not w.get("enabled") or not ids: return {}
    info = {c["id"]: c for c in channel_info(cfg, db, ids)}
    live = [i for i in ids if i in info]
    now, rows = now_rows_fn(live) if live else (None, [])
    feats = features.build(cfg, [info[i] for i in live], rows, now) if live else {}
    key = probe_mod.load_key(cfg)
    out = {}
    for i in ids:
        if i not in info:
            out[i] = dict(info=None, probes=[], f60=None, f10=None, suggest_enable=False,
                          why=["channel does not exist (deleted); remove it from watch.ids"], missing=True)
            continue
        f60 = (feats.get(i) or {}).get("60m")
        auto = w.get("auto_skip", False)
        if auto and info[i]["status"] != 1 and (cfg.get("toggle") or {}).get("enabled"):
            # v1.94: disabled channels are covered by the autonomous on/off direct upstream test (see rounds.toggle); no placeholder "undecidable" verdict here
            out[i] = dict(info=info[i], probes=[], f60=f60, f10=(feats.get(i) or {}).get("10m"),
                          suggest_enable=None, why=["disabled; covered by the on/off direct test (see toggle)"], covered_by="toggle")
            continue
        n60 = (f60 or {}).get("n") or 0
        if auto and info[i]["status"] == 1 and n60 >= w.get("auto_skip_min_n60", 30):
            # v1.94: enabled with enough real 1h requests; the main probe and logs already cover it, skip the extra site probe
            out[i] = dict(info=info[i], probes=[], f60=f60, f10=(feats.get(i) or {}).get("10m"),
                          suggest_enable=None, why=[f"enabled with {n60} real requests in 1h; covered by the main probe"], covered_by="main")
            continue
        if info[i]["status"] != 1 and not w.get("probe_disabled", False):
            out[i] = dict(info=info[i], probes=[], f60=f60, f10=(feats.get(i) or {}).get("10m"),
                          suggest_enable=None, why=["disabled; the site refuses probes, undecidable (operator decides whether to enable temporarily for testing)"])
            continue
        ps = [probe_mod.probe_once(cfg, key, i) for _ in range(w.get("n_probe", 1))] if key else [dict(ok=False, err="no key")]
        ok, why = verdict(ps, f60, w["criteria"])
        out[i] = dict(info=info.get(i), probes=ps, f60=f60, f10=(feats.get(i) or {}).get("10m"), suggest_enable=ok, why=why)
    return out
