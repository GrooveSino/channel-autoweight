"""v1.19 shadow mode unit tests"""
import os, sys, unittest, ast
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import shadow as S
import evaluate_shadow as ES

CFG = {"windows_min": [60, 10], "probe_token_ids": [900], "trust": {"enabled": True},
       "shadow": {"min_real_n": 3, "probe_window_min": 10, "probe_cooldown_min": 30, "sev_gap_min": 10}}
def F(n10=10, err10=0):
    return {"60m": dict(n=100, err=err10, err_rate=err10 / 100, slow_rate=0, p50=10, p90=30),
            "10m": dict(n=n10, err=err10, err_rate=err10 / n10 if n10 else None, slow_rate=0, p50=10, p90=30)}

class T(unittest.TestCase):
    def test_real_counts_exclude_probe_token(self):
        rows = [dict(ch=1, t=990, tok=900), dict(ch=1, t=995, tok=7), dict(ch=1, t=100, tok=7)]
        self.assertEqual(S.real_counts(CFG, rows, 1000, 10), {1: 1})
    def test_plan_probes_threshold_and_cooldown(self):
        now = 1_800_000_000
        self.assertEqual(list(S.plan_probes(CFG, [1, 2], {1: 5, 2: 1}, [], now)), [2])
        from datetime import datetime, timezone
        rec = {"ts": datetime.fromtimestamp(now - 600, timezone.utc).isoformat(), "probes": {"2": [{"ok": True}]}}
        self.assertEqual(S.plan_probes(CFG, [1, 2], {1: 5, 2: 1}, [rec], now), {})
    def test_trust_independent_and_sev_discount(self):
        prev = {1: 0.9}
        a = S.trust_step(CFG, [1], {1: F(n10=5, err10=2)}, {}, prev, 5)
        b = S.trust_step(CFG, [1], {1: F(n10=5, err10=2)}, {}, prev, 10)
        self.assertGreater(a[1]["trust"], b[1]["trust"])   # a 5-minute interval only deducts half
        self.assertEqual(prev, {1: 0.9})
    def test_shadow_never_writes(self):
        for f in ("shadow.py", "autoweight/shadow.py"):
            names = {n.attr if isinstance(n, ast.Attribute) else n.id for n in ast.walk(ast.parse(open(os.path.join(HERE, f)).read()))
                     if isinstance(n, (ast.Attribute, ast.Name))}
            self.assertFalse(names & {"apply_weights", "psql"}, f)
    def test_parse_since(self):
        self.assertEqual(ES.parse_since("45m"), 45); self.assertEqual(ES.parse_since("2h"), 120)
    def test_summarize(self):
        def r(ts, w, sc, n):
            return {"ts": ts, "suggest": {"1": w, "2": 1}, "old": {"1": 12, "2": 1}, "names": {"1": "a", "2": "b"},
                    "scores": {"1": {"score": sc}, "2": {"score": 0.1}}, "trust": {"1": {"trust": 1.0}, "2": {"trust": 0.5}},
                    "features": {"1": {"10m": dict(n=n, err=0, p50=10, p90=20)}, "2": {"10m": dict(n=n, err=1, p50=30, p90=90)}}, "probed": {}}
        recs = [r("2026-10-24T20:00:00+08:00", 12, 0.5, 20), r("2026-10-24T20:05:00+08:00", 9, 0.4, 20), r("2026-10-24T20:10:00+08:00", 12, 0.5, 20)]   # since v1.65 windows are de-overlapped, so n is raised to 20
        s = ES.summarize(recs, 45)
        c = s["channels"][1]
        self.assertEqual(c["changes"], 2); self.assertEqual(c["diff_runs"], 1); self.assertEqual(s["verdict"], "match")
        self.assertIn("#1", ES.render(s))

if __name__ == "__main__":
    unittest.main()
