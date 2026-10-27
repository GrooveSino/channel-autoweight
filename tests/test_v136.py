import copy, json, os, sys, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight import decide
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

def cfg(on=True):
    c = json.load(open(os.path.join(HERE, "tests/fixtures/config.test.json")))
    c = copy.deepcopy(c); c["score"]["slow_burst"]["enabled"] = on
    return c

def F(nn10, np10, np60=15.0, nn60=20):
    return {"60m": {"nn": nn60, "np50": np60}, "10m": {"nn": nn10, "np50": np10}}

class TestSlowBurst(unittest.TestCase):
    def test_off_is_neutral(self):
        fs = {20: F(10, 90), 2: F(5, 15), 5: F(5, 12)}
        self.assertEqual(decide.slow_burst_factor(fs, 20, cfg(False)), (1.0, None))
    def test_burst_penalized(self):
        fs = {20: F(10, 85.66), 2: F(1, 11, 19.8), 5: F(1, 8, 11.74)}
        f, why = decide.slow_burst_factor(fs, 20, cfg())
        self.assertLess(f, 0.6); self.assertIsNotNone(why)
    def test_small_sample_ignored(self):
        fs = {20: F(3, 200), 2: F(5, 15), 5: F(5, 12)}
        self.assertEqual(decide.slow_burst_factor(fs, 20, cfg())[0], 1.0)
    def test_normal_not_penalized(self):
        fs = {20: F(10, 30), 2: F(5, 15, 16), 5: F(5, 12, 16)}
        self.assertEqual(decide.slow_burst_factor(fs, 20, cfg())[0], 1.0)
    def test_floor(self):
        fs = {20: F(10, 10000), 2: F(5, 15), 5: F(5, 12)}
        self.assertAlmostEqual(decide.slow_burst_factor(fs, 20, cfg())[0], cfg()["score"]["slow_burst"]["floor"])

if __name__ == "__main__":
    unittest.main()
