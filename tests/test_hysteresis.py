"""v1.15 hysteresis unit tests"""
import os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight.decide import hold_top

class T(unittest.TestCase):
    def test_off(self):
        self.assertEqual(hold_top([1, 2], {1: 1.0, 2: 0.99}, {2: 12, 1: 3}, 0), ([1, 2], None))
    def test_hold_within(self):
        self.assertEqual(hold_top([1, 2, 3], {1: 1.0, 2: 0.6, 3: 0.97}, {3: 12, 1: 6}, 0.05), ([3, 1, 2], 3))
    def test_no_hold_outside(self):
        self.assertEqual(hold_top([1, 3], {1: 1.0, 3: 0.9}, {3: 12, 1: 6}, 0.05), ([1, 3], None))
    def test_prev_top_already_first(self):
        self.assertEqual(hold_top([3, 1], {1: 0.9, 3: 1.0}, {3: 12}, 0.05), ([3, 1], None))
    def test_prev_missing_channel(self):
        self.assertEqual(hold_top([1, 2], {1: 1.0, 2: 0.99}, {9: 12}, 0.05), ([1, 2], None))

if __name__ == "__main__": unittest.main()
