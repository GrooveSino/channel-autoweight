import unittest, datetime as dt
from autoweight import shadow as S

def ts(h, m, s=0):
    return dt.datetime(2026, 10, 25, h, m, s, tzinfo=dt.timezone(dt.timedelta(hours=8))).isoformat()

P = {"2": [{"ok": True, "ttft": 7.5, "streaming": False}]}

class T(unittest.TestCase):
    def _now(self, h, m):
        return dt.datetime(2026, 10, 25, h, m, tzinfo=dt.timezone(dt.timedelta(hours=8))).timestamp()

    def test_skip_reused_round(self):
        recs = [{"ts": ts(6, 14, 54), "probes": P}, {"ts": ts(6, 15, 18), "probes": P, "probes_reused": True}]
        ps, age = S.last_probe(recs, 2, self._now(6, 20), 90)
        self.assertIsNotNone(ps)
        self.assertGreaterEqual(age, 5.0)               # uses the original probe time
        self.assertFalse(S.probe_new(age, 5))          # no longer new evidence at 06:20

    def test_original_probe_still_new_once(self):
        recs = [{"ts": ts(6, 14, 54), "probes": P}, {"ts": ts(6, 15, 18), "probes": P, "probes_reused": True}]
        ps, age = S.last_probe(recs, 2, self._now(6, 15), 90)
        self.assertTrue(S.probe_new(age, 5))

    def test_non_reused_round_kept(self):
        recs = [{"ts": ts(6, 15, 18), "probes": P, "probes_reused": False}]
        ps, age = S.last_probe(recs, 2, self._now(6, 20), 90)
        self.assertLess(age, 5)

if __name__ == "__main__":
    unittest.main()
