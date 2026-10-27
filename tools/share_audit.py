"""v1.26 new-session share audit (read-only diagnostics, not used for scoring).

Weights only decide which channel a "new session" lands on; follow-up requests of the same (token, model) stick to the original channel (session stickiness).
So "total request share" decouples from weight share and can neither show whether weights take effect nor how representative a channel's experience is.
This tool splits requests into:
    - new-session starts: same (token, model) more than gap minutes after its previous request (or the first in the window with no predecessor within lookback)
    - continuations: the rest; reports the share of continuations on the same channel as the previous request (stick rate)
and compares new-session share vs weight share (weights normalised over the enabled channels of the same model; expectation computed per start's model).
Usage: python3 tools/share_audit.py [--since 45] [--gap 10] [--json]
"""
import argparse, json, os, sys
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight import config as _awcfg

def audit(rows, weights, model_channels, t0, gap_s=600, probe_toks=()):
    """rows: dict(ch,t,tok,model,type), sorted or not; only requests with t>=t0 are counted, t<t0 only provides prior context.
    weights: {ch: w}; model_channels: {model: [ch,...]} (enabled channels serving that model)."""
    rows = sorted((r for r in rows if r["tok"] not in set(probe_toks)), key=lambda r: r["t"])
    last = {}
    out = {ch: dict(starts=0, exp=0.0, cont=0, stick=0, n=0) for ch in weights}
    for r in rows:
        k = (r["tok"], r["model"])
        prev = last.get(k)
        last[k] = r
        if r["t"] < t0 or r["ch"] not in out:
            continue
        o = out[r["ch"]]
        o["n"] += 1
        if prev is None or r["t"] - prev["t"] > gap_s:
            o["starts"] += 1
            chs = [c for c in model_channels.get(r["model"], weights) if weights.get(c, 0) > 0]
            tw = sum(weights[c] for c in chs)
            for c in chs:
                out[c]["exp"] += weights[c] / tw
        else:
            o["cont"] += 1
            if prev["ch"] == r["ch"]:
                o["stick"] += 1
    S = sum(o["starts"] for o in out.values())
    N = sum(o["n"] for o in out.values())
    res = {}
    for ch, o in out.items():
        res[ch] = dict(weight=weights[ch], n=o["n"], n_share=round(o["n"] / N, 3) if N else None,
                       starts=o["starts"], start_share=round(o["starts"] / S, 3) if S else None,
                       exp_starts=round(o["exp"], 1), exp_share=round(o["exp"] / S, 3) if S else None,
                       stick_rate=round(o["stick"] / o["cont"], 3) if o["cont"] else None,
                       req_per_start=round(o["n"] / o["starts"], 1) if o["starts"] else None)
    cont = sum(o["cont"] for o in out.values()); stick = sum(o["stick"] for o in out.values())
    # total deviation of new-session share from expectation (chi-square, k-1 degrees of freedom); no conclusion with too few starts
    chi = sum((o["starts"] - o["exp"]) ** 2 / o["exp"] for o in out.values() if o["exp"] > 0)
    return dict(starts=S, n=N, stick_rate=round(stick / cont, 3) if cont else None, chi2=round(chi, 2),
                df=max(0, sum(1 for o in out.values() if o["exp"] > 0) - 1), channels=res)

def fetch(cfg, ids, since_min, gap_min):
    from autoweight.db import ro
    lb = since_min + gap_min
    sql = f"""select channel_id, created_at, coalesce(token_id,0), model_name from logs
where created_at >= extract(epoch from now())::bigint - {int(lb)*60} and type in (2,5)
and channel_id in ({",".join(map(str, ids))}) and model_name like '{cfg["model_like"]}' and model_name not like '{cfg["exclude_model_like"]}';
select a.model, a.channel_id from abilities a join channels c on c.id=a.channel_id
where c.status=1 and a.enabled and a.channel_id in ({",".join(map(str, ids))}) and a.model like '{cfg["model_like"]}';
select extract(epoch from now())::bigint;"""
    r = ro(cfg, sql)
    now = int(r[-1][0])
    logs = [dict(ch=int(x[0]), t=int(x[1]), tok=int(x[2]), model=x[3]) for x in r[:-1] if len(x) == 4]
    mc = {}
    for x in r[:-1]:
        if len(x) == 2:
            mc.setdefault(x[0], []).append(int(x[1]))
    return now, logs, mc

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--since", type=int, default=45); ap.add_argument("--gap", type=int, default=10)
    ap.add_argument("--json", action="store_true")
    a = ap.parse_args()
    base = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    cfg = _awcfg.load(os.path.join(base, "config.json"))
    from autoweight.db import channels
    ch = channels(cfg)
    w = {c["id"]: c["weight"] for c in ch}
    now, logs, mc = fetch(cfg, list(w), a.since, a.gap)
    res = audit(logs, w, mc, now - a.since * 60, a.gap * 60, cfg.get("probe_token_ids") or [])
    if a.json:
        print(json.dumps(res, ensure_ascii=False)); return
    print(f"New-session share audit, last {a.since} min (gap={a.gap}m): starts {res['starts']} / requests {res['n']}, continuation stick rate {res['stick_rate']}, chi2={res['chi2']} df={res['df']}")
    print("| Channel | Weight | Requests n (share) | New-session starts (share) | Expected starts (share) | Requests per start | Stick rate |")
    print("|---|---|---|---|---|---|---|")
    for i, c in res["channels"].items():
        print(f"| #{i} | {c['weight']} | {c['n']} ({c['n_share']}) | {c['starts']} ({c['start_share']}) | {c['exp_starts']} ({c['exp_share']}) | {c['req_per_start']} | {c['stick_rate']} |")

if __name__ == "__main__":
    main()
