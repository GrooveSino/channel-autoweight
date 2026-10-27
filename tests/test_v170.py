import os, sys
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "tools"))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
import unittest
import site_compare as sc

def _body(blocked, reasons, cold=False):
    return {"rows": [{"channel": {"id": 2, "weight": 10}, "suggested": 10, "trust": 0.5, "features": {"60m": {"n": 9}}}],
            "quality": {"cold": cold, "started": "x"}, "blocked": blocked, "reasons": reasons}

class T(unittest.TestCase):

    def test_shadow_blocked_without_reason_is_valid(self):
        r = sc.parse_run(1, "shadow", False, _body(True, []))
        assert r["blocked"] is False and r["rows"][2]["suggested"] == 10

    def test_blocked_with_reason_is_invalid(self):
        r = sc.parse_run(1, "shadow", False, _body(True, ["insufficient collector coverage; current weights retained"]))
        assert r["blocked"] is True

    def test_cold_flag(self):
        assert sc.parse_run(1, "shadow", False, _body(False, [], cold=True))["cold"] is True

if __name__ == "__main__":
    unittest.main()
