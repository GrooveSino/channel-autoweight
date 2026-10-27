#!/usr/bin/env python3
"""v1.17: render the latest round in data/rounds.jsonl (default: latest formal round) as a Markdown report table.

Reads local files only: no DB connection, no keys. Columns: channel, priority, old/new weight, status, first token, streaming,
1h requests, p50/p90 (s), >60s slow share, error rate; disabled channels on the watch list get their probe results appended.
Usage: python3 report_table.py [--file data/rounds.jsonl] [--any]   # --any does not require applied
"""
import argparse, json


def _fmt(v, nd=None, pct=False):
    if v is None:
        return "-"
    if pct:
        return f"{v*100:.1f}%"
    return f"{v:.{nd}f}" if nd is not None else str(v)


def pick_round(rows, applied_only=True):
    for r in reversed(rows):
        if not applied_only or r.get("applied") or r.get("dry_run") is False:
            return r
    return rows[-1] if rows else None


def _trust(r, cid):
    """v1.18: trust previous -> this round (fast fall, slow rise); - without a record"""
    t = (r.get("trust") or {}).get(cid)
    if not t: return "-"
    return f"{_fmt(t.get('prev'), 2)}->{t['trust']:.2f}"


def render(r):
    old, tgt = r.get("old", {}), r.get("decision", {}).get("target", {})
    scores = r.get("decision", {}).get("scores", {})
    lines = ["| Channel | Priority | Old weight | New weight | Status | First token (s) | Streaming | 1h requests | p50/p90 (s) | Slow >60s | Error rate | Trust |",
             "|---|---|---|---|---|---|---|---|---|---|---|---|"]
    for ch in r.get("channels", []):
        cid = str(ch["id"])
        f60 = r.get("features", {}).get(cid, {}).get("60m", {})
        s = scores.get(cid, {})
        stream = {True: "yes", False: "no (buffered)", None: "-"}.get(s.get("streaming"))
        lines.append(f"| {ch['name']} #{cid} | {ch.get('priority')} | {old.get(cid, '-')} | {tgt.get(cid, old.get(cid, '-'))} | enabled | "
                     f"{_fmt(s.get('ttft'), 2)} | {stream} | {_fmt(f60.get('n'))} | {_fmt(f60.get('p50'))}/{_fmt(f60.get('p90'))} | "
                     f"{_fmt(f60.get('slow_rate'), pct=True)} | {_fmt(f60.get('err_rate'), pct=True)} | {_trust(r, cid)} |")
    shown = {str(ch["id"]) for ch in r.get("channels", [])}
    for cid, w in (r.get("watch") or {}).items():
        if str(cid) in shown:   # v1.87: watch-list channels that are enabled and already listed above are not repeated
            continue
        info, probes, f60 = w.get("info", {}), w.get("probes", []), w.get("f60", {})
        ok = [p for p in probes if p.get("ok")]
        if ok:
            p = min(ok, key=lambda x: x.get("ttft") or 1e9)
            probe = f"{_fmt(p.get('ttft'), 2)}"
            stream = {True: "yes", False: "no (buffered)", None: "-"}.get(p.get("streaming"))
        else:
            probe = "probe failed(" + ",".join(str(p.get("status")) for p in probes) + ")"
            stream = "-"
        status = "enabled" if info.get("status") == 1 else f"disabled(status={info.get('status')})"
        lines.append(f"| {info.get('name')} #{cid} | {info.get('priority')} | {info.get('weight')} | {info.get('weight')} (unchanged) | {status} | "
                     f"{probe} | {stream} | {_fmt(f60.get('n'))} | {_fmt(f60.get('p50'))}/{_fmt(f60.get('p90'))} | - | {_fmt(f60.get('err_rate'), pct=True)} | - |")
    return "\n".join(lines)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--file", default="data/rounds.jsonl")
    ap.add_argument("--any", action="store_true")
    a = ap.parse_args()
    rows = [json.loads(l) for l in open(a.file, encoding="utf-8") if l.strip()]
    r = pick_round(rows, applied_only=not a.any)
    print(f"Round {r.get('ts')} applied={r.get('applied')}")
    print(render(r))


if __name__ == "__main__":
    main()
