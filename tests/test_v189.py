"""v1.89 newcomer.explore_w: during the newcomer cap period with 0 errors in 1h, weight is at least explore_w"""
import os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import decide
sys.path.insert(0, os.path.join(HERE, "tests")); from test_trust import F
from test_v146 import cfg as base
CH = [dict(id=1), dict(id=2), dict(id=3)]
def cfg(ew):
    c = base(); c["w_min"] = 0
    c["strategies"]["rank"]["low_w"] = 0; c["strategies"]["rank"]["mid_w"] = 0
    c["newcomer"] = {"enabled": True, "min_real_n60": 30, "cap": 6, "explore_w": ew}
    return c
def FE(err3=0):
    f3 = F(n60=2, n10=1, err60=err3); f3["60m"]["p50"] = 200; f3["60m"]["p90"] = 400
    f3["mix_60m"] = {"n_user": 2}
    return {1: F(n60=200), 2: F(n60=200), 3: f3}
class T(unittest.TestCase):
    def test_off_can_be_zero(self):
        d = decide.decide(cfg(0), CH, FE(), {}, newcomers={3})
        self.assertEqual(d["target"][3], 0)
    def test_floor_lifts_newcomer(self):
        d = decide.decide(cfg(2), CH, FE(), {}, newcomers={3})
        self.assertEqual(d["target"][3], 2)
        self.assertTrue(any("exploration floor" in n for n in d["notes"]))
    def test_errors_block_floor(self):
        d = decide.decide(cfg(2), CH, FE(err3=1), {}, newcomers={3})
        self.assertEqual(d["target"][3], 0)
    def test_not_newcomer_no_floor(self):
        d = decide.decide(cfg(2), CH, FE(), {}, newcomers=set())
        self.assertEqual(d["target"][3], 0)
if __name__ == "__main__": unittest.main()
