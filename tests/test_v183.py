import unittest
from autoweight import upstream, diag

NOW = 1793012400  # arbitrary

class T(unittest.TestCase):
    def test_classify(self):
        self.assertEqual(upstream.classify({"ok": True, "ttft": 3}), "ok")
        self.assertEqual(upstream.classify({"ok": True, "ttft": 30}), "slow")
        self.assertEqual(upstream.classify({"ok": False, "status": 524}), "timeout")
        self.assertEqual(upstream.classify({"ok": False, "err": "ReadTimeout: x"}), "timeout")
        self.assertEqual(upstream.classify({"ok": False, "status": 401}), "http_err")
        self.assertEqual(upstream.classify({"ok": False, "status": 200}), "empty")

    def test_verdict(self):
        self.assertTrue(upstream.verdict([{"ok": True, "ttft": 3}] * 2)["recovered"])
        self.assertFalse(upstream.verdict([{"ok": True, "ttft": 3}])["recovered"])
        self.assertFalse(upstream.verdict([{"ok": True, "ttft": 3}, {"ok": False, "status": 524}])["recovered"])

    def _e(self, age_min):
        from datetime import datetime, timezone
        t = datetime.fromtimestamp(NOW - age_min * 60, timezone.utc).isoformat()
        return {"22": {"disabled_at": t, "vendor": "ACME"}}

    def test_upstream_replaces_missing_sibling(self):
        r = diag.reenable_check(self._e(130), [], {}, NOW, upstream={22: {"recovered": True, "classes": ["ok", "ok"]}})
        self.assertTrue(r[22]["ready"]); self.assertIn("direct upstream probe healthy", r[22]["why"][0])

    def test_no_evidence_without_upstream(self):
        r = diag.reenable_check(self._e(130), [], {}, NOW)
        self.assertFalse(r[22]["ready"])

    def test_upstream_bad_blocks(self):
        r = diag.reenable_check(self._e(130), [], {}, NOW, upstream={22: {"recovered": False, "classes": ["timeout", "ok"]}})
        self.assertFalse(r[22]["ready"])

    def test_age_still_required(self):
        r = diag.reenable_check(self._e(100), [], {}, NOW, upstream={22: {"recovered": True, "classes": ["ok", "ok"]}})
        self.assertFalse(r[22]["ready"])

    def test_remote_script_never_prints_key(self):
        # the remote script prints exactly one json; no field contains the key
        self.assertEqual(upstream.REMOTE.count("print("), 2)
        self.assertNotIn('"key":', upstream.REMOTE)

class T2(unittest.TestCase):
    def test_reenabled_skipped(self):
        r = diag.reenable_check({"22": {"disabled_at": "2026-10-26T17:05:00+08:00", "reenabled_at": "2026-10-26T19:07:00+08:00"}}, [], {}, NOW)
        self.assertEqual(r, {})

if __name__ == "__main__":
    unittest.main()
