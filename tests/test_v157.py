"""v1.57 concurrency capacity cap r_mode=rest: the cap no longer loops too tight when the capacity channel ranks first"""
import json, os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import decide
sys.path.insert(0, os.path.join(HERE, "tests")); from test_v147 import cfg, FE, CH
from test_v148 import withconc, CAP

class T(unittest.TestCase):
    def setUp(self):
        self.fe = FE(600, 600)
        self.fe[21]["60m"]["p50"] = self.fe[21]["10m"]["p50"] = 2; self.fe[21]["60m"]["p90"] = self.fe[21]["10m"]["p90"] = 6
        withconc(self.fe, {2: 6, 21: 2, 22: 2})  # C=20, s=0.2
    def run_mode(self, mode):
        c = cfg(); c["capacity"] = dict(CAP, r_mode=mode)
        return decide.decide(c, CH, self.fe, {})
    def test_rest_share_close_to_limit(self):
        t = self.run_mode("rest")["target"]; share = t[21] / sum(t.values())
        self.assertLessEqual(share, 0.2 + 0.06)
        self.assertGreater(share, 0.1)
    def test_rest_not_tighter_than_first(self):
        a = self.run_mode("first")["scores"][21].get("capacity_cap"); b = self.run_mode("rest")["scores"][21].get("capacity_cap")
        self.assertGreaterEqual(b, a)
    def test_config_rest(self):
        c = json.load(open(os.path.join(HERE, "tests/fixtures/config.test.json")))
        self.assertEqual(c["capacity"].get("r_mode"), "rest")
if __name__ == "__main__": unittest.main()
