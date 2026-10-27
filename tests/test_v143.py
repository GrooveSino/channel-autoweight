import json, os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import shadow as S
sys.path.insert(0, os.path.join(HERE, "tools"))
import trust_source_eval as E

def cfg():
    return json.load(open(os.path.join(HERE, "tests/fixtures/config.test.json")))

def f(n=100, err=0):
    w = {"n": n, "err": err, "err_rate": err / n if n else 0, "p50": 10, "p90": 30, "slow_rate": 0.0}
    return {"60m": dict(w), "10m": dict(w, n=20, err=err)}

class T(unittest.TestCase):
    def test_config_source_shadow(self):
        c = cfg(); assert c["trust"]["source"] == "shadow" and c["trust"]["shadow_max_age_min"] > 0

    def test_shadow_trust_at_age_limit(self):
        recs = [{"ts": "2026-10-25T14:00:00+08:00", "trust": {"2": {"trust": 0.5}}}]
        t = E.ts("2026-10-25T14:10:00+08:00")
        assert E.shadow_trust_at(recs, t, 15)[0] == {2: 0.5}
        assert E.shadow_trust_at(recs, t, 5)[0] is None

    def test_short_gap_clean_rises_slowly(self):
        out = S.trust_step(cfg(), [2], {2: f()}, {}, {2: 0.5}, 5.0, {})
        assert 0.5 < out[2]["trust"] < 0.55

    def test_error_burst_drops_fast(self):
        out = S.trust_step(cfg(), [2], {2: f(err=8)}, {}, {2: 1.0}, 5.0, {})
        assert out[2]["trust"] < 0.9

if __name__ == '__main__': unittest.main()
