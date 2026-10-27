"""v1.18 trust slow-rise / fast-fall unit tests"""
import os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import trust, decide

CFG = {"windows_min": [60, 10], "trust": {"enabled": True}}
def F(n60=100, err60=0, n10=10, err10=0, slow=0.0):
    return {"60m": dict(n=n60, err=err60, err_rate=err60 / n60 if n60 else None, slow_rate=slow, p50=10, p90=30),
            "10m": dict(n=n10, err=err10, err_rate=err10 / n10 if n10 else None, slow_rate=0, p50=10, p90=30)}

class T(unittest.TestCase):
    def setUp(self): self.p = trust.params(CFG)
    def test_new_channel_starts_at_init(self):
        self.assertAlmostEqual(trust.update(None, 0, 0, self.p), self.p["t_init"])
    def test_slow_up_fast_down(self):
        t = trust.update(0.6, 0, 100, self.p); self.assertAlmostEqual(t, 0.6 + self.p["up"], 4)
        d = trust.update(0.9, 1.0, 100, self.p); self.assertAlmostEqual(d, round(0.9 * (1 - self.p["down"]), 4))
        self.assertGreater(0.9 - d, t - 0.6)       # falls faster than it rises
    def test_low_traffic_grows_slower(self):
        self.assertLess(trust.update(0.5, 0, 3, self.p), trust.update(0.5, 0, 100, self.p))
    def test_floor_and_cap(self):
        self.assertEqual(trust.update(0.1, 1.0, 100, self.p), self.p["t_min"])
        self.assertEqual(trust.update(1.0, 0, 100, self.p), 1.0)
    def test_short_gap_no_double_count(self):
        self.assertEqual(trust.update(0.5, 0, 100, self.p, gap_min=0), 0.5)
    def test_long_gap_pulls_to_init(self):
        self.assertLess(trust.update(1.0, 0, 0, self.p, gap_min=1000), 1.0)
    def test_severity_events(self):
        self.assertEqual(trust.severity(F(), [{"ok": True, "streaming": True}], self.p)[0], 0)
        self.assertGreater(trust.severity(F(n10=5, err10=1), [], self.p)[0], 0)
        self.assertEqual(trust.severity(F(), [{"ok": False}], self.p)[0], self.p["probe_fail_sev"])
        self.assertEqual(trust.severity(F(), [{"ok": True, "streaming": False}], self.p)[0], self.p["buffered_sev"])
    def test_step_and_bootstrap(self):
        st = trust.step(CFG, [1, 2], {1: F(), 2: F(n10=5, err10=2)}, {}, {"1": 0.8, "2": 0.8}, 45)
        self.assertGreater(st[1]["trust"], 0.8); self.assertLess(st[2]["trust"], 0.8)
        hist = [{"ts": "2026-10-24T10:00:00+08:00", "dry_run": False, "features": {"1": F()}, "probes": {}},
                {"ts": "2026-10-24T10:30:00+08:00", "dry_run": True, "features": {"1": F(n10=5, err10=5)}, "probes": {}}]
        s, ts = trust.bootstrap(CFG, hist); self.assertIn(1, s); self.assertEqual(ts, "2026-10-24T10:00:00+08:00")
    def test_last_state_ignores_dry_run(self):
        hist = [{"ts": "a", "dry_run": False, "trust": {"5": {"trust": 0.8}}},
                {"ts": "b", "dry_run": True, "trust": {"5": {"trust": 0.3}}}]
        self.assertEqual(trust.last_state(hist), ({5: 0.8}, "a"))
        self.assertEqual(trust.last_state(hist[1:]), ({}, None))
    def test_decide_applies_multiplier_only_when_enabled(self):
        cfg = {"windows_min": [60, 10], "score": {"t_ref": 20, "a_p50": .3, "b_p90": .3, "c_ttft": 1, "w_10m": .4, "min_n_window": 5,
               "ttft_missing_s": 30, "nonstream_penalty": .85}, "min_n_rank1": 10, "strategy": "rank",
               "strategies": {"rank": dict(top_w=12, close_ratio=.85, close_factor=.8, mid_ratio=.6, mid_w=3, low_w=1, tie_ratio=.9)},
               "w_total": 18, "w_min": 1, "w_max": 20, "error_alert": {"rate": .2, "min_n": 20}, "trust": {"enabled": True, "floor": 0.5}}
        ch = [dict(id=1), dict(id=2)]; fe = {1: F(), 2: F()}
        d = decide.decide(cfg, ch, fe, {}, trust={1: 1.0, 2: 0.1})
        self.assertEqual(d["order"][0], 1); self.assertLess(d["scores"][2]["score"], d["scores"][1]["score"])
        cfg["trust"]["enabled"] = False
        d2 = decide.decide(cfg, ch, fe, {}, trust={1: 1.0, 2: 0.1})
        self.assertEqual(d2["scores"][1]["score"], d2["scores"][2]["score"])

if __name__ == "__main__": unittest.main()
