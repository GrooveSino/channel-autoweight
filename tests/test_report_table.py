import sys, os, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import report_table as rt

R = {"ts": "t", "applied": True,
     "channels": [{"id": 2, "name": "A", "weight": 9, "priority": 10}],
     "old": {"2": 9},
     "decision": {"target": {"2": 12}, "scores": {"2": {"ttft": 2.5, "streaming": True}}},
     "features": {"2": {"60m": {"n": 10, "p50": 5, "p90": 9, "slow_rate": 0.1, "err_rate": 0.0}}},
     "watch": {"14": {"info": {"name": "S", "status": 2, "weight": 1, "priority": 10},
                      "probes": [{"ok": False, "status": 403}], "f60": {"n": 0}}}}

class T(unittest.TestCase):
    def test_render(self):
        out = rt.render(R)
        self.assertIn("| A #2 | 10 | 9 | 12 | enabled | 2.50 | yes | 10 | 5/9 | 10.0% | 0.0% |", out)
        self.assertIn("S #14", out); self.assertIn("probe failed(403)", out); self.assertIn("disabled(status=2)", out)

    def test_pick(self):
        rows = [{"applied": True, "n": 1}, {"applied": False, "n": 2}]
        self.assertEqual(rt.pick_round(rows)["n"], 1)
        self.assertEqual(rt.pick_round(rows, False)["n"], 2)

if __name__ == "__main__":
    unittest.main()
