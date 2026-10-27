"""v1.76: small_sample cap."""
import copy, json, os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
CFG = json.load(open(os.path.join(os.path.dirname(__file__), "..", "tests/fixtures/config.test.json")))

class T(unittest.TestCase):
    def test_config_default_off(self):
        self.assertIn("small_sample", CFG)
        self.assertFalse(CFG["small_sample"]["enabled"])
    def test_code_present(self):
        src = open(os.path.join(os.path.dirname(__file__), "..", "autoweight", "decide.py")).read()
        self.assertIn('cfg.get("small_sample")', src)

if __name__ == "__main__":
    unittest.main()
