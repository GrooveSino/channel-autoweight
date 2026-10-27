import unittest, json, os, copy
from autoweight import decide
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CFG = json.load(open(os.path.join(HERE, "tests/fixtures/config.test.json")))

def win(n): return {"n": n, "err": 0, "err_rate": 0.0, "empty": 0, "empty_rate": 0.0, "p50": 10, "p90": 20, "slow_rate": 0.0}
def f(n): return {"60m": win(n), "10m": win(5)}
P = [{"ok": True, "ttft": 4.0, "streaming": True, "deltas": 100}]

class T(unittest.TestCase):
    def test_off_is_identity(self):
        c = copy.deepcopy(CFG); c["score"]["conf_k"] = 0
        self.assertEqual(decide.score(c, f(13), P)[0], decide.score(CFG, f(13), P)[0])
    def test_small_sample_penalized_more(self):
        c = copy.deepcopy(CFG); c["score"]["conf_k"] = 5
        base = decide.score(CFG, f(13), P)[0]
        small, big = decide.score(c, f(13), P)[0], decide.score(c, f(200), P)[0]
        self.assertAlmostEqual(small, base * 13 / 18, places=3)
        self.assertGreater(big, small)

if __name__ == "__main__": unittest.main()
