import os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "tools"))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
import site_compare as sc

def S(t, sug, trust=None):
    return dict(t=t, op="shadow", applied=False, started=None, cold=False, blocked=False,
                rows={i: dict(suggested=w, trust=(trust or {}).get(i, 0.5), score=0.3, n60=50, ttft=5, weight=1) for i, w in sug.items()})

def P(ts, sug, trust=None):
    from datetime import datetime
    return (datetime.fromisoformat(ts).timestamp(), dict(ts=ts, suggest={str(k): v for k, v in sug.items()},
            trust={str(k): {"trust": v} for k, v in (trust or {}).items()}, scores={str(k): {"n60": 100} for k in sug}))

class TestSiteCompare(unittest.TestCase):
    def test_match_tolerance(self):
        p = [P("2026-10-25T19:40:00+08:00", {2: 3})]
        t0 = p[0][0]
        self.assertEqual(len(sc.match([S(t0 + 55, {2: 3})], p)), 1)
        self.assertEqual(len(sc.match([S(t0 + 600, {2: 3})], p)), 0)

    def test_compare_agreement(self):
        p = [P("2026-10-25T19:40:00+08:00", {2: 3, 22: 12}, {2: 0.9}), P("2026-10-25T19:45:00+08:00", {2: 10, 22: 12})]
        s = [S(p[0][0] + 50, {2: 3, 22: 12}, {2: 0.4}), S(p[1][0] + 50, {2: 3, 22: 12})]
        r = sc.compare(sc.match(s, p))
        self.assertEqual(r["n"], 2); self.assertEqual(r["all_agree"], 0.5); self.assertEqual(r["top_agree"], 1.0)
        self.assertEqual(r["per_channel"][22], 1.0); self.assertEqual(r["per_channel"][2], 0.5)
        self.assertAlmostEqual(r["trust_diff"][2], -0.5, places=3)  # only the first pair has prototype trust: 0.4-0.9

    def test_parse_run(self):
        body = {"rows": [{"channel": {"id": 2, "weight": 10}, "suggested": 3, "trust": 0.4, "score": 0.3,
                          "features": {"60m": {"n": 196}}, "ttft": 9.6}], "quality": {"started": "x"}}
        r = sc.parse_run(1, "shadow", False, body)
        self.assertEqual(r["rows"][2]["n60"], 196); self.assertEqual(r["rows"][2]["suggested"], 3)

if __name__ == "__main__":
    unittest.main()
