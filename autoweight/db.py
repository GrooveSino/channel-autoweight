"""Database access via ssh + docker exec psql. Every read runs inside BEGIN READ ONLY; writes happen only in apply_weights / apply_toggles."""
import subprocess

def psql(cfg, sql, timeout=60):
    """Send SQL over stdin to psql inside the container and return rows (split on '|')."""
    cmd = ["ssh", "-o", "ConnectTimeout=10", cfg["ssh_host"],
           f'docker exec -i {cfg["pg_container"]} sh -c "psql -v ON_ERROR_STOP=1 -U \\$POSTGRES_USER -d \\$POSTGRES_DB -At -F\\"|\\""']
    r = subprocess.run(cmd, input=sql, capture_output=True, text=True, timeout=timeout)
    if r.returncode != 0:
        raise RuntimeError(f"psql failed: {r.stderr.strip()[:500]}")
    return [l.split("|") for l in r.stdout.splitlines() if l and l not in ("BEGIN", "COMMIT") and not l.startswith("UPDATE")]

def ro(cfg, sql):
    return psql(cfg, "BEGIN READ ONLY;\n" + sql + "\nCOMMIT;\n")

def channels(cfg):
    """Enabled channels (status=1) that serve the target model family (excluding the exclude pattern)."""
    sql = f"""select c.id, c.name, c.weight, c.priority from channels c
where c.status=1 and exists (select 1 from abilities a where a.channel_id=c.id
  and a.model like '{cfg["model_like"]}' and a.model not like '{cfg["exclude_model_like"]}') order by c.id;"""
    return [dict(id=int(r[0]), name=r[1], weight=int(r[2] or 0), priority=int(r[3] or 0)) for r in ro(cfg, sql)]

def disabled_channels(cfg):
    """v1.90: disabled channels (status<>1) that serve the target model family. Read-only; used as the per-round direct-test list for autonomous on/off."""
    sql = f"""select c.id, c.name, c.status from channels c
where c.status<>1 and exists (select 1 from abilities a where a.channel_id=c.id
  and a.model like '{cfg["model_like"]}' and a.model not like '{cfg["exclude_model_like"]}') order by c.id;"""
    return [dict(id=int(r[0]), name=r[1], status=int(r[2] or 0)) for r in ro(cfg, sql)]

def logs(cfg, ids, minutes):
    """Consume (type=2) / error (type=5) logs from the last `minutes` minutes. Since v1.53 only other.frt with frt_version=2 is read (older frt values are not trusted)."""
    # v1.5: also fetch token_id (tok); probe-token filtering happens in features.build so backtest snapshots can reproduce both definitions
    # v1.53: also fetch log first-token time for frt_version=2 (milliseconds, null for non-streaming; older records are ignored)
    sql = f"""select channel_id, created_at, type, coalesce(use_time,0), coalesce(completion_tokens,0), coalesce(token_id,0),
 case when type=2 and other like '%"frt_version":2%' then coalesce((substring(other from '"frt":([0-9.]+)'))::float, -1) else -1 end,
 case when type=2 and other like '%"frt_version":2%' then coalesce((substring(other from '"content_span_ms":([0-9.]+)'))::float, -1) else -1 end,
 case when type=2 and other like '%"frt_version":2%' then coalesce((substring(other from '"content_chunks":([0-9]+)'))::int, -1) else -1 end,
 case when type=5 then coalesce((substring(content from 'status_code=([0-9]{{3}})'))::int, -1) else -1 end
from logs where created_at >= extract(epoch from now())::bigint - {int(minutes)*60}
and type in (2,5) and channel_id in ({",".join(map(str, ids))})
and model_name like '{cfg["model_like"]}' and model_name not like '{cfg["exclude_model_like"]}';
select extract(epoch from now())::bigint;"""
    rows = ro(cfg, sql)
    now = int(rows[-1][0])
    return now, [dict(ch=int(r[0]), t=int(r[1]), type=int(r[2]), ut=int(r[3]), ct=int(r[4]), tok=int(r[5]),
                 **({"frt": round(float(r[6]) / 1000, 3)} if len(r) > 6 and r[6] not in (None, "") and float(r[6]) > 0 else {}),
                 # v1.56: frt_version=2 content span (ms) and chunk count, used to detect whole-response buffering from real traffic
                 **({"span": round(float(r[7]) / 1000, 3), "chunks": int(r[8])} if len(r) > 8 and r[7] not in (None, "") and r[8] not in (None, "") and float(r[7]) >= 0 and int(r[8]) >= 0 else {}),
                 # v1.58: upstream status code of error logs (status_code=NNN in content, digits only), used to separate client errors
                 **({"sc": int(r[9])} if len(r) > 9 and r[9] not in (None, "") and int(r[9]) > 0 else {})) for r in rows[:-1]]

def apply_weights(cfg, new):
    """Weight write: changes channels.weight only and syncs weight/priority into abilities. Never touches status."""
    ids = ",".join(str(i) for i in new)
    sets = "\n".join(f"update channels set weight={int(w)}, priority={int(cfg['priority'])} where id={int(i)} and status=1;" for i, w in new.items())
    sql = f"""BEGIN;
{sets}
update abilities a set weight=c.weight, priority=c.priority from channels c where a.channel_id=c.id and c.id in ({ids});
COMMIT;
"""
    return psql(cfg, sql)


def apply_toggles(cfg, actions, enabled_ids):
    """v1.92: execute autonomous on/off (the only allowed status write); the floor is re-checked inside toggle.toggle_sql."""
    from autoweight import toggle as TG
    p = TG.P(cfg)
    sql, ok, rej = TG.toggle_sql(actions, enabled_ids, cfg.get("priority", 10), p["hard_min"], p["min_enabled"])
    if sql: psql(cfg, sql)
    return ok, rej
