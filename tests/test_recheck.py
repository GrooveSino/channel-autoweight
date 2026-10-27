"""v1.4 outlier first-token recheck verdict"""
import os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import probe
RC = dict(enabled=True, ratio=2.0, min_s=8.0, history=3, min_hist=2)
def rnd(**t): return {"probes": {str(k): [dict(ok=True, ttft=v)] for k, v in t.items()}}

class T(unittest.TestCase):
    def test_history(self):
        rounds = [rnd(**{"2": 4.0}), rnd(**{"2": 5.0}), {"probes": {"2": [dict(ok=False, ttft=None)]}}, rnd(**{"2": 3.0})]
        self.assertEqual(probe.history_ttft(rounds, 2, 3), [3.0, 5.0, 4.0])   # failed rounds skipped, most recent first
    def test_needs_recheck(self):
        h = [3.6, 4.9, 4.7]
        self.assertTrue(probe.needs_recheck([dict(ok=True, ttft=19.95)], h, RC))   # spike
        self.assertFalse(probe.needs_recheck([dict(ok=True, ttft=6.0)], h, RC))    # normal fluctuation
        self.assertFalse(probe.needs_recheck([dict(ok=True, ttft=7.9)], [2, 2, 2], RC))  # below min_s
        self.assertTrue(probe.needs_recheck([dict(ok=False)], [], RC))            # failures are always rechecked
        self.assertFalse(probe.needs_recheck([dict(ok=True, ttft=30)], [4.0], RC))  # not enough history
if __name__ == "__main__": unittest.main()
