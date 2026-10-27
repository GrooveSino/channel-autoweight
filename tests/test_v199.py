import json, os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight import decide
CFG = json.load(open(os.path.join(os.path.dirname(__file__), "..", "tests/fixtures/config.test.json")))
P = dict(CFG["strategies"]["rank"])

class T(unittest.TestCase):
    def run_rank(self, sc):
        order = sorted(sc, key=lambda i: -sc[i])
        return decide.strat_rank(order, sc, P, {"w_total": 40})
    def test_config_mid_ratio(self):
        self.assertAlmostEqual(P["mid_ratio"], 0.7)
    def test_ratio_069_goes_low(self):
        w = self.run_rank({20: 0.757, 22: 0.711, 2: 0.523})
        self.assertEqual(w[2], P["low_w"])
    def test_ratio_072_stays_mid(self):
        w = self.run_rank({20: 0.757, 22: 0.711, 2: 0.545})
        self.assertEqual(w[2], P["mid_w"])
    def test_close_unchanged(self):
        w = self.run_rank({20: 0.757, 22: 0.711, 2: 0.4})
        self.assertEqual(w[20], P["top_w"]); self.assertAlmostEqual(w[22], P["top_w"] * P["close_factor"])

if __name__ == "__main__": unittest.main()
