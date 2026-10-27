"""Feature computation: per-window request count, errors, use_time median/p90, slow share, average/peak concurrency."""

def pct(xs, q):
    if not xs: return None
    s = sorted(xs); return s[min(len(s) - 1, int(q * (len(s) - 1) + 0.5))]

def norm_lat(ok, min_ct=100, ref_ct=500):
    """v1.22 length-normalised latency: only successful requests with completion_tokens >= min_ct, converted via use_time/ct to "seconds to output ref_ct tokens".
    use_time depends on both channel speed and how long an answer the user asked for (sticky sessions give channels different workloads); normalising compares speed only.
    Returns (sample count, p50, p90); sample count is 0 when ct is unknown (old CSV)."""
    v = [x["ut"] * ref_ct / x["ct"] for x in ok if x.get("ct") is not None and x["ct"] >= min_ct]
    return len(v), (round(pct(v, .5), 2) if v else None), (round(pct(v, .9), 2) if v else None)

def log_stream(ok, min_chunks=5, buf_span_s=0.3):
    """v1.56 streaming evidence from real traffic: among successful requests with frt_version=2 and >= min_chunks content chunks, the share whose content span < buf_span_s (flushed at once).
    Returns (sample count, buffered share); (0, None) without samples. Read-only diagnostics / optional veto of a probe "buffered" verdict."""
    v = [x for x in ok if x.get("chunks") is not None and x["chunks"] >= min_chunks and x.get("span") is not None]
    if not v: return 0, None
    return len(v), round(sum(1 for x in v if x["span"] < buf_span_s) / len(v), 4)

def log_stream_veto(probes, f, lv, win="60m", use=None):
    """v1.56: the probe says "buffered" but real traffic proves streaming (ls_n >= min_n and ls_buf <= max_buf in window win) -> True (veto the verdict).
    lv = config.log_stream_veto; returns False when disabled or evidence is insufficient.
    v1.69 scope: both (default, veto for score and trust) / trust (only skip the trust buffered event, score still penalised) / score (score only); use = caller "score"/"trust"."""
    if not lv or not lv.get("enabled") or not probes: return False
    scope = lv.get("scope", "both")
    if use and scope != "both" and scope != use: return False
    vals = [p.get("streaming") for p in probes if p.get("ok")]
    if not vals or any(v is True for v in vals) or not any(v is False for v in vals): return False
    w = (f or {}).get(win) or {}
    return (w.get("ls_n") or 0) >= lv.get("min_n", 20) and w.get("ls_buf") is not None and w["ls_buf"] <= lv.get("max_buf", 0.2)

CLIENT_EXCLUDE = (408, 429)

TIMEOUT_SC = (524, 504, 408)

def is_client_err(x):
    """v1.58: type=5 with an upstream 4xx (except 408 timeout and 429 rate limit) = a problem with the client's own request (400 bad params, 413 too large...), not a channel fault."""
    sc = x.get("sc")
    return x["type"] == 5 and sc is not None and 400 <= sc < 500 and sc not in CLIENT_EXCLUDE

def cross_channel_tokens(rows, t0, t1, min_ch=2):
    """v1.71: tokens that hit client errors (is_client_err) on >= min_ch different channels within [t0,t1].
    The error follows the user (the same token gets 403/400 on any channel, e.g. session blocked by a safety policy or content moderation), so it is a user problem, not a channel fault."""
    by = {}
    for x in rows:
        if t0 <= x["t"] <= t1 and x.get("tok") is not None and is_client_err(x):
            by.setdefault(x["tok"], set()).add(x["ch"])
    return {t for t, chs in by.items() if len(chs) >= min_ch}

def drop_client_errors(feats, field="err_client"):
    """v1.58 score.client_err: replace each window's n/err/err_rate with the client-error-free definition (raw values kept in n_raw/err_raw/err_rate_raw).
    feats = {ch: {"60m": w, "10m": w, ...}}; windows without err_client (old records) are kept as-is. Modifies in place and returns."""
    for f in feats.values():
        for k, w in f.items():
            if not (isinstance(w, dict) and k.endswith("m") and w.get(field) is not None and "err_raw" not in w): continue
            c = min(w[field], w["err"] or 0)
            w.update(n_raw=w["n"], err_raw=w["err"], err_rate_raw=w["err_rate"])
            w["n"] = w["n"] - c; w["err"] = w["err"] - c
            w["err_rate"] = round(w["err"] / w["n"], 4) if w["n"] else None
    return feats

def err_burst(r):
    """v1.72: burst shape of errors in a window (error count, first-to-last error span in s, successes after the last error). (0, None, None) without errors."""
    et = sorted(x["t"] for x in r if x["type"] == 5)
    if not et: return 0, None, None
    return len(et), et[-1] - et[0], sum(1 for x in r if x["type"] == 2 and x["t"] > et[-1])

def recover_errors(feats, rc):
    """v1.72 score.err_recover: when the window's errors are one short burst within span_s seconds followed by >= min_ok successes (channel recovered),
    the burst only counts keep x towards err/err_rate (raw kept in err_pre_rec). Persistent faults (long span) or unrecovered ones are not discounted. Modifies in place and returns."""
    if not rc or not rc.get("enabled"): return feats
    for f in feats.values():
        for k, w in f.items():
            if not (isinstance(w, dict) and k.endswith("m")) or "err_pre_rec" in w: continue
            if not w.get("err") or w.get("eb_span") is None or w.get("eb_ok_after") is None: continue
            if w["eb_span"] > rc.get("span_s", 60) or w["eb_ok_after"] < rc.get("min_ok", 10): continue
            keep = int(round(w["err"] * rc.get("keep", 0.3)))
            w["err_pre_rec"] = w["err"]; w["err"] = keep
            w["err_rate"] = round(keep / w["n"], 4) if w["n"] else None
    return feats

def window(rows, now, minutes, slow_s, ln=None):
    r = [x for x in rows if x["t"] >= now - minutes * 60]
    ok = [x for x in r if x["type"] == 2]
    ut = [x["ut"] for x in ok]
    n, err = len(r), sum(1 for x in r if x["type"] == 5)
    # Concurrency: each request occupies [t-ut, t]; average = total occupied seconds / window seconds; peak sampled every 10s
    span = minutes * 60
    avg_c = sum(ut) / span if span else 0
    peak = 0
    for s in range(now - span, now, 10):
        peak = max(peak, sum(1 for x in ok if x["t"] - x["ut"] <= s <= x["t"]))
    ctok = [x["ct"] for x in ok if x.get("ct") is not None]
    # v1.8: "empty successes" (type=2 with completion_tokens=0, the user got an empty reply). None when ct is unknown (old CSV); not used for scoring
    empty = sum(1 for x in ok if x.get("ct") == 0) if ok and all(x.get("ct") is not None for x in ok) else None
    ln = ln or {}
    nn, n50, n90 = norm_lat(ok, ln.get("min_ct", 100), ln.get("ref_ct", 500))
    lt = [x["frt"] for x in ok if x.get("frt") is not None]   # v1.53 log first-token time (frt_version=2, seconds)
    ls_n, ls_buf = log_stream(ok)
    # v1.58: client error count (needs the sc field; None for old snapshots/CSV)
    has_sc = any("sc" in x for x in r if x["type"] == 5) or not any(x["type"] == 5 for x in r)
    err_client = sum(1 for x in r if is_client_err(x)) if has_sc else None
    eb_n, eb_span, eb_ok = err_burst(r)
    # v1.79: timeout-type errors (524/504/408, users often wait ~100s for nothing), and a slow rate that also counts them as slow (used by score.timeout_slow)
    err_timeout = sum(1 for x in r if x["type"] == 5 and x.get("sc") in TIMEOUT_SC) if has_sc else None
    slow_ok = sum(1 for u in ut if u > slow_s)
    den_t = len(ut) + (err_timeout or 0)
    slow_rate_t = round((slow_ok + (err_timeout or 0)) / den_t, 4) if den_t and err_timeout is not None else None
    return dict(err_timeout=err_timeout, slow_rate_t=slow_rate_t, eb_span=eb_span, eb_ok_after=eb_ok, err_client=err_client, lt_n=len(lt), lt50=pct(lt, .5), lt90=pct(lt, .9), ls_n=ls_n, ls_buf=ls_buf, n=n, err=err, err_rate=round(err / n, 4) if n else None,
                empty=empty, empty_rate=round(empty / n, 4) if n and empty is not None else None,
                p50=pct(ut, .5), p90=pct(ut, .9),
                slow_rate=round(sum(1 for u in ut if u > slow_s) / len(ut), 4) if ut else None,
                avg_conc=round(avg_c, 2), peak_conc=peak,
                ctok_p50=pct(ctok, .5), nn=nn, np50=n50, np90=n90)

def _bucket(ct, edges=(150, 400, 900)):
    for k, e in enumerate(edges):
        if ct < e: return k
    return len(edges)

def matched_ratios(rows, t0, t1, min_n=5):
    """v1.52 workload-match ratio (self-consistent within the window): successful requests with ct>0 are bucketed by output tokens; baseline = all channels' use_time p50 in the same bucket.
    For each channel take p50/p90 of per-request use_time/baseline, then divide by the global ratio p50/p90 (global = 1).
    <1 means faster for the same workload. Returns {ch: (n, r50, r90)}; channels with < min_n samples are omitted."""
    ok = [x for x in rows if t0 <= x["t"] < t1 and x["type"] == 2 and (x.get("ct") or 0) > 0]
    pool = {}
    for x in ok: pool.setdefault(_bucket(x["ct"]), []).append(x["ut"])
    base = {b: pct(v, .5) for b, v in pool.items()}
    by, allr = {}, []
    for x in ok:
        bb = base.get(_bucket(x["ct"]))
        if not bb: continue
        r = x["ut"] / bb; by.setdefault(x["ch"], []).append(r); allr.append(r)
    if not allr: return {}
    g50, g90 = pct(allr, .5) or 1, pct(allr, .9) or 1
    return {ch: (len(v), round(pct(v, .5) / g50, 4), round(pct(v, .9) / g90, 4)) for ch, v in by.items() if len(v) >= min_n}

def user_rows(cfg, rows):
    """v1.5: drop logs produced by the probe itself (config.probe_token_ids = token ids of the probe key, matched on logs.token_id).
    Backtest evaluation always uses this (real user experience only). Old snapshots/CSV without tok are kept as-is."""
    ex = set(cfg.get("probe_token_ids") or [])
    return [x for x in rows if x.get("tok") not in ex] if ex else rows

def traffic_mix(rows, now, minutes, probe_ids):
    """v1.6 diagnostics (not used for scoring): real user requests, distinct tokens, and the largest token share in the window.
    Detects "traffic carried by a single sticky session (prompt_cache_key)": then actual traffic share decouples from weight share,
    and weights mostly affect new sessions. Returns None for old data without tok."""
    r = [x for x in rows if x["t"] >= now - minutes * 60]
    if any("tok" not in x for x in r):
        return None
    u = [x for x in r if x["tok"] not in set(probe_ids or [])]
    cnt = {}
    for x in u:
        cnt[x["tok"]] = cnt.get(x["tok"], 0) + 1
    return dict(n_user=len(u), n_probe=len(r) - len(u), tokens=len(cnt),
                top_tok_share=round(max(cnt.values()) / len(u), 3) if u else None)

def build(cfg, chans, rows, now):
    # Whether decision features drop probe logs is controlled by score.exclude_probe_rows;
    # backtest: dropping them made rank slightly worse (probe 429s/slowness are real signals too), default false
    rr_all = {}
    for x in rows:
        rr_all.setdefault(x["ch"], []).append(x)
    if cfg.get("score", {}).get("exclude_probe_rows"):
        rows = user_rows(cfg, rows)
    out = {}
    for c in chans:
        rr = [x for x in rows if x["ch"] == c["id"]]
        out[c["id"]] = {f"{m}m": window(rr, now, m, cfg["slow_s"], cfg.get("score", {}).get("len_norm")) for m in cfg["windows_min"]}
        out[c["id"]]["mix_60m"] = traffic_mix(rr_all.get(c["id"], []), now, 60, cfg.get("probe_token_ids"))
    # v1.52: workload-match ratio (same-window, same-bucket baseline across all channels), used by score.workload_match
    for m in cfg["windows_min"]:
        mr = matched_ratios(rows, now - m * 60, now + 1, (cfg.get("score", {}).get("workload_match") or {}).get("min_n", 5))
        for cid, v in mr.items():
            if cid in out: out[cid][f"{m}m"].update(mr_n=v[0], mr50=v[1], mr90=v[2])
    # v1.71: cross-channel same-token client errors err_user (same token with client errors on >= 2 channels within 60m)
    ce = cfg.get("score", {}).get("client_err") or {}
    xt = cross_channel_tokens(rows, now - 60 * 60, now + 1, ce.get("min_ch", 2))
    for c in chans:
        for m in cfg["windows_min"]:
            w = out[c["id"]][f"{m}m"]
            if w.get("err_client") is None: w["err_user"] = None; continue
            w["err_user"] = sum(1 for x in rows if x["ch"] == c["id"] and x["t"] >= now - m * 60 and x.get("tok") in xt and is_client_err(x))
    if ce.get("enabled"):
        drop_client_errors(out, "err_user" if ce.get("mode") == "cross_channel" else "err_client")
    recover_errors(out, cfg.get("score", {}).get("err_recover"))   # v1.72
    return out
