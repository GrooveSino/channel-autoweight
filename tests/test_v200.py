import unittest
from autoweight import toggle as T


def R(ok=True, ttft=1.0, total=2.0, streaming=True):
    return dict(ok=ok, ttft=ttft, total=total, streaming=streaming)


class TestV200(unittest.TestCase):
    def test_buffered_probe_uses_small_log(self):
        b = T.baseline([20], {20: [R(ttft=7.59, total=7.7, streaming=False)]},
                       {20: {"60m": {"lt50": 2.65, "lt_n": 5, "p50": 7, "n": 28}}}, skip_buffered_ttft=True)
        self.assertEqual(b, dict(ttft=2.65, total=7))

    def test_buffered_probe_falls_back_to_upstream(self):
        b = T.baseline([2], {2: [R(ttft=7.24, total=7.24, streaming=False)]},
                       {2: {"60m": {"lt50": 3.77, "lt_n": 1, "p50": 13, "n": 6}}}, skip_buffered_ttft=True,
                       up_tests={2: [R(ttft=3.08), R(ttft=3.02)]})
        self.assertEqual(b, dict(ttft=3.05, total=7.24))

    def test_buffered_no_alternative_skips_channel(self):
        b = T.baseline([2, 23], {2: [R(ttft=7.24, total=7.24, streaming=False)], 23: [R(ttft=3.1, total=4.4)]},
                       {}, skip_buffered_ttft=True)
        self.assertEqual(b, dict(ttft=3.1, total=4.4))

    def test_off_keeps_legacy(self):
        b = T.baseline([2], {2: [R(ttft=7.24, total=7.24, streaming=False)]},
                       {2: {"60m": {"lt50": 3.77, "lt_n": 1}}})
        self.assertEqual(b["ttft"], 7.24)

    def test_streaming_probe_unchanged(self):
        b = T.baseline([22], {22: [R(ttft=4.8, total=8.1)]}, {22: {"60m": {"lt50": 4.5, "lt_n": 9}}}, skip_buffered_ttft=True)
        self.assertEqual(b["ttft"], 4.8)

    def test_plan_rejects_case_0826(self):
        cfg = {"toggle": {"skip_buffered_ttft": True}}
        cur = dict(scores={2: 0.43, 5: 0.74, 20: 0.46, 22: 0.53, 23: 0.70},
                   probes={2: [R(ttft=7.24, total=7.24, streaming=False)], 20: [R(ttft=7.59, total=7.7, streaming=False)]},
                   feats={2: {"60m": {"lt50": 3.77, "lt_n": 1, "p50": 13, "n": 6}}, 20: {"60m": {"lt50": 2.65, "lt_n": 5, "p50": 7, "n": 28}}})
        out = T.plan(cfg, [2, 5, 20, 22, 23], {21: [R(ttft=4.81, total=5.13), R(ttft=4.09, total=4.3), R(ttft=5.31, total=5.79)]},
                     [], [cur], bottom_up_tests={2: [R(ttft=3.08, total=3.57)], 20: [R(ttft=3.18, total=4.14)]})
        self.assertFalse(out["disabled"]["21"]["pass"])
        self.assertEqual(out["disabled"]["21"]["streak"], 0)
        self.assertAlmostEqual(out["base"]["ttft"], 2.65)


if __name__ == "__main__":
    unittest.main()
