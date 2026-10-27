"""Offline backtest on the synthetic snapshot: the rank strategy should beat uniform weights."""
import json, os, sys, unittest
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT); sys.path.insert(0, os.path.join(ROOT, "examples"))
import backtest, make_synthetic_snapshot as mk

class T(unittest.TestCase):
    def test_rank_beats_equal(self):
        cfg = json.load(open(os.path.join(ROOT, "config.example.json")))
        cfg["rounds_file"] = "nonexistent/rounds.jsonl"
        d = mk.build()
        res = backtest.run(cfg, d["chans"], d["rows"], d["end"], 24, quiet=True)
        self.assertLess(res["rank"]["p90"], res["equal"]["p90"])
        self.assertLess(res["rank"]["err_rate"], res["equal"]["err_rate"])

if __name__ == "__main__":
    unittest.main()
