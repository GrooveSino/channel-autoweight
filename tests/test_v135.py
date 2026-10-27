import os, sys, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight import decide

class TestConfirmRiseOnly(unittest.TestCase):
    def test_only_small_sample_channels_confirmed(self):
        hist = [{2: 1, 5: 3}, {2: 1, 5: 3}, {2: 9, 5: 12}]
        prev = {2: 1, 5: 3}
        out = decide.confirm_rise(hist, prev, 3, only={2})
        self.assertEqual(out[2], 1)      # small-sample channel: increase not confirmed
        self.assertEqual(out[5], 12)     # high-traffic channel: rises immediately
    def test_only_none_same_as_v134(self):
        hist = [{2: 1}, {2: 3}, {2: 9}]
        self.assertEqual(decide.confirm_rise(hist, {2: 1}, 3, None), decide.confirm_rise(hist, {2: 1}, 3))
    def test_drop_always_immediate(self):
        out = decide.confirm_rise([{2: 12}, {2: 12}, {2: 1}], {2: 12}, 3, only={2})
        self.assertEqual(out[2], 1)

if __name__ == "__main__":
    unittest.main()
