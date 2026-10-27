import unittest, sys, os
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight import decide

class TestBlockingDrift(unittest.TestCase):
    def test_no_final_blocks_all(self):
        d = {21: (12, 11)}
        self.assertEqual(decide.blocking_drift(d, None), d)

    def test_overridden_channel_not_blocking(self):
        # #21 system 12->11, but the manual final value is 3: does not block
        self.assertEqual(decide.blocking_drift({21: (12, 11)}, {21: 3, 2: 10, 20: 3, 22: 12}), {})

    def test_followed_channel_blocks(self):
        # #20 final value follows the system expectation 3, recomputed to 10: must block
        d = {20: (3, 10), 21: (12, 11)}
        self.assertEqual(decide.blocking_drift(d, {21: 3, 2: 10, 20: 3, 22: 12}), {20: (3, 10)})

    def test_channel_missing_from_final_blocks(self):
        self.assertEqual(decide.blocking_drift({5: (1, 2)}, {21: 3}), {5: (1, 2)})

if __name__ == "__main__":
    unittest.main()
