package trial

// Remote runs on the production host via `ssh host python3 - <b64 json>`.
// It sends interleaved streaming /v1/responses requests through the site with
// the test token pinned to each channel ("<token>-<cid>"), so every request is
// logged under token 1318. The token is read from a file on the host and never
// printed; each request prints one JSON line with timings and output tokens.
const Remote = `
import base64, json, sys, time, requests, threading
from concurrent.futures import ThreadPoolExecutor
cfg = json.loads(base64.b64decode(sys.argv[1]))
tk = open(cfg["key_path"]).read().strip()
filler = ("以下是一段用于测试长上下文的背景材料，请在最后回答问题。" + "天空呈现蓝色是因为瑞利散射，短波长的蓝光更容易被大气分子散射。" * 4) + "\n"
lock = threading.Lock()
def one(job):
    cid, big, k = job
    prompt = cfg["prompt"]
    if big:
        prompt = filler * cfg["big_repeat"] + "问题：" + prompt
    t0 = time.time(); first = None; deltas = 0; status = None; err = None; out_tok = None; in_tok = None
    try:
        with requests.post(cfg["site"].rstrip("/") + "/v1/responses", stream=True, timeout=cfg["timeout"],
                           headers={"Authorization": "Bearer " + tk + "-" + str(cid), "Content-Type": "application/json", "Accept-Encoding": "identity"},
                           json={"model": cfg["model"], "input": prompt, "stream": True, "max_output_tokens": cfg["max_out"]}) as r:
            status = r.status_code
            if status != 200:
                err = r.text[:160]
            else:
                for line in r.iter_lines(decode_unicode=True):
                    if not line or not line.startswith("data:"): continue
                    d = line[5:].strip()
                    if d == "[DONE]": break
                    try: j = json.loads(d)
                    except Exception: continue
                    t = j.get("type")
                    if t == "response.output_text.delta" and j.get("delta"):
                        deltas += 1
                        if first is None: first = time.time() - t0
                    if t in ("response.completed", "response.failed", "response.incomplete"):
                        u = (j.get("response") or {}).get("usage") or {}
                        out_tok = u.get("output_tokens"); in_tok = u.get("input_tokens")
                        if t == "response.failed": err = "response.failed"
                        break
    except Exception as e:
        err = type(e).__name__ + ": " + str(e)[:120]
    if err: err = err.replace(tk, "***")
    rec = {"ch": cid, "big": big, "k": k, "status": status, "ttft": round(first, 2) if first else None,
           "total": round(time.time() - t0, 2), "deltas": deltas, "out": out_tok, "in": in_tok, "err": err}
    with lock:
        print(json.dumps(rec), flush=True)
jobs = []
for k in range(cfg["n"]):
    big = k % cfg["big_every"] == cfg["big_every"] - 1
    for cid in cfg["channels"]:
        jobs.append((cid, big, k))
# the site limits a user to 30 requests/min; pace starts so the trial never trips it
gap = 60.0 / cfg["rpm"]; futs = []
with ThreadPoolExecutor(max_workers=cfg["conc"]) as ex:
    for j in jobs:
        futs.append(ex.submit(one, j)); time.sleep(gap)
    for f in futs: f.result()
`
