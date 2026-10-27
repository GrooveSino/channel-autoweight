import unittest
from autoweight.decide import strat_rank

P = {"top_w": 12, "close_ratio": 0.85, "close_factor": 0.8, "mid_ratio": 0.6, "mid_w": 3, "low_w": 0, "tie_ratio": 0.95}
CFG = {"w_total": 24}

class T(unittest.TestCase):
    def test_off_matches_old(self):
        sc = {22: 0.436, 2: 0.358, 20: 0.241}
        self.assertEqual(strat_rank([22, 2, 20], sc, dict(P), CFG), {22: 12, 2: 3, 20: 0})

    def test_low_trust_leader_keeps_trusted_close(self):
        sc = {22: 0.436, 2: 0.358, 20: 0.241}
        p = dict(P, lead_trust=0.6, _trust={22: 0.51, 2: 1.0, 20: 0.93})
        w = strat_rank([22, 2, 20], sc, p, CFG)
        self.assertEqual(w[22], 12); self.assertAlmostEqual(w[2], 9.6); self.assertEqual(w[20], 3)

    def test_trusted_leader_unchanged(self):
        sc = {2: 0.5, 22: 0.4, 20: 0.2}
        p = dict(P, lead_trust=0.6, _trust={22: 0.4, 2: 1.0, 20: 1.0})
        self.assertEqual(strat_rank([2, 22, 20], sc, p, CFG), strat_rank([2, 22, 20], sc, dict(P), CFG))

    def test_no_trusted_channels_fallback(self):
        sc = {22: 0.5, 2: 0.3}
        p = dict(P, lead_trust=0.6, _trust={22: 0.4, 2: 0.5})
        self.assertEqual(strat_rank([22, 2], sc, p, CFG), {22: 12, 2: 3})

if __name__ == "__main__": unittest.main()
