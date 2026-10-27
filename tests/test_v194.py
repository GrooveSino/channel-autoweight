import unittest, sys, os
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from unittest import mock
from autoweight import watch


def _run(status, n60, extra=None, toggle=True):
    cfg = dict(watch=dict(enabled=True, ids=[7], n_probe=2, auto_skip=True, auto_skip_min_n60=30,
                          criteria=dict(max_ttft=10.0, min_n=10, max_err=0.05, max_empty=0.05)),
               toggle=dict(enabled=toggle))
    if extra: cfg["watch"].update(extra)
    calls = []
    class P:
        @staticmethod
        def load_key(c): return "k"
        @staticmethod
        def probe_once(c, k, i):
            calls.append(i); return dict(ok=True, status=200, ttft=2.0, total=3.0, deltas=10, streaming=True)
    with mock.patch.object(watch, "channel_info", lambda c, d, ids: [dict(id=7, name="x", status=status, weight=0, priority=10)]), \
         mock.patch.object(watch.features, "build", lambda c, ch, rows, now: {7: {"60m": dict(n=n60, err_rate=0, empty_rate=0), "10m": {}}}):
        out = watch.run(cfg, None, P, lambda ids: (0, []))
    return out[7], calls


class TestWatchAutoSkip(unittest.TestCase):
    def test_disabled_covered_by_toggle(self):
        o, calls = _run(2, 0)
        self.assertEqual(o["covered_by"], "toggle"); self.assertEqual(calls, []); self.assertIsNone(o["suggest_enable"])

    def test_disabled_without_toggle_keeps_old(self):
        o, calls = _run(2, 0, toggle=False)
        self.assertNotIn("covered_by", o); self.assertIn("the site refuses probes", o["why"][0])

    def test_enabled_enough_traffic_skips(self):
        o, calls = _run(1, 30)
        self.assertEqual(o["covered_by"], "main"); self.assertEqual(calls, [])

    def test_enabled_newcomer_still_probed(self):
        o, calls = _run(1, 12)
        self.assertEqual(len(calls), 2); self.assertTrue(o["suggest_enable"])

    def test_auto_skip_off_probes(self):
        o, calls = _run(1, 100, extra=dict(auto_skip=False))
        self.assertEqual(len(calls), 2)


if __name__ == "__main__":
    unittest.main()
