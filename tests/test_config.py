"""Config loading: environment overrides and config.example.json consistency."""
import json, os, sys, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight import config

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

class T(unittest.TestCase):
    def test_env_overrides(self):
        cfg = {"ssh_host": "a", "probe": {"base_url": "x"}}
        config.apply_env(cfg, {"AUTOWEIGHT_SSH_HOST": "h", "AUTOWEIGHT_PG_CONTAINER": "pg", "AUTOWEIGHT_PROBE_BASE_URL": "https://g.example.com"})
        self.assertEqual(cfg["ssh_host"], "h"); self.assertEqual(cfg["pg_container"], "pg")
        self.assertEqual(cfg["probe"]["base_url"], "https://g.example.com")

    def test_empty_env_keeps_file_values(self):
        cfg = config.apply_env({"ssh_host": "a"}, {"AUTOWEIGHT_SSH_HOST": ""})
        self.assertEqual(cfg["ssh_host"], "a")

    def test_example_has_same_sections_as_fixture(self):
        ex = json.load(open(os.path.join(ROOT, "config.example.json")))
        fx = json.load(open(os.path.join(ROOT, "tests", "fixtures", "config.test.json")))
        self.assertEqual(set(ex), set(fx))
        self.assertEqual({k: v for k, v in ex["soft_caps"].items() if not k.startswith("_")}, {}); self.assertEqual(ex["watch"]["ids"], [])

if __name__ == "__main__":
    unittest.main()
