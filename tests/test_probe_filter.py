import os, sys, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight import features

CFG = {"slow_s": 60, "windows_min": [60, 10], "probe_token_ids": [900], "score": {"exclude_probe_rows": True}}

class ProbeFilter(unittest.TestCase):
    def test_probe_rows_dropped(self):
        rows = [dict(ch=2, t=990, type=5, ut=0, ct=0, tok=900),   # failed probe
                dict(ch=2, t=995, type=2, ut=3, ct=10, tok=7)]   # real user
        f = features.build(CFG, [{"id": 2}], rows, 1000)
        self.assertEqual(f[2]["60m"]["n"], 1)
        self.assertEqual(f[2]["60m"]["err"], 0)

    def test_rows_without_tok_kept(self):
        rows = [dict(ch=2, t=990, type=2, ut=3, ct=0)]
        self.assertEqual(len(features.user_rows(CFG, rows)), 1)

    def test_empty_exclude_is_noop(self):
        rows = [dict(ch=2, t=990, type=5, ut=0, ct=0, tok=900)]
        self.assertEqual(features.user_rows({"probe_token_ids": []}, rows), rows)

    def test_flag_off_keeps_probe_rows(self):
        rows = [dict(ch=2, t=990, type=5, ut=0, ct=0, tok=900)]
        cfg = dict(CFG, score={"exclude_probe_rows": False})
        self.assertEqual(features.build(cfg, [{"id": 2}], rows, 1000)[2]["60m"]["err"], 1)

if __name__ == "__main__":
    unittest.main()
