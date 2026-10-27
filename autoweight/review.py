"""Review: compare the previous round's decision with this round's observations to see whether up-weighted channels really did better."""
def review(prev, cur_feats):
    if not prev: return None
    out = {}
    for i, w in prev.get("decision", {}).get("target", {}).items():
        before = prev["features"].get(str(i)) or prev["features"].get(i)
        after = cur_feats.get(int(i))
        if not before or not after: continue
        b, a = before["10m"], after["10m"]
        out[str(i)] = dict(weight_set=w, applied=prev.get("applied"),
                           n_10m=(b["n"], a["n"]), p50=(b["p50"], a["p50"]), p90=(b["p90"], a["p90"]),
                           slow=(b["slow_rate"], a["slow_rate"]), err=(b["err"], a["err"]))
    return dict(prev_ts=prev["ts"], channels=out)
