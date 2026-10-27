import unittest, sys, os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "tools"))
import recheck_audit as ra

class T(unittest.TestCase):
    def test_classify(self):
        ok = dict(ok=True, streaming=True)
        self.assertIsNone(ra.classify([]))
        self.assertEqual(ra.classify([ok]), "ok")
        self.assertEqual(ra.classify([dict(ok=True, streaming=False), dict(ok=True, streaming=True, recheck=True)]), "flaky")
        self.assertEqual(ra.classify([dict(ok=False), dict(ok=False, recheck=True)]), "bad")
    def test_audit_after_window(self):
        f = lambda p90: {"21": {"60m": {"n": 5, "p90": p90, "err_rate": 0.0, "slow_rate": 0.0}}}
        rounds = [dict(ts="2026-10-26T01:00:00+08:00", probes={"21": [dict(ok=True, streaming=False), dict(ok=True, streaming=True, recheck=True)]}),
                  dict(ts="2026-10-26T01:10:00+08:00", features=f(99)),   # too close, skipped
                  dict(ts="2026-10-26T01:45:00+08:00", features=f(30), probes={"21": [dict(ok=True, streaming=True)]})]
        res = ra.audit(rounds)
        self.assertEqual(res["flaky"]["events"], 1); self.assertEqual(res["flaky"]["p90_med"], 30)
        self.assertEqual(res["ok"]["with_after"], 0)



if __name__ == "__main__": unittest.main()
