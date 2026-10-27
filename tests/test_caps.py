import os, sys, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from evaluate import cap_review


def rnd(applied, notes, e5, e2):
    return dict(applied=applied, decision={"notes": notes},
                features={"5": {"60m": {"empty_rate": e5, "err_rate": 0.0, "ctok_p50": 100}},
                          "2": {"60m": {"empty_rate": e2, "err_rate": 0.1, "ctok_p50": 300}}})


class TestCaps(unittest.TestCase):
    def test_counts_and_means(self):
        rs = [rnd(True, ["#5 soft cap 6 (x), target 12.0 truncated"], 0.02, 0.04),
              rnd(True, [], 0.0, 0.02),
              rnd(False, ["#5 soft cap 6"], 0.5, 0.5)]  # dry runs do not count
        c = cap_review(rs, [5])["5"]
        self.assertEqual(c["rounds"], 2)
        self.assertEqual(c["truncated"], 1)
        self.assertEqual(c["own"]["empty"], 0.01)
        self.assertEqual(c["peers"]["empty"], 0.03)
        self.assertEqual(c["peers"]["ctok"], 300)

    def test_absent_channel(self):
        c = cap_review([rnd(True, [], 0, 0)], [14])["14"]
        self.assertEqual(c["rounds"], 0)
        self.assertIsNone(c["own"]["empty"])


if __name__ == "__main__":
    unittest.main()
