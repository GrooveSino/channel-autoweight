import os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight import toggle as TG

CFG = {"toggle": {"err_probe_fail": True, "min_enabled": 4, "hard_min": 3, "err_rounds": 2, "err_rate": 0.2}}
FAIL = [{"ok": False, "err": "ReadTimeout"}, {"ok": False, "err": "ReadTimeout"}]
OK = [{"ok": True, "ttft": 3.0, "total": 5.0, "streaming": True}]


def rnd(t, old5, probes5, err5=0.0):
    return dict(ts_epoch=t, scores={"2": 0.5, "5": 0.1, "20": 0.6, "22": 0.4, "23": 0.45},
                n60={"2": 30, "5": 30, "20": 30, "22": 30, "23": 30},
                err60={"2": 0, "5": err5, "20": 0, "22": 0, "23": 0},
                old={2: 6, 5: old5, 20: 12, 22: 3, 23: 6}, probes={"5": probes5, "2": OK}, feats={})


class T(unittest.TestCase):
    def test_probe_all_failed(self):
        self.assertTrue(TG.probe_all_failed({"5": FAIL}, 5))
        self.assertFalse(TG.probe_all_failed({"5": FAIL + OK}, 5))
        self.assertFalse(TG.probe_all_failed({}, 5))

    def test_streak_counts_probe_fail_only_at_zero(self):
        fm = [rnd(1, 6, FAIL), rnd(2, 0, FAIL), rnd(3, 0, FAIL)]
        self.assertEqual(TG.err_streak(fm, 5, 0.2, probe_fail=True), 2)
        self.assertEqual(TG.err_streak(fm, 5, 0.2, probe_fail=False), 0)

    def test_streak_breaks_on_recovery(self):
        fm = [rnd(1, 0, FAIL), rnd(2, 0, OK), rnd(3, 0, FAIL)]
        self.assertEqual(TG.err_streak(fm, 5, 0.2, probe_fail=True), 1)

    def test_formal_current_keeps_err60(self):
        feats = {"5": {"60m": {"n": 10, "err_rate": 0.5}}}
        r = TG.formal_current(0, {"5": {"score": 0.1}}, feats, [{"id": 5, "weight": 0}], {"5": FAIL})
        self.assertEqual(r["err60"]["5"], 0.5)
        self.assertEqual(r["old"][5], 0)
        self.assertEqual(TG.err_streak([r], "5", 0.2), 1)

    def test_plan_disables_when_above_floor(self):
        fm = [rnd(1, 0, FAIL), rnd(2, 0, FAIL)]
        out = TG.plan(CFG, [2, 5, 20, 22, 23], {}, [], fm)
        self.assertTrue(out["enabled"]["5"]["disable"])
        self.assertEqual(out["n_enabled_after"], 4)
        self.assertFalse(out["notify"])

    def test_plan_respects_hard_min(self):
        fm = [rnd(1, 0, FAIL), rnd(2, 0, FAIL)]
        out = TG.plan(CFG, [5, 20, 23], {}, [], fm)
        self.assertFalse(out["enabled"]["5"]["disable"])

    def test_plan_notify_when_drop_to_3(self):
        fm = [rnd(1, 0, FAIL), rnd(2, 0, FAIL)]
        out = TG.plan(CFG, [5, 20, 22, 23], {}, [], fm)
        self.assertTrue(out["enabled"]["5"]["disable"]); self.assertTrue(out["notify"])


if __name__ == "__main__":
    unittest.main()
