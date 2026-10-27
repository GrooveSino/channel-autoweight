"""v1.19 shadow review: summarise the suggestions in data/shadow.jsonl over a period and compare them with the real performance that followed. Reads files only, no DB.
Usage: python3 evaluate.py --shadow [--since 45m] [--json]"""
import json, os, statistics as st
from datetime import datetime

def parse_since(s):
    s = str(s).strip().lower()
    mult = {"m": 1, "h": 60, "d": 1440}
    return float(s[:-1]) * mult[s[-1]] if s[-1] in mult else float(s)

def _t(r): return datetime.fromisoformat(r["ts"]).timestamp()

def coverage(win, interval_min=5, dedupe_s=60):
    """v1.20 coverage: expected runs, actual runs, max gap, and holes longer than 1.5x interval (they appear when the host is frozen or the loop dies)."""
    ts = [_t(r) for r in win]
    gaps = [(b - a) / 60 for a, b in zip(ts, ts[1:])]
    holes = [(datetime.fromtimestamp(a).strftime("%H:%M"), round(g, 1)) for a, g in zip(ts, gaps) if g > 1.5 * interval_min]
    dup = sum(1 for g in gaps if g * 60 < dedupe_s)
    span = (ts[-1] - ts[0]) / 60 if len(ts) > 1 else 0
    expected = int(span // interval_min) + 1
    return dict(runs=len(win) - dup, raw=len(win), expected=expected, ratio=round(min(1.0, (len(win) - dup) / expected), 2) if expected else None,
                max_gap=round(max(gaps), 1) if gaps else None, holes=holes, dup=dup)

def dedupe(win, dedupe_s=60):
    """Duplicate runs within the same minute (manual + loop) keep only the last one, so later comparisons do not double count."""
    out = []
    for r in win:
        if out and _t(r) - _t(out[-1]) < dedupe_s: out[-1] = r
        else: out.append(r)
    return out

def summarize(recs, since_min=45, now=None, min_after_n=20, interval_min=5, low_min_n=5, after_win_min=10):
    if not recs: return dict(n=0, channels={})
    now = now or _t(recs[-1])
    win = [r for r in recs if _t(r) >= now - since_min * 60]
    cov = coverage(win, interval_min)
    win = dedupe(win)
    ids = sorted({int(i) for r in win for i in r["suggest"]})
    out = {}
    for i in ids:
        k = str(i)
        rs = [r for r in win if k in r["suggest"] or i in r["suggest"]]
        g = lambda r, f: (r[f].get(k) if k in r[f] else r[f].get(i))
        sug = [g(r, "suggest") for r in rs]
        sc = [g(r, "scores")["score"] for r in rs]
        tr = [g(r, "trust")["trust"] for r in rs]
        db = [g(r, "old") for r in rs]
        dist = {}
        for w in sug: dist[w] = dist.get(w, 0) + 1
        changes = sum(1 for a, b in zip(sug, sug[1:]) if a != b)
        diff_runs = sum(1 for a, b in zip(sug, db) if a != b)
        # Suggestion vs what followed: compare each suggestion with the next record's 10m window (real requests ~5-10 min after the suggestion)
        after = []
        last_b = None
        for a, b in zip(rs, rs[1:]):
            if _t(b) - _t(a) > 15 * 60: continue   # v1.20: after a long gap the next 10m window is no longer "after the suggestion" traffic; skip
            # v1.65: with a point every 5 min and overlapping 10m windows, the same request used to be counted twice (after_n doubled); only use non-overlapping points
            if last_b is not None and _t(b) - last_b < after_win_min * 60 - 1: continue
            last_b = _t(b)
            f = g(b, "features")["10m"]
            after.append(dict(sug=g(a, "suggest"), n=f["n"], err=f["err"], p50=f["p50"], p90=f["p90"]))
        def agg(xs, key):
            v = [x[key] for x in xs if x[key] is not None]
            return round(st.mean(v), 1) if v else None
        out[i] = dict(runs=len(rs), name=g(rs[-1], "names") if rs[-1].get("names") else None,
                      suggest_dist=dist, suggest_last=sug[-1], changes=changes,
                      db_last=db[-1], diff_runs=diff_runs,
                      score_mean=round(st.mean(sc), 4), score_sd=round(st.pstdev(sc), 4),
                      score_min=min(sc), score_max=max(sc),
                      trust_path=tr if len(tr) <= 12 else tr[:3] + ["..."] + tr[-6:], trust_first=tr[0], trust_last=tr[-1],
                      probes=sum(1 for r in rs if k in (r.get("probed") or {}) or i in (r.get("probed") or {})),
                      after_n=sum(x["n"] for x in after), after_err=sum(x["err"] for x in after),
                      after_p50=agg(after, "p50"), after_p90=agg(after, "p90"))
    # Sanity: does the suggested-weight ranking match the ranking of real performance that followed (by after_p90, errors)?
    ranked_sug = sorted(out, key=lambda i: -st.mean([w for w, c in out[i]["suggest_dist"].items() for _ in range(c)]))
    def bad(i):
        o = out[i]; er = o["after_err"] / o["after_n"] if o["after_n"] else 0
        return (er > 0.02, o["after_p90"] if o["after_p90"] is not None else 1e9)
    ranked_real = sorted([i for i in out if out[i]["after_n"] >= min_after_n], key=bad)   # channels with too few samples are left out
    # v1.37: rank by mean score (preference before soft caps). When a soft cap hands first place to the next channel, comparing by weight would misjudge "correct scoring" as a mismatch
    ranked_score = sorted(out, key=lambda i: -out[i]["score_mean"])
    # v1.37: real traffic share afterwards vs suggested weight share (sticky sessions make shares drift from weights)
    tot_n = sum(out[i]["after_n"] for i in out)
    tot_w = sum(st.mean([w for w, c in out[i]["suggest_dist"].items() for _ in range(c)]) for i in out)
    for i in out:
        mw = st.mean([w for w, c in out[i]["suggest_dist"].items() for _ in range(c)])
        out[i]["share_real"] = round(out[i]["after_n"] / tot_n, 3) if tot_n else None
        out[i]["share_weight"] = round(mw / tot_w, 3) if tot_w else None
    # v1.21: with only one channel having enough samples there is nothing to compare; report "insufficient samples" instead of "mismatch"
    if len(ranked_real) < 2: verdict = "insufficient samples (< 2 channels with enough real n afterwards; cannot compare)"
    else: verdict = "match" if ranked_sug[0] == ranked_real[0] else "mismatch: suggested first differs from the best real performer afterwards"
    # v1.65: low-traffic reference. At night each channel has 10-20 requests, so the n>=20 threshold is always "insufficient"; relax to low_min_n,
    # prefer p50 (with small samples p90 is just the max and noisy); the result is for reference only
    def bad_low(i):
        o = out[i]; er = o["after_err"] / o["after_n"] if o["after_n"] else 0
        return (er > 0.02, o["after_p50"] if o["after_p50"] is not None else 1e9, o["after_p90"] if o["after_p90"] is not None else 1e9)
    ranked_low = sorted([i for i in out if out[i]["after_n"] >= low_min_n], key=bad_low) if len(ranked_real) < 2 else []
    if len(ranked_real) >= 2: verdict_low = None
    elif len(ranked_low) < 2: verdict_low = f"low-traffic reference also has insufficient samples (< 2 channels with n >= {low_min_n})"
    else:
        verdict_low = ("low-traffic reference: match" if ranked_sug[0] == ranked_low[0] else
                       f"low-traffic reference: mismatch (suggested first #{ranked_sug[0]}, best p50 afterwards #{ranked_low[0]})")
    if len(ranked_real) < 2: verdict_score = verdict
    else: verdict_score = "match" if ranked_score[0] == ranked_real[0] else "mismatch: top score differs from the best real performer afterwards"
    bf = sum(1 for r in win if r.get("backfill"))
    return dict(n=len(win), coverage=cov, min_after_n=min_after_n, since_min=since_min, start=win[0]["ts"], end=win[-1]["ts"], channels=out,
                rank_suggest=ranked_sug, rank_real_after=ranked_real, verdict=verdict,
                rank_score=ranked_score, verdict_score=verdict_score, backfilled=bf,
                rank_low=ranked_low, verdict_low=verdict_low, low_min_n=low_min_n)

def render(s):
    if not s.get("n"): return "no shadow records"
    L = [f"Shadow review {s['start'][11:16]}-{s['end'][11:16]} ({s['n']} runs, window {s['since_min']:g} min)",
         "| Channel | DB weight | Suggested (last) | Suggestion distribution | Changes | Differs from DB | Score mean+-sd [min,max] | Trust first->last | Probes | Real after n / err / p50 / p90 | Real share/weight share |",
         "|---|---|---|---|---|---|---|---|---|---|---|"]
    for i, c in s["channels"].items():
        dist = " ".join(f"{w}x{n}" for w, n in sorted(c["suggest_dist"].items(), key=lambda x: -x[1]))
        L.append(f"| #{i} {c['name'] or ''} | {c['db_last']} | {c['suggest_last']} | {dist} | {c['changes']} | {c['diff_runs']} | "
                 f"{c['score_mean']}+-{c['score_sd']} [{c['score_min']},{c['score_max']}] | {c['trust_first']}->{c['trust_last']} | {c['probes']} | "
                 f"{c['after_n']} / {c['after_err']} / {c['after_p50']} / {c['after_p90']} | {c.get('share_real')}/{c.get('share_weight')} |")
    cv = s.get("coverage") or {}
    if cv: L.append(f"Coverage {cv['runs']}/{cv['expected']} ({cv['ratio']}), max gap {cv['max_gap']} min" + (f", holes {cv['holes']}" if cv['holes'] else "") + (f", {cv['dup']} duplicates removed" if cv['dup'] else "") + (f", {s.get('backfilled')} backfilled" if s.get('backfilled') else ""))
    L.append(f"Suggested order {s['rank_suggest']}; real order afterwards {s['rank_real_after']}; verdict: {s['verdict']} (only channels with real n >= {s.get('min_after_n')} afterwards are compared)")
    if s.get("verdict_low"): L.append(f"Low-traffic reference order (n >= {s.get('low_min_n')}, p50 first) {s.get('rank_low')}; {s['verdict_low']}")
    if "rank_score" in s: L.append(f"Score order (before soft caps) {s['rank_score']}; verdict: {s['verdict_score']}")
    return "\n".join(L)
