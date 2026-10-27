"""v1.10 tool: after a write, verify read-only that channels and abilities agree (read-only, not used for scoring)."""
from . import db

def fetch(cfg):
    """All channels serving the target model family (including disabled), plus the weight/priority combinations seen in their abilities."""
    sql = f"""select c.id, c.name, c.status, c.priority, c.weight,
  coalesce((select string_agg(distinct a.weight::text||':'||a.priority::text, ',') from abilities a where a.channel_id=c.id),'')
from channels c where exists (select 1 from abilities a where a.channel_id=c.id
  and a.model like '{cfg["model_like"]}' and a.model not like '{cfg["exclude_model_like"]}') order by c.id;"""
    out = []
    for r in db.ro(cfg, sql):
        ab = [tuple(int(x) for x in p.split(":")) for p in r[5].split(",") if p]
        out.append(dict(id=int(r[0]), name=r[1], status=int(r[2]), priority=int(r[3]), weight=int(r[4]), abilities=ab))
    return out

def check(rows, priority, expected=None):
    """Return a list of problems (empty = OK). expected: {id: weight} targets written this round; only enabled channels are checked."""
    probs = []
    for r in rows:
        if r["status"] != 1:
            continue
        if r["priority"] != priority:
            probs.append(f"#{r['id']} channels.priority={r['priority']} != {priority}")
        bad = [ab for ab in r["abilities"] if ab != (r["weight"], r["priority"])]
        if bad:
            probs.append(f"#{r['id']} abilities and channels disagree: {bad} vs ({r['weight']},{r['priority']})")
        if expected and r["id"] in expected and int(expected[r["id"]]) != r["weight"]:
            probs.append(f"#{r['id']} weight={r['weight']} != target {expected[r['id']]}")
    return probs
