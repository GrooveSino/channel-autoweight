"""v1.12 probe result reuse"""
import os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from datetime import datetime
from autoweight import probe
TS = "2026-10-24T14:08:16+08:00"; T0 = datetime.fromisoformat(TS).timestamp()
R = {"ts": TS, "probes": {"2": [dict(ok=True, ttft=3.4)], "14": [dict(ok=True, ttft=9)]}}
class T(unittest.TestCase):
    def test_fresh(self):
        self.assertEqual(probe.reuse([R, {"ts": TS, "probes": {}}], [2, 5], T0 + 60, 10), {2: [dict(ok=True, ttft=3.4)]})
    def test_stale(self):
        self.assertEqual(probe.reuse([R], [2], T0 + 11 * 60, 10), {})
    def test_none(self):
        self.assertEqual(probe.reuse([], [2], T0, 10), {})
if __name__ == "__main__": unittest.main()
