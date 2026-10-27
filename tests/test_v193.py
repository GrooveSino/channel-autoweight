import unittest, sys, os
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from datetime import datetime
from autoweight import shadow as S, decide

def ts(h, m): return f"2026-10-27T{h:02d}:{m:02d}:00+08:00"
def now(h, m): return datetime.fromisoformat(ts(h, m)).timestamp()

class TestUpstreamAsProbes(unittest.TestCase):
    def test_convert(self):
        out = S.upstream_as_probes([{"ok": True, "status": 200, "ttft": 2.0, "total": 2.5, "deltas": 20, "span": 0.2},
                                    {"ok": True, "status": 200, "ttft": 3.0, "total": 3.0, "deltas": 1, "span": 0.0},
                                    {"ok": False, "status": 524, "ttft": None, "total": None}], 1.5)
        self.assertEqual(out[0]["ttft"], 3.5); self.assertTrue(out[0]["streaming"])
        self.assertFalse(out[1]["streaming"]); self.assertIsNone(out[2]["streaming"]); self.assertIsNone(out[2]["ttft"])
        self.assertEqual(out[0]["source"], "upstream")
    def test_empty(self):
        self.assertIsNone(S.upstream_as_probes(None)); self.assertIsNone(S.upstream_as_probes([]))

class TestLastProbeFallback(unittest.TestCase):
    R = [{"ts": ts(1, 41), "probes": {"2": [{"ok": True, "ttft": 5}]},
          "watch": {"5": {"probes": []}},
          "toggle": {"disabled": {"5": {"results": [{"ok": True, "status": 200, "ttft": 2.0, "total": 2.4, "deltas": 25, "span": 0.2}]}}}}]
    def test_off_default(self):
        self.assertEqual(S.last_probe(self.R, 5, now(2, 0), 90), (None, None))
    def test_upstream_fallback(self):
        ps, age = S.last_probe(self.R, 5, now(2, 0), 90, watch_fallback=True)
        self.assertEqual(ps[0]["ttft"], 3.5); self.assertEqual(age, 19.0)
    def test_watch_preferred(self):
        r = [dict(self.R[0], watch={"5": {"probes": [{"ok": True, "ttft": 4.0, "streaming": True}]}})]
        ps, _ = S.last_probe(r, 5, now(2, 0), 90, watch_fallback=True)
        self.assertEqual(ps[0]["ttft"], 4.0)
    def test_main_probe_first(self):
        ps, _ = S.last_probe(self.R, 2, now(2, 0), 90, watch_fallback=True)
        self.assertEqual(ps[0]["ttft"], 5)
    def test_too_old(self):
        self.assertEqual(S.last_probe(self.R, 5, now(4, 0), 90, watch_fallback=True), (None, None))

class TestNewcomerMaxAge(unittest.TestCase):
    CH = [{"id": 2}, {"id": 23}]
    def hist(self):
        h = [{"ts": f"2026-10-25T00:{m:02d}:00+08:00", "applied": True, "channels": [{"id": 2}]} for m in range(0, 50, 10)]
        h += [{"ts": ts(1, m), "applied": True, "channels": [{"id": 2}, {"id": 23}]} for m in range(0, 50, 10)]
        return h
    def cfg(self, age): return {"newcomer": {"enabled": True, "min_rounds": 4, "max_age_h": age}}
    def test_off_graduates(self):
        self.assertEqual(decide.newcomer_ids(self.cfg(0), self.CH, self.hist(), ts(2, 0)), set())
    def test_young_stays(self):
        self.assertEqual(decide.newcomer_ids(self.cfg(24), self.CH, self.hist(), ts(2, 0)), {23})
    def test_old_graduates(self):
        self.assertEqual(decide.newcomer_ids(self.cfg(24), self.CH, self.hist(), "2026-10-28T02:00:00+08:00"), set())
    def test_now_default_last_round(self):
        self.assertEqual(decide.newcomer_ids(self.cfg(24), self.CH, self.hist()), {23})

if __name__ == "__main__": unittest.main()
