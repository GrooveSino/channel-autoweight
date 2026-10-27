"""v1.9 diagnostics (not used for scoring):
- channel_set_change: enabled channels added/removed compared with the previous round (e.g. manually enabled or disabled), to explain sudden traffic shifts.
- buffered_streak: consecutive rounds (including this one) in which a channel probe was "buffered (streaming=False)", to spot an upstream that persistently stopped streaming.
- recovering (v1.11, used in decisions): channels buffered >= min_streak rounds before the previous round that stream again this round enter a one-round "observation period".
"""

def _ids(chans):
    return {int(c["id"]) for c in chans or []}


def channel_set_change(prev, chans):
    """Return {"added": [...], "removed": [...]}; None without a previous round."""
    if not prev:
        return None
    before, now = _ids(prev.get("channels")), _ids(chans)
    return dict(added=sorted(now - before), removed=sorted(before - now))


def _round_buffered(probes, cid):
    """For one channel in one round: any successful probe with streaming=True -> False;
    successful probes that are all False -> True; otherwise (no probes / all None) -> None (neither breaks nor extends the streak)."""
    ps = (probes or {}).get(str(cid)) or (probes or {}).get(cid) or []
    vs = [p.get("streaming") for p in ps if p.get("ok")]
    if any(v is True for v in vs):
        return False
    if vs and all(v is False for v in vs):
        return True
    return None


def buffered_streak(hist, probes, ids, current_reused=False):
    """hist: past rounds in time order (excluding this one); probes: this round's probes. Returns {id: consecutive buffered rounds} (only >0).
    v1.34: current_reused=True means this round's probes were copied from an earlier round (--reuse-probe) and are not counted again; rounds with probes_reused in history are skipped too,
    so the count = consecutive real probe rounds that were buffered (a dry run still counts as one real probe)."""
    out = {}
    for cid in ids:
        # v1.34: skip probes_reused rounds (a formal round copying the dry-run probe; the same probe is not counted twice)
        rounds = ([] if current_reused else [probes]) + [h.get("probes") for h in reversed(hist or []) if not h.get("probes_reused")]
        n = 0
        for pr in rounds:
            v = _round_buffered(pr, cid)
            if v is True:
                n += 1
            elif v is False:
                break
        if n:
            out[int(cid)] = n
    return out


def recovering(hist, probes, ids, min_streak=2):
    """v1.11: channels that just recovered from "consecutive buffering" to real streaming this round, {id: previous buffered streak}.
    Condition: this round's probe is real streaming and the buffered streak up to the previous round >= min_streak.
    One round only: if it still streams in the next round, the previous streak is already 0 and it leaves observation automatically."""
    if not hist or not min_streak:
        return {}
    prev_streak = buffered_streak(hist[:-1], hist[-1].get("probes"), ids, bool(hist[-1].get("probes_reused")))
    return {int(c): prev_streak[int(c)] for c in ids
            if _round_buffered(probes, c) is False and prev_streak.get(int(c), 0) >= min_streak}


def buffer_prob(events, now, current, params):
    """v1.31 buffering probability (score.buffer_prob): "buffered vs streaming" is intermittent for some channels (buffered for a few hours, streaming the rest),
    and a hard x0.85 on a single probe makes the score jump with every probe. Estimate the "current buffering probability" with a Beta posterior + exponential time decay:
        p = (a + sum w*[buffered]) / (a + b + sum w*[decided]), w = 0.5^(age_h/half_life_h); this round's probe has age=0.
    events: [(ts, {id: [probe,...]}), ...] past probes; current: this round {id: [probe,...]}; returns {id: p}. Only probes with a True/False streaming verdict count; None is ignored."""
    from .decide import stream_verdict
    a, b, hl = params.get("prior_a", 0.3), params.get("prior_b", 1.7), params.get("half_life_h", 6.0)
    num, den = {}, {}
    def add(pr, w):
        for k, v in (pr or {}).items():
            i = int(k); sv = stream_verdict(v) if any(x.get("ok") for x in v or []) else None
            if sv is None: continue
            num[i] = num.get(i, 0) + w * (sv is False); den[i] = den.get(i, 0) + w
    for ts, pr in events:
        age = max(0.0, (now - ts) / 3600)
        if age > params.get("max_age_h", 48): continue
        add(pr, 0.5 ** (age / hl))
    add(current, 1.0)
    ids = set(num) | set(int(k) for k in (current or {}))
    return {i: round((a + num.get(i, 0)) / (a + b + den.get(i, 0)), 4) for i in ids}


def zero_since(hist, cid):
    """v1.80: start ts (ISO) of the run in which the channel weight has been 0. hist in time order (excluding this round).
    Walking backwards, records with old==0 push the start earlier; at a record with old!=0, if it was a write round with target 0, that record is the start (it wrote the 0).
    If the last record has old!=0 and did not write 0 -> None."""
    since = None
    for r in reversed(hist or []):
        o = (r.get("old") or {}).get(str(cid), (r.get("old") or {}).get(cid))
        if o == 0:
            since = r.get("ts"); continue
        t = ((r.get("decision") or {}).get("target") or {})
        tv = t.get(str(cid), t.get(cid))
        if r.get("applied") and tv == 0:
            since = r.get("ts")
        break
    return since


def zero_weight_leak(hist, old, feats, now, min_age_min=30, min_n10=5):
    """v1.80 diagnostics (not used for scoring): channels whose DB weight has been 0 for >= min_age_min minutes but still get >= min_n10 real requests in 10m.
    Motivation: after a channel was set to 0 it kept receiving requests and errors for most of an hour; prompt_cache_key sticky sessions ignore weights,
    so weight 0 does not stop errors from existing sessions. Returns {id: {since, age_min, n10, err10, n60, err60}} to alert the operator (who decides whether to disable)."""
    from datetime import datetime
    out = {}
    for k, w in (old or {}).items():
        if w != 0: continue
        i = int(k); f = feats.get(i) or feats.get(str(i)) or {}
        f10, f60 = f.get("10m") or {}, f.get("60m") or {}
        if (f10.get("n") or 0) < min_n10: continue
        s = zero_since(hist, i)
        if not s: continue
        age = (now - datetime.fromisoformat(s).timestamp()) / 60
        if age < min_age_min: continue
        out[i] = dict(since=s, age_min=round(age, 1), n10=f10.get("n"), err10=f10.get("err"), n60=f60.get("n"), err60=f60.get("err"))
    return out


def vendor_of(name):
    """v1.81: upstream prefix from a channel name of the form [UPSTREAM]-model (e.g. [ACME]-GPT-Pro -> ACME); None if the name does not follow the convention."""
    import re
    m = re.match(r"\s*\[([^\]]+)\]", name or "")
    return m.group(1) if m else None


def reenable_check(entries, chans, feats, now, names=None, upstream=None):
    """v1.81 diagnostics (no scoring, no DB writes): for disabled channels that the operator allowed to be "re-attached when appropriate" (config.reenable),
    decide whether re-enable conditions hold. While disabled the channel has no traffic and the site refuses probes, so timeouts (524/504/408) of still-enabled channels of the same upstream serve as evidence.
    entries: {id: {disabled_at, vendor?, min_age_min=120, max_sib_timeout_60m=0, min_sib_n60=5, start_weight=3}}
    chans: channels enabled this round [{id,name,...}]; feats: this round's features; names: {id: name} (names of disabled channels, to derive the upstream).
    Returns {id: {age_min, vendor, siblings, sib_n60, sib_timeout_60m, sib_timeout_10m, ready, why, start_weight}}."""
    from datetime import datetime
    out = {}
    for k, e in (entries or {}).items():
        i = int(k); e = e or {}
        if not e.get("disabled_at") or e.get("reenabled_at"): continue   # v1.83: skip channels already re-enabled
        age = (now - datetime.fromisoformat(e["disabled_at"]).timestamp()) / 60
        ven = e.get("vendor") or vendor_of((names or {}).get(i) or (names or {}).get(str(i)))
        sibs = [int(c["id"]) for c in chans or [] if int(c["id"]) != i and ven and vendor_of(c.get("name")) == ven]
        n60 = to60 = to10 = 0
        for s in sibs:
            f = feats.get(s) or feats.get(str(s)) or {}
            n60 += (f.get("60m") or {}).get("n") or 0
            to60 += (f.get("60m") or {}).get("err_timeout") or 0
            to10 += (f.get("10m") or {}).get("err_timeout") or 0
        why = []
        # v1.83: direct upstream probe evidence (upstream={id: {recovered, classes}}); used instead when no enabled channel shares the upstream
        up = (upstream or {}).get(i) or (upstream or {}).get(str(i))
        if age < e.get("min_age_min", 120): why.append(f"disabled for only {age:.0f} min (needs >= {e.get('min_age_min', 120)})")
        if up is not None and not up.get("recovered"):
            why.append(f"direct upstream probe not recovered {up.get('classes')}")
        if not sibs:
            if up is None: why.append("no enabled channel of the same upstream to use as evidence")
        elif n60 < e.get("min_sib_n60", 5) and up is None: why.append(f"same upstream has only {n60} requests in 1h, not enough evidence")
        if to60 > e.get("max_sib_timeout_60m", 0): why.append(f"same upstream still has {to60} timeouts in 1h ({to10} in 10m)")
        ok_why = "conditions met: disabled long enough, " + ("direct upstream probe healthy" if up is not None else "no timeouts on the same upstream in 1h")
        out[i] = dict(age_min=round(age, 1), vendor=ven, siblings=sibs, sib_n60=n60, sib_timeout_60m=to60, sib_timeout_10m=to10,
                      upstream=up, ready=not why, why=why or [ok_why], start_weight=e.get("start_weight", 3))
    return out


def shadow_flap(srecs, now, window_min=45, min_flips=3):
    """v1.82 diagnostics: how many times each channel's system suggestion flipped across shadow points in the last window_min minutes.
    Channels with flips >= min_flips are returned as {id: dict(flips, values, n_points)} to flag "the suggestion is jittering; do not follow it blindly".
    Read-only; not used for scoring or suggestions."""
    from datetime import datetime
    pts = []
    for r in srecs:
        try: t = datetime.fromisoformat(r["ts"]).timestamp()
        except Exception: continue
        if 0 <= now - t <= window_min * 60 and r.get("suggest"): pts.append((t, r["suggest"]))
    pts.sort(key=lambda x: x[0])
    seq = {}
    for _, s in pts:
        for k, w in s.items(): seq.setdefault(int(k), []).append(w)
    out = {}
    for i, vs in seq.items():
        flips = sum(1 for a, b in zip(vs, vs[1:]) if a != b)
        if flips >= min_flips: out[i] = dict(flips=flips, values=sorted(set(vs)), n_points=len(vs))
    return out


def _round_flip(plist):
    """v1.86: within one round, the first probe was buffered (streaming=False) but the recheck streamed (streaming=True)."""
    if not plist: return False
    first = plist[0]; rest = [p for p in plist[1:] if p.get("recheck")]
    return first.get("ok") and first.get("streaming") is False and any(p.get("ok") and p.get("streaming") is True for p in rest)


def probe_flip(hist, probes, ids, window=8, min_flips=2):
    """v1.86 diagnostics (not used for scoring/suggestions): over the last `window` rounds with real probes (including this one, skipping probes_reused),
    channels with >= min_flips "first probe buffered -> recheck streaming" events. Their buffered verdict is unreliable; suggestion jitter is mostly probe noise, not real performance.
    Returns {id: {flips, rounds}}."""
    rows = [r.get("probes") or {} for r in hist if r.get("probes") and not r.get("probes_reused")]
    if probes: rows = rows + [probes]
    rows = rows[-window:]
    out = {}
    for i in ids:
        k = str(i)
        seen = [r.get(k, r.get(i)) for r in rows if (r.get(k) or r.get(i))]
        f = sum(1 for p in seen if _round_flip(p))
        if f >= min_flips: out[i] = dict(flips=f, rounds=len(seen))
    return out
