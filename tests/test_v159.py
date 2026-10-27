import os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "tools"))
import buffer_audit as ba

class T(unittest.TestCase):
    def test_classify(self):
        self.assertEqual(ba.classify(78, 0.026), "likely_fp")
        self.assertEqual(ba.classify(30, 0.7), "confirmed")
        self.assertEqual(ba.classify(10, 0.0), "unknown")
        self.assertEqual(ba.classify(None, None), "unknown")
        self.assertEqual(ba.classify(30, 0.3), "unknown")
    def test_audit_dedup(self):
        p = {"2": [{"ok": True, "streaming": False}], "21": [{"ok": True, "streaming": True}]}
        f = {"2": {"60m": {"ls_n": 50, "ls_buf": 0.04}}}
        rs = [{"ts": "2026-10-26T01:45:01", "probes": p, "features": f},
              {"ts": "2026-10-26T01:45:40", "probes": p, "features": f, "applied": True}]
        ev, s = ba.audit(rs)
        self.assertEqual(len(ev), 1)
        self.assertEqual(s, {2: {"likely_fp": 1, "confirmed": 0, "unknown": 0}})
    def test_failed_probe_ignored(self):
        rs = [{"ts": "t", "probes": {"20": [{"ok": False, "streaming": None}]}, "features": {}}]
        self.assertEqual(ba.audit(rs)[0], [])

if __name__ == "__main__": unittest.main()
