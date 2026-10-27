import json, os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import decide as D

def p(**k):
    c = json.load(open(os.path.join(HERE, "tests/fixtures/config.test.json")))["strategies"]["rank"]; c = dict(c); c.update(k); return c
CFG = {"w_total": 16}

class T(unittest.TestCase):
    def test_default_off(self):
        assert p()["ramp"] == 0

    def test_off_equals_step(self):
        sc = {2: 1.0, 20: 0.80, 5: 0.5}
        w = D.strat_rank([2, 20, 5], sc, p(), CFG)
        assert w[20] == p()["mid_w"] and w[5] == p()["low_w"]

    def test_ramp_interpolates_mid_close(self):
        pp = p(ramp=0.1); cw = pp["top_w"] * pp["close_factor"]
        mid = (pp["mid_ratio"] + pp["close_ratio"]) / 2
        w = D.strat_rank([2, 20], {2: 1.0, 20: mid}, pp, CFG)
        assert abs(w[20] - (pp["mid_w"] + cw) / 2) < 1e-6

    def test_ramp_low_side_and_bounds(self):
        pp = p(ramp=0.1)
        w = D.strat_rank([2, 20, 5], {2: 1.0, 20: pp["close_ratio"], 5: pp["mid_ratio"] - 0.05}, pp, CFG)
        assert w[20] == pp["top_w"] * pp["close_factor"]
        assert pp["low_w"] < w[5] < pp["mid_w"]
        w = D.strat_rank([2, 5], {2: 1.0, 5: 0.1}, pp, CFG); assert w[5] == pp["low_w"]

if __name__ == '__main__': unittest.main()
