"""v1.90: autonomous on/off. This module only decides and records; the DB write is applied via run.py --apply-toggle after review.
Rules (config.toggle):
- Re-enable: every round each disabled channel is tested n times directly against its upstream from the production host (autoweight.upstream; the key never leaves that host).
    A round "passes" = all succeed (200 with a first token) + real streaming (deltas>=2 and span>0) + max first token and max total time both beat the bottom-third baseline of enabled channels.
    >= need_streak consecutive passing rounds (default 3) -> suggest re-enabling at start_weight (2), priority 10.
- Disable: an enabled channel that ranks last in formal rounds (ranked only among channels with >= min_n60 real 1h requests) for >= last_rounds rounds spanning >= last_span_min -> suggest disabling;
    floors: no rank-based disable when enabled <= min_enabled (4); persistent errors (err_rounds rounds with 1h error rate >= err_rate while weight is already 0) may go down to hard_min (3) and require notifying the operator immediately; never below hard_min.
"""
import math

DEFAULT = dict(need_streak=3, n_test=3, start_weight=2, min_n60=20, last_rounds=5, last_span_min=210,
               min_enabled=4, hard_min=3, err_rate=0.2, err_rounds=2, min_log_n=10,
               upstream_baseline=True, n_base_test=2, baseline_mode="site", real_n60=True,
               skip_buffered_ttft=True, min_log_n_buffered=2, err_probe_fail=True)


def P(cfg):
    p = dict(DEFAULT); p.update({k: v for k, v in ((cfg or {}).get("toggle") or {}).items() if not k.startswith("_")}); return p


def bottom_third(scores):
    """scores {id: score} (enabled channels) -> ids of the bottom third (at least 1; rounded up)."""
    ids = sorted(scores, key=lambda i: scores[i])
    return ids[:max(1, math.ceil(len(ids) / 3))] if ids else []


def baseline(bottom, probes, feats, min_log_n=10, skip_buffered_ttft=False, min_log_n_buffered=2, up_tests=None):
    """Bottom-third baseline for this round: per channel, first token = min(site probe first token, 1h log first-token median lt50), total = min(probe total, 1h p50);
    then the minimum across those channels (a candidate must beat every one of them). None without data.
    v1.91: log values only count with enough samples (lt50 needs lt_n >= min_log_n, p50 needs n >= min_log_n), so a few short requests cannot drag the baseline too low.
    v2.00 skip_buffered_ttft: the "first token" of a buffered (streaming=False) site probe is really the total time and must not be used as a first-token baseline;
    otherwise the bottom-third baseline is inflated and disabled channels are more easily judged as passing (observed: buffered probes raised the baseline enough to let a slower candidate pass,
    while log first tokens and the same-path baseline both rejected it). For buffered probes the first token falls back to: log lt50 (lt_n >= min_log_n_buffered) ->
    the channel's same-path direct-test first-token median -> otherwise the channel is left out of the first-token baseline. Total time is unchanged."""
    tt, to = [], []
    for i in bottom:
        pr = [x for x in (probes or {}).get(i, (probes or {}).get(str(i))) or [] if x.get("ok")]
        f60 = ((feats or {}).get(i) or (feats or {}).get(str(i)) or {}).get("60m") or {}
        lt_ok = f60.get("lt50") and (f60.get("lt_n") is None or f60["lt_n"] >= min_log_n)
        p_ok = f60.get("p50") and (f60.get("n") is None or f60["n"] >= min_log_n)
        pr_t = [x for x in pr if x.get("ttft") and not (skip_buffered_ttft and x.get("streaming") is False)]
        a = [x["ttft"] for x in pr_t] + ([f60["lt50"]] if lt_ok else [])
        if skip_buffered_ttft and pr and not pr_t and not a:
            if f60.get("lt50") and (f60.get("lt_n") or 0) >= min_log_n_buffered:
                a = [f60["lt50"]]
            else:
                ut = [r["ttft"] for r in ((up_tests or {}).get(i) or (up_tests or {}).get(str(i)) or []) if r.get("ok") and r.get("ttft")]
                if ut: a = [_med(ut)]
        b = [x["total"] for x in pr if x.get("total")] + ([f60["p50"]] if p_ok else [])
        if a: tt.append(min(a))
        if b: to.append(min(b))
    return dict(ttft=min(tt) if tt else None, total=min(to) if to else None)


def _med(xs):
    xs = sorted(xs); n = len(xs)
    return None if not n else (xs[n // 2] if n % 2 else round((xs[n // 2 - 1] + xs[n // 2]) / 2, 2))


def baseline_upstream(bottom, up_tests):
    """v1.95 same-path baseline: the bottom-third channels are also tested directly against their upstream this round (same path as disabled channels, no site overhead).
    Per channel take the median first token / total of successful tests, then the minimum across the bottom third. Channels without a successful test are left out; None if none."""
    tt, to = [], []
    for i in bottom or []:
        ok = [r for r in ((up_tests or {}).get(i) or (up_tests or {}).get(str(i)) or []) if r.get("ok") and r.get("ttft")]
        if not ok: continue
        tt.append(_med([r["ttft"] for r in ok])); to.append(_med([r["total"] for r in ok if r.get("total")] or [None]) if any(r.get("total") for r in ok) else None)
    to = [x for x in to if x is not None]
    if not tt: return None
    return dict(ttft=min(tt), total=min(to) if to else None, n=len(tt))


def test_pass_upstream(results, base):
    """v1.95 same-path verdict: all succeed, real streaming, and both median first token and median total beat the same-path bottom-third baseline."""
    why = []
    if not results: return False, ["no tests this round"]
    bad = [r for r in results if not (r.get("ok") and r.get("status") == 200 and r.get("ttft"))]
    if bad: why.append(f"{len(bad)}/{len(results)} tests failed")
    nst = [r for r in results if r.get("ok") and not ((r.get("deltas") or 0) >= 2 and (r.get("span") or 0) > 0)]
    if nst: why.append(f"{len(nst)} tests not really streaming")
    ok = [r for r in results if r.get("ok") and r.get("ttft")]
    if not base or base.get("ttft") is None: why.append("no same-path bottom-third baseline")
    elif ok:
        mt = _med([r["ttft"] for r in ok]); mo = _med([r["total"] for r in ok if r.get("total")]) if any(r.get("total") for r in ok) else None
        if mt >= base["ttft"]: why.append(f"median first token {mt}s does not beat same-path baseline {base['ttft']}s")
        if base.get("total") is not None and (mo is None or mo >= base["total"]): why.append(f"median total {mo}s does not beat same-path baseline {base['total']}s")
    return not why, why or ["same-path: all succeeded, real streaming, median first token and total both beat the direct bottom third"]


def test_pass(results, base):
    """Whether one round of disabled-channel tests passes; returns (ok, why)."""
    why = []
    if not results: return False, ["no tests this round"]
    bad = [r for r in results if not (r.get("ok") and r.get("status") == 200 and r.get("ttft"))]
    if bad: why.append(f"{len(bad)}/{len(results)} tests failed")
    nst = [r for r in results if r.get("ok") and not ((r.get("deltas") or 0) >= 2 and (r.get("span") or 0) > 0)]
    if nst: why.append(f"{len(nst)} tests not really streaming")
    ok = [r for r in results if r.get("ok") and r.get("ttft")]
    if ok and base:
        mt, mo = max(r["ttft"] for r in ok), max(r["total"] for r in ok)
        if base.get("ttft") is None or mt >= base["ttft"]: why.append(f"first token {mt}s does not beat bottom-third baseline {base.get('ttft')}s")
        if base.get("total") is None or mo >= base["total"]: why.append(f"total {mo}s does not beat bottom-third baseline {base.get('total')}s")
    elif not base: why.append("no bottom-third baseline")
    return not why, why or ["all succeeded, real streaming, first token and total both beat the bottom third"]


def pass_streak(history, cid):
    """history: past rounds' toggle records in time order; count this channel's latest consecutive passing rounds (excluding this round)."""
    n = 0
    for t in reversed(history or []):
        d = ((t or {}).get("disabled") or {}).get(str(cid)) or ((t or {}).get("disabled") or {}).get(cid)
        if d is None: continue
        if d.get("pass"): n += 1
        else: break
    return n


def last_streak(formal, cid, min_n60):
    """formal: formal rounds in time order [{ts_epoch, scores{id:score}, n60{id:n}}] (including this round).
    Returns (consecutive last-place rounds, span in minutes). Ranked only among channels with n60>=min_n60; the streak breaks when the channel itself lacks n60 or is absent from a round."""
    n, t_first, t_last = 0, None, None
    for r in reversed(formal or []):
        sc = {str(k): v for k, v in (r.get("scores") or {}).items()}; nn = {str(k): v for k, v in (r.get("n60") or {}).items()}
        elig = [k for k in sc if (nn.get(k) or 0) >= min_n60]
        if str(cid) not in elig or len(elig) < 2: break
        if min(elig, key=lambda k: sc[k]) != str(cid): break
        n += 1; t_first = r["ts_epoch"]; t_last = t_last or r["ts_epoch"]
    return n, (round((t_last - t_first) / 60, 1) if n else 0.0)


def _kget(d, cid):
    """v2.02: look up an id key regardless of str/int mixing."""
    d = d or {}
    for k in (str(cid), cid, int(cid) if str(cid).isdigit() else None):
        if k is not None and k in d: return d[k]
    return None


def probe_all_failed(probes, cid):
    """v2.02: all site probes (including rechecks) of the channel failed this round (timeout/5xx/no first token) -> True; no probes -> False."""
    ps = _kget(probes, cid) or []
    return bool(ps) and not any(x.get("ok") for x in ps)


def err_streak(formal, cid, err_rate, weights_hist=None, probe_fail=False):
    """How many consecutive formal rounds the channel had "persistent errors" while its old weight was already 0.
    Persistent errors = 1h error rate >= err_rate; with v2.02 probe_fail=True, all site probes (including rechecks) failing also counts
    (524s/timeouts are often not logged as type 5, e.g. two 90s probe timeouts while the 1h log shows 0 errors)."""
    n = 0
    for r in reversed(formal or []):
        e, w = _kget(r.get("err60"), cid), _kget(r.get("old"), cid)
        bad = (e is not None and e >= err_rate) or (probe_fail and probe_all_failed(r.get("probes"), cid))
        if bad and w == 0: n += 1
        else: break
    return n


def formal_current(now_epoch, scores, feats, chans, probes, real=True):
    """v2.02: this round's formal record (same structure as formal_from_rounds). scores is dec["scores"] ({id: {score}})."""
    return dict(ts_epoch=now_epoch, scores={k: (v or {}).get("score") for k, v in (scores or {}).items()},
                n60={k: n60_of(v, real) for k, v in (feats or {}).items()},
                err60={k: ((v or {}).get("60m") or {}).get("err_rate") for k, v in (feats or {}).items()},
                old={c["id"]: c["weight"] for c in (chans or [])}, probes=probes or {}, feats=feats or {})


def plan(cfg, enabled_ids, disabled_tests, history, formal, bottom_up_tests=None):
    """Returns {disabled: {id: {results, pass, why, streak, enable}}, enabled: {id: {last_streak, last_span_min, err_streak, disable, why}},
    base, bottom, n_enabled, actions:[...], notify:bool}. Decides only; no DB writes.
    disabled_tests: {id: results}; the last entry of formal should be this round (with scores/n60/err60/old/probes/feats)."""
    p = P(cfg); cur = (formal or [{}])[-1]
    scores = {int(k): v for k, v in (cur.get("scores") or {}).items() if int(k) in set(enabled_ids)}
    bot = bottom_third(scores)
    base = baseline(bot, cur.get("probes"), cur.get("feats"), p.get("min_log_n", 10),
                    p.get("skip_buffered_ttft", False), p.get("min_log_n_buffered", 2), bottom_up_tests)
    base_up = baseline_upstream(bot, bottom_up_tests) if bottom_up_tests is not None else None
    mode = p.get("baseline_mode", "site")
    out = dict(bottom=bot, base=base, base_up=base_up, baseline_mode=mode, disabled={}, enabled={}, actions=[], notify=False)
    n_en = len(enabled_ids)
    for cid, res in (disabled_tests or {}).items():
        ok_s, why_s = test_pass(res, base)
        ok_u, why_u = test_pass_upstream(res, base_up) if bottom_up_tests is not None else (None, ["no same-path test"])
        use_up = mode == "upstream" and base_up is not None
        ok, why = (ok_u, why_u) if use_up else (ok_s, why_s)
        st = pass_streak(history, cid) + 1 if ok else 0
        en = ok and st >= p["need_streak"]
        out["disabled"][str(cid)] = dict(results=res, pass_=ok, why=why, streak=st, enable=en,
                                         pass_site=ok_s, why_site=why_s, pass_up=ok_u, why_up=why_u, decided_by="upstream" if use_up else "site")
        if en:
            out["actions"].append(dict(op="enable", id=int(cid), weight=p["start_weight"], why=f"passed direct tests for {st} consecutive rounds: " + "; ".join(why)))
            n_en += 1
    for cid in enabled_ids:
        ls, span = last_streak(formal, cid, p["min_n60"]); es = err_streak(formal, cid, p["err_rate"], probe_fail=p.get("err_probe_fail", False))
        d, why = False, []
        if es >= p["err_rounds"]:
            if n_en - 1 >= p["hard_min"]:
                d = True; why.append(f"persistent errors for {es} consecutive rounds after weight 0 (1h error rate >= {p['err_rate']:.0%} or all site probes failed)")
                if n_en - 1 < p["min_enabled"]: out["notify"] = True; why.append(f"enabled count drops to {n_en - 1}; notify the operator immediately")
            else: why.append(f"persistent errors but enabled count is already at the floor {p['hard_min']}; not disabling")
        elif ls >= p["last_rounds"] and span >= p["last_span_min"]:
            if n_en - 1 >= p["min_enabled"]:
                d = True; why.append(f"ranked last for {ls} consecutive rounds ({span:.0f} min)")
            else: why.append(f"ranked last for {ls} consecutive rounds, but enabled count {n_en} is at the normal floor {p['min_enabled']}; no rank-based disable")
        out["enabled"][str(cid)] = dict(last_streak=ls, last_span_min=span, err_streak=es, disable=d, why=why)
        if d:
            out["actions"].append(dict(op="disable", id=int(cid), why="; ".join(why))); n_en -= 1
    for v in out["disabled"].values(): v["pass"] = v.pop("pass_")
    out["n_enabled_after"] = n_en
    return out


def n60_of(feat, real=True):
    """v1.96: definition of "enough real requests" for on/off. real=True uses mix_60m.n_user (logs without probe tokens);
    old records without mix_60m fall back to 60m.n. Probe logs are not real requests."""
    feat = feat or {}
    if real:
        m = feat.get("mix_60m") or {}
        if m.get("n_user") is not None: return m["n_user"]
    return (feat.get("60m") or {}).get("n")


def formal_from_rounds(rounds, real=True):
    """Extract the fields plan needs from formal rounds (dry_run=False) in rounds.jsonl."""
    from datetime import datetime
    out = []
    for r in rounds:
        if r.get("dry_run"): continue
        f = r.get("features") or {}
        out.append(dict(ts_epoch=datetime.fromisoformat(r["ts"]).timestamp(),
                        scores={k: v.get("score") for k, v in ((r.get("decision") or {}).get("scores") or {}).items()},
                        n60={k: n60_of(v, real) for k, v in f.items()},
                        err60={k: (v.get("60m") or {}).get("err_rate") for k, v in f.items()},
                        old=r.get("old") or {}, probes=r.get("probes") or {}, feats=f))
    return out


def history_from_rounds(rounds, now_epoch, gap_min=30):
    """Past rounds' toggle records (time order); a dry run and apply of the same round count once: adjacent records < gap_min apart keep only the later one;
    records < gap_min before this round are dropped too (treated as duplicate tests of this round)."""
    from datetime import datetime
    xs = [(datetime.fromisoformat(r["ts"]).timestamp(), r["toggle"]) for r in rounds if r.get("toggle")]
    xs = [x for x in xs if now_epoch - x[0] >= gap_min * 60]
    out = []
    for t, tg in xs:
        if out and t - out[-1][0] < gap_min * 60: out[-1] = (t, tg)
        else: out.append((t, tg))
    return [tg for _, tg in out]


def toggle_sql(actions, enabled_ids, priority=10, hard_min=3, min_enabled=4):
    """v1.92: turn plan() actions into one transactional SQL (enable: status=1/priority/weight; disable: status=2/weight 0; abilities synced for enabled/weight/priority).
    Re-check the floor: enables first, then disables; a disable leaving < hard_min enabled is rejected; a rank-based disable (no "error" in why) is rejected if the result < min_enabled.
    Returns (sql or None, executed actions, rejected actions)."""
    en = set(int(i) for i in enabled_ids)
    ok, rej, stmts = [], [], []
    for a in sorted(actions, key=lambda x: 0 if x["op"] == "enable" else 1):
        cid = int(a["id"])
        if a["op"] == "enable":
            if cid in en: rej.append(dict(a, reject="already enabled")); continue
            w = max(0, int(a.get("weight", 2)))
            stmts.append(f"update channels set status=1, priority={int(priority)}, weight={w} where id={cid} and status<>1;")
            en.add(cid); ok.append(a)
        elif a["op"] == "disable":
            if cid not in en: rej.append(dict(a, reject="not enabled")); continue
            err_reason = any("error" in str(x).lower() for x in ([a.get("why")] if isinstance(a.get("why"), str) else (a.get("why") or [])))
            floor = hard_min if err_reason else min_enabled
            if len(en) - 1 < floor: rej.append(dict(a, reject=f"enabled count would drop to {len(en) - 1}, below the floor {floor}")); continue
            stmts.append(f"update channels set status=2, weight=0 where id={cid} and status=1;")
            en.discard(cid); ok.append(a)
    if not stmts: return None, ok, rej
    ids = ",".join(str(int(a["id"])) for a in ok)
    sql = "BEGIN;\n" + "\n".join(stmts) + f"\nupdate abilities a set weight=c.weight, priority=c.priority, enabled=(c.status=1) from channels c where a.channel_id=c.id and c.id in ({ids});\nCOMMIT;\n"
    return sql, ok, rej
