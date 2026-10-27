"""Active probe of real first-token time: send a short streaming request to a channel and record when the first non-empty text delta arrives.
The key is only used in memory; no output or log ever contains it.
First-token definition: only the first response.output_text.delta (or chat delta.content) with non-empty text counts;
empty status events such as response.created / in_progress do not. Decisions never use the raw log other.frt (some upstreams log empty status events as the first token).
This definition has been used since v1."""
import json, os, subprocess, time, requests

def load_key(cfg):
    """Probe key lookup order: env AUTOWEIGHT_PROBE_KEY -> local key files (probe.key_paths_local) -> remote file over ssh (probe.key_path_remote)."""
    if os.environ.get("AUTOWEIGHT_PROBE_KEY"):
        return os.environ["AUTOWEIGHT_PROBE_KEY"].strip()
    for p in cfg["probe"].get("key_paths_local") or []:
        p = os.path.expanduser(p)
        if os.path.exists(p):
            return open(p).read().strip()
    if not cfg["probe"].get("key_path_remote"):
        return None
    r = subprocess.run(["ssh", cfg["ssh_host"], f'cat {cfg["probe"]["key_path_remote"]}'],
                       capture_output=True, text=True, timeout=20)
    return r.stdout.strip() or None

def probe_once(cfg, key, ch_id):
    p = cfg["probe"]
    t0 = time.time(); first = None; chunks = 0; text_events = []; status = None; err = None
    try:
        with requests.post(p["base_url"] + p["path"], stream=True, timeout=p["timeout_s"],
                           headers={"Authorization": f"Bearer {key}-{ch_id}", "Content-Type": "application/json"},
                           json={"model": p["model"], "input": p["prompt"], "stream": True,
                                 "max_output_tokens": 600}) as r:
            status = r.status_code
            if r.status_code != 200:
                err = r.text[:200]
            else:
                for line in r.iter_lines(decode_unicode=True):
                    if not line or not line.startswith("data:"): continue
                    d = line[5:].strip()
                    if d == "[DONE]": break
                    try: j = json.loads(d)
                    except Exception: continue
                    chunks += 1
                    delta = j.get("delta") if j.get("type") == "response.output_text.delta" else None
                    if delta is None and "choices" in j:   # chat.completions compatibility
                        delta = ((j["choices"] or [{}])[0].get("delta") or {}).get("content")
                    if delta:
                        now = time.time() - t0
                        text_events.append(now)
                        if first is None: first = now
                    if j.get("type") in ("response.completed", "response.failed"): break
    except Exception as e:
        err = type(e).__name__ + ": " + str(e)[:150]
    total = time.time() - t0
    # Real-streaming check (old): >=3 text deltas and first/last gap > 0.3s, or first token clearly before the end
    # Real-streaming check: text deltas spread over >= 1s. Too short a span (short answer arriving at once) gives None = undecidable, no penalty
    spread = (text_events[-1] - text_events[0]) if text_events else 0
    if len(text_events) >= 20:      # long enough: span >= 1s means real streaming, < 0.3s means buffered then flushed at once
        streaming = True if spread >= 1.0 else (False if spread < 0.3 else None)
    else:
        streaming = None
    if err and key in err: err = err.replace(key, "***")
    return dict(ok=first is not None, status=status, ttft=round(first, 2) if first else None,
                total=round(total, 2), deltas=len(text_events), streaming=streaming, err=err)

def run(cfg, ids):
    if not cfg["probe"]["enabled"]: return {}
    key = load_key(cfg)
    if not key: return {i: [dict(ok=False, err="no key")] for i in ids}
    from concurrent.futures import ThreadPoolExecutor
    jobs = [(i, k) for i in ids for k in range(cfg["probe"]["n_per_channel"])]
    with ThreadPoolExecutor(len(jobs)) as ex:
        res = list(ex.map(lambda j: (j[0], probe_once(cfg, key, j[0])), jobs))
    out = {}
    for i, r in res: out.setdefault(i, []).append(r)
    return out


def history_ttft(rounds, ch_id, k):
    """First-token times of successful probes for this channel over the last k rounds (minimum per round), used as the outlier baseline."""
    vals = []
    for r in reversed(rounds or []):
        ps = (r.get("probes") or {}).get(str(ch_id)) or (r.get("probes") or {}).get(ch_id) or []
        ok = [p["ttft"] for p in ps if p.get("ok") and p.get("ttft") is not None]
        if ok: vals.append(min(ok))
        if len(vals) >= k: break
    return vals


def needs_recheck(probes, hist, rc):
    """A single probe first token well above the history median (> ratio x median and > min_s), or a failed probe -> recheck.
    With fewer than min_hist rounds of history, only failures are rechecked."""
    ok = [p["ttft"] for p in probes or [] if p.get("ok") and p.get("ttft") is not None]
    if not ok: return True
    if len(hist) < rc.get("min_hist", 2): return False
    med = sorted(hist)[len(hist) // 2]
    t = min(ok)
    return t > rc["min_s"] and t > rc["ratio"] * med


def recheck(cfg, probes, rounds):
    """Probe suspected one-off slow/failed channels once more and append to that channel's probes (ttft_of takes the minimum, so one-off spikes are filtered;
    a channel that really got slow is slow twice and is penalised as usual). Recheck records carry recheck=True for later review."""
    rc = cfg["probe"].get("recheck") or {}
    if not rc.get("enabled") or not probes: return []
    ids = [i for i, ps in probes.items()
           if needs_recheck(ps, history_ttft(rounds, i, rc.get("history", 3)), rc)]
    if not ids: return []
    key = load_key(cfg)
    if not key: return []
    from concurrent.futures import ThreadPoolExecutor
    with ThreadPoolExecutor(len(ids)) as ex:
        res = list(ex.map(lambda i: (i, probe_once(cfg, key, i)), ids))
    for i, r in res: probes[i].append(dict(r, recheck=True))
    return ids


def anomalies(probes):
    """v1.7 diagnostics (not used for scoring): classify probe anomalies for review and alerts.
    Returns {channel_id: [kind...]}, kinds:
      empty_200   HTTP 200 but no non-empty text delta (empty reply / upstream stalled)
      rate_limit  HTTP 429
      http_err    any other non-200 status or request exception
    Only counted when **all** probes of the channel are anomalous (a successful recheck means it was a one-off)."""
    out = {}
    for i, ps in (probes or {}).items():
        if not ps or any(p.get("ok") for p in ps): continue
        kinds = []
        for p in ps:
            st = p.get("status")
            if st == 429: k = "rate_limit"
            elif st == 200 and not p.get("deltas"): k = "empty_200"
            else: k = "http_err"
            if k not in kinds: kinds.append(k)
        out[i] = kinds
    return out


def reuse(rounds, ids, now_ts, max_age_min):
    """v1.12: reuse the probe results of the latest round (within max_age_min minutes), so --apply after a dry run does not probe again
    and --no-probe does not lose first-token data. Returns only channels currently in use; {} when too old or no probes."""
    from datetime import datetime
    for r in reversed(rounds or []):
        if not r.get("probes"): continue
        try: age = (now_ts - datetime.fromisoformat(r["ts"]).timestamp()) / 60
        except Exception: return {}
        if age < 0 or age > max_age_min: return {}
        return {int(k): v for k, v in r["probes"].items() if int(k) in set(ids)}
    return {}


def reuse_age(rounds, now_ts):
    """v1.68: real age (minutes) of reused probes. Uses the latest record with probes that is not a probes_reused copy.
    For --apply --reuse-probe after a dry run, the dry-run probe is still fresh evidence after the last shadow point;
    it used to be treated as 999 minutes old, which made dry-run and apply disagree on trust/suggestions. Returns None if not found."""
    from datetime import datetime
    for r in reversed(rounds or []):
        if not r.get("probes") or r.get("probes_reused"): continue
        try: return max(0.0, (now_ts - datetime.fromisoformat(r["ts"]).timestamp()) / 60)
        except Exception: return None
    return None
