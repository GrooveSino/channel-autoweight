"""v1.18 trust state (core principle: every channel is unstable by default; trust rises slowly and falls fast).

Each channel has a trust value trust in [t_min, 1], persisted across rounds in the `trust` field of rounds.jsonl:
- New channel / no record: starts at t_init (never fully trusted by default).
- A "bad event" this round (severity > 0): trust <- max(t_min, trust * (1 - down * severity))  -- fast fall
- A clean round: trust <- min(1, trust + up * sample_factor)                                  -- slow rise
    sample_factor = min(1, n_60m / up_full_n): the less traffic, the less "verified", the slower trust grows.
- A long gap between rounds (> gap_reset_min minutes, e.g. the routine was paused) pulls trust halfway back to t_init; stale trust does not count.

Bad-event severity (maximum over sources, 0..1):
- errors in 10m (n >= err_10m_min_n): mapped from err_10m_rate, >= err_full scores the full 1
- 1h error rate > err_1h_thr: proportional to the excess
- all probes failed this round: probe_fail_sev
- probe judged whole-response buffering (non-streaming): buffered_sev
- 1h slow-request rate (> slow_s) > slow_thr: slow_sev (half strength; slow is not the same as broken)

When scoring: score * (floor + (1 - floor) * trust). floor makes low trust only lower the score, never zero it (zeroing is left to the error-alert guard).
All parameters live in config.trust; with enabled=false trust is recorded only and not used for scoring (observe first, enable later).
"""

DEFAULTS = dict(enabled=False, t_init=0.6, t_min=0.1, up=0.08, down=0.6, up_full_n=30, floor=0.5,
                err_10m_min_n=2, err_full=0.2, err_1h_thr=0.02, probe_fail_sev=0.8, buffered_sev=0.5,
                slow_thr=0.15, slow_sev=0.3, gap_reset_min=180, round_min=45)

def params(cfg):
    p = dict(DEFAULTS); p.update({k: v for k, v in (cfg.get("trust") or {}).items() if not k.startswith("_")})
    p["log_stream_veto"] = cfg.get("log_stream_veto")   # v1.56
    return p

def slow_posterior(n, r, thr, a0=1.0, b0=1.0, steps=2000):
    """v1.29: k=round(n*r) slow requests out of n, Beta(a0,b0) prior; returns P(true slow rate > thr) (midpoint numerical integration).
    Default prior is Beta(1,1); config slow_prior_a/b sets a weak prior around a typical slow rate (e.g. mean 8%, strength 10) so small samples are not inflated by the prior."""
    from math import lgamma, exp, log
    n = int(n); k = int(round(n * r)); a, b = a0 + k, b0 + n - k
    lb = lgamma(a + b) - lgamma(a) - lgamma(b)
    h = (1 - thr) / steps; tot = 0.0
    for t in range(steps):
        x = thr + (t + 0.5) * h
        tot += exp(lb + (a - 1) * log(x) + (b - 1) * log(1 - x))
    return min(1.0, tot * h)

def severity_parts(f, probes, p, win_long="60m", win_short="10m"):
    """v1.28: bad events split by evidence source [(sev, why, src)], src in {"short","long","probe"}.
    Shadow mode de-overlaps per source: 10m events x gap/10, 1h events x gap/60 (a 1h window only gains 1/12 new evidence every 5 minutes).
    slow_min_n: minimum samples for a 1h slow-request event (default 5, same as v1.18); slow_wilson=true requires the Wilson lower bound of the slow rate > slow_thr."""
    parts = []
    s, l = f.get(win_short) or {}, f.get(win_long) or {}
    if s.get("n", 0) >= p["err_10m_min_n"] and s.get("err"):
        sev10 = min(1.0, (s["err_rate"] or 0) / p["err_full"])
        fn = p.get("err_10m_full_n") or 0
        if fn > 0:  # v1.73: scale small-sample 10m errors by n/full_n (noise like 1/2 does not score full severity)
            sev10 *= min(1.0, s["n"] / fn)
        parts.append((sev10, f"10m errors {s['err']}/{s['n']}", "short"))
    if l.get("n") and (l.get("err_rate") or 0) > p["err_1h_thr"]:
        parts.append((min(1.0, ((l["err_rate"] - p["err_1h_thr"]) / p["err_full"])), f"1h error rate {l['err_rate']:.1%}", "long"))
    if probes and not any(x.get("ok") for x in probes):
        parts.append((p["probe_fail_sev"], "all probes failed", "probe"))
    vals = [x.get("streaming") for x in probes or [] if x.get("ok")]
    from .features import log_stream_veto
    if vals and not any(v is True for v in vals) and any(v is False for v in vals) and not log_stream_veto(probes, f, p.get("log_stream_veto"), win_long, use="trust"):
        parts.append((p["buffered_sev"], "probe buffered (non-streaming)", "probe"))
    n, r = l.get("n", 0), (l.get("slow_rate") or 0)
    if n >= p.get("slow_min_n", 5) and r > p["slow_thr"]:
        ok = True
        if p.get("slow_wilson"):
            z = 1.96; lb = (r + z*z/(2*n) - z * ((r*(1-r) + z*z/(4*n)) / n) ** 0.5) / (1 + z*z/n)
            ok = lb > p["slow_thr"]
        sev = p["slow_sev"]
        if ok and p.get("slow_bayes"):
            # v1.29: posterior probability q that the true slow rate > slow_thr under a Beta(slow_prior_a, slow_prior_b) prior; q<=0.5 is no event, 0.5->1 maps linearly to slow_sev
            q = slow_posterior(n, r, p["slow_thr"], p.get("slow_prior_a", 1.0), p.get("slow_prior_b", 1.0))
            sev = p["slow_sev"] * max(0.0, min(1.0, (q - 0.5) / 0.5))
            ok = sev > 0
        if ok: parts.append((sev, f"1h slow requests {r:.0%}", "long"))
    return [(round(a, 3), b, c) for a, b, c in parts]

def severity(f, probes, p, win_long="60m", win_short="10m"):
    """Return (severity 0..1, reasons). f is the per-channel feature dict from features.build."""
    parts = severity_parts(f, probes, p, win_long, win_short)
    return (max([x[0] for x in parts]) if parts else 0.0), [x[1] for x in parts]

def update(prev_t, sev, n60, p, gap_min=None):
    """One-step update for one channel: fast fall, slow rise. prev_t=None means a new channel."""
    # v1.47: new channels (no trust record at all) start at t_new (default t_init), lower than the gap pull-back of old channels
    t = p.get("t_new", p["t_init"]) if prev_t is None else prev_t
    if gap_min is not None and gap_min > p["gap_reset_min"]:
        t = (t + p["t_init"]) / 2
    if sev > 0:
        t = max(p["t_min"], t * (1 - p["down"] * sev))
    else:
        # If two rounds are closer than round_min (e.g. the same round applied twice), rise proportionally less to avoid double counting; falls are not discounted
        g = 1.0 if gap_min is None else max(0.0, min(1.0, gap_min / p["round_min"]))
        t = min(1.0, t + p["up"] * g * min(1.0, (n60 or 0) / p["up_full_n"]))
    return round(t, 4)

def step(cfg, ids, feats, probes, prev_trust, gap_min=None):
    """Update a whole round. Returns {id: dict(trust, prev, sev, why)}. prev_trust: {id: trust} (keys may be str)."""
    p = params(cfg); prev_trust = {int(k): v for k, v in (prev_trust or {}).items()}
    wl, ws = f"{cfg['windows_min'][0]}m", f"{cfg['windows_min'][1]}m"
    out = {}
    for i in ids:
        sev, why = severity(feats[i], probes.get(i), p, wl, ws)
        t = update(prev_trust.get(i), sev, feats[i][wl].get("n"), p, gap_min)
        out[i] = dict(trust=t, prev=prev_trust.get(i), sev=sev, why=why)
    return out

def multiplier(t, p):
    return p["floor"] + (1 - p["floor"]) * t

def last_state(hist):
    """Trust values and timestamp of the latest **formal** record (dry_run=False) in the rounds history.
    Dry-run rounds do not count: when a round is dry-run then applied, continuing from the dry-run value would count bad events twice (a bug found in the first v1.18 round).
    Returns ({}, None) without a formal record; bootstrap then replays historical formal rounds."""
    for r in reversed(hist or []):
        if r.get("trust") and not r.get("dry_run", True):
            return {int(k): v["trust"] for k, v in r["trust"].items()}, r.get("ts")
    return {}, None

def bootstrap(cfg, hist):
    """Without a trust record, replay current trust from the existing rounds history (formal rounds only) so the first round does not start everyone at t_init."""
    from datetime import datetime
    st, last_ts = {}, None
    for r in hist or []:
        if r.get("dry_run", True): continue
        feats = {int(k): v for k, v in r["features"].items()}
        probes = {int(k): v for k, v in (r.get("probes") or {}).items()}
        ts = datetime.fromisoformat(r["ts"])
        gap = (ts - last_ts).total_seconds() / 60 if last_ts else None
        st = {i: v["trust"] for i, v in step(cfg, list(feats), feats, probes, st, gap).items()}
        last_ts = ts
    return st, (last_ts.isoformat() if last_ts else None)
