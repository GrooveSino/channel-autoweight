import copy, json, os, unittest
from autoweight import decide, diag
CFG = json.load(open(os.path.join(os.path.dirname(__file__), "..", "tests/fixtures/config.test.json")))
P = dict(prior_a=0.3, prior_b=1.7, half_life_h=6.0, max_age_h=48)
def pr(st): return [{"ok": True, "ttft": 5.0, "streaming": st}]

class TestBufferProb(unittest.TestCase):
    def test_prior_only_when_unknown(self):
        out = diag.buffer_prob([], 0, {"2": pr(None)}, P)
        self.assertAlmostEqual(out[2], 0.15)

    def test_current_buffer_raises(self):
        out = diag.buffer_prob([], 0, {"2": pr(False)}, P)
        self.assertAlmostEqual(out[2], 1.3 / 3.0, places=4)

    def test_old_events_decay(self):
        old = [(0, {"2": pr(False)})] * 5
        near = diag.buffer_prob(old, 3600, {"2": pr(True)}, P)[2]
        far = diag.buffer_prob(old, 40 * 3600, {"2": pr(True)}, P)[2]
        self.assertGreater(near, far)
        self.assertEqual(diag.buffer_prob(old, 50 * 3600, {"2": pr(True)}, P)[2], round(0.3 / 3.0, 4))

    def test_score_continuous_penalty_only_when_enabled(self):
        f = {"60m": {"n": 30, "err": 0, "err_rate": 0.0, "empty_rate": 0.0, "p50": 10, "p90": 20, "slow_rate": 0.0}, "10m": {"n": 0, "err": 0, "err_rate": None, "p50": None, "p90": None, "slow_rate": None}}
        cfg = copy.deepcopy(CFG)
        base = decide.score(cfg, f, pr(False))[0]
        self.assertEqual(decide.score(cfg, f, pr(False), pbuf=0.0)[0], base)   # pbuf ignored when disabled
        cfg["score"]["buffer_prob"]["enabled"] = True
        clean = decide.score(cfg, f, pr(False), pbuf=0.0)[0]
        half = decide.score(cfg, f, pr(False), pbuf=0.5)[0]
        self.assertAlmostEqual(half, clean * (1 - 0.15 * 0.5), places=3)
        self.assertAlmostEqual(base, clean * 0.85, places=3)

if __name__ == "__main__":
    unittest.main()
