"""Effect evaluation: reads data/rounds.jsonl only, no DB.

For each pair of adjacent "formal rounds" (A, B) (B at least --min-gap minutes after A), check how A's weights actually played out in B's 1h window:
    - weight share vs actual traffic share (sticky sessions / cache sync make them drift)
    - p50 / p90 / error rate weighted by actual traffic (real user experience)
    - gap vs "send everything to B's top-scoring channel" (regret, lower is better)
Usage: python3 evaluate.py [--shadow --since 45m] [--min-gap 40] [--json] [--stability]
"""
from autoweight import config as _awcfg
import argparse, json, os
from datetime import datetime

HERE = os.path.dirname(os.path.abspath(__file__))


def load_rounds(path):
    with open(path) as f:
        return [json.loads(l) for l in f if l.strip()]


def effective_weights(r):
    """Weights effective in the DB after the round: target if written, else old."""
    src = r["decision"]["target"] if r.get("applied") else (r.get("old") or r["decision"]["target"])
    return {str(k): v for k, v in src.items()}


def pick_pairs(rounds, min_gap_min=40):
    """Multiple dry runs within an hour keep only the last; adjacent formal rounds must be >= min_gap apart."""
    by_hour = {}
    for r in rounds:
        by_hour[r["ts"][:13]] = r
    seq = [by_hour[k] for k in sorted(by_hour)]
    pairs = []
    for a, b in zip(seq, seq[1:]):
        gap = (datetime.fromisoformat(b["ts"]) - datetime.fromisoformat(a["ts"])).total_seconds() / 60
        if gap >= min_gap_min:
            pairs.append((a, b))
    return pairs


def evaluate_pair(a, b):
    w = effective_weights(a)
    feats = {str(k): v["60m"] for k, v in b["features"].items()}
    ids = sorted(set(w) & set(feats), key=int)
    wt = sum(w[i] for i in ids) or 1
    nt = sum(feats[i]["n"] for i in ids) or 0
    rows, wp50, wp90, werr = {}, 0.0, 0.0, 0.0
    for i in ids:
        f = feats[i]
        share_n = f["n"] / nt if nt else 0.0
        rows[i] = dict(weight=w[i], w_share=round(w[i] / wt, 3), n=f["n"], n_share=round(share_n, 3),
                       p50=f["p50"], p90=f["p90"], err_rate=f["err_rate"])
        mix = (b["features"].get(i) or {}).get("mix_60m")
        if mix:  # v1.6: real users / token concentration (absent in old records)
            rows[i].update(n_user=mix["n_user"], tokens=mix["tokens"], top_tok_share=mix["top_tok_share"])
        if f["n"]:
            wp50 += share_n * (f["p50"] or 0)
            wp90 += share_n * (f["p90"] or 0)
            werr += share_n * (f["err_rate"] or 0)
    # scores may be {id: float} (tests) or {id: {"score": float, ...}} (actual run.py output)
    scores = {str(k): (v["score"] if isinstance(v, dict) else v)
              for k, v in (b["decision"].get("scores") or {}).items()}
    best = max(scores, key=scores.get) if scores else None
    best_share = rows.get(best, {}).get("n_share") if best else None
    return dict(set_at=a["ts"], seen_at=b["ts"], applied=bool(a.get("applied")), n_total=nt,
                w_p50=round(wp50, 2), w_p90=round(wp90, 2), w_err=round(werr, 4),
                best_after=best, best_traffic_share=best_share, channels=rows)


def stability(rounds):
    """v1.14 diagnostics: target-weight differences between a dry run and the following formal round within the same hour.
    They are only tens of seconds apart with nearly identical data; a large difference means scores sit on a rank threshold and the decision is noise-sensitive.
    Returns [{hour, dry_ts, apply_ts, max_diff, order_flip, dry, apply}]."""
    by_hour = {}
    for r in rounds:
        by_hour.setdefault(r["ts"][:13], []).append(r)
    out = []
    for h in sorted(by_hour):
        rs = by_hour[h]
        app = [r for r in rs if r.get("applied")]
        if not app:
            continue
        a = app[-1]
        dry = [r for r in rs if not r.get("applied") and r["ts"] < a["ts"]]
        if not dry:
            continue
        d = dry[-1]
        dt = {str(k): v for k, v in d["decision"]["target"].items()}
        at = {str(k): v for k, v in a["decision"]["target"].items()}
        ids = set(dt) | set(at)
        diff = max((abs(dt.get(i, 0) - at.get(i, 0)) for i in ids), default=0)
        flip = (d["decision"].get("order") or [None])[0] != (a["decision"].get("order") or [None])[0]
        out.append(dict(hour=h, dry_ts=d["ts"], apply_ts=a["ts"], max_diff=diff, order_flip=flip, dry=dt, apply=at))
    return out


def cap_review(rounds, cap_ids):
    """v1.16 diagnostics: soft-cap review. For each soft-capped channel, count how often it was truncated in formal rounds,
    and compare its 1h empty-success rate / error rate / reasoning-token p50 with other channels in those rounds.
    Used to judge whether the quality risk behind the cap (e.g. too little reasoning / empty replies under high concurrency) is still visible; read-only, no config changes, no scoring.
    Returns {id: {rounds, truncated, own:{empty,err,ctok}, peers:{empty,err,ctok}}} (all means)."""
    def mean(xs):
        xs = [x for x in xs if x is not None]
        return round(sum(xs) / len(xs), 4) if xs else None
    out = {}
    applied = [r for r in rounds if r.get("applied") and r.get("features")]
    for cid in map(str, cap_ids):
        own = {"empty": [], "err": [], "ctok": []}
        peers = {"empty": [], "err": [], "ctok": []}
        n_r = trunc = 0
        for r in applied:
            f = r["features"]
            if cid not in f:
                continue
            n_r += 1
            if any(n.startswith(f"#{cid} soft cap") for n in (r["decision"].get("notes") or [])):
                trunc += 1
            for k, src in f.items():
                w = f[k].get("60m") or {}
                dst = own if k == cid else peers
                dst["empty"].append(w.get("empty_rate")); dst["err"].append(w.get("err_rate")); dst["ctok"].append(w.get("ctok_p50"))
        out[cid] = dict(rounds=n_r, truncated=trunc,
                        own={k: mean(v) for k, v in own.items()}, peers={k: mean(v) for k, v in peers.items()})
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--rounds", default=os.path.join(HERE, "data/rounds.jsonl"))
    ap.add_argument("--min-gap", type=float, default=40)
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--stability", action="store_true", help="target differences between dry run and formal round in the same hour (decision stability)")
    ap.add_argument("--caps", action="store_true", help="soft-cap review: truncation count and empty/error/reasoning tokens vs other channels")
    ap.add_argument("--shadow", action="store_true", help="v1.19 shadow review: summarise data/shadow.jsonl")
    ap.add_argument("--since", default="45m", help="shadow review window, e.g. 45m / 2h")
    ap.add_argument("--shadow-file", default=os.path.join(HERE, "data/shadow.jsonl"))
    args = ap.parse_args()
    if args.shadow:
        import evaluate_shadow as ES
        recs = load_rounds(args.shadow_file) if os.path.exists(args.shadow_file) else []
        s = ES.summarize(recs, ES.parse_since(args.since))
        print(json.dumps(s, ensure_ascii=False, indent=1, default=str) if args.json else ES.render(s)); return
    if args.caps:
        cfg = _awcfg.load(os.path.join(HERE, "config.json"))
        ids = [k for k, v in (cfg.get("soft_caps") or {}).items() if not k.startswith("_") and v.get("enabled", True)]
        cr = cap_review(load_rounds(args.rounds), ids)
        if args.json:
            print(json.dumps(cr, ensure_ascii=False, indent=1)); return
        for i, c in cr.items():
            print(f"#{i} rounds={c['rounds']} truncated={c['truncated']} own={c['own']} peers={c['peers']}")
        return
    if args.stability:
        st = stability(load_rounds(args.rounds))
        if args.json:
            print(json.dumps(st, ensure_ascii=False, indent=1)); return
        for x in st:
            print(f"{x['hour']} max_diff={x['max_diff']} top_flip={x['order_flip']} dry={x['dry']} apply={x['apply']}")
        return
    res = [evaluate_pair(a, b) for a, b in pick_pairs(load_rounds(args.rounds), args.min_gap)]
    if args.json:
        print(json.dumps(res, ensure_ascii=False, indent=1)); return
    for r in res:
        print(f"{r['set_at'][:16]} -> {r['seen_at'][:16]} applied={r['applied']} n={r['n_total']} "
              f"w_p50={r['w_p50']} w_p90={r['w_p90']} w_err={r['w_err']} best=#{r['best_after']} share={r['best_traffic_share']}")
        for i, c in r["channels"].items():
            print(f"   #{i:<3} w={c['weight']:>2} ({c['w_share']:.0%})  traffic {c['n']:>3} ({c['n_share']:.0%})  p50={c['p50']} p90={c['p90']} err={c['err_rate']}"
                  + (f"  user={c['n_user']} tokens={c['tokens']} top_tok={c['top_tok_share']}" if 'n_user' in c else ""))


if __name__ == "__main__":
    main()
