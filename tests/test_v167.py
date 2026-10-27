import json, os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight.decide import effective_rank_params

def cfg(mx):
    c = json.load(open(os.path.join(os.path.dirname(__file__), "..", "tests/fixtures/config.test.json")))
    c["strategies"]["rank"]["low_traffic"] = {"max_total_n60": mx, "close_ratio": 0.8}
    return c

ROWS_LOW = {2: {"n60": 7}, 20: {"n60": 7}, 21: {"n60": 7}, 22: {"n60": 7}}
ROWS_HIGH = {2: {"n60": 106}, 20: {"n60": 177}}

class T(unittest.TestCase):
    def test_off_returns_original(self):
        c = cfg(0)
        self.assertIs(effective_rank_params(c, "rank", ROWS_LOW), c["strategies"]["rank"])

    def test_low_traffic_overrides_close_ratio(self):
        p = effective_rank_params(cfg(120), "rank", ROWS_LOW)
        self.assertEqual(p["close_ratio"], 0.8)
        self.assertEqual(p["_low_traffic"], 28)
        self.assertNotIn("max_total_n60", {k for k in p if k != "low_traffic"})

    def test_high_traffic_unchanged(self):
        c = cfg(120)
        p = effective_rank_params(c, "rank", ROWS_HIGH)
        self.assertEqual(p["close_ratio"], c["strategies"]["rank"]["close_ratio"])
        self.assertNotIn("_low_traffic", p)

    def test_default_config_off(self):
        c = json.load(open(os.path.join(os.path.dirname(__file__), "..", "tests/fixtures/config.test.json")))
        self.assertEqual(c["strategies"]["rank"]["low_traffic"]["max_total_n60"], 0)

if __name__ == "__main__":
    unittest.main()
