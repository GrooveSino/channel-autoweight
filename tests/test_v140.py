import unittest
from autoweight.decide import prev_tiers, strat_rank

P = {"top_w": 12, "close_ratio": 0.85, "close_factor": 0.8, "mid_ratio": 0.6, "mid_w": 3, "low_w": 1, "tie_ratio": 0.9, "band": 0.05}
CFG = {"w_total": 21}

class T(unittest.TestCase):
    def test_capped_weight_is_close_tier(self):
        prev = {2: 12, 5: 6, 20: 3}
        self.assertEqual(prev_tiers(prev, [2, 5, 20], P)[5], "mid")
        self.assertEqual(prev_tiers(prev, [2, 5, 20], P, caps={5: 6})[5], "close")
        self.assertEqual(prev_tiers(prev, [2, 5, 20], P, caps={5: 6})[20], "mid")

    def test_cap_aware_keeps_capped_in_close(self):
        prev = {2: 12, 5: 6, 20: 3}
        sc = {2: 1.0, 5: 0.82, 20: 0.5}   # 0.82 is below the close threshold 0.85 but above the relaxed 0.80
        p = dict(P, band_cap_aware=False)
        self.assertEqual(strat_rank([2, 5, 20], sc, p, CFG, prev=prev, caps={5: 6})[5], 3)
        p = dict(P, band_cap_aware=True)
        self.assertAlmostEqual(strat_rank([2, 5, 20], sc, p, CFG, prev=prev, caps={5: 6})[5], 9.6)

    def test_band_zero_unchanged(self):
        prev = {2: 12, 5: 6, 20: 3}
        sc = {2: 1.0, 5: 0.82, 20: 0.5}
        p = dict(P, band=0, band_cap_aware=True)
        self.assertEqual(strat_rank([2, 5, 20], sc, p, CFG, prev=prev, caps={5: 6}),
                         strat_rank([2, 5, 20], sc, dict(P, band=0), CFG, prev=prev))

if __name__ == "__main__": unittest.main()
