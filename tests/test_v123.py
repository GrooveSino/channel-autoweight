import json, os, subprocess, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, HERE)
from autoweight import decide

P = {"top_w": 12, "close_ratio": 0.85, "close_factor": 0.8, "mid_ratio": 0.6, "mid_w": 3, "low_w": 1, "tie_ratio": 0.9}
CFG = {"w_total": 18}

class T(unittest.TestCase):
    def test_band0_same_as_old(self):
        sc = {1: 1.0, 2: 0.86, 3: 0.5}
        self.assertEqual(decide.strat_rank([1, 2, 3], sc, dict(P, band=0), CFG, prev={1: 12, 2: 3, 3: 1}),
                         decide.strat_rank([1, 2, 3], sc, P, CFG))

    def test_prev_tiers(self):
        self.assertEqual(decide.prev_tiers({1: 9, 2: 9}, [1, 2], P), {1: "tie", 2: "tie"})
        self.assertEqual(decide.prev_tiers({1: 12, 2: 10, 3: 3, 4: 1}, [1, 2, 3, 4], P),
                         {1: "top", 2: "close", 3: "mid", 4: "low"})
        self.assertEqual(decide.prev_tiers({1: 12}, [1, 2], P), {})

    def test_tie_sticky(self):
        sc = {1: 1.0, 2: 0.88}       # 0.88 < tie 0.9, but the previous round was uniform; within band 0.03 it stays uniform
        w = decide.strat_rank([1, 2], sc, dict(P, band=0.03), CFG, prev={1: 9, 2: 9})
        self.assertEqual(w, {1: 9, 2: 9})
        w0 = decide.strat_rank([1, 2], sc, P, CFG, prev={1: 9, 2: 9})
        self.assertEqual(w0[1], 12)

    def test_enter_tie_needs_margin(self):
        sc = {1: 1.0, 2: 0.91}       # previous round 12/10; entering uniform needs >= 0.93
        w = decide.strat_rank([1, 2], sc, dict(P, band=0.03), CFG, prev={1: 12, 2: 10})
        self.assertEqual(w[1], 12)

    def test_close_sticky_and_mid_entry(self):
        sc = {1: 1.0, 2: 0.83}
        self.assertAlmostEqual(decide.strat_rank([1, 2], sc, dict(P, band=0.03), CFG, prev={1: 12, 2: 10})[2], 9.6)
        self.assertEqual(decide.strat_rank([1, 2], sc, dict(P, band=0.03), CFG, prev={1: 12, 2: 3})[2], 3)
        self.assertEqual(decide.strat_rank([1, 2], {1: 1.0, 2: 0.87}, dict(P, band=0.03), CFG, prev={1: 12, 2: 3})[2], 3)


if __name__ == "__main__":
    unittest.main()
