"""v1.7 probe anomaly classification"""
import os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import probe

class T(unittest.TestCase):
    def test_empty_200(self):
        ps = {14: [dict(ok=False, status=200, deltas=0), dict(ok=False, status=200, deltas=0, recheck=True)]}
        self.assertEqual(probe.anomalies(ps), {14: ["empty_200"]})
    def test_rate_limit_and_mixed(self):
        ps = {2: [dict(ok=False, status=429, deltas=0), dict(ok=False, status=None, err="timeout")]}
        self.assertEqual(probe.anomalies(ps), {2: ["rate_limit", "http_err"]})
    def test_recovered_by_recheck_not_reported(self):
        ps = {5: [dict(ok=False, status=200, deltas=0), dict(ok=True, status=200, deltas=50, recheck=True)]}
        self.assertEqual(probe.anomalies(ps), {})
    def test_empty_input(self):
        self.assertEqual(probe.anomalies({}), {}); self.assertEqual(probe.anomalies(None), {})
if __name__ == "__main__": unittest.main()
