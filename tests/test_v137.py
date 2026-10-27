import unittest, evaluate_shadow as es

def rec(ts, sug, sc, n10, p90):
    ids = list(sug)
    return dict(ts=ts, suggest=dict(sug), old=dict(sug), names={i: i for i in ids},
                scores={i: {"score": sc[i]} for i in ids}, trust={i: {"trust": 1.0} for i in ids},
                features={i: {"10m": {"n": n10[i], "err": 0, "p50": 5, "p90": p90[i]}} for i in ids})

class T(unittest.TestCase):
    def setUp(self):
        # #5 has the top score but is truncated to 6 by its soft cap, #2 gets 12; afterwards #5 is really the best
        # since v1.65 windows are de-overlapped and only 1 window counts, so n is doubled
        sug = {"2": 12, "5": 6}; sc = {"2": 0.5, "5": 0.6}; n = {"2": 20, "5": 60}; p90 = {"2": 20, "5": 10}
        self.recs = [rec(f"2026-10-25T09:{m:02d}:00+08:00", sug, sc, n, p90) for m in (0, 5, 10)]
    def test_score_verdict_handles_cap(self):
        s = es.summarize(self.recs, since_min=45)
        self.assertTrue(s["verdict"].startswith("mismatch"))
        self.assertEqual(s["rank_score"][0], 5)
        self.assertEqual(s["verdict_score"], "match")
    def test_share(self):
        s = es.summarize(self.recs, since_min=45)
        self.assertAlmostEqual(s["channels"][5]["share_real"], 0.75)
        self.assertAlmostEqual(s["channels"][5]["share_weight"], 0.333, places=3)
        self.assertIn("Real share", es.render(s))

if __name__ == "__main__": unittest.main()
