import unittest, sys, os
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "tools"))
import toggle_audit as TA

def R(ts, ps, pu, tt=(4.0, 3.5, 3.6), base=3.0, bu=2.0):
    return dict(ts=ts, toggle=dict(base={"ttft": base, "total": 5}, base_up={"ttft": bu, "total": 2.2},
        disabled={"21": dict(pass_site=ps, pass_up=pu, results=[dict(ok=True, ttft=t, total=t + .3, span=.2, deltas=20) for t in tt])}))

class TestV198(unittest.TestCase):
    def test_dedup_keeps_later_of_pair(self):
        rs = [R("2026-10-27T05:26:00+08:00", False, False), R("2026-10-27T05:29:00+08:00", True, False)]
        out = TA.audit(rs)
        self.assertEqual(len(out["rows"]), 1); self.assertTrue(out["rows"][0]["pass_site"])

    def test_agree_and_streaks(self):
        rs = [R("2026-10-27T03:00:00+08:00", False, False), R("2026-10-27T04:00:00+08:00", True, False),
              R("2026-10-27T05:00:00+08:00", True, True)]
        s = TA.audit(rs)["summary"]["21"]
        self.assertEqual(s["rounds"], 3); self.assertEqual(s["agree"], 0.67)
        self.assertEqual(s["streak_site"], 2); self.assertEqual(s["streak_up"], 1)

    def test_ratios(self):
        x = TA.audit([R("2026-10-27T03:00:00+08:00", False, False)])["rows"][0]
        self.assertEqual(x["r_site_ttft"], round(4.0 / 3.0, 2)); self.assertEqual(x["r_up_ttft"], 1.8)
        self.assertEqual(x["span_med"], .2); self.assertEqual(x["deltas_med"], 20)

    def test_old_records_without_up(self):
        r = R("2026-10-27T02:00:00+08:00", False, None); r["toggle"].pop("base_up")
        out = TA.audit([r])
        self.assertIsNone(out["rows"][0]["r_up_ttft"]); self.assertIsNone(out["summary"]["21"]["agree"])

    def test_last_n(self):
        rs = [R(f"2026-10-27T0{i}:00:00+08:00", False, False) for i in range(5)]
        self.assertEqual(len(TA.audit(rs, last=2)["rows"]), 2)

if __name__ == "__main__":
    unittest.main()
