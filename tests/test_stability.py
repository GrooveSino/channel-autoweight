import os, sys, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from evaluate import stability


def R(ts, applied, target, order):
    return dict(ts=ts, applied=applied, decision=dict(target=target, order=order))


class TestStability(unittest.TestCase):
    def test_diff_and_flip(self):
        rs = [R("2026-10-24T16:09:21", False, {"20": 12, "2": 10}, [20, 2]),
              R("2026-10-24T16:09:46", True, {"20": 9, "2": 9}, [20, 2])]
        st = stability(rs)
        self.assertEqual(len(st), 1)
        self.assertEqual(st[0]["max_diff"], 3)
        self.assertFalse(st[0]["order_flip"])

    def test_flip(self):
        rs = [R("2026-10-24T14:01:00", False, {"2": 12, "5": 3}, [2, 5]),
              R("2026-10-24T14:02:00", True, {"2": 3, "5": 12}, [5, 2])]
        self.assertTrue(stability(rs)[0]["order_flip"])

    def test_no_dry_or_no_apply(self):
        self.assertEqual(stability([R("2026-10-24T13:00:00", True, {"2": 1}, [2])]), [])
        self.assertEqual(stability([R("2026-10-24T13:00:00", False, {"2": 1}, [2])]), [])


if __name__ == "__main__":
    unittest.main()
