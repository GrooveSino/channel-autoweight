import json, os, sys, unittest, copy
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight import decide

CFG = json.load(open(os.path.join(os.path.dirname(__file__), "..", "tests/fixtures/config.test.json")))

def win(n, p50, p90):
    return {"n": n, "err": 0, "err_rate": 0.0, "p50": p50, "p90": p90, "slow_rate": 0.0, "empty_rate": 0.0}

class TestW10Adaptive(unittest.TestCase):
    def feats(self, n10):
        return {"60m": win(200, 10, 30), "10m": win(n10, 30, 60)}
    def test_off_equals_v126(self):
        c = copy.deepcopy(CFG); c["score"]["w10_n0"] = 0
        a = decide.score(c, self.feats(6), [])[0]
        c2 = copy.deepcopy(CFG); c2["score"].pop("w10_n0", None)
        self.assertEqual(a, decide.score(c2, self.feats(6), [])[0])
    def test_small_sample_less_weight(self):
        c = copy.deepcopy(CFG); c["score"]["w10_n0"] = 20
        off = copy.deepcopy(CFG); off["score"]["w10_n0"] = 0
        # 10m worse: with adaptation a small sample drags the total score less
        self.assertGreater(decide.score(c, self.feats(6), [])[0], decide.score(off, self.feats(6), [])[0])
    def test_large_sample_approaches_full(self):
        c = copy.deepcopy(CFG); c["score"]["w10_n0"] = 1
        off = copy.deepcopy(CFG); off["score"]["w10_n0"] = 0
        self.assertAlmostEqual(decide.score(c, self.feats(10000), [])[0], decide.score(off, self.feats(10000), [])[0], places=3)

if __name__ == "__main__": unittest.main()
