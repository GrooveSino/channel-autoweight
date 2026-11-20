package probe

// Remote retains upstream probe semantics with optional process-local PG overrides.
// It runs on the production host via `ssh host python3 -` (no compilation
// there), reads base_url/key inside BEGIN READ ONLY, and prints only
// status/ttft/total/deltas/span/err with the key redacted.
const Remote = `
import json, subprocess, sys, time, requests, re
cid = int(sys.argv[1]); model = sys.argv[2]; timeout = float(sys.argv[3]); prompt = sys.argv[4]
site = sys.argv[5] if len(sys.argv) > 5 else ""; kpath = sys.argv[6] if len(sys.argv) > 6 else ""
sql = "BEGIN READ ONLY; select base_url, split_part(key, E'\\n', 1), status from channels where id=%d; COMMIT;" % cid
container = sys.argv[7] if len(sys.argv) > 7 else "new-api-postgres"
user = sys.argv[8] if len(sys.argv) > 8 else ""
database = sys.argv[9] if len(sys.argv) > 9 else ""
if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", container) or any(v and not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", v) for v in (user, database)):
    print(json.dumps({"ok": False, "err": "invalid database connection configuration"})); sys.exit(1)
pgargs = ["docker", "exec", "-i"]
if user: pgargs += ["-e", "POSTGRES_USER=" + user]
if database: pgargs += ["-e", "POSTGRES_DB=" + database]
r = subprocess.run(pgargs + [container, "sh", "-c",
                    'psql -v ON_ERROR_STOP=1 -U $POSTGRES_USER -d $POSTGRES_DB -At -F"|"'],
                   input=sql, capture_output=True, text=True, timeout=30)
rows = [l for l in r.stdout.splitlines() if l not in ("BEGIN", "COMMIT") and "|" in l]
if not rows:
    print(json.dumps({"ok": False, "err": "channel_not_found"})); sys.exit(0)
base, key, st = rows[0].rsplit("|", 2) if rows[0].count("|") >= 2 else (rows[0].split("|", 1) + ["0"])
base = base.rstrip("/"); key = key.strip(); route = "direct"
if st.strip() == "1" and site and kpath:
    try:
        tk = open(kpath).read().strip()
        if tk: key = tk + "-" + str(cid); base = site.rstrip("/"); route = "site"
    except Exception: pass
t0 = time.time(); first = None; ev = []; status = None; err = None
try:
    with requests.post(base + "/v1/responses", stream=True, timeout=timeout,
                       headers={"Authorization": "Bearer " + key, "Content-Type": "application/json", "Accept-Encoding": "identity"},
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
if err and route == "site": err = err.replace(tk, "***")
print(json.dumps({"route": route, "ok": first is not None, "status": status, "ttft": round(first, 2) if first else None,
                  "total": round(time.time() - t0, 2), "deltas": len(ev),
                  "span": round(ev[-1] - ev[0], 2) if ev else None, "err": err}))
`
