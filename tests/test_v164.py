import json, os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight.decide import strat_rank

P = {"top_w": 12, "close_ratio": 0.85, "close_factor": 0.8, "mid_ratio": 0.6, "mid_w": 3, "low_w": 0, "tie_ratio": 0.95}
CFG = {"w_total": 24}

class T(unittest.TestCase):
    def test_config_tie_ratio_095(self):
        c = json.load(open(os.path.join(os.path.dirname(__file__), "..", "tests/fixtures/config.test.json")))
        self.assertEqual(c["strategies"]["rank"]["tie_ratio"], 0.95)
    def test_ratio_090_no_longer_ties(self):
        sc = {22: 0.642, 2: 0.58, 21: 0.60, 20: 0.62}
        order = sorted(sc, key=lambda i: -sc[i])
        w = strat_rank(order, sc, P, CFG)
        self.assertEqual(w[22], 12); self.assertGreater(len(set(w.values())), 1)
    def test_true_tie_still_averages(self):
        sc = {22: 0.642, 2: 0.63, 21: 0.625, 20: 0.635}
        order = sorted(sc, key=lambda i: -sc[i])
        self.assertEqual(set(strat_rank(order, sc, P, CFG).values()), {6.0})
