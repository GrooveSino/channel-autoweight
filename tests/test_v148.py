"""v1.48 concurrency capacity cap"""
import json, os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import decide
sys.path.insert(0, os.path.join(HERE, "tests")); from test_v147 import cfg, FE, CH

def withconc(fe, conc):
    for i, c in conc.items(): fe[i]["60m"]["avg_conc"] = c
    return fe
CAP = dict(enabled=True, peak_factor=2.0, **{"21": dict(max_conc=4, reason="t")})

class T(unittest.TestCase):
    def setUp(self):
        self.fe = FE(600, 600)
        for i in (21, 22): self.fe[i]["60m"]["p50"] = self.fe[i]["10m"]["p50"] = 3; self.fe[i]["60m"]["p90"] = self.fe[i]["10m"]["p90"] = 9
    def test_off_no_change(self):
        a = decide.decide(cfg(), CH, withconc(self.fe, {2: 4, 21: 1, 22: 1}), {})
        c = cfg(); c["capacity"] = dict(CAP, enabled=False)
        b = decide.decide(c, CH, withconc(self.fe, {2: 4, 21: 1, 22: 1}), {})
        self.assertEqual(a["target"], b["target"])
    def test_share_bounded_under_high_load(self):
        c = cfg(); c["capacity"] = CAP
        d = decide.decide(c, CH, withconc(self.fe, {2: 6, 21: 2, 22: 2}), {})  # C=20, s=0.2
        t = d["target"]; share = t[21] / sum(t.values())
        self.assertLessEqual(share, 0.2 + 0.06)
        self.assertIn("capacity_cap", d["scores"][21])
    def test_low_load_not_binding(self):
        c = cfg(); c["capacity"] = CAP
        fe = withconc(self.fe, {2: 0.5, 21: 0.2, 22: 0.3})  # C=2 < 4
        a = decide.decide(cfg(), CH, fe, {}); b = decide.decide(c, CH, fe, {})
        self.assertEqual(a["target"], b["target"])
    def test_config_has_21(self):
        c = json.load(open(os.path.join(HERE, "tests/fixtures/config.test.json")))
        self.assertTrue(c["capacity"]["enabled"]); self.assertEqual(c["capacity"]["21"]["max_conc"], 4)
if __name__ == "__main__": unittest.main()
