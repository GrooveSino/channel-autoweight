"""v1.21: gap backfill time points, per-time-point log truncation, review verdict with insufficient samples"""
import os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import shadow as S
import evaluate_shadow as ES

class T(unittest.TestCase):
    def test_slots_fill_gap_aligned(self):
        last = 1_000_000_200 + 3   # just past a 5-minute boundary
        now = last + 30 * 60
        sl = S.backfill_slots(last, now, 5)
        self.assertTrue(all(t % 300 == 0 for t in sl))
        self.assertTrue(all(last + 150 <= t < now - 150 for t in sl))
        self.assertEqual(len(sl), 5)
    def test_slots_none_without_gap_or_history(self):
        self.assertEqual(S.backfill_slots(None, 10_000, 5), [])
        self.assertEqual(S.backfill_slots(9_900, 10_000, 5), [])
    def test_slots_capped(self):
        sl = S.backfill_slots(0, 100 * 3600, 5, max_min=60)
        self.assertLessEqual(len(sl), 12)
    def test_compute_truncates_future_rows(self):
        import shadow as SH
        from unittest import mock
        cfg = {"windows_min": [60, 10], "slow_s": 60, "probe_token_ids": []}
        rows = [dict(ch=2, t=1000, type=2, ut=5, ct=10, tok=1), dict(ch=2, t=5000, type=5, ut=0, ct=0, tok=1)]
        seen = {}
        def fake_build(cfg_, chans, rows_, now): seen["rows"] = rows_; raise StopIteration
        with mock.patch.object(SH.features, "build", fake_build):
            with self.assertRaises(StopIteration):
                SH.compute(cfg, S.params({}), [dict(id=2, name="a", weight=1)], rows, 2000, [], [], {}, {}, backfill=True)
        self.assertEqual([r["t"] for r in seen["rows"]], [1000])
    def test_verdict_single_channel_is_insufficient(self):
        def rec(ts, n2, n5):
            return {"ts": ts, "suggest": {"2": 12, "5": 3}, "old": {"2": 12, "5": 3}, "names": {"2": "a", "5": "b"},
                    "scores": {"2": {"score": .5}, "5": {"score": .4}}, "trust": {"2": {"trust": 1}, "5": {"trust": 1}},
                    "features": {"2": {"10m": dict(n=n2, err=0, p50=10, p90=20)}, "5": {"10m": dict(n=n5, err=0, p50=5, p90=8)}}, "probed": {}}
        s = ES.summarize([rec("2026-10-24T21:00:00+08:00", 30, 1), rec("2026-10-24T21:05:00+08:00", 30, 1)], 45)
        self.assertTrue(s["verdict"].startswith("insufficient samples"))

if __name__ == "__main__":
    unittest.main()
