import unittest
from autoweight import toggle as T

def R(ok=True, ttft=2.0, total=3.0, deltas=20, span=0.3, status=200):
    return dict(ok=ok, status=status, ttft=ttft if ok else None, total=total, deltas=deltas, span=span)

def F(ts, scores, n60, err=None, old=None, probes=None, feats=None):
    return dict(ts_epoch=ts*60, scores=scores, n60=n60, err60=err or {}, old=old or {}, probes=probes or {}, feats=feats or {})

class T190(unittest.TestCase):
    def test_bottom_third(self):
        self.assertEqual(T.bottom_third({1: .5, 2: .1, 3: .3, 4: .9, 5: .2}), [2, 5])
        self.assertEqual(T.bottom_third({1: .5, 2: .1}), [2])
    def test_baseline_min_of_probe_and_log(self):
        b = T.baseline([2], {2: [R(ttft=8, total=12)]}, {2: {"60m": {"lt50": 6, "p50": 20}}})
        self.assertEqual(b, dict(ttft=6, total=12))
    def test_pass_and_fail_reasons(self):
        base = dict(ttft=6, total=12)
        self.assertTrue(T.test_pass([R(), R()], base)[0])
        self.assertFalse(T.test_pass([R(), R(ok=False, status=524)], base)[0])
        self.assertFalse(T.test_pass([R(deltas=1, span=0)], base)[0])   # not real streaming
        self.assertFalse(T.test_pass([R(ttft=7)], base)[0])              # first token worse than baseline
        self.assertFalse(T.test_pass([], base)[0])
    def test_pass_streak_counts_consecutive(self):
        h = [{"disabled": {"5": {"pass": False}}}, {"disabled": {"5": {"pass": True}}}, {}, {"disabled": {"5": {"pass": True}}}]
        self.assertEqual(T.pass_streak(h, 5), 2)
    def test_enable_after_three(self):
        h = [{"disabled": {"5": {"pass": True}}}, {"disabled": {"5": {"pass": True}}}]
        cur = F(0, {2: .5, 20: .4, 21: .1}, {2: 100, 20: 100, 21: 50}, probes={21: [R(ttft=15, total=20)]})
        p = T.plan({}, [2, 20, 21], {5: [R(), R(), R()]}, h, [cur])
        self.assertTrue(p["disabled"]["5"]["enable"]); self.assertEqual(p["disabled"]["5"]["streak"], 3)
        self.assertEqual(p["actions"][0]["op"], "enable"); self.assertEqual(p["actions"][0]["weight"], 2)
        p1 = T.plan({}, [2, 20, 21], {5: [R(), R(), R()]}, h[:1], [cur])
        self.assertFalse(p1["disabled"]["5"]["enable"])
    def test_last_streak_needs_traffic(self):
        fs = [F(i * 45, {2: .5, 21: .1, 23: .0}, {2: 100, 21: 30, 23: 3}) for i in range(6)]
        self.assertEqual(T.last_streak(fs, 21, 20), (6, 225.0))   # #23 has too little traffic to be ranked
        fs[2]["n60"][21] = 5
        self.assertEqual(T.last_streak(fs, 21, 20)[0], 3)
    def test_disable_respects_floor(self):
        fs = [F(i * 45, {1: .5, 2: .4, 3: .3, 4: .2, 5: .1}, {k: 50 for k in range(1, 6)}) for i in range(6)]
        p = T.plan({}, [1, 2, 3, 4, 5], {}, [], fs)
        self.assertTrue(p["enabled"]["5"]["disable"]); self.assertEqual(p["n_enabled_after"], 4)
        fs4 = [F(i * 45, {1: .5, 2: .4, 3: .3, 4: .2}, {k: 50 for k in range(1, 5)}) for i in range(6)]
        p4 = T.plan({}, [1, 2, 3, 4], {}, [], fs4)
        self.assertFalse(p4["enabled"]["4"]["disable"]); self.assertEqual(p4["actions"], [])
    def test_error_disable_to_hard_min_notifies(self):
        fs = [F(i * 45, {1: .5, 2: .4, 3: .3, 4: .2}, {k: 50 for k in range(1, 5)}, err={"4": .5}, old={"4": 0}) for i in range(2)]
        p = T.plan({}, [1, 2, 3, 4], {}, [], fs)
        self.assertTrue(p["enabled"]["4"]["disable"]); self.assertTrue(p["notify"])
        fs3 = [F(i * 45, {1: .5, 2: .4, 3: .2}, {k: 50 for k in range(1, 4)}, err={"3": .5}, old={"3": 0}) for i in range(2)]
        self.assertFalse(T.plan({}, [1, 2, 3], {}, [], fs3)["enabled"]["3"]["disable"])   # never fewer than 3

if __name__ == "__main__": unittest.main()

class T190Hist(unittest.TestCase):
    def test_history_dedupes_same_round(self):
        rs = [dict(ts="2026-10-27T00:00:00+08:00", toggle={"disabled": {"5": {"pass": False}}}),
              dict(ts="2026-10-27T00:05:00+08:00", toggle={"disabled": {"5": {"pass": True}}}),
              dict(ts="2026-10-27T00:50:00+08:00", toggle={"disabled": {"5": {"pass": True}}}),
              dict(ts="2026-10-27T01:30:00+08:00", toggle={"disabled": {"5": {"pass": True}}})]
        from datetime import datetime
        now = datetime.fromisoformat("2026-10-27T01:40:00+08:00").timestamp()
        h = T.history_from_rounds(rs, now)
        self.assertEqual(len(h), 2)   # 00:00/00:05 merged keeping the latter; 01:30 is < 30 min from this round and dropped
        self.assertEqual(T.pass_streak(h, 5), 2)
