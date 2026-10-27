import json, os, sys, unittest, copy
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight import trust as T, shadow as S

CFG = json.load(open(os.path.join(os.path.dirname(__file__), "..", "tests/fixtures/config.test.json")))

def win(n, err=0, slow=0.0):
    return {"n": n, "err": err, "err_rate": err / n if n else 0.0, "p50": 10, "p90": 30, "slow_rate": slow, "empty_rate": 0.0}

class TestSevDedup(unittest.TestCase):
    def test_parts_sources(self):
        f = {"60m": win(40, 0, 0.25), "10m": win(5, 1)}
        parts = T.severity_parts(f, [], T.params(CFG))
        self.assertEqual({x[2] for x in parts}, {"short", "long"})
        self.assertEqual(T.severity(f, [], T.params(CFG))[0], max(x[0] for x in parts))
    def test_long_event_discounted_by_60(self):
        c = copy.deepcopy(CFG); c["shadow"]["sev_gap_long_min"] = 60
        f = {2: {"60m": win(18, 0, 0.22), "10m": win(2)}}
        r = S.trust_step(c, [2], f, {}, {2: 1.0}, 5.0)[2]
        self.assertAlmostEqual(r["sev_eff"], round(0.3 * 5 / 60, 3))
        off = copy.deepcopy(CFG); off["shadow"].pop("sev_gap_long_min", None)
        self.assertAlmostEqual(S.trust_step(off, [2], f, {}, {2: 1.0}, 5.0)[2]["sev_eff"], 0.15)
    def test_short_error_not_discounted_by_60(self):
        c = copy.deepcopy(CFG); c["shadow"]["sev_gap_long_min"] = 60
        f = {2: {"60m": win(100), "10m": win(10, 2)}}
        r = S.trust_step(c, [2], f, {}, {2: 1.0}, 5.0)[2]
        self.assertAlmostEqual(r["sev_eff"], 0.5)   # 2/10=0.2 -> sev 1.0 x 5/10
    def test_wilson_blocks_small_sample(self):
        p = dict(T.params(CFG)); p["slow_wilson"] = True
        self.assertEqual(T.severity({"60m": win(18, 0, 0.22), "10m": win(2)}, [], p)[0], 0.0)
        self.assertGreater(T.severity({"60m": win(400, 0, 0.30), "10m": win(2)}, [], p)[0], 0)

if __name__ == "__main__": unittest.main()
