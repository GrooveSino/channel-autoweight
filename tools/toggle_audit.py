"""v1.98 on/off baseline comparison audit (reads local rounds.jsonl only; no DB, no effect on scoring or on/off).
Since v1.95 every round records two verdicts for disabled channels: site baseline (bottom-third site probes / logs) and same-path upstream baseline (bottom third also tested directly),
to "collect a few rounds before deciding whether to switch to upstream". This script summarises each formal round (dry run and apply de-duplicated):
per round and disabled channel whether each baseline passed, the gap (disabled first token / total divided by baseline), agreement rate of the two, consecutive passing rounds per baseline,
and the median streaming span and deltas of disabled-channel tests, to judge whether "real streaming" is reliable.
Usage: python3 tools/toggle_audit.py [--rounds N] [--json]"""
import json, os, sys, argparse
sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
from autoweight import toggle as TG


def _ratio(a, b):
    return round(a / b, 2) if a and b else None


def dedup(rounds, gap_min=30):
    """Records with toggle in time order; of adjacent records < gap_min apart only the later one is kept (same rule as history_from_rounds, but the latest round is kept)."""
    from datetime import datetime
    xs = [(datetime.fromisoformat(r["ts"]).timestamp(), r) for r in rounds if r.get("toggle")]
    out = []
    for t, r in xs:
        if out and t - out[-1][0] < gap_min * 60: out[-1] = (t, r)
        else: out.append((t, r))
    return [r for _, r in out]


def audit(rounds, last=None):
    rs = dedup(rounds)
    if last: rs = rs[-last:]
    rows, per = [], {}
    for r in rs:
        tg = r["toggle"]; base = tg.get("base") or {}; bu = tg.get("base_up") or {}
        for cid, d in (tg.get("disabled") or {}).items():
            res = [x for x in d.get("results") or [] if x.get("ok")]
            tt = [x["ttft"] for x in res if x.get("ttft")]; to = [x["total"] for x in res if x.get("total")]
            row = dict(ts=r["ts"][11:16], cid=str(cid), n_ok=len(res), n=len(d.get("results") or []),
                       pass_site=d.get("pass_site", d.get("pass")), pass_up=d.get("pass_up"),
                       r_site_ttft=_ratio(max(tt) if tt else None, base.get("ttft")),
                       r_up_ttft=_ratio(TG._med(tt), bu.get("ttft")),
                       span_med=TG._med([x.get("span") for x in res if x.get("span") is not None]),
                       deltas_med=TG._med([x.get("deltas") for x in res if x.get("deltas") is not None]),
                       decided_by=d.get("decided_by", "site"))
            rows.append(row); per.setdefault(row["cid"], []).append(row)
    summ = {}
    for cid, xs in per.items():
        both = [x for x in xs if x["pass_up"] is not None and x["pass_site"] is not None]
        def streak(key):
            n = 0
            for x in reversed(xs):
                if x[key]: n += 1
                else: break
            return n
        summ[cid] = dict(rounds=len(xs), n_both=len(both),
                         agree=round(sum(x["pass_site"] == x["pass_up"] for x in both) / len(both), 2) if both else None,
                         pass_site=sum(bool(x["pass_site"]) for x in xs), pass_up=sum(bool(x["pass_up"]) for x in both),
                         streak_site=streak("pass_site"), streak_up=streak("pass_up"))
    return dict(rows=rows, summary=summ)


def main():
    ap = argparse.ArgumentParser(); ap.add_argument("--rounds", type=int, default=12); ap.add_argument("--json", action="store_true")
    ap.add_argument("--file", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "data", "rounds.jsonl"))
    a = ap.parse_args()
    rounds = [json.loads(l) for l in open(a.file) if l.strip()]
    out = audit(rounds, a.rounds)
    if a.json: print(json.dumps(out, ensure_ascii=False)); return
    print("| Round | Channel | Success | Site pass | Same-path pass | First token / site baseline | Median first token / same-path baseline | Median span | Median deltas |")
    print("|---|---|---|---|---|---|---|---|---|")
    for x in out["rows"]:
        print(f"| {x['ts']} | #{x['cid']} | {x['n_ok']}/{x['n']} | {x['pass_site']} | {x['pass_up']} | {x['r_site_ttft']} | {x['r_up_ttft']} | {x['span_med']} | {x['deltas_med']} |")
    for cid, s in out["summary"].items():
        print(f"SUMMARY #{cid} {s}")


if __name__ == "__main__":
    main()
