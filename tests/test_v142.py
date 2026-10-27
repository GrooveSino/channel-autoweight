import unittest, json, os, copy
from autoweight import decide
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CFG = json.load(open(os.path.join(HERE, "tests/fixtures/config.test.json")))

def win(n, p50, p90): return {"n": n, "err": 0, "err_rate": 0.0, "empty": 0, "empty_rate": 0.0, "p50": p50, "p90": p90, "slow_rate": 0.0, "avg_conc": 1.0}
P = [{"ok": True, "ttft": 4.0, "streaming": True, "deltas": 100}]

class T(unittest.TestCase):
    def test_off_is_identity(self):
        c = copy.deepcopy(CFG); c["score"]["w10_good_n0"] = 0
        f = {"60m": win(100, 15, 50), "10m": win(10, 9, 13)}
        self.assertEqual(decide.score(c, f, P)[0], decide.score(CFG, f, P)[0])
    def test_good_small_10m_discounted(self):
        c = copy.deepcopy(CFG); c["score"]["w10_good_n0"] = 20
        f = {"60m": win(100, 15, 50), "10m": win(10, 9, 13)}
        on, off = decide.score(c, f, P)[0], decide.score(CFG, f, P)[0]
        self.assertLess(on, off)
        long_only = {"60m": win(100, 15, 50), "10m": win(1, 9, 13)}  # 10m < min_n -> 1h only
        self.assertGreater(on, decide.score(CFG, long_only, P)[0])
    def test_bad_10m_full_weight(self):
        c = copy.deepcopy(CFG); c["score"]["w10_good_n0"] = 20
        f = {"60m": win(100, 15, 50), "10m": win(10, 30, 90)}
        self.assertEqual(decide.score(c, f, P)[0], decide.score(CFG, f, P)[0])

if __name__ == "__main__": unittest.main()
