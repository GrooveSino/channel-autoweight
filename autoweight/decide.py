"""Decision: score -> strategy assigns weights -> soft caps / guards. Strategies are pluggable so they can be compared with data."""
from datetime import datetime, timezone

def stream_verdict(probes):
    """Three-state streaming verdict: any probe True -> True; else any False -> False; all None (short answers, undecidable) -> None.
    Do not use any(): with all None, any() returns False and would treat "undecidable" as "non-streaming" and penalise it (a v1.0 bug)."""
    vals = [p.get("streaming") for p in probes or [] if p.get("ok")]
    if any(v is True for v in vals): return True
    if any(v is False for v in vals): return False
    return None

def ttft_of(probes, cfg):
    ok = [p["ttft"] for p in probes or [] if p.get("ok")]
    return (min(ok) if ok else None), (stream_verdict(probes) if ok else None)

def shrink(v, n, prior, k):
    """Small-sample shrinkage (v1.3): pull the latency of low-sample channels towards the pooled value of all channels, (n*v + k*prior)/(n+k).
    The fewer samples n, the closer to prior, so a lucky fast value from 6 requests cannot push a channel to first place. Returned unchanged when k=0 or there is no prior."""
    if not k or prior is None or v is None: return v
    return (n * v + k * prior) / (n + k)

def pooled(feats, win):
    """Request-weighted mean of each channel's p50/p90 in this window, used as the shrinkage prior."""
    num50 = num90 = den = 0
    for f in feats.values():
        w = f.get(win)
        if not w or not w["n"] or w["p50"] is None: continue
        num50 += w["n"] * w["p50"]; num90 += w["n"] * w["p90"]; den += w["n"]
    return (num50 / den, num90 / den) if den else (None, None)

def cmp_subs(feats, win, s):
    """v1.30 workload comparability guard (score.cmp_guard): when a channel's median output tokens in the window are far below its peers (e.g. it only served a few short title-generation requests),
    its low use_time only means "small jobs", not a fast channel. Its p50/p90 for that window are replaced with the request-pooled value of "comparable" channels (ctok_p50 above threshold),
    making the latency term neutral; errors/slow requests/first token still count. Returns {id: (p50, p90)}; {} when disabled or no comparable peers."""
    g = s.get("cmp_guard") or {}
    if not g.get("enabled"): return {}
    mn = s["min_n_window"]
    ws = {i: f.get(win) for i, f in feats.items()}
    ct = {i: w["ctok_p50"] for i, w in ws.items() if w and w["n"] >= mn and w.get("ctok_p50") is not None and w["p50"] is not None}
    if len(ct) < 2: return {}
    thr = max(g.get("min_ctok", 60), g.get("ratio", 0.25) * max(ct.values()))
    good = {i: ws[i] for i in ct if ct[i] >= thr}
    bad = [i for i in ct if ct[i] < thr]
    if not good or not bad: return {}
    den = sum(w["n"] for w in good.values())
    p50 = sum(w["n"] * w["p50"] for w in good.values()) / den
    p90 = sum(w["n"] * w["p90"] for w in good.values()) / den
    return {i: (p50, p90) for i in bad}

def _slow(w, s):
    """v1.79: with score.timeout_slow=true, timeout-type errors (524/504/408) also count as slow (failed + wasted wait); otherwise the plain slow_rate."""
    if s.get("timeout_slow") and w.get("slow_rate_t") is not None: return w["slow_rate_t"]
    return w["slow_rate"]

def score_window(w, ttft, s, prior=(None, None), sub=None):
    if not w or w["n"] < s["min_n_window"] or w["p50"] is None: return None
    k = s.get("shrink_k", 0)
    p50, p90 = shrink(w["p50"], w["n"], prior[0], k), shrink(w["p90"], w["n"], prior[1], k)
    if sub is not None:   # v1.30 cmp_guard: workload not comparable, use the comparable peers' pooled latency
        p50, p90 = sub
        lat = s["a_p50"] * p50 + s["b_p90"] * p90 + s["c_ttft"] * ttft
        bad = min(1.0, (w["err_rate"] or 0) + s.get("empty_weight", 0) * (w.get("empty_rate") or 0))
        return (1 - bad) ** s.get("err_exp", 2) * (1 - (_slow(w, s) or 0)) * s["t_ref"] / (s["t_ref"] + lat)
    # v1.22: with len_norm.enabled, use length-normalised latency (equivalent seconds for ref_ct output tokens) instead of raw use_time;
    # fall back to raw values when normalised samples < len_norm.min_n; a mix between 0..1 blends the two
    # v1.52: with workload_match.enabled, latency becomes "workload-match ratio x pooled p50/p90" (blended by mix),
    # removing the bias sticky sessions introduce into raw use_time (some channels get short jobs, others long ones); prior is the pooled value
    wm = s.get("workload_match") or {}
    if wm.get("enabled") and w.get("mr50") is not None and (w.get("mr_n") or 0) >= wm.get("min_n", 5) and prior[0] is not None:
        m = wm.get("mix", 1.0)
        p50 = (1 - m) * p50 + m * w["mr50"] * prior[0]; p90 = (1 - m) * p90 + m * w["mr90"] * prior[1]
    ln = s.get("len_norm") or {}
    if ln.get("enabled") and (w.get("nn") or 0) >= ln.get("min_n", 5) and w.get("np50") is not None:
        m = ln.get("mix", 1.0)
        p50 = (1 - m) * p50 + m * w["np50"]; p90 = (1 - m) * p90 + m * w["np90"]
    lat = s["a_p50"] * p50 + s["b_p90"] * p90 + s["c_ttft"] * ttft
    # v1.8: empty successes (type=2 with completion_tokens=0) count towards the bad-request rate with empty_weight; empty_weight=0 equals v1.7
    bad = min(1.0, (w["err_rate"] or 0) + s.get("empty_weight", 0) * (w.get("empty_rate") or 0))
    return (1 - bad) ** s.get("err_exp", 2) * (1 - (_slow(w, s) or 0)) * s["t_ref"] / (s["t_ref"] + lat)

def err_burst_factor(short, s):
    """Fast error response (v1.2): when the 10m window has errors, multiply the total score by (1-err_10m)^err_10m_exp.
    Unlike score_window, min_n_window is not required (off-peak 10m windows often have 1-4 requests and errors must still act immediately);
    only n >= err_10m_min_n. err_10m_exp=0 disables it."""
    if not short or short["n"] < s.get("err_10m_min_n", 2) or not short["err"]: return 1.0
    return (1 - short["err_rate"]) ** s.get("err_10m_exp", 0)

def slow_burst_factor(fs, i, cfg):
    """v1.36 sudden-slowdown detection: when the 10m length-normalised latency np50 (equivalent seconds for ref_ct output tokens) is clearly above
    the peers' 1h normalised np50 median (more than ratio times) and the 10m normalised sample count nn >= min_nn, multiply the score by
    max(floor, ratio*ref/np50)^exp. It uses throughput rather than use_time, so long answers are not misjudged; it only looks at 10m, so recovery is fast too."""
    sb = (cfg["score"].get("slow_burst") or {})
    if not sb.get("enabled"): return 1.0, None
    wl, ws = f"{cfg['windows_min'][0]}m", f"{cfg['windows_min'][1]}m"
    sh = fs[i].get(ws) or {}
    if (sh.get("nn") or 0) < sb.get("min_nn", 4) or sh.get("np50") is None: return 1.0, None
    peers = sorted(fs[j][wl]["np50"] for j in fs if j != i and fs[j].get(wl, {}).get("np50") is not None
                   and (fs[j][wl].get("nn") or 0) >= sb.get("peer_min_nn", 5))
    if not peers: return 1.0, None
    ref = peers[len(peers) // 2] if len(peers) % 2 else (peers[len(peers) // 2 - 1] + peers[len(peers) // 2]) / 2
    r = sb.get("ratio", 2.5)
    if sh["np50"] <= r * ref: return 1.0, None
    f = max(sb.get("floor", 0.2), r * ref / sh["np50"]) ** sb.get("exp", 1.0)
    return f, f"10m normalised np50 {sh['np50']:.1f}s > {r}x peers {ref:.1f}s"

def blend_log_ttft(t, probe_ttft, w60, s):
    """v1.53 score.log_ttft: blend the 1h window's frt_version=2 log first-token p50 (real users, from the first content delta) with the probe first token:
    t = (1-mix)*probe + mix*log_p50; without a probe use log p50 directly. Returned unchanged with < min_n log samples or when disabled."""
    g = s.get("log_ttft") or {}
    if not g.get("enabled"): return t
    n, v = w60.get("lt_n") or 0, w60.get("lt50")
    if v is None or n < g.get("min_n", 10): return t
    if probe_ttft is None: return v
    m = g.get("mix", 0.5)
    # v1.75 adapt_full_n: the more log first-token samples, the more the log is trusted; mix rises linearly to 1.0 as n goes from min_n to full_n (0 disables)
    fn = g.get("adapt_full_n", 0) or 0
    if fn > g.get("min_n", 10):
        m = m + (1 - m) * min(1.0, (n - g.get("min_n", 10)) / (fn - g.get("min_n", 10)))
    return (1 - m) * t + m * v

def score(cfg, f, probes, priors=None, subs=None, pbuf=None):
    s = cfg["score"]; priors = priors or {}; subs = subs or {}
    ttft, streaming = ttft_of(probes, cfg)
    t = ttft if ttft is not None else s["ttft_missing_s"]
    from .features import log_stream_veto
    if log_stream_veto(probes, f, cfg.get("log_stream_veto"), f"{cfg['windows_min'][0]}m", use="score"):
        # v1.56: real traffic streams -> no penalty for a probe "buffered" verdict, and the probe first token (= total time) is unreliable; use log p50 only
        streaming = None
        t = blend_log_ttft(t, None, f.get(f"{cfg['windows_min'][0]}m") or {}, s)
    else:
        t = blend_log_ttft(t, ttft, f.get(f"{cfg['windows_min'][0]}m") or {}, s)
    long_, short = f[f"{cfg['windows_min'][0]}m"], f[f"{cfg['windows_min'][1]}m"]
    wl, ws = f"{cfg['windows_min'][0]}m", f"{cfg['windows_min'][1]}m"
    a = score_window(long_, t, s, priors.get(wl, (None, None)), subs.get(wl))
    b = score_window(short, t, s, priors.get(ws, (None, None)), subs.get(ws))
    if a is None and b is None:     # no log samples: rely on the probe only
        sc = s["t_ref"] / (s["t_ref"] + s["c_ttft"] * t) * 0.5 if ttft is not None else 0.0
    elif b is None: sc = a
    elif a is None: sc = b
    else:
        # v1.27: 10m weight adapts to sample size w = w_10m * n10/(n10 + w10_n0); with few 10m samples trust the short window less, damping off-peak jitter. w10_n0=0 equals v1.26
        w10 = s["w_10m"]; n0 = s.get("w10_n0", 0)
        if n0 > 0: w10 = w10 * short["n"] / (short["n"] + n0)
        # v1.42: asymmetric 10m weight (slow rise / fast fall applied to the short window): when 10m is worse than 1h use full weight (bad news acts immediately);
        # when 10m is better than 1h discount by sample size w*n10/(n10+w10_good_n0), so small-sample "good news" needs more evidence.
        # Motivation: a channel with only 10 requests in 10m looked great and was suggested for the top tier, then its real p90 turned out very slow. w10_good_n0=0 disables.
        g0 = s.get("w10_good_n0", 0) or 0
        if g0 > 0 and b > a: w10 = w10 * short["n"] / (short["n"] + g0)
        sc = (1 - w10) * a + w10 * b
    sc *= err_burst_factor(short, s)
    # v1.38: small-sample confidence discount n60/(n60+conf_k): channels with few 1h samples have volatile scores
    # (a channel with a handful of samples was once suggested for the top tier and then performed worst). conf_k=0 disables, identical to v1.37.
    ck = s.get("conf_k", 0) or 0
    if ck > 0: sc *= (long_.get("n") or 0) / ((long_.get("n") or 0) + ck)
    # v1.39: concurrency load discount: when 10m average concurrency > conc_c0, score x 1/(1+conc_k*(conc10-conc_c0)).
    # Sticky sessions can push a channel's real share far above its weight share, so new sessions would queue and slow down; conc_k=0 disables, identical to v1.38.
    kk = s.get("conc_k", 0) or 0
    if kk > 0:
        c10 = short.get("avg_conc")
        if c10 is not None and c10 > s.get("conc_c0", 2.0): sc /= 1 + kk * (c10 - s.get("conc_c0", 2.0))
    bp = s.get("buffer_prob") or {}
    if bp.get("enabled") and pbuf is not None:
        # v1.31: continuous penalty by buffering probability 1-(1-penalty)*p instead of a 0/1 verdict from one probe
        sc *= 1 - (1 - s["nonstream_penalty"]) * pbuf
    elif streaming is False: sc *= s["nonstream_penalty"]
    # v1.8: all probes failed this round (e.g. 200 but zero text deltas) -> multiply by probe_fail_penalty; 1.0 equals v1.7
    if probes and not any(p.get("ok") for p in probes): sc *= s.get("probe_fail_penalty", 1.0)
    return round(sc, 4), ttft, streaming

# ---- Strategies: input {id: score} (already ordered by guards), output {id: target weight (float)} ----
def prev_tiers(prev, ids, p, caps=None):
    """v1.23: infer each channel's previous tier (tie/top/close/mid/low) from the previous weights. Only the ids in this allocation are considered.
    v1.40 caps={id: cap} (band_cap_aware): a channel whose previous weight equals its soft cap, with the cap above the mid tier, is classified as close
    (it is a truncated high tier, not mid). v1.23 misclassified a capped channel at 6 as mid, so band made it drop to 3 more easily."""
    pv = {i: prev[i] for i in ids if prev and i in prev}
    if len(pv) < len(ids) or not pv: return {}
    vals = list(pv.values())
    if len(ids) > 1 and max(vals) - min(vals) <= 1: return {i: "tie" for i in ids}
    top = max(vals); cw = p["top_w"] * p["close_factor"]
    out = {}
    for i, w in pv.items():
        cp = (caps or {}).get(i)
        if cp is not None and w != top and abs(w - round(cp)) < 0.5 and round(cp) > p["mid_w"] and round(cp) < cw:
            out[i] = "close"; continue
        if w == top: out[i] = "top"
        elif w >= (cw + p["mid_w"]) / 2: out[i] = "close"
        elif w > (p["mid_w"] + p["low_w"]) / 2: out[i] = "mid"
        else: out[i] = "low"
    return out

def strat_rank(order, sc, p, cfg, prev=None, caps=None):
    """band>0 (v1.23 tier hysteresis): a channel in a tier only drops when it is band below the threshold and only rises when band above it,
    avoiding suggestion jitter when the score ratio hovers around a threshold (especially with shadow runs every 5 min). band=0 is identical to the old version."""
    top = sc[order[0]] or 1e-9
    m = p.get("band", 0) or 0
    pt = prev_tiers(prev, order, p, caps if p.get("band_cap_aware") else None) if m > 0 else {}
    was_tie = bool(pt) and all(v == "tie" for v in pt.values())
    tie_thr = p["tie_ratio"] - m if was_tie else p["tie_ratio"] + (m if pt else 0)
    if all(sc[i] >= top * tie_thr for i in order):     # roughly equal -> uniform
        return {i: cfg["w_total"] / len(order) for i in order}
    rank_of = {"top": 3, "close": 2, "mid": 1, "low": 0}
    def thr(base, level, i):
        if not pt or i not in pt or pt[i] == "tie": return base
        return base - m if rank_of[pt[i]] >= level else base + m
    w = {}
    ramp = p.get("ramp", 0) or 0
    cw = p["top_w"] * p["close_factor"]
    # v1.85 low-trust leader reference: when the leader's trust < lead_trust, channels with trust >= lead_trust use "highest score among trusted channels" as the denominator
    # (ratio capped at 1), so a just-recovered low-trust channel in the lead cannot push established high-traffic channels down to mid/low. 0 disables.
    lt = p.get("lead_trust", 0) or 0
    tr = p.get("_trust") or {}
    ref = top
    if lt > 0 and tr and tr.get(order[0], 1.0) < lt:
        ts = [sc[j] for j in order if tr.get(j, 1.0) >= lt]
        if ts: ref = max(ts) or 1e-9
    for k, i in enumerate(order):
        r = sc[i] / top
        if ref is not top and tr.get(i, 1.0) >= lt: r = min(1.0, sc[i] / ref)
        if k == 0: w[i] = p["top_w"]
        # v1.88 co-leaders: channels with score ratio >= co_lead_ratio also get top_w, so two near-equal leaders do not swap 12/10 on noise (0 disables)
        elif (p.get("co_lead_ratio", 0) or 0) > 0 and r >= p["co_lead_ratio"]: w[i] = p["top_w"]
        elif ramp > 0 and not pt:
            # v1.44 continuous tiers: between mid..close interpolate mid_w->close_w linearly by score ratio; within ramp below mid interpolate low_w->mid_w.
            # Small ratio fluctuations near a threshold then change the weight slightly instead of jumping a whole tier 10<->3.
            c, md = p["close_ratio"], p["mid_ratio"]
            if r >= c: w[i] = cw
            elif r >= md: w[i] = p["mid_w"] + (cw - p["mid_w"]) * (r - md) / max(c - md, 1e-9)
            elif r >= md - ramp: w[i] = p["low_w"] + (p["mid_w"] - p["low_w"]) * (r - (md - ramp)) / ramp
            else: w[i] = p["low_w"]
        elif r >= thr(p["close_ratio"], 2, i): w[i] = p["top_w"] * p["close_factor"]
        elif r >= thr(p["mid_ratio"], 1, i): w[i] = p["mid_w"]
        else: w[i] = p["low_w"]
    return w

def strat_proportional(order, sc, p, cfg):
    tot = sum(max(sc[i], 1e-6) ** p["gamma"] for i in order)
    return {i: cfg["w_total"] * max(sc[i], 1e-6) ** p["gamma"] / tot for i in order}

def confirm_rise(raw_hist, prev, n, only=None):
    """v1.34 rise confirmation (slow rise / fast fall at the weight layer): raw_hist is the system's raw suggestions at recent time points (old->new, last = now).
    Decreases follow immediately; an increase takes full effect only if the raw suggestions at the last n points are all >= the new value, otherwise it rises to their minimum (not below the previous weight).
    A channel missing at a historical point is treated as equal to now (does not block). Returned unchanged when n<=1 or no prev.
    v1.35: when only is a set of channel ids, only those channels (e.g. 1h small-sample channels) need confirmation; others rise immediately."""
    if not raw_hist: return {}
    cur = raw_hist[-1]
    if not prev or not n or n <= 1: return dict(cur)
    win = raw_hist[-n:]
    out = {}
    for i, w in cur.items():
        pw = prev.get(i)
        if pw is None or w <= pw or (only is not None and i not in only):
            out[i] = w; continue
        lo = min(h.get(i, w) for h in win)
        out[i] = max(pw, min(w, lo))
    return out

def effective_rank_params(cfg, name, rows):
    """v1.67: return the strategy parameters actually used. For rank with low_traffic.max_total_n60>0 and the total 1h sample below it,
    override with the low_traffic keys except max_total_n60 (no low_traffic by default -> returned unchanged)."""
    prm = cfg["strategies"][name]
    lt = prm.get("low_traffic") if name == "rank" else None
    mx = (lt or {}).get("max_total_n60", 0) or 0
    if not mx: return prm
    tot = sum((r.get("n60") or 0) for r in rows.values())
    if tot >= mx: return prm
    out = dict(prm)
    for k, v in lt.items():
        if k != "max_total_n60" and not k.startswith("_"): out[k] = v
    out["_low_traffic"], out["_lt_max"] = tot, mx
    return out

STRATEGIES = {"rank": strat_rank, "proportional": strat_proportional}

def active_caps(cfg, now=None):
    now = now or datetime.now(timezone.utc)
    caps = {}
    for k, v in cfg.get("soft_caps", {}).items():
        if k.startswith("_") or not v.get("enabled", True): continue
        if v.get("until") and now >= datetime.fromisoformat(v["until"]): continue
        caps[int(k)] = v
    return caps

def relax_cap(i, base, relax, feats, rows, wins):
    """v1.25 evidence-based soft-cap relaxation (slow rise / fast fall): when a capped channel is clean in both long and short windows, its cap rises linearly with trust from cap to relax.max;
    it returns to cap immediately when any condition fails. Conditions: 1h n >= min_n; 1h/10m error rate <= err_max; 1h/10m empty-success rate <= empty_max;
    1h peak concurrency <= conc_max (high concurrency is the typical risk); 1h output-token p50 >= ctok_ratio x median of other channels (guards against "too little reasoning");
    trust >= trust_min. Returns (effective cap, reason or None). Returns base when relax is missing or enabled=false."""
    if not relax or not relax.get("enabled"): return base, None
    f = feats.get(i) or {}
    l, s_ = f.get(wins[0]) or {}, f.get(wins[1]) or {}
    why = []
    if (l.get("n") or 0) < relax.get("min_n", 50): why.append(f"1h n={l.get('n')}")
    for nm, w in (("1h", l), ("10m", s_)):
        if (w.get("err_rate") or 0) > relax.get("err_max", 0.02): why.append(f"{nm} err={w.get('err_rate')}")
        if w.get("empty_rate") is None and nm == "1h": why.append("empty-success rate unknown")
        elif (w.get("empty_rate") or 0) > relax.get("empty_max", 0.03): why.append(f"{nm} empty={w.get('empty_rate')}")
    if (l.get("peak_conc") or 0) > relax.get("conc_max", 4): why.append(f"peak concurrency={l.get('peak_conc')}")
    peers = sorted((feats[j].get(wins[0]) or {}).get("ctok_p50") for j in feats if j != i and (feats[j].get(wins[0]) or {}).get("ctok_p50"))
    if peers and l.get("ctok_p50") is not None:
        med = peers[len(peers) // 2]
        if l["ctok_p50"] < relax.get("ctok_ratio", 0.7) * med: why.append(f"ctok_p50={l['ctok_p50']}<{relax.get('ctok_ratio', 0.7)}x{med}")
    t = (rows.get(i) or {}).get("trust")
    tmin = relax.get("trust_min", 0.8)
    if t is None or t < tmin: why.append(f"trust={t}")
    if why: return base, "not relaxed: " + ", ".join(why)
    frac = min(1.0, (t - tmin) / max(1e-6, 1 - tmin))
    eff = base + (relax.get("max", base) - base) * frac
    return round(eff, 2), f"relaxed to {eff:.1f} (quality clean, trust={t})"

def hold_top(order, sc, prev, h):
    """v1.15 hysteresis: keep the previous leader (largest prev weight) first when its score >= this round's leader x (1-h), avoiding flips from threshold noise. h<=0 disables."""
    if h <= 0 or not prev: return order, None
    cand = [i for i in prev if i in sc]
    if not cand: return order, None
    pt = max(cand, key=lambda i: prev[i])
    if order[0] == pt or sc[pt] < sc[order[0]] * (1 - h): return order, None
    j = order.index(pt); order = [pt] + order[:j] + order[j + 1:]
    return order, pt

def smooth_scores(sc, prev_smooth, p):
    """v1.24 asymmetric score smoothing (multi-timescale): worse follows immediately (alpha_down, default 1 = no smoothing), better rises slowly (alpha_up).
    s_t = s_{t-1} + alpha * (raw - s_{t-1}), alpha depends on whether raw is above or below the last value. Channels without a last value use raw directly."""
    out = {}
    for i, raw in sc.items():
        ps = (prev_smooth or {}).get(i)
        if ps is None: out[i] = raw; continue
        a = p.get("alpha_up", 0.5) if raw > ps else p.get("alpha_down", 1.0)
        out[i] = round(ps + a * (raw - ps), 4)
    return out


def decide(cfg, chans, feats, probes, strategy=None, recovering=None, prev=None, trust=None, prev_smooth=None, pbuf=None, newcomers=None):
    """newcomers: v1.47 set of new channels (newcomer_ids computed from formal-round history; None = no cap, historical replays unchanged).
    prev_smooth: {id: smoothed score at the previous time point} (v1.24 score_smooth, off by default).
    trust: {id: trust} (computed by v1.18 autoweight.trust.step); with config.trust.enabled the score is multiplied by (floor+(1-floor)*trust).
    prev: {id: previous round / DB weight}, used by v1.15 hysteresis (rank.hysteresis, 0 = off by default).
    recovering: {id: previous consecutive buffered rounds} (computed by diag.recovering); these channels are capped at cfg.recovery.cap this round (v1.11 observation period)."""
    name = strategy or cfg["strategy"]
    rows, notes = {}, []
    fs = {c["id"]: feats[c["id"]] for c in chans}
    priors = {f"{m}m": pooled(fs, f"{m}m") for m in cfg["windows_min"]}
    cs = {f"{m}m": cmp_subs(fs, f"{m}m", cfg["score"]) for m in cfg["windows_min"]}
    for c in chans:
        sub = {w: v[c["id"]] for w, v in cs.items() if c["id"] in v}
        sc, ttft, st = score(cfg, feats[c["id"]], probes.get(c["id"]), priors, sub, (pbuf or {}).get(c["id"]) if pbuf else None)
        n60 = feats[c["id"]][f"{cfg['windows_min'][0]}m"]["n"]
        probe_ok = sum(1 for p in probes.get(c["id"], []) if p.get("ok"))
        rows[c["id"]] = dict(score=sc, ttft=ttft, streaming=st, n60=n60, probe_ok=probe_ok,
                             eligible_rank1=not (n60 < cfg["min_n_rank1"] and probe_ok < 1))
        if pbuf and c["id"] in pbuf: rows[c["id"]]["p_buf"] = pbuf[c["id"]]
        if sub: rows[c["id"]]["cmp_sub"] = {w: [round(x, 1) for x in v] for w, v in sub.items()}
        bf, bwhy = slow_burst_factor(fs, c["id"], cfg)
        if bwhy:
            rows[c["id"]]["slow_burst"] = round(bf, 3)
            rows[c["id"]]["score"] = round(sc * bf, 4)
            notes.append(f"#{c['id']} sudden slowdown: {bwhy}, score x{bf:.2f}")
    if trust and (cfg.get("trust") or {}).get("enabled"):
        from . import trust as _tr
        tp = _tr.params(cfg)
        for i, r in rows.items():
            if i in trust:
                r["raw_score"] = r["score"]; r["trust"] = trust[i]
                r["score"] = round(r["score"] * _tr.multiplier(trust[i], tp), 4)
    sc = {i: r["score"] for i, r in rows.items()}
    sp = cfg.get("score_smooth") or {}
    if sp.get("enabled") and prev_smooth:
        sm = smooth_scores(sc, prev_smooth, sp)
        for i in rows: rows[i]["pre_smooth"] = sc[i]; rows[i]["score"] = sm[i]
        sc = sm
    order = sorted(sc, key=lambda i: -sc[i])
    hy = cfg["strategies"].get(name, {}).get("hysteresis", 0)
    order, held = hold_top(order, sc, prev, hy)
    if held is not None: notes.append(f"hysteresis: #{held} score within {hy:.0%} of the leader, keeps first place")
    # Guard: a channel with 1h samples < min_n_rank1 and no successful probe may not rank first
    if not rows[order[0]]["eligible_rank1"]:
        for j, i in enumerate(order):
            if rows[i]["eligible_rank1"]:
                notes.append(f"#{order[0]} has too few samples to rank first; #{i} moves up")
                order.insert(0, order.pop(j)); break
    # Soft caps: truncated channels are pinned at cap, and the strategy reruns on the rest so the "best channel" slot goes to the next one
    caps = active_caps(cfg)
    wins = [f"{m}m" for m in cfg["windows_min"]]
    for i in list(caps):
        if i not in rows: continue
        eff, why = relax_cap(i, caps[i]["cap"], caps[i].get("relax"), fs, rows, wins)
        if why: rows[i]["cap_relax"] = why
        if eff != caps[i]["cap"]:
            caps[i] = dict(caps[i], cap=eff, base_cap=caps[i]["cap"])
    # v1.11 observation period: a channel that just resumed streaming after consecutive buffering does not get a high weight from one probe; cap it for one round
    rc = cfg.get("recovery", {})
    if rc.get("enabled") and recovering:
        for i, n in recovering.items():
            if i in rows and (i not in caps or caps[i]["cap"] > rc["cap"]):
                caps[i] = {"cap": rc["cap"], "reason": f"just resumed streaming after {n} buffered rounds; observe for one round"}
    # v1.46 low-trust cap: channels with trust below thr (recent fault, still recovering) get at most cap until trust slowly recovers
    tc = cfg.get("trust_cap") or {}
    if tc.get("enabled") and trust:
        for i, t in trust.items():
            # v1.61 max_n60>0: only cap low-trust channels with 1h samples < max_n60 (in low traffic a few good requests do not outweigh a recent fault)
            mx = tc.get("max_n60", 0)
            if mx and i in rows and rows[i]["n60"] >= mx: continue
            if i in rows and t < tc.get("thr", 0.6) and (i not in caps or caps[i]["cap"] > tc.get("cap", 3)):
                caps[i] = {"cap": tc.get("cap", 3), "reason": f"trust {t:.2f} < {tc.get('thr', 0.6)}" + (f" and 1h samples {rows[i]['n60']} < {mx}" if mx else "") + ", recovery cap"}
            # v1.66 ramp>0: when trust just crossed thr, do not jump back to full weight at once; within [thr, thr+ramp) the cap rises linearly from cap to top_w
            rp = tc.get("ramp", 0) or 0
            thr_, cap_ = tc.get("thr", 0.6), tc.get("cap", 3)
            if rp > 0 and i in rows and thr_ <= t < thr_ + rp:
                top = (cfg.get("strategies", {}).get("rank") or {}).get("top_w", 12)
                ce = int(round(cap_ + (top - cap_) * (t - thr_) / rp))
                if ce < top and (i not in caps or caps[i]["cap"] > ce):
                    caps[i] = {"cap": ce, "reason": f"trust {t:.2f} just crossed {thr_} (ramp {rp}), cap {ce}"}
    # v1.47 newcomer cap: channels with 1h real user requests (probe tokens removed) < min_real_n60 get at most cap.
    # The channel set itself comes from the DB (status=1 with a target-model ability), so new channels join automatically; this only prevents "first place on probe data alone".
    nc = cfg.get("newcomer") or {}
    if nc.get("enabled") and newcomers:
        for i in rows:
            if i not in newcomers: continue
            mix = (fs.get(i) or {}).get("mix_60m") or {}
            nu = mix.get("n_user")
            if nu is None: continue
            if nu < nc.get("min_real_n60", 30):
                rows[i]["newcomer_n"] = nu  # v1.89: mark newcomer identity even when a lower cap (e.g. trust cap) applies, for the exploration floor
            if nu < nc.get("min_real_n60", 30) and (i not in caps or caps[i]["cap"] > nc.get("cap", 6)):
                caps[i] = {"cap": nc.get("cap", 6), "reason": f"new channel / not enough real traffic: 1h real requests {nu} < {nc.get('min_real_n60', 30)}"}
                rows[i]["newcomer"] = nu
    # v1.76 small-sample cap: any channel (not only newcomers) with 1h real user requests < max_n60 gets at most cap.
    # Motivation: a channel with single-digit hourly requests reached first place on a few fast requests and one probe; the system kept suggesting the top tier and shadow suggestions jittered.
    ss = cfg.get("small_sample") or {}
    if ss.get("enabled") and ss.get("max_n60", 0) > 0:
        for i in rows:
            nu = ((fs.get(i) or {}).get("mix_60m") or {}).get("n_user")
            if nu is None: nu = rows[i]["n60"]
            if nu < ss["max_n60"] and (i not in caps or caps[i]["cap"] > ss.get("cap", 3)):
                caps[i] = {"cap": ss.get("cap", 3), "reason": f"small sample: 1h real requests {nu} < {ss['max_n60']}, cap {ss.get('cap', 3)}"}
                rows[i]["small_sample"] = nu
    # v1.48 concurrency capacity cap: for channels with a known concurrency limit (e.g. one that queues beyond a few concurrent requests), estimate peak total concurrency C
    # as 1h total average concurrency x peak_factor and require the traffic share w/(w+R) <= max_conc/C, i.e. w <= s*R/(1-s) (R = sum of other channels' first-pass target weights).
    # v1.67 low-traffic tiers: when the total 1h sample < low_traffic.max_total_n60, use the tier parameters in low_traffic (e.g. close_ratio).
    # At night each channel has only a few requests and the score ratio near the close threshold jumps with one or two requests; a looser threshold reduces 10<->3 jitter.
    prm_eff = effective_rank_params(cfg, name, rows)
    if prm_eff.get("_low_traffic"): notes.append(f"low-traffic tiers: 1h total samples {prm_eff['_low_traffic']} < {prm_eff['_lt_max']}, close_ratio={prm_eff['close_ratio']}")
    cc = cfg.get("capacity") or {}
    if cc.get("enabled"):
        tot = sum(((fs.get(i) or {}).get(f"{cfg['windows_min'][0]}m") or {}).get("avg_conc") or 0 for i in rows)
        C = tot * cc.get("peak_factor", 2.0)
        if C > 0:
            fn0, prm0 = STRATEGIES[name], prm_eff
            kw0 = {"prev": prev, "caps": {i: v["cap"] for i, v in caps.items()}} if name == "rank" else {}
            t0 = fn0(order, sc, prm0, cfg, **kw0)
            for k, v in cc.items():
                if k.startswith("_") or not isinstance(v, dict): continue
                i = int(k)
                if i not in rows: continue
                s_ = min(1.0, v["max_conc"] / C)
                if s_ >= 1.0: continue
                if cc.get("r_mode", "first") == "rest":
                    # v1.57: R = sum of target weights when the other channels are re-ranked without i. The old definition used t0 with i ranked first;
                    # when i is the leader the others' targets are tiny (R~5), and after truncation they are re-ranked to 12/10, so the real share is far below s (loop too tight)
                    rest_o = [j for j in order if j != i]
                    R = sum(fn0(rest_o, sc, prm0, cfg, **kw0).values()) if rest_o else 0
                else:
                    R = sum(t0[j] for j in t0 if j != i)
                cap = max(cfg["w_min"], round(s_ * R / (1 - s_), 1))
                if i not in caps or caps[i]["cap"] > cap:
                    caps[i] = {"cap": cap, "reason": f"concurrency capacity {v['max_conc']} ({v.get('reason','')}), estimated peak total concurrency {C:.1f}, share <= {s_:.0%}"}
                rows[i]["capacity_cap"] = cap
    fn, prm = STRATEGIES[name], prm_eff
    if name == "rank" and (prm.get("lead_trust") or 0) > 0 and trust:
        prm = dict(prm, _trust=dict(trust))
    kw = {"prev": prev, "caps": {i: v["cap"] for i, v in caps.items()}} if name == "rank" else {}
    tgt = fn(order, sc, prm, cfg, **kw)
    capped = {i for i in order if i in caps and tgt[i] > caps[i]["cap"]}
    for i in capped:
        notes.append(f"#{i} soft cap {caps[i]['cap']} ({caps[i]['reason']}), target {tgt[i]:.1f} truncated, flagged as quality risk")
    rest = [i for i in order if i not in capped]
    if capped and rest:
        tgt.update(fn(rest, sc, prm, cfg, **kw))
        # v1.47: if a channel moved up after the rerun also has a cap (e.g. two new channels), keep truncating and rerunning until nothing exceeds its cap
        for _ in range(len(order)):
            more = {i for i in rest if i in caps and tgt[i] > caps[i]["cap"]}
            if not more: break
            for i in more:
                notes.append(f"#{i} soft cap {caps[i]['cap']} ({caps[i]['reason']}), target after re-rank {tgt[i]:.1f} truncated")
            capped |= more; rest = [i for i in rest if i not in more]
            if rest: tgt.update(fn(rest, sc, prm, cfg, **kw))
    for i in capped: tgt[i] = caps[i]["cap"]
    new = {i: int(max(cfg["w_min"], min(cfg["w_max"], round(tgt[i])))) for i in order}
    # v1.89 newcomer exploration floor: channels in the newcomer cap period (rows[i]["newcomer"]) with 0 errors in 1h get at least explore_w,
    # avoiding the "no traffic -> score 0 -> weight 0 -> even less traffic" death spiral (a newly attached channel got 0 at every shadow point with only 2 real requests in 1h). Default 0 = off.
    ew = int(nc.get("explore_w", 0) or 0) if nc.get("enabled") else 0
    if ew > 0:
        for i in order:
            r = rows.get(i) or {}
            if r.get("newcomer_n") is None: continue
            e60 = ((feats.get(i) or {}).get(f"{cfg['windows_min'][0]}m") or {}).get("err") or 0
            if e60 == 0 and new[i] < ew:
                notes.append(f"#{i} newcomer exploration floor: {new[i]}->{ew} (1h real requests {r['newcomer_n']}, 0 errors)")
                new[i] = ew; r["explore_floor"] = ew
    for c in chans:
        w60 = feats[c["id"]][f"{cfg['windows_min'][0]}m"]
        a = cfg["error_alert"]
        if w60["n"] >= a["min_n"] and (w60["err_rate"] or 0) > a["rate"]:
            notes.append(f"ALERT: #{c['id']} 1h error rate {w60['err_rate']:.0%} (n={w60['n']}); weight lowered only, not disabled")
            new[c["id"]] = cfg["w_min"]
    return dict(strategy=name, order=order, scores=rows, target=new, notes=notes)


def parse_override(spec, chans, cfg):
    """v1.20: parse manual final weights "5=3,2=12". Only accepts this round's candidates (enabled target-model channels) within w_min..w_max; otherwise raises."""
    ok = {int(c["id"]) for c in chans}
    lo, hi = int(cfg.get("w_min", 0)), int(cfg.get("w_max", 20))
    out = {}
    for part in str(spec).split(","):
        if not part.strip(): continue
        k, v = part.split("=")
        i, w = int(k.strip().lstrip("#")), int(v)
        if i not in ok: raise ValueError(f"#{i} is not an adjustable channel this round (disabled or non-target channels cannot be changed)")
        if not lo <= w <= hi: raise ValueError(f"#{i} weight {w} outside {lo}..{hi}")
        out[i] = w
    return out


def parse_final(spec, chans, cfg):
    """v1.41: final weights for every adjustable channel at once after manual review. Must cover exactly all of this round's candidates (enabled target-model channels),
    so a recomputation between dry run and apply cannot leave a channel uncovered (v1.40 found a channel briefly written with an unintended value)."""
    out = parse_override(spec, chans, cfg)
    ok = {int(c["id"]) for c in chans}
    miss = sorted(ok - set(out))
    if miss: raise ValueError(f"--final must cover every adjustable channel this round; missing {miss}")
    return out


def system_drift(expect, system):
    """v1.41: compare the system suggestion seen in the dry run with the one recomputed at apply time; returns differences {id: (expect, now)}."""
    return {i: (w, system.get(i)) for i, w in expect.items() if system.get(i) != w}


def blocking_drift(drift, final):
    """v1.78: with --final, only drift on channels whose final value follows the system suggestion (final[i] == expected) blocks the write.
    For manually overridden channels (final differs from expected) the written value does not change whatever the system recomputes, so their drift is irrelevant.
    Typical case: a capacity cap of 11.5 rounds to 11 or 12 between dry run and apply as average concurrency shifts slightly, while that channel is manually kept at 3 anyway.
    Without final (no --final) every drift blocks (old behaviour)."""
    if not final: return dict(drift)
    return {i: d for i, d in drift.items() if i not in final or final[i] == d[0]}


def newcomer_ids(cfg, chans, rounds, now_ts=None):
    """v1.47: among this round's adjustable channels, those appearing < newcomer.min_rounds times in the channel lists of formal write rounds (applied=true) are new.
    No hard-coded ids: a new target-model channel in the DB becomes a newcomer automatically and graduates after enough formal rounds."""
    nc = cfg.get("newcomer") or {}
    if not nc.get("enabled"): return set()
    seen, first = {}, {}
    gap = int(nc.get("reset_gap_rounds", 0) or 0)   # v1.84: reappearing after being absent for >= gap consecutive formal rounds (re-enabled after disabling) counts as new again; 0 = off
    miss = {}
    for r in rounds or []:
        ids = {int(c["id"]) for c in r.get("channels") or []}
        if gap > 0:   # absence counts over all rounds (including dry runs), so disabled periods are detected even when formal rounds are sparse
            for k in list(seen):
                if k not in ids: miss[k] = miss.get(k, 0) + 1
            for k in ids:
                if miss.get(k, 0) >= gap: seen[k] = 0
                miss[k] = 0
        if not r.get("applied"): continue
        for k in ids:
            if seen.get(k, 0) == 0: first[k] = r.get("ts")
            seen[k] = seen.get(k, 0) + 1
    # v1.93: with max_age_h>0, a channel that first (or since re-enabling) appeared in formal rounds less than max_age_h hours ago is still new
    # (a newly attached channel graduated after 4 formal rounds in about 3 hours with only 8 real requests in 1h; the exploration floor then stopped applying).
    age_h = float(nc.get("max_age_h", 0) or 0); now = now_ts
    if now is None and rounds: now = rounds[-1].get("ts")
    def young(k):
        if age_h <= 0 or not first.get(k) or not now: return False
        from datetime import datetime as _dt
        try: return (_dt.fromisoformat(now) - _dt.fromisoformat(first[k])).total_seconds() / 3600 < age_h
        except Exception: return False
    # v1.97: with graduate_real_n60>0, a channel graduates only after some formal round (since its latest appearance) had 1h real requests (mix_60m.n_user)
    # of at least that value -- matching the rule "max weight 6 until about 30 real requests in an hour", not just a count of formal rounds.
    grad = int(nc.get("graduate_real_n60", 0) or 0)
    proven = {}
    if grad > 0:
        for r in rounds or []:
            if not r.get("applied"): continue
            ft = r.get("features") or {}
            for k in {int(c["id"]) for c in r.get("channels") or []}:
                if first.get(k) and r.get("ts") and r.get("ts") < first[k]: continue
                m = ((ft.get(str(k)) or {}).get("mix_60m") or {})
                nu = m.get("n_user")
                if nu is not None and nu >= grad: proven[k] = True
    def unproven(k): return grad > 0 and not proven.get(k)
    return {int(c["id"]) for c in chans if seen.get(int(c["id"]), 0) < nc.get("min_rounds", 4) or young(int(c["id"])) or unproven(int(c["id"]))}
