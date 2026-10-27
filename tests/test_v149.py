import os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "tools"))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
import workload_match as wm

def R(ch, ut, ct, tok=1, type=2): return dict(ch=ch, ut=ut, ct=ct, tok=tok, type=type)

class TestWorkloadMatch(unittest.TestCase):
    def test_short_work_not_mistaken_for_fast(self):
        # #1 only gets short jobs but is slower than #2 in every bucket; its raw p50 is smaller, the matched ratio must be > 1
        rows = [R(1, 8, 100) for _ in range(10)] + [R(1, 30, 500) for _ in range(5)]
        rows += [R(2, 5, 100) for _ in range(5)] + [R(2, 20, 500) for _ in range(20)]
        res, _ = wm.analyze(rows, min_n=5)
        self.assertGreater(res[1]["matched_ratio"], 1.0)
        self.assertLess(res[2]["matched_ratio"], 1.0)

    def test_excludes_probe_errors_empty(self):
        rows = [R(1, 5, 100) for _ in range(5)] + [R(1, 99, 100, tok=7) for _ in range(9)]
        rows += [R(1, 99, 0) for _ in range(9)] + [R(1, 99, 100, type=5) for _ in range(9)]
        res, pool = wm.analyze(rows, probe_tokens=[7], min_n=5)
        self.assertEqual(res[1]["n"], 5); self.assertEqual(pool[0], (5, 5))

    def test_min_n_and_cover(self):
        rows = [R(1, 5, 100) for _ in range(5)] + [R(1, 50, 2000) for _ in range(2)] + [R(2, 10, 2000) for _ in range(6)]
        res, _ = wm.analyze(rows, min_n=5)
        self.assertAlmostEqual(res[1]["cover"], 5 / 7)
        self.assertAlmostEqual(res[1]["matched_ratio"], 1.0)  # alone in the short bucket, ratio 1

if __name__ == "__main__":
    unittest.main()
