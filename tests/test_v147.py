"""v1.47 newcomer onboarding: auto discovery, t_new low-trust start, cap until enough real traffic"""
import json, os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import decide, trust, features
sys.path.insert(0, os.path.join(HERE, "tests")); from test_v146 import cfg as base_cfg
from test_trust import F

def cfg(nc=None):
    c = base_cfg(); c["trust"]["enabled"] = False
    if nc is not None: c["newcomer"] = nc
    return c
def FX(n_user, **kw):
    f = F(**kw); f["mix_60m"] = dict(n_user=n_user, n_probe=0, tokens=1, top_tok_share=1.0); return f
CH = [dict(id=2), dict(id=21), dict(id=22)]
NEW = {21, 22}
def FE(u21, u22):
    fe = {2: FX(600), 21: FX(u21), 22: FX(u22)}
    for i, p in ((2, 10), (21, 40), (22, 5)):
        fe[i]["60m"]["p50"] = fe[i]["10m"]["p50"] = p; fe[i]["60m"]["p90"] = fe[i]["10m"]["p90"] = p * 3
    return fe
NC = dict(enabled=True, min_real_n60=30, cap=6)

class T(unittest.TestCase):
    def test_config_newcomer_on_and_no_hardcoded_ids(self):
        c = json.load(open(os.path.join(HERE, "tests/fixtures/config.test.json")))
        self.assertTrue(c["newcomer"]["enabled"])
        self.assertNotIn("ids", c["newcomer"])
        self.assertLess(c["trust"]["t_new"], c["trust"]["t_init"])
    def test_t_new_used_for_new_channel(self):
        p = dict(trust.params({"trust": {"t_init": 0.6, "t_new": 0.3}}))
        self.assertAlmostEqual(trust.update(None, 0, 0, p), 0.3)
        self.assertAlmostEqual(trust.update(0.8, 0, 0, p, gap_min=999), 0.7)  # gap pull-back still uses t_init
    def test_t_new_defaults_to_t_init(self):
        p = trust.params({"trust": {"t_init": 0.6}})
        self.assertAlmostEqual(trust.update(None, 0, 0, p), 0.6)
    def test_newcomer_capped_even_if_best(self):
        d = decide.decide(cfg(NC), CH, FE(0, 0), {}, newcomers=NEW)
        self.assertLessEqual(d["target"][22], 6); self.assertLessEqual(d["target"][21], 6)
        self.assertEqual(d["target"][2], 12)
    def test_newcomer_released_with_real_traffic(self):
        d = decide.decide(cfg(NC), CH, FE(10, 100), {}, newcomers=NEW)
        self.assertEqual(d["target"][22], 12); self.assertLessEqual(d["target"][21], 6)
    def test_two_newcomers_both_capped_after_rerank(self):
        fe = FE(0, 0)
        for i in (21, 22): fe[i]["60m"]["p50"] = fe[i]["10m"]["p50"] = 3; fe[i]["60m"]["p90"] = fe[i]["10m"]["p90"] = 9
        d = decide.decide(cfg(NC), CH, fe, {}, newcomers=NEW)
        self.assertLessEqual(d["target"][21], 6); self.assertLessEqual(d["target"][22], 6)
        self.assertGreaterEqual(d["target"][2], 12)
    def test_no_newcomers_no_cap(self):
        a = decide.decide(cfg(NC), CH, FE(0, 0), {}, newcomers=None)["target"]
        self.assertEqual(a[22], 12)
    def test_newcomer_ids_from_applied_rounds(self):
        c = dict(newcomer=dict(enabled=True, min_rounds=2))
        old = [dict(id=2)]; allc = [dict(id=2), dict(id=22)]
        rounds = [dict(applied=True, channels=old)] * 3 + [dict(applied=False, channels=allc)] * 5
        self.assertEqual(decide.newcomer_ids(c, allc, rounds), {22})
        rounds += [dict(applied=True, channels=allc)] * 2
        self.assertEqual(decide.newcomer_ids(c, allc, rounds), set())
    def test_off_or_missing_mix_no_cap(self):
        fe = {2: F(n60=600), 21: F(), 22: F()}
        a = decide.decide(cfg(NC), CH, fe, {})["target"]; b = decide.decide(cfg(), CH, fe, {})["target"]
        self.assertEqual(a, b)
    def test_traffic_mix_excludes_probe_token(self):
        rows = [dict(ch=21, t=100, type=2, ut=5, ct=100, tok=900)] * 8
        self.assertEqual(features.traffic_mix(rows, 100, 60, [900])["n_user"], 0)

if __name__ == "__main__": unittest.main()
