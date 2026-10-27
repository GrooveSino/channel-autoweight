"""v1.46 low-trust cap trust_cap (off by default)"""
import json, os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import decide
sys.path.insert(0, os.path.join(HERE, "tests")); from test_trust import F

def cfg(**tc):
    c = {"windows_min": [60, 10], "score": {"t_ref": 20, "a_p50": .3, "b_p90": .3, "c_ttft": 1, "w_10m": .4, "min_n_window": 5,
         "ttft_missing_s": 30, "nonstream_penalty": .85}, "min_n_rank1": 10, "strategy": "rank",
         "strategies": {"rank": dict(top_w=12, close_ratio=.85, close_factor=.8, mid_ratio=.6, mid_w=3, low_w=1, tie_ratio=.9)},
         "w_total": 18, "w_min": 1, "w_max": 20, "error_alert": {"rate": .2, "min_n": 20}, "trust": {"enabled": True, "floor": 0.95}}
    if tc: c["trust_cap"] = tc
    return c
FE = {1: F(), 2: F(n60=200)}
CH = [dict(id=1), dict(id=2)]
TR = {1: 0.4, 2: 1.0}

class T(unittest.TestCase):
    def test_config_v161(self):
        # enabled since v1.61: thr 0.3 + max_n60 20 (only low-traffic low-trust channels are capped)
        c = json.load(open(os.path.join(HERE, "tests/fixtures/config.test.json")))["trust_cap"]
        self.assertTrue(c["enabled"]); self.assertEqual(c["max_n60"], 20); self.assertEqual(c["thr"], 0.3)
    def test_off_no_cap(self):
        a = decide.decide(cfg(), CH, FE, {}, trust=TR)["target"]
        b = decide.decide(cfg(enabled=False, thr=0.6, cap=3), CH, FE, {}, trust=TR)["target"]
        self.assertEqual(a, b)
    def test_low_trust_capped(self):
        d = decide.decide(cfg(enabled=True, thr=0.6, cap=3), CH, FE, {}, trust={1: 0.4, 2: 0.4})
        self.assertTrue(all(w <= 3 for w in d["target"].values()))
        d = decide.decide(cfg(enabled=True, thr=0.6, cap=3), CH, FE, {}, trust={1: 0.9, 2: 0.9})
        self.assertGreater(max(d["target"].values()), 3)

if __name__ == "__main__": unittest.main()
