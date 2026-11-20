package probe

import (
	m "autoweight/model"
	"context"
	"strconv"
	"strings"
)

// remoteSiteOnce never loads a credential into the box. Only metrics return over SSH.
func (c *Client) remoteSiteOnce(ch int) m.Probe {
	p := c.Cfg.Sub("probe")
	fail := func(s string) m.Probe { return m.Probe{OK: false, Err: s, Route: "site"} }
	if p.S("base_url", "") != "https://reverix.ai" || p.S("path", "/v1/responses") != "/v1/responses" {
		return fail("invalid site endpoint")
	}
	container, user, database := c.Cfg.S("pg_container", "new-api-postgres"), c.Cfg.S("pg_user", ""), c.Cfg.S("pg_database", "")
	if _, err := m.PGExec(container, user, database); err != nil {
		return fail("invalid database connection configuration")
	}
	lim, ok := c.slot()
	if !ok {
		return fail("probe_budget_exceeded")
	}
	// v5.00: the remote script's own wall-clock deadline ends ~10s before ours,
	// so a slow stream still returns metrics instead of being killed.
	st := min(p.I("timeout_s", 90), int(lim.Seconds())-10)
	if st < 1 {
		st = 1
	}
	args := []string{strconv.Itoa(ch), p.S("model", ""), strconv.Itoa(st), p.S("prompt", ""), container, user, database}
	for i := range args {
		args[i] = ShellQuote(args[i])
	}
	ctx, cancel := context.WithTimeout(context.Background(), lim)
	defer cancel()
	out, err := c.Run(ctx, []string{"ssh", "-o", "ConnectTimeout=10", "-o", "BatchMode=yes", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=2",
		c.Cfg.S("ssh_host", ""), "python3 - " + strings.Join(args, " ")}, RemoteSiteScript)
	if err != nil {
		if ctx.Err() != nil {
			return m.Probe{OK: false, Err: "probe_timeout", Total: m.F(m.Round(lim.Seconds(), 1)), Route: "site"}
		}
		return fail("remote_site_failed")
	}
	r := ParseRemote(out)
	r.Route = "site"
	return r
}

const RemoteSiteScript = `
import json, subprocess, sys, time, re
# Only this literal token and endpoint are authorized. Never print DB rows or HTTP bodies.
def emit(**kw):
    allowed = {"ok", "status", "ttft", "total", "deltas", "span", "err", "route"}
    print(json.dumps({k:v for k,v in kw.items() if k in allowed}))
try:
    import requests
    cid = int(sys.argv[1]); model = sys.argv[2]; timeout = int(sys.argv[3]); prompt = sys.argv[4]
    container, user, database = sys.argv[5:8]
    if cid not in (2,5,23,26,32) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]*", container) or any(v and not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", v) for v in (user,database)):
        raise ValueError()
    argv = ["docker", "exec", "-i"]
    if user: argv += ["-e", "POSTGRES_USER=" + user]
    if database: argv += ["-e", "POSTGRES_DB=" + database]
    argv += [container, "sh", "-c", 'psql -v ON_ERROR_STOP=1 -U $POSTGRES_USER -d $POSTGRES_DB -At']
    sql = "BEGIN READ ONLY; select key from public.tokens where id=1318 and status=1 and deleted_at is null and (expired_time=-1 or expired_time>extract(epoch from now())) and (unlimited_quota or remain_quota>0); ROLLBACK;"
    db = subprocess.run(argv,input=sql,capture_output=True,text=True,timeout=30)
    if db.returncode: raise ValueError()
    rows = [s for s in db.stdout.splitlines() if s and s not in ("BEGIN", "ROLLBACK")]
    if len(rows)!=1: raise ValueError()
    key = rows[0].strip()
except Exception:
    emit(ok=False,err="remote_site_setup_failed",route="site"); sys.exit(0)
t0=time.monotonic(); first=None; ev=[]; status=None; err=None
try:
    session=requests.Session(); session.trust_env=False
    with session.post("https://reverix.ai/v1/responses",allow_redirects=False,stream=True,timeout=timeout,
         headers={"Authorization":"Bearer " + key + "-" + str(cid),"Content-Type":"application/json","Accept-Encoding":"identity"},
         json={"model":model,"input":prompt,"stream":True,"max_output_tokens":600}) as resp:
        status=resp.status_code
        if status!=200: err="http_error"
        else:
            for line in resp.iter_lines(decode_unicode=True):
                if time.monotonic()-t0 > timeout: err="total_timeout"; break
                if not line or not line.startswith("data:"): continue
                data=line[5:].strip()
                if data=="[DONE]": break
                try: item=json.loads(data)
                except Exception: continue
                if item.get("type")=="response.output_text.delta" and item.get("delta"):
                    now=time.monotonic()-t0; ev.append(now)
                    if first is None: first=now
                if item.get("type")=="response.failed": err="response_failed"; break
                if item.get("type")=="response.completed": break
except Exception:
    err="network_error"
emit(ok=first is not None and err is None,status=status,ttft=round(first,2) if first is not None else None,
     total=round(time.monotonic()-t0,2),deltas=len(ev),span=round(ev[-1]-ev[0],2) if ev else None,err=err,route="site")
`
