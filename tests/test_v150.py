import os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "tools"))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
import workload_match as wm
import shadow_replay as sr

def R(ch, ut, ct, t=100, tok=1, type=2): return dict(ch=ch, ut=ut, ct=ct, t=t, tok=tok, type=type)

class TestMatchedEval(unittest.TestCase):
    def test_window_ratios_normalize_by_bucket(self):
        # #1 only gets short jobs (raw faster) but is slower than the baseline in every bucket -> ratio > 1; #2 gets long jobs but is faster per bucket -> < 1
        rows = [R(1, 8, 100) for _ in range(5)] + [R(2, 30, 500) for _ in range(5)]
        base = {0: 5.0, 2: 40.0}
        m = wm.window_ratios(rows, 0, 200, base)
        self.assertGreater(m[1][1], 1.0); self.assertLess(m[2][1], 1.0)

    def test_window_filters(self):
        rows = [R(1, 10, 100, t=50)] * 3 + [R(1, 10, 100, t=500)] * 3 + [R(1, 99, 100, tok=7)] * 3 \
             + [R(1, 99, 100, type=5)] * 3 + [R(1, 99, 0)] * 3 + [R(2, 10, 100)] * 2
        m = wm.window_ratios(rows, 0, 200, {0: 10.0}, probe_tokens=[7])
        self.assertEqual(m[1], (3, 1.0)); self.assertNotIn(2, m)   # outside window / probe / error / empty output / too few samples are all excluded

    def test_pool_p50(self):
        rows = [R(1, x, 100) for x in (1, 2, 3)] + [R(2, 100, 100, tok=7)]
        self.assertEqual(wm.pool_p50(rows, [7]), {0: 2})

    def test_evaluate_without_mr_has_no_key(self):
        self.assertTrue(callable(sr.make_mr_fn))
        import inspect
        self.assertIn("mr_fn", inspect.signature(sr.evaluate).parameters)

if __name__ == "__main__":
    unittest.main()
