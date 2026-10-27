import unittest, sys, os
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight import trust

def feats(err, n):
    return {"10m": {"n": n, "err": err, "err_rate": err / n}, "60m": {"n": 100, "err": 0, "err_rate": 0.0, "slow_rate": 0.0}}

class T(unittest.TestCase):
    def sev(self, full_n, err, n):
        p = trust.params({"trust": {"err_10m_full_n": full_n}})
        parts = [x for x in trust.severity_parts(feats(err, n), [], p) if x[2] == "short"]
        return parts[0][0] if parts else 0.0
    def test_off_unchanged(self):
        self.assertEqual(self.sev(0, 1, 2), 1.0)
    def test_small_sample_scaled(self):
        self.assertAlmostEqual(self.sev(10, 1, 2), 0.2)
    def test_large_sample_full(self):
        self.assertEqual(self.sev(10, 20, 100), 1.0)

if __name__ == "__main__":
    unittest.main()
