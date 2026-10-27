"""v1.61 trust_cap.max_n60: only cap low-trust channels with a small 1h sample"""
import os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import decide
sys.path.insert(0, os.path.join(HERE, "tests")); from test_trust import F
from test_v146 import cfg

CH = [dict(id=1), dict(id=2)]

class T(unittest.TestCase):
    def test_small_sample_low_trust_capped(self):
        fe = {1: F(n60=6), 2: F(n60=200)}
        d = decide.decide(cfg(enabled=True, thr=0.3, cap=3, max_n60=20), CH, fe, {}, trust={1: 0.12, 2: 0.12})
        self.assertLessEqual(d["target"][1], 3)
    def test_large_sample_not_capped(self):
        fe = {1: F(n60=200), 2: F(n60=200)}
        d = decide.decide(cfg(enabled=True, thr=0.3, cap=3, max_n60=20), CH, fe, {}, trust={1: 0.12, 2: 0.12})
        self.assertGreater(max(d["target"].values()), 3)
    def test_max_n60_zero_is_old_behaviour(self):
        fe = {1: F(n60=200), 2: F(n60=200)}
        d = decide.decide(cfg(enabled=True, thr=0.3, cap=3, max_n60=0), CH, fe, {}, trust={1: 0.12, 2: 0.12})
        self.assertTrue(all(w <= 3 for w in d["target"].values()))
    def test_high_trust_small_sample_free(self):
        fe = {1: F(n60=6), 2: F(n60=6)}
        d = decide.decide(cfg(enabled=True, thr=0.3, cap=3, max_n60=20), CH, fe, {}, trust={1: 0.9, 2: 0.9})
        self.assertGreater(max(d["target"].values()), 3)

if __name__ == "__main__": unittest.main()
