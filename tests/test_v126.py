import os, sys, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from tools.share_audit import audit

def R(ch, t, tok, model="m"): return dict(ch=ch, t=t, tok=tok, model=model)

class T(unittest.TestCase):
    def test_sticky_session_counts_one_start(self):
        rows = [R(20, 100 + i * 30, 7) for i in range(10)]
        r = audit(rows, {2: 12, 20: 3}, {"m": [2, 20]}, 0)
        self.assertEqual(r["starts"], 1); self.assertEqual(r["channels"][20]["n"], 10)
        self.assertEqual(r["channels"][20]["stick_rate"], 1.0); self.assertEqual(r["stick_rate"], 1.0)
        self.assertAlmostEqual(r["channels"][2]["exp_starts"], 0.8, places=1)
    def test_gap_makes_new_start(self):
        rows = [R(2, 0, 1), R(20, 700, 1)]
        r = audit(rows, {2: 1, 20: 1}, {"m": [2, 20]}, 0, gap_s=600)
        self.assertEqual(r["starts"], 2); self.assertIsNone(r["stick_rate"])
    def test_prior_context_not_counted(self):
        rows = [R(2, 50, 1), R(2, 150, 1)]
        r = audit(rows, {2: 1, 20: 1}, {"m": [2, 20]}, 100)
        self.assertEqual(r["starts"], 0); self.assertEqual(r["channels"][2]["n"], 1)
    def test_model_restricted_expectation_and_probe_excluded(self):
        rows = [R(2, 0, 1, "a"), R(20, 0, 2, "b"), R(5, 0, 99, "a")]
        r = audit(rows, {2: 1, 5: 1, 20: 1}, {"a": [2, 5], "b": [20]}, 0, probe_toks=[99])
        self.assertEqual(r["starts"], 2)
        self.assertAlmostEqual(r["channels"][20]["exp_starts"], 1.0)
        self.assertAlmostEqual(r["channels"][2]["exp_starts"], 0.5)
        self.assertEqual(r["channels"][5]["n"], 0)

if __name__ == "__main__":
    unittest.main()
