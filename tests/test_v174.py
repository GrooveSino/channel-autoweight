"""v1.74: shrink_k re-evaluated on modern replays (kept off) + shrinkage regression tests."""
import json, os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight.decide import shrink, score_window

CFG = json.load(open(os.path.join(os.path.dirname(__file__), "..", "tests/fixtures/config.test.json")))

class T(unittest.TestCase):
    def test_shrink_off_identity(self):
        self.assertEqual(shrink(10.0, 5, 40.0, 0), 10.0)
        self.assertEqual(shrink(10.0, 5, None, 30), 10.0)

    def test_shrink_pulls_small_sample(self):
        small = shrink(10.0, 5, 40.0, 30); big = shrink(10.0, 500, 40.0, 30)
        self.assertGreater(small, big); self.assertLess(big, 12.0)

    def test_score_window_small_sample_penalised_when_on(self):
        s = dict(CFG["score"]); s["workload_match"] = {}; s["len_norm"] = {}
        w = dict(n=20, p50=14.0, p90=34.0, err_rate=0.0, slow_rate=0.0)
        s["shrink_k"] = 0; a = score_window(w, 3.0, s, prior=(15.0, 45.0))
        s["shrink_k"] = 30; b = score_window(w, 3.0, s, prior=(15.0, 45.0))
        self.assertLess(b, a)

    def test_default_off(self):
        self.assertEqual(CFG["score"].get("shrink_k", 0), 0)

if __name__ == "__main__":
    unittest.main()
