import unittest
from autoweight import decide, features

class T(unittest.TestCase):
    S = {"log_ttft": {"enabled": True, "mix": 0.5, "min_n": 10}}
    def test_blend(self):
        self.assertAlmostEqual(decide.blend_log_ttft(4.0, 4.0, {"lt_n": 20, "lt50": 10.0}, self.S), 7.0)
    def test_small_sample_or_off(self):
        self.assertEqual(decide.blend_log_ttft(4.0, 4.0, {"lt_n": 5, "lt50": 10.0}, self.S), 4.0)
        self.assertEqual(decide.blend_log_ttft(4.0, 4.0, {"lt_n": 50, "lt50": 10.0}, {"log_ttft": {"enabled": False}}), 4.0)
    def test_missing_probe_uses_log(self):
        self.assertEqual(decide.blend_log_ttft(30.0, None, {"lt_n": 20, "lt50": 6.0}, self.S), 6.0)
    def test_window_lt(self):
        rows = [dict(ch=1, t=100, type=2, ut=5, ct=100, frt=2.0), dict(ch=1, t=100, type=2, ut=5, ct=100),
                dict(ch=1, t=100, type=5, ut=0, ct=0, frt=9.0)]
        w = features.window(rows, 101, 60, 60)
        self.assertEqual(w["lt_n"], 1); self.assertEqual(w["lt50"], 2.0)

if __name__ == "__main__": unittest.main()
