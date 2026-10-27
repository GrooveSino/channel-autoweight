"""Configuration loading: JSON file plus environment-variable overrides for site-specific values.

Environment variables (all optional) take precedence over the file:
  AUTOWEIGHT_SSH_HOST         ssh host/alias of the gateway database host   -> cfg["ssh_host"]
  AUTOWEIGHT_PG_CONTAINER     name of the Postgres container on that host    -> cfg["pg_container"]
  AUTOWEIGHT_PROBE_BASE_URL   public base URL of the gateway used by probes  -> cfg["probe"]["base_url"]
  AUTOWEIGHT_PROBE_MODEL      model name used by probes                      -> cfg["probe"]["model"]
  AUTOWEIGHT_PROBE_KEY        probe API key (read by probe.load_key, never stored in the config)
"""
import json, os

ENV = {
    "AUTOWEIGHT_SSH_HOST": ("ssh_host",),
    "AUTOWEIGHT_PG_CONTAINER": ("pg_container",),
    "AUTOWEIGHT_PROBE_BASE_URL": ("probe", "base_url"),
    "AUTOWEIGHT_PROBE_MODEL": ("probe", "model"),
}

def apply_env(cfg, env=None):
    env = os.environ if env is None else env
    for var, path in ENV.items():
        v = env.get(var)
        if not v: continue
        d = cfg
        for k in path[:-1]: d = d.setdefault(k, {})
        d[path[-1]] = v
    return cfg

def load(path):
    with open(path) as f:
        return apply_env(json.load(f))
