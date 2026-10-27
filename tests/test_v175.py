"""v1.75: log_ttft.adapt_full_n adaptive mix by log first-token sample size."""
import json, os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight.decide import blend_log_ttft

CFG = json.load(open(os.path.join(os.path.dirname(__file__), "..", "tests/fixtures/config.test.json")))

def S(fn): return {"log_ttft": {"enabled": True, "mix": 0.5, "min_n": 10, "adapt_full_n": fn}}

class T(unittest.TestCase):
    def test_off_identity(self):
        self.assertAlmostEqual(blend_log_ttft(3.0, 3.0, {"lt_n": 100, "lt50": 11.0}, S(0)), 7.0)
    def test_full_trust_log_when_many(self):
        self.assertAlmostEqual(blend_log_ttft(3.0, 3.0, {"lt_n": 100, "lt50": 11.0}, S(60)), 11.0)
    def test_partial(self):
        v = blend_log_ttft(3.0, 3.0, {"lt_n": 35, "lt50": 11.0}, S(60))
        self.assertAlmostEqual(v, 3.0 * 0.25 + 11.0 * 0.75)
    def test_below_min_n_untouched(self):
        self.assertEqual(blend_log_ttft(3.0, 3.0, {"lt_n": 5, "lt50": 11.0}, S(60)), 3.0)
    def test_config_key_present(self):
        self.assertIn("adapt_full_n", CFG["score"]["log_ttft"])

if __name__ == "__main__":
    unittest.main()
