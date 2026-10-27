import unittest
from autoweight import watch

class FakeDB:
    def __init__(self, rows): self.rows = rows
    def ro(self, cfg, sql): return self.rows

class FakeProbe:
    def __init__(self): self.calls = 0
    def load_key(self, cfg): return "k"
    def probe_once(self, cfg, key, i):
        self.calls += 1
        return dict(ok=True, streaming=True, ttft=2.0, status=200, deltas=50)

CFG = {"watch": {"enabled": True, "ids": [5, 14], "n_probe": 2,
                 "criteria": {"max_ttft": 10, "min_n": 10, "max_err": 0.05, "max_empty": 0.05}},
       "windows_min": [60, 10]}

class T(unittest.TestCase):
    def _run(self, rows, cfg=CFG):
        pr = FakeProbe()
        orig = watch.features.build
        watch.features.build = lambda *a, **k: {}
        try:
            out = watch.run(cfg, FakeDB(rows), pr, lambda ids: (None, []))
        finally:
            watch.features.build = orig
        return out, pr

    def test_missing_channel_not_probed(self):
        out, pr = self._run([(5, "x", 2, 1, 10)])
        self.assertTrue(out[14]["missing"]); self.assertEqual(out[14]["probes"], [])

    def test_disabled_not_probed_unknown(self):
        out, pr = self._run([(5, "x", 2, 1, 10)])
        self.assertIsNone(out[5]["suggest_enable"]); self.assertEqual(pr.calls, 0)

    def test_enabled_probed(self):
        out, pr = self._run([(5, "x", 1, 1, 10)])
        self.assertEqual(pr.calls, 2); self.assertTrue(out[5]["suggest_enable"])

    def test_probe_disabled_flag(self):
        cfg = dict(CFG, watch=dict(CFG["watch"], probe_disabled=True))
        out, pr = self._run([(5, "x", 2, 1, 10)], cfg)
        self.assertEqual(pr.calls, 2)

if __name__ == "__main__": unittest.main()
