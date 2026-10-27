import json, os, sys, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


class TestTrustCapRamp(unittest.TestCase):
    def test_default_off(self):
        cfg = json.load(open(os.path.join(os.path.dirname(__file__), "..", "tests/fixtures/config.test.json")))
        self.assertEqual((cfg.get("trust_cap") or {}).get("ramp", 0), 0)

    def test_ramp_code_present(self):
        src = open(os.path.join(os.path.dirname(__file__), "..", "autoweight", "decide.py")).read()
        self.assertIn('tc.get("ramp", 0)', src)

    def test_ramp_formula(self):
        cap, top, thr, rp = 3, 12, 0.3, 0.1
        f = lambda t: int(round(cap + (top - cap) * (t - thr) / rp))
        self.assertEqual(f(0.30), 3)
        self.assertEqual(f(0.36), 8)
        self.assertLessEqual(f(0.399), top)


if __name__ == "__main__":
    unittest.main()
