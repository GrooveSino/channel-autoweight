"""v1.83: direct upstream probe for disabled channels (read-only, no DB writes, bypasses the site).
The site refuses to probe disabled channels, so when every channel of the same upstream is disabled there is no evidence of recovery.
Approach: the whole probe script runs on the production host via ssh stdin; on that host it reads the channel base_url and key with BEGIN READ ONLY,
sends one short streaming /v1/responses request directly to the upstream, and returns only status / first token / total time / delta count / error class.
The key only lives in that remote process's memory: it never reaches the local machine and never appears in any output or log (error text is also masked with ***)."""
from autoweight import config as _awcfg
import json, subprocess

REMOTE = r'''
import json, subprocess, sys, time, requests
cid = int(sys.argv[1]); model = sys.argv[2]; timeout = float(sys.argv[3]); prompt = sys.argv[4]; container = sys.argv[5]
sql = "BEGIN READ ONLY; select base_url, split_part(key, E'\\n', 1) from channels where id=%d; COMMIT;" % cid
r = subprocess.run(["docker", "exec", "-i", container, "sh", "-c",
                    'psql -v ON_ERROR_STOP=1 -U $POSTGRES_USER -d $POSTGRES_DB -At -F"|"'],
                   input=sql, capture_output=True, text=True, timeout=30)
rows = [l for l in r.stdout.splitlines() if l not in ("BEGIN", "COMMIT") and "|" in l]
if not rows:
    print(json.dumps({"ok": False, "err": "channel_not_found"})); sys.exit(0)
base, key = rows[0].split("|", 1)
base = base.rstrip("/"); key = key.strip()
t0 = time.time(); first = None; ev = []; status = None; err = None
try:
    with requests.post(base + "/v1/responses", stream=True, timeout=timeout,
                       headers={"Authorization": "Bearer " + key, "Content-Type": "application/json"},
                       json={"model": model, "input": prompt, "stream": True, "max_output_tokens": 300}) as resp:
        status = resp.status_code
        if status != 200:
            err = resp.text[:160]
        else:
            for line in resp.iter_lines(decode_unicode=True):
                if not line or not line.startswith("data:"): continue
                d = line[5:].strip()
                if d == "[DONE]": break
                try: j = json.loads(d)
                except Exception: continue
                if j.get("type") == "response.output_text.delta" and j.get("delta"):
                    now = time.time() - t0; ev.append(now)
                    if first is None: first = now
                if j.get("type") in ("response.completed", "response.failed"): break
except Exception as e:
    err = type(e).__name__ + ": " + str(e)[:120]
if err and key: err = err.replace(key, "***")
print(json.dumps({"ok": first is not None, "status": status, "ttft": round(first, 2) if first else None,
                  "total": round(time.time() - t0, 2), "deltas": len(ev),
                  "span": round(ev[-1] - ev[0], 2) if ev else None, "err": err}))
'''

def classify(res, max_ttft=20.0):
    """Classify one upstream probe: ok / slow / timeout / http_err / empty."""
    if res.get("ok"):
        return "slow" if (res.get("ttft") or 0) > max_ttft else "ok"
    e = (res.get("err") or "")
    if "Timeout" in e or res.get("status") in (504, 524, 408):
        return "timeout"
    if res.get("status") and res["status"] != 200:
        return "http_err"
    return "empty"

def verdict(results, need_ok=2, max_ttft=20.0):
    """Evidence of "upstream recovered" requires at least need_ok probes, all ok (not slow, no timeout)."""
    cls = [classify(r, max_ttft) for r in results]
    return dict(classes=cls, recovered=len(cls) >= need_ok and all(c == "ok" for c in cls))

def probe(cfg, ch_id, n=2, model=None, timeout=90):
    model = model or cfg["probe"]["model"]
    out = []
    for _ in range(n):
        r = subprocess.run(["ssh", "-o", "ConnectTimeout=10", cfg["ssh_host"], "python3", "-", str(int(ch_id)),
                            model, str(timeout), cfg["probe"]["prompt"], cfg["pg_container"]],
                           input=REMOTE, capture_output=True, text=True, timeout=timeout + 40)
        try:
            out.append(json.loads(r.stdout.strip().splitlines()[-1]))
        except Exception:
            out.append({"ok": False, "err": "remote_failed"})
    return out

if __name__ == "__main__":
    import argparse, os
    ap = argparse.ArgumentParser(); ap.add_argument("ids"); ap.add_argument("--n", type=int, default=2)
    a = ap.parse_args()
    cfg = _awcfg.load(os.path.join(os.path.dirname(__file__), "..", "config.json"))
    for cid in a.ids.split(","):
        res = probe(cfg, int(cid), a.n)
        print(f"UPSTREAM #{cid}", json.dumps(res, ensure_ascii=False), verdict(res))
