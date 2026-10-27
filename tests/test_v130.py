import json, os, unittest
from autoweight import decide
CFG = json.load(open(os.path.join(os.path.dirname(__file__), "..", "tests/fixtures/config.test.json")))

def w(n, p50, p90, ct):
    return {"n": n, "err": 0, "err_rate": 0.0, "empty": 0, "empty_rate": 0.0, "p50": p50, "p90": p90,
            "slow_rate": 0.0, "ctok_p50": ct, "nn": 0, "np50": None, "np90": None}

FE = {2: {"60m": w(7, 2, 2, 26)}, 5: {"60m": w(7, 1, 2, 20)}, 20: {"60m": w(82, 13, 49, 347)}}

class TestCmpGuard(unittest.TestCase):
    def s(self, **g):
        s = dict(CFG["score"]); s["cmp_guard"] = dict(s["cmp_guard"], **g); return s

    def test_off_returns_empty(self):
        self.assertEqual(decide.cmp_subs(FE, "60m", self.s(enabled=False)), {})

    def test_tiny_output_channels_get_pooled_latency(self):
        out = decide.cmp_subs(FE, "60m", self.s(enabled=True))
        self.assertEqual(set(out), {2, 5})
        self.assertEqual(out[2], (13.0, 49.0))

    def test_no_comparable_peer_no_sub(self):
        fe = {2: {"60m": w(7, 2, 2, 26)}, 5: {"60m": w(7, 1, 2, 20)}}
        self.assertEqual(decide.cmp_subs(fe, "60m", self.s(enabled=True)), {})

    def test_sub_makes_latency_neutral(self):
        s = self.s(enabled=True)
        a = decide.score_window(FE[2]["60m"], 4.0, s, sub=(13.0, 49.0))
        b = decide.score_window(FE[20]["60m"], 4.0, s)
        self.assertAlmostEqual(a, b, places=6)

if __name__ == "__main__":
    unittest.main()
