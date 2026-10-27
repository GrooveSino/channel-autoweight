import unittest
from autoweight import toggle as T


def R(ok=True, ttft=1.0, total=2.0):
    return dict(ok=ok, ttft=ttft, total=total)


class TestV191(unittest.TestCase):
    def test_small_log_sample_ignored(self):
        b = T.baseline([23], {23: [R(ttft=17.4, total=17.4)]}, {23: {"60m": {"lt50": 3, "lt_n": 2, "p50": 5, "n": 7}}})
        self.assertEqual(b, dict(ttft=17.4, total=17.4))

    def test_enough_log_sample_used(self):
        b = T.baseline([2], {2: [R(ttft=8, total=12)]}, {2: {"60m": {"lt50": 6, "lt_n": 30, "p50": 10, "n": 40}}}, min_log_n=10)
        self.assertEqual(b, dict(ttft=6, total=10))

    def test_legacy_feats_without_counts(self):
        b = T.baseline([2], {}, {2: {"60m": {"lt50": 6, "p50": 20}}})
        self.assertEqual(b, dict(ttft=6, total=20))

    def test_min_log_n_default(self):
        self.assertEqual(T.P({})["min_log_n"], 10)


if __name__ == "__main__":
    unittest.main()
