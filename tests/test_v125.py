import os, sys, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight.decide import relax_cap

def W(n=100, err=0.0, empty=0.0, conc=2, ct=250):
    return dict(n=n, err_rate=err, empty_rate=empty, peak_conc=conc, ctok_p50=ct)
R = dict(enabled=True, max=12, min_n=50, err_max=0.02, empty_max=0.03, conc_max=4, ctok_ratio=0.7, trust_min=0.8)
WINS = ["60m", "10m"]

def feats(f5):
    return {5: f5, 2: {"60m": W(ct=260), "10m": W(n=20)}, 20: {"60m": W(ct=300), "10m": W(n=20)}}

class T(unittest.TestCase):
    def test_disabled_returns_base(self):
        self.assertEqual(relax_cap(5, 6, dict(R, enabled=False), feats({"60m": W(), "10m": W()}), {5: {"trust": 1}}, WINS), (6, None))
    def test_clean_full_trust_reaches_max(self):
        eff, why = relax_cap(5, 6, R, feats({"60m": W(), "10m": W(n=20)}), {5: {"trust": 1.0}}, WINS)
        self.assertEqual(eff, 12); self.assertIn("relaxed", why)
    def test_partial_trust_linear(self):
        eff, _ = relax_cap(5, 6, R, feats({"60m": W(), "10m": W(n=20)}), {5: {"trust": 0.9}}, WINS)
        self.assertAlmostEqual(eff, 9.0, places=1)
    def test_fast_down_on_each_violation(self):
        bad = [{"60m": W(conc=5), "10m": W()}, {"60m": W(empty=0.05), "10m": W()}, {"60m": W(), "10m": W(err=0.1)},
               {"60m": W(n=10), "10m": W()}, {"60m": W(ct=100), "10m": W()}]
        for f in bad:
            eff, why = relax_cap(5, 6, R, feats(f), {5: {"trust": 1.0}}, WINS)
            self.assertEqual(eff, 6, f); self.assertTrue(why.startswith("not relaxed"))
        eff, _ = relax_cap(5, 6, R, feats({"60m": W(), "10m": W()}), {5: {"trust": 0.7}}, WINS)
        self.assertEqual(eff, 6)
    def test_unknown_empty_not_relaxed(self):
        eff, _ = relax_cap(5, 6, R, feats({"60m": W(empty=None), "10m": W()}), {5: {"trust": 1.0}}, WINS)
        self.assertEqual(eff, 6)

if __name__ == "__main__": unittest.main()
