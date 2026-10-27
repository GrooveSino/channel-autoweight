import json, os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, HERE)
from autoweight import decide

CFG = json.load(open(os.path.join(HERE, "tests/fixtures/config.test.json")))

class T(unittest.TestCase):
    def test_config_floor_zero(self):
        self.assertEqual(CFG["w_min"], 0)
        self.assertEqual(CFG["strategies"]["rank"]["low_w"], 0)

    def test_trust_cap_kept(self):
        self.assertEqual(CFG["trust_cap"]["cap"], 3)

    def test_override_accepts_zero(self):
        ch = [{"id": 2}, {"id": 20}]
        self.assertEqual(decide.parse_override("2=12,20=0", ch, CFG), {2: 12, 20: 0})
        with self.assertRaises(ValueError): decide.parse_override("2=-1", ch, CFG)

    def test_override_rejects_disabled(self):
        with self.assertRaises(ValueError): decide.parse_override("5=0", [{"id": 2}], CFG)

if __name__ == "__main__": unittest.main()
