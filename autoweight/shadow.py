"""v1.19 shadow mode: every 5 minutes compute scores and suggested weights from existing logs; record only, never write the DB.

- Data: read-only (db.ro -> BEGIN READ ONLY); db.apply_weights is never called.
- Probes: none by default. An enabled channel is probed once only when it had < min_real_n real user requests in probe_window_min minutes
    (and at most once per probe_cooldown_min per channel, so off-peak hours are not probed every 5 minutes).
- First token: unprobed channels reuse the latest probe within ttft_max_age_min (shadow.jsonl or formal rounds.jsonl), else ttft_missing_s.
- Trust: stored separately in the trust field of shadow.jsonl and evolves independently; formal-round trust is never read or written.
    Because it runs every 5 minutes with overlapping 10m windows, bad-event severity x min(1, gap/sev_gap_min) avoids counting the same error twice.
"""
import re
from datetime import datetime
from . import features, trust as T

DEFAULTS = dict(interval_min=5, min_real_n=3, probe_window_min=10, probe_cooldown_min=30,
                ttft_max_age_min=90, sev_gap_min=10, file="data/shadow.jsonl")

def params(cfg):
    p = dict(DEFAULTS); p.update({k: v for k, v in (cfg.get("shadow") or {}).items() if not k.startswith("_")})
    return p

def real_counts(cfg, rows, now, minutes):
    """Real user requests per channel within `minutes` (probe tokens excluded)."""
    u = features.user_rows(cfg, rows) if cfg.get("probe_token_ids") else rows
    out = {}
    for x in u:
        if x["t"] >= now - minutes * 60: out[x["ch"]] = out.get(x["ch"], 0) + 1
    return out

def _ts(r):
    try: return datetime.fromisoformat(r["ts"]).timestamp()
    except Exception: return None

def upstream_as_probes(results, add_s=1.5):
    """v1.93: on/off direct upstream test results -> probe format. Streaming check matches toggle (deltas>=2 and span>0); first token adds add_s to approximate site overhead."""
    out = []
    for x in results or []:
        st = bool((x.get("deltas") or 0) >= 2 and (x.get("span") or 0) > 0) if x.get("ok") else None
        out.append(dict(ok=bool(x.get("ok")), status=x.get("status"),
                        ttft=(round(x["ttft"] + add_s, 2) if x.get("ttft") is not None else None),
                        total=(round(x["total"] + add_s, 2) if x.get("total") is not None else None),
                        deltas=x.get("deltas"), streaming=st, err=x.get("err"), source="upstream"))
    return out or None

def last_probe(recs, cid, now, max_age_min, watch_fallback=False, upstream_add_s=1.5):
    """Latest probe of cid (within max_age_min); returns (probes, age_min) or (None, None). recs must be in ascending time order.
    v1.33: skip formal-round records with probes_reused=true: their probes are copies of an older probe, and aging them from the copy time would count the same probe
    as new evidence again (a dry-run probe copied into the next formal round was penalised twice for buffering in consecutive shadow points)."""
    for r in reversed(recs or []):
        if r.get("probes_reused"): continue
        ps = (r.get("probes") or {}).get(str(cid)) or (r.get("probes") or {}).get(cid)
        if not ps and watch_fallback:   # v1.93: channel not in this round's probes (disabled / just re-enabled), use the watch-list probe
            w = (r.get("watch") or {}).get(str(cid)) or (r.get("watch") or {}).get(cid) or {}
            ps = w.get("probes") or None
            if not ps:   # the site refuses probes of disabled channels; fall back to the on/off direct upstream test (no site overhead, first token + upstream_ttft_add_s)
                ps = upstream_as_probes((((r.get("toggle") or {}).get("disabled") or {}).get(str(cid)) or {}).get("results"),
                                        upstream_add_s)
        if not ps: continue
        t = _ts(r)
        if t is None: continue
        age = (now - t) / 60
        if 0 <= age <= max_age_min: return ps, round(age, 1)
        if age > max_age_min: return None, None
    return None, None

def plan_probes(cfg, ids, counts, recs, now):
    """Return {id: reason} for the channels that need a probe."""
    p = params(cfg); out = {}
    for i in ids:
        n = counts.get(i, 0)
        if n >= p["min_real_n"]: continue
        _, age = last_probe(recs, i, now, p["probe_cooldown_min"])
        if age is not None: continue   # already probed within the cooldown, reuse
        out[i] = f"{p['probe_window_min']}m real requests {n} < {p['min_real_n']}"
    return out

def trust_prev(shadow_recs):
    for r in reversed(shadow_recs or []):
        if r.get("trust"): return {int(k): v["trust"] for k, v in r["trust"].items()}, r.get("ts")
    return {}, None

def probe_ages(src):
    """v1.32: probe age (minutes) from ttft_src ("probe" / "reuse 12.3m" / "missing"); 0 for a fresh probe."""
    out = {}
    for k, v in (src or {}).items():
        if v == "probe": out[int(k)] = 0.0
        else:
            m = re.match(r"reuse ([0-9.]+)m", str(v))
            if m: out[int(k)] = float(m.group(1))
    return out

def probe_new(age, gap_min):
    """v1.32: whether this probe is new evidence that appeared after the previous time point (only new evidence counts as a trust bad event)."""
    if age is None or gap_min is None: return True
    return age < gap_min - 1e-9

def trust_step(cfg, ids, feats, probes, prev, gap_min, probe_age=None):
    """Same as trust.step, but severity is discounted by gap (window-overlap de-duplication).
    v1.28: with shadow.sev_gap_long_min set (e.g. 60), discount per source: 10m/probe events x gap/sev_gap_min, 1h events x gap/sev_gap_long_min;
    unset behaves like v1.19 (everything x gap/sev_gap_min).
    v1.32: with shadow.probe_once=true and probe_age given, probe events (buffered / all failed) count once (factor 1) only when the probe is new evidence;
    time points reusing an old probe are not penalised again (before v1.31 one buffered probe reused for 90 minutes was penalised every 5 minutes)."""
    p, sp = T.params(cfg), params(cfg)
    prev = {int(k): v for k, v in (prev or {}).items()}
    wl, ws = f"{cfg['windows_min'][0]}m", f"{cfg['windows_min'][1]}m"
    def kf(w):
        return 1.0 if gap_min is None else max(0.0, min(1.0, gap_min / w))
    k, kl = kf(sp["sev_gap_min"]), kf(sp.get("sev_gap_long_min") or sp["sev_gap_min"])
    out = {}
    for i in ids:
        parts = T.severity_parts(feats[i], probes.get(i), p, wl, ws)
        sev = max([x[0] for x in parts]) if parts else 0.0
        once = sp.get("probe_once") and probe_age is not None
        def fac(src):
            if src == "long": return kl
            if src == "probe" and once: return 1.0 if probe_new(probe_age.get(i), gap_min) else 0.0
            return k
        eff = max([x[0] * fac(x[2]) for x in parts]) if parts else 0.0
        eff = round(eff, 3)
        t = T.update(prev.get(i), eff, feats[i][wl].get("n"), p, gap_min)
        out[i] = dict(trust=t, prev=prev.get(i), sev=sev, sev_eff=eff, why=[x[1] for x in parts])
    return out

def backfill_slots(last_ts, now, interval_min, max_min=360):
    """v1.21: missing clock-aligned time points (multiples of interval) between last_ts and now, excluding points < interval/2 from either end; backfill at most max_min minutes."""
    if not last_ts: return []
    step = int(interval_min * 60); half = step // 2
    start = max(last_ts, now - max_min * 60)
    t = (int(start) // step + 1) * step
    out = []
    while t < now - half:
        if t - last_ts >= half: out.append(t)
        t += step
    return out
