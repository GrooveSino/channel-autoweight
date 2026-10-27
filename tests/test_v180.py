import os, sys, unittest
from datetime import datetime
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight.diag import zero_since, zero_weight_leak

def R(ts, old, target=None, applied=False):
    return dict(ts=ts, old=old, applied=applied, decision=dict(target=target or old))

H = [R("2026-10-26T15:17:00+08:00", {"22": 12}),
     R("2026-10-26T15:55:00+08:00", {"22": 12}, {"22": 0}, applied=True),
     R("2026-10-26T16:46:00+08:00", {"22": 0})]
NOW = datetime.fromisoformat("2026-10-26T16:47:00+08:00").timestamp()
F = {22: {"10m": dict(n=24, err=6), "60m": dict(n=187, err=22)}}

class T(unittest.TestCase):
    def test_since_from_applied_zero(self):
        self.assertEqual(zero_since(H, 22), "2026-10-26T15:55:00+08:00")

    def test_since_none_when_not_zero(self):
        self.assertIsNone(zero_since(H[:1], 22))

    def test_leak_detected(self):
        z = zero_weight_leak(H, {22: 0}, F, NOW)
        self.assertEqual(z[22]["n10"], 24); self.assertAlmostEqual(z[22]["age_min"], 52.0)

    def test_leak_young_or_quiet(self):
        self.assertEqual(zero_weight_leak(H, {22: 0}, F, NOW, min_age_min=60), {})
        self.assertEqual(zero_weight_leak(H, {22: 0}, {22: {"10m": dict(n=2), "60m": {}}}, NOW), {})
        self.assertEqual(zero_weight_leak(H, {22: 3}, F, NOW), {})
