import os, sys, copy, json, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight import features, decide

CFG = json.load(open(os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "tests/fixtures/config.test.json")))

class TestLenNorm(unittest.TestCase):
    def test_norm_lat_filters_short(self):
        ok = [dict(ut=3, ct=25), dict(ut=40, ct=1000), dict(ut=10, ct=250)]
        n, p50, p90 = features.norm_lat(ok, 100, 500)
        self.assertEqual(n, 2); self.assertEqual(p50, 20.0); self.assertEqual(p90, 20.0)

    def test_norm_lat_unknown_ct(self):
        self.assertEqual(features.norm_lat([dict(ut=5, ct=None)]), (0, None, None))

    def test_window_has_norm_fields(self):
        rows = [dict(ch=1, t=1000 - i, type=2, ut=40, ct=1000) for i in range(6)]
        w = features.window(rows, 1000, 10, 60)
        self.assertEqual(w["nn"], 6); self.assertEqual(w["np50"], 20.0)

    def test_long_output_not_penalized_when_enabled(self):
        s = copy.deepcopy(CFG["score"])
        fast_long = dict(n=10, err=0, err_rate=0, p50=80, p90=120, slow_rate=0, nn=10, np50=20, np90=30)
        slow_short = dict(n=10, err=0, err_rate=0, p50=20, p90=30, slow_rate=0, nn=10, np50=60, np90=90)
        s["len_norm"]["enabled"] = False
        self.assertLess(decide.score_window(fast_long, 3, s), decide.score_window(slow_short, 3, s))
        s["len_norm"]["enabled"] = True
        self.assertGreater(decide.score_window(fast_long, 3, s), decide.score_window(slow_short, 3, s))

    def test_fallback_when_few_samples(self):
        s = copy.deepcopy(CFG["score"]); s["len_norm"]["enabled"] = True
        w = dict(n=10, err=0, err_rate=0, p50=20, p90=30, slow_rate=0, nn=2, np50=99, np90=99)
        s2 = copy.deepcopy(s); s2["len_norm"]["enabled"] = False
        self.assertEqual(decide.score_window(w, 3, s), decide.score_window(w, 3, s2))

    def test_default_off(self):
        self.assertFalse(CFG["score"]["len_norm"]["enabled"])

if __name__ == "__main__":
    unittest.main()
