import os, sys, unittest, json, subprocess
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, HERE)
from autoweight import decide

class T(unittest.TestCase):
    def test_down_fast_up_slow(self):
        out = decide.smooth_scores({1: 0.2, 2: 0.8}, {1: 0.5, 2: 0.5}, {"alpha_up": 0.3, "alpha_down": 1.0})
        self.assertEqual(out[1], 0.2)            # worse follows immediately
        self.assertAlmostEqual(out[2], 0.59)     # better only rises 30%

    def test_new_channel_raw(self):
        self.assertEqual(decide.smooth_scores({3: 0.4}, {1: 0.5}, {"alpha_up": 0.3}), {3: 0.4})

    def test_disabled_config_no_change(self):
        cfg = json.load(open(os.path.join(HERE, "tests/fixtures/config.test.json")))
        self.assertFalse(cfg["score_smooth"]["enabled"])


if __name__ == "__main__":
    unittest.main()
