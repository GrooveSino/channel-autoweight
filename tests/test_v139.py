import unittest, json, os, copy
from autoweight import decide
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CFG = json.load(open(os.path.join(HERE, "tests/fixtures/config.test.json")))

def win(n, conc): return {"n": n, "err": 0, "err_rate": 0.0, "empty": 0, "empty_rate": 0.0, "p50": 10, "p90": 20, "slow_rate": 0.0, "avg_conc": conc}
def f(conc): return {"60m": win(100, 1.0), "10m": win(20, conc)}
P = [{"ok": True, "ttft": 4.0, "streaming": True, "deltas": 100}]

class T(unittest.TestCase):
    def test_off_is_identity(self):
        c = copy.deepcopy(CFG); c["score"]["conc_k"] = 0
        self.assertEqual(decide.score(c, f(4.0), P)[0], decide.score(c, f(0.5), P)[0])
    def test_high_conc_penalized(self):
        c = copy.deepcopy(CFG); c["score"]["conc_k"] = 0.4; c["score"]["conc_c0"] = 2.0
        base = decide.score(CFG, f(4.0), P)[0]
        self.assertAlmostEqual(decide.score(c, f(4.0), P)[0], base / 1.8, places=3)
        self.assertEqual(decide.score(c, f(1.5), P)[0], base)
    def test_missing_conc_safe(self):
        c = copy.deepcopy(CFG); c["score"]["conc_k"] = 0.4
        x = f(1.0); del x["10m"]["avg_conc"]
        self.assertGreater(decide.score(c, x, P)[0], 0)

if __name__ == "__main__": unittest.main()
