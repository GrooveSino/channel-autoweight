"""v1.8: empty-success statistics + all-probes-failed penalty"""
import json, os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import features, decide
CFG = json.load(open(os.path.join(HERE, "tests/fixtures/config.test.json")))

def rows(n, ct, now=10000):
    return [dict(ch=1, t=now - 1 - k, type=2, ut=5, ct=ct) for k in range(n)]

class T(unittest.TestCase):
    def test_empty_counted(self):
        w = features.window(rows(8, 100) + rows(2, 0), 10000, 60, 60)
        self.assertEqual((w["empty"], w["empty_rate"]), (2, 0.2))

    def test_empty_unknown_ct(self):
        w = features.window(rows(5, None), 10000, 60, 60)
        self.assertIsNone(w["empty"]); self.assertIsNone(w["empty_rate"]); self.assertIsNone(w["ctok_p50"])

    def test_empty_weight_lowers_score(self):
        w = features.window(rows(8, 100) + rows(2, 0), 10000, 60, 60)
        s0 = decide.score_window(w, 3, dict(CFG["score"], empty_weight=0))
        s1 = decide.score_window(w, 3, dict(CFG["score"], empty_weight=1))
        self.assertAlmostEqual(s1 / s0, 0.8 ** CFG["score"]["err_exp"], places=4)

    def test_probe_fail_penalty(self):
        f = {"60m": features.window(rows(20, 100), 10000, 60, 60), "10m": features.window(rows(20, 100), 10000, 10, 60)}
        cfg = json.loads(json.dumps(CFG)); cfg["score"]["probe_fail_penalty"] = 0.5
        bad = [{"ok": False}, {"ok": False}]
        a, _, _ = decide.score(cfg, f, None)            # no probe: no penalty
        b, _, _ = decide.score(cfg, f, bad)             # all failed: penalty
        self.assertAlmostEqual(b, round(a * 0.5, 4), places=3)
        cfg["score"]["probe_fail_penalty"] = 1.0
        self.assertEqual(decide.score(cfg, f, bad)[0], a)

if __name__ == "__main__": unittest.main()
