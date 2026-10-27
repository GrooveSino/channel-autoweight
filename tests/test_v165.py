"""v1.65: de-overlapped real samples after shadow review + low-traffic reference comparison"""
import os, sys, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import evaluate_shadow as ES

def rec(ts, chans):
    # chans: {id: (suggest, n10, p50, p90)}
    return {"ts": ts, "suggest": {str(i): v[0] for i, v in chans.items()},
            "scores": {str(i): {"score": 0.5} for i in chans},
            "trust": {str(i): {"trust": 0.5} for i in chans},
            "old": {str(i): 3 for i in chans},
            "features": {str(i): {"10m": {"n": v[1], "err": 0, "p50": v[2], "p90": v[3]}} for i, v in chans.items()}}

def series(n_pts, chans, start_min=0):
    return [rec(f"2026-10-26T05:{start_min + 5*k:02d}:00+08:00", chans) for k in range(n_pts)]

class T(unittest.TestCase):
    def test_no_double_count(self):
        # 7 points (every 5 min), 4 requests per 10m window; non-overlapping only 3 windows = 12 requests (previously 6x4=24)
        s = ES.summarize(series(7, {2: (12, 4, 3, 5)}), 45)
        self.assertEqual(s["channels"][2]["after_n"], 12)

    def test_low_traffic_reference(self):
        s = ES.summarize(series(7, {2: (12, 4, 3, 5), 20: (3, 4, 9, 10)}), 45)
        self.assertTrue(s["verdict"].startswith("insufficient samples"))
        self.assertEqual(s["rank_low"], [2, 20])
        self.assertEqual(s["verdict_low"], "low-traffic reference: match")

    def test_low_traffic_disagree(self):
        s = ES.summarize(series(7, {2: (12, 4, 9, 10), 20: (3, 4, 3, 5)}), 45)
        self.assertIn("mismatch", s["verdict_low"])
        self.assertIn("low-traffic reference", ES.render(s))

    def test_enough_samples_no_low(self):
        s = ES.summarize(series(7, {2: (12, 10, 3, 5), 20: (3, 10, 9, 10)}), 45)
        self.assertEqual(s["verdict"], "match")
        self.assertIsNone(s["verdict_low"])

if __name__ == "__main__": unittest.main()
