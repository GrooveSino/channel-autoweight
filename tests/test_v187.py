import os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
import report_table as rt


def _round():
    return {"old": {"21": 2}, "decision": {"target": {"21": 1}, "scores": {}},
            "channels": [{"id": 21, "name": "[ACME]-GPT-A", "priority": 10}],
            "watch": {"21": {"info": {"name": "[ACME]-GPT-A", "status": 1, "priority": 10, "weight": 2}, "probes": [], "f60": {}},
                      "5": {"info": {"name": "[INITECH]-GPT", "status": 2, "priority": 10, "weight": 1}, "probes": [], "f60": {}}}}


class T(unittest.TestCase):
    def test_enabled_watch_not_duplicated(self):
        self.assertEqual(rt.render(_round()).count("#21 |"), 1)

    def test_disabled_watch_still_listed(self):
        out = rt.render(_round())
        self.assertIn("#5 |", out); self.assertIn("disabled(status=2)", out)


if __name__ == "__main__":
    unittest.main()
