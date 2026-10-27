"""evaluate.py unit tests: formal round pairing, effective weights, traffic-weighted metrics."""
import os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
import evaluate

def R(ts, old, target, applied, feats=None, scores=None):
    return dict(ts=ts, old=old, applied=applied, features=feats or {},
                decision=dict(target=target, scores=scores or {}))

def F(n, p50, p90, err=0.0):
    return {"60m": dict(n=n, p50=p50, p90=p90, err_rate=err), "10m": {}}

class T(unittest.TestCase):
    def test_effective_weights(self):
        self.assertEqual(evaluate.effective_weights(R("x", {"2": 1}, {"2": 9}, True)), {"2": 9})
        self.assertEqual(evaluate.effective_weights(R("x", {"2": 1}, {"2": 9}, False)), {"2": 1})

    def test_pairs_dedupe_and_gap(self):
        rs = [R("2026-10-24T01:18:06+08:00", {}, {}, False), R("2026-10-24T01:18:33+08:00", {}, {}, True),
              R("2026-10-24T02:09:58+08:00", {}, {}, False), R("2026-10-24T02:20:00+08:00", {}, {}, True)]
        pairs = evaluate.pick_pairs(rs, 40)
        self.assertEqual(len(pairs), 1)
        self.assertEqual(pairs[0][0]["ts"], "2026-10-24T01:18:33+08:00")
        self.assertEqual(pairs[0][1]["ts"], "2026-10-24T02:20:00+08:00")

    def test_traffic_weighted(self):
        a = R("2026-10-24T01:00:00+08:00", {}, {"2": 3, "20": 1}, True)
        b = R("2026-10-24T02:00:00+08:00", {}, {}, False,
              feats={"2": F(30, 2, 6), "20": F(10, 10, 20, 0.1)}, scores={"2": {"score": 0.8}, "20": 0.5})
        r = evaluate.evaluate_pair(a, b)
        self.assertEqual(r["n_total"], 40)
        self.assertAlmostEqual(r["w_p50"], 0.75 * 2 + 0.25 * 10)
        self.assertAlmostEqual(r["w_err"], 0.025)
        self.assertEqual(r["best_after"], "2")
        self.assertEqual(r["best_traffic_share"], 0.75)

if __name__ == "__main__": unittest.main()
