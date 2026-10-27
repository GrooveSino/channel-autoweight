import os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "tools"))
import error_timeline as et

class T(unittest.TestCase):
    def test_classify(self):
        self.assertEqual(et.classify("status_code=524, bad"), "timeout")
        self.assertEqual(et.classify("status_code=502, x"), "upstream")
        self.assertEqual(et.classify("status_code=429"), "ratelimit")
        self.assertEqual(et.classify("status_code=400"), "client")
        self.assertEqual(et.classify("boom"), "other")
    def test_timeline_streak(self):
        rows = [dict(ch=1, t=10, type=5, ut=100, cls="timeout"), dict(ch=1, t=20, type=2, ut=5),
                dict(ch=1, t=310, type=2, ut=5),
                dict(ch=1, t=610, type=5, ut=90, cls="timeout"), dict(ch=1, t=1210, type=5, ut=1, cls="upstream")]
        d = et.timeline(rows, 0, 1500)[1]
        self.assertEqual(d["b"][0], [2, 1]); self.assertEqual(d["b"][3], [0, 0])
        self.assertEqual(d["streak"], 2)  # buckets 1200 and 600 have errors (900 has no traffic, skipped), 300 is clean
        self.assertEqual(d["err_buckets"], 3); self.assertEqual(d["n_buckets"], 4)
        self.assertEqual(d["wasted_s"], 191); self.assertEqual(d["cls"], {"timeout": 2, "upstream": 1})

if __name__ == "__main__": unittest.main()
