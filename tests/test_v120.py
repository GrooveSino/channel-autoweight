"""v1.20: manual final-weight override validation, shadow coverage / de-duplication / gap skipping"""
import os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import decide
import evaluate_shadow as ES

CH = [dict(id=2, name="a", weight=12), dict(id=5, name="b", weight=1)]
CFG = {"w_min": 1, "w_max": 20}

def rec(ts, n=10):
    return {"ts": ts, "suggest": {"2": 12}, "old": {"2": 12}, "names": {"2": "a"}, "scores": {"2": {"score": 0.5}},
            "trust": {"2": {"trust": 1.0}}, "features": {"2": {"10m": dict(n=n, err=0, p50=10, p90=20)}}, "probed": {}}

class T(unittest.TestCase):
    def test_override_ok(self):
        self.assertEqual(decide.parse_override("5=3, #2=12", CH, CFG), {5: 3, 2: 12})
    def test_override_rejects_disabled_or_unknown(self):
        with self.assertRaises(ValueError): decide.parse_override("14=2", CH, CFG)
    def test_override_rejects_range(self):
        with self.assertRaises(ValueError): decide.parse_override("5=0", CH, CFG)
        with self.assertRaises(ValueError): decide.parse_override("5=21", CH, CFG)
    def test_coverage_holes_and_dup(self):
        rs = [rec("2026-10-24T20:37:16+08:00"), rec("2026-10-24T20:37:22+08:00"), rec("2026-10-24T20:40:03+08:00"),
              rec("2026-10-24T20:50:28+08:00"), rec("2026-10-24T21:09:06+08:00"), rec("2026-10-24T21:10:02+08:00")]
        c = ES.coverage(rs, 5)
        self.assertEqual(c["dup"], 2); self.assertEqual(len(c["holes"]), 2); self.assertGreater(c["max_gap"], 18)
        self.assertLess(c["ratio"], 0.6)
        self.assertEqual(len(ES.dedupe(rs)), 4); self.assertEqual(c["runs"], 4)
    def test_after_skips_long_gap(self):
        rs = [rec("2026-10-24T20:00:00+08:00"), rec("2026-10-24T20:30:00+08:00", n=50), rec("2026-10-24T20:35:00+08:00", n=7)]
        s = ES.summarize(rs, 60)
        self.assertEqual(s["channels"][2]["after_n"], 7)   # the pair across the 30-minute gap is not counted
        self.assertIn("Coverage", ES.render(s))

if __name__ == "__main__":
    unittest.main()
