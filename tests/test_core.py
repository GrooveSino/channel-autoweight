"""Basic unit tests: python3 -m unittest discover -s tests"""
import json, os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import features, decide
CFG = json.load(open(os.path.join(HERE, "tests/fixtures/config.test.json")))

def mk(ch, n, ut, err=0, now=10000):
    return [dict(ch=ch, t=now - 1 - k, type=2, ut=ut, ct=100) for k in range(n)] + \
           [dict(ch=ch, t=now - 1, type=5, ut=0, ct=0) for _ in range(err)]

def feats(spec, now=10000):
    rows = sum((mk(i, n, ut, e, now) for i, (n, ut, e) in spec.items()), [])
    chans = [dict(id=i, name=str(i), weight=5, priority=10) for i in spec]
    return chans, features.build(CFG, chans, rows, now)

class T(unittest.TestCase):
    def test_err_burst_factor(self):
        s = dict(CFG["score"], err_10m_exp=2, err_10m_min_n=2)
        self.assertEqual(decide.err_burst_factor({"n": 3, "err": 0, "err_rate": 0}, s), 1.0)
        self.assertEqual(decide.err_burst_factor({"n": 1, "err": 1, "err_rate": 1.0}, s), 1.0)   # too few samples, no penalty
        self.assertAlmostEqual(decide.err_burst_factor({"n": 4, "err": 2, "err_rate": 0.5}, s), 0.25)
        self.assertEqual(decide.err_burst_factor({"n": 4, "err": 2, "err_rate": 0.5}, dict(s, err_10m_exp=0)), 1.0)

    def test_shrink_small_sample(self):
        self.assertEqual(decide.shrink(2, 6, 6, 0), 2)                 # k=0 disables
        self.assertAlmostEqual(decide.shrink(2, 5, 6, 5), 4.0)          # n=k -> midpoint
        self.assertAlmostEqual(decide.shrink(6, 1000, 2, 5), 6, places=1)  # large samples barely move
        p50, p90 = decide.pooled({1: {"60m": {"n": 10, "p50": 2, "p90": 4}}, 2: {"60m": {"n": 30, "p50": 6, "p90": 8}}}, "60m")
        self.assertAlmostEqual((p50, p90), (5.0, 7.0))

    def test_backtest_set_path(self):
        import backtest
        c = {"score": {"a": 1}}
        backtest.set_path(c, "score.a=2.5"); backtest.set_path(c, "x.y=abc")
        self.assertEqual((c["score"]["a"], c["x"]["y"]), (2.5, "abc"))

    def test_window_stats(self):
        w = features.window(mk(1, 10, 70, 2), 10000, 60, 60)
        self.assertEqual((w["n"], w["err"], w["p50"], w["slow_rate"]), (12, 2, 70, 1.0))

    def test_best_gets_top(self):
        chans, f = feats({1: (50, 10, 0), 2: (50, 40, 0), 3: (50, 80, 5)})
        d = decide.decide(CFG, chans, f, {}, "rank")
        self.assertEqual(d["order"][0], 1)
        self.assertGreater(d["target"][1], d["target"][2])
        self.assertGreaterEqual(d["target"][2], d["target"][3])

    def test_tie_is_equal(self):
        chans, f = feats({1: (50, 20, 0), 2: (50, 20, 0)})
        d = decide.decide(CFG, chans, f, {}, "rank")
        self.assertEqual(d["target"][1], d["target"][2])

    def test_small_sample_not_first(self):
        chans, f = feats({1: (3, 2, 0), 2: (50, 30, 0)})
        d = decide.decide(CFG, chans, f, {}, "rank")
        self.assertEqual(d["order"][0], 2)

    def test_bounds_and_soft_cap(self):
        chans, f = feats({5: (50, 5, 0), 2: (50, 60, 0)})
        d = decide.decide(CFG, chans, f, {}, "rank")
        self.assertLessEqual(d["target"][5], CFG["soft_caps"]["5"]["cap"])
        for w in d["target"].values(): self.assertTrue(CFG["w_min"] <= w <= CFG["w_max"])

    def test_error_alert_lowers(self):
        chans, f = feats({1: (20, 5, 20), 2: (50, 30, 0)})
        d = decide.decide(CFG, chans, f, {}, "rank")
        self.assertEqual(d["target"][1], CFG["w_min"])

    def test_capped_top_hands_over(self):
        chans, f = feats({5: (50, 2, 0), 2: (50, 20, 0), 20: (50, 60, 0)})
        d = decide.decide(CFG, chans, f, {}, "rank")
        self.assertEqual(d["target"][5], CFG["soft_caps"]["5"]["cap"])
        self.assertEqual(d["target"][2], CFG["strategies"]["rank"]["top_w"])

    def test_stream_verdict_tristate(self):
        P = lambda st: {"ok": True, "ttft": 1.0, "streaming": st}
        self.assertIsNone(decide.stream_verdict([P(None)]))          # undecidable must not be treated as non-streaming
        self.assertFalse(decide.stream_verdict([P(False), P(None)]))
        self.assertTrue(decide.stream_verdict([P(False), P(True)]))
        self.assertIsNone(decide.ttft_of([{"ok": False}], {})[1])

if __name__ == "__main__": unittest.main()
