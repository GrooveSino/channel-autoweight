import unittest, sys, os
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight import toggle as TG

def R(t, tot, ok=True, d=50, span=1.0): return dict(ok=ok, status=200 if ok else 524, ttft=t if ok else None, total=tot, deltas=d, span=span)

def formal(scores):
    return [dict(ts_epoch=1000, scores=scores, n60={k: 30 for k in scores}, err60={k: 0 for k in scores}, old={k: 3 for k in scores},
                 probes={k: [dict(ok=True, ttft=3.0, total=4.0)] for k in scores}, feats={})]

class TestV195(unittest.TestCase):
    def test_baseline_upstream_median_then_min(self):
        b = TG.baseline_upstream([2, 5], {2: [R(4, 6), R(6, 8)], 5: [R(3, 5), R(9, 9)]})
        self.assertEqual(b["ttft"], 5.0); self.assertEqual(b["total"], 7.0); self.assertEqual(b["n"], 2)

    def test_baseline_upstream_skips_failed_and_none(self):
        self.assertIsNone(TG.baseline_upstream([2], {2: [R(0, 0, ok=False)]}))
        b = TG.baseline_upstream([2, 5], {2: [R(0, 0, ok=False)], 5: [R(4, 5)]})
        self.assertEqual(b["ttft"], 4); self.assertEqual(b["n"], 1)

    def test_pass_upstream(self):
        ok, why = TG.test_pass_upstream([R(2, 3), R(2.5, 3.5), R(6, 7)], dict(ttft=4, total=5))
        self.assertTrue(ok, why)
        ok, why = TG.test_pass_upstream([R(4.5, 5), R(4.6, 5.1)], dict(ttft=4, total=5))
        self.assertFalse(ok); self.assertTrue(any("median first token" in w for w in why))
        ok, why = TG.test_pass_upstream([R(2, 3, span=0)], dict(ttft=4, total=5))
        self.assertFalse(ok)
        self.assertFalse(TG.test_pass_upstream([R(2, 3)], None)[0])

    def test_plan_records_both_site_decides_by_default(self):
        sc = {2: 0.3, 5: 0.4, 20: 0.9, 22: 0.8, 23: 0.7}
        out = TG.plan({}, list(sc), {21: [R(3.5, 4.5)] * 3}, [], formal(sc), bottom_up_tests={2: [R(5, 6)] * 2, 5: [R(4, 5)] * 2})
        d = out["disabled"]["21"]
        self.assertFalse(d["pass_site"]); self.assertTrue(d["pass_up"]); self.assertEqual(d["decided_by"], "site"); self.assertFalse(d["pass"])
        self.assertEqual(out["base_up"]["ttft"], 4)

    def test_plan_upstream_mode_decides(self):
        sc = {2: 0.3, 5: 0.4, 20: 0.9, 22: 0.8, 23: 0.7}
        out = TG.plan(dict(toggle=dict(baseline_mode="upstream")), list(sc), {21: [R(3.5, 4.5)] * 3}, [], formal(sc),
                      bottom_up_tests={2: [R(5, 6)] * 2, 5: [R(4, 5)] * 2})
        d = out["disabled"]["21"]; self.assertTrue(d["pass"]); self.assertEqual(d["decided_by"], "upstream"); self.assertEqual(d["streak"], 1)

    def test_plan_upstream_mode_falls_back_without_base(self):
        sc = {2: 0.3, 5: 0.4, 20: 0.9}
        out = TG.plan(dict(toggle=dict(baseline_mode="upstream")), list(sc), {21: [R(3.5, 4.5)] * 3}, [], formal(sc), bottom_up_tests={})
        self.assertEqual(out["disabled"]["21"]["decided_by"], "site")

    def test_plan_without_up_tests_unchanged(self):
        sc = {2: 0.3, 5: 0.4, 20: 0.9}
        out = TG.plan({}, list(sc), {21: [R(2, 3)] * 3}, [], formal(sc))
        self.assertIsNone(out["base_up"]); self.assertTrue(out["disabled"]["21"]["pass"])

if __name__ == "__main__": unittest.main()
