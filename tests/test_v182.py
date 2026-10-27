import os, sys, unittest
from datetime import datetime
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight.diag import shadow_flap

def R(hm, s): return dict(ts=f"2026-10-26T{hm}:00+08:00", mode="shadow", suggest=s)
NOW = datetime.fromisoformat("2026-10-26T18:12:00+08:00").timestamp()
PTS = [R("17:30", {"2": 12, "20": 3}), R("17:35", {"2": 12, "20": 3}), R("17:45", {"2": 12, "20": 10}),
       R("17:50", {"2": 12, "20": 3}), R("17:55", {"2": 12, "20": 10}), R("18:10", {"2": 12, "20": 3})]

class T(unittest.TestCase):
    def test_flapping_channel_flagged(self):
        r = shadow_flap(PTS, NOW)
        self.assertIn(20, r); self.assertEqual(r[20]["flips"], 4); self.assertEqual(r[20]["values"], [3, 10])
    def test_stable_channel_not_flagged(self):
        self.assertNotIn(2, shadow_flap(PTS, NOW))
    def test_window_excludes_old(self):
        self.assertEqual(shadow_flap(PTS, NOW, window_min=20), {})
    def test_threshold(self):
        self.assertIn(20, shadow_flap(PTS, NOW, min_flips=4)); self.assertNotIn(20, shadow_flap(PTS, NOW, min_flips=5))
    def test_empty(self):
        self.assertEqual(shadow_flap([], NOW), {})
