import unittest, sys, os
sys.path.insert(0, os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "tools"))
import probe_log_gap as G

def R(ts, t, lt, n=5, recheck_first=False):
    ps = [dict(ok=True, ttft=99.0, recheck=True)] if recheck_first else []
    ps.append(dict(ok=True, ttft=t, streaming=True))
    return dict(ts=ts, probes={"5": ps}, features={"5": {"60m": {"lt50": lt, "lt_n": n}}})

class TestV201(unittest.TestCase):
    def test_slow_flag(self):
        s = G.audit([R("2026-10-27T09:18:00+08:00", 24.5, 3.3)])["summary"]["5"]
        self.assertEqual(s["last_flag"], "PROBE_SLOW"); self.assertEqual(s["last"], 7.42)
    def test_fast_and_normal(self):
        rows = G.audit([R("2026-10-27T07:00:00+08:00", 1.0, 3.0), R("2026-10-27T08:00:00+08:00", 3.0, 3.0)])["rows"]
        self.assertEqual([x["flag"] for x in rows], ["PROBE_FAST", ""])
    def test_min_lt_n(self):
        self.assertEqual(G.audit([R("2026-10-27T09:00:00+08:00", 24, 3, n=2)])["rows"], [])
    def test_dedup_dryrun_apply(self):
        out = G.audit([R("2026-10-27T09:18:00+08:00", 24, 3), R("2026-10-27T09:21:00+08:00", 3, 3)])
        self.assertEqual(len(out["rows"]), 1); self.assertEqual(out["rows"][0]["ratio"], 1.0)
    def test_skips_recheck(self):
        r = G.audit([R("2026-10-27T09:00:00+08:00", 3, 3, recheck_first=True)])["rows"][0]
        self.assertEqual(r["probe"], 3)

if __name__ == "__main__":
    unittest.main()
