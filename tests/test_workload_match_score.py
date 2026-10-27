"""v1.52 workload-match ratio scoring"""
import os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import features, decide

def rows():
    r = []
    # channel 1: short jobs only (ct 100, 10s, twice as slow as the bucket baseline); channel 2: long jobs (ct 1000, 40s), fastest in its bucket
    for k in range(6):
        r.append(dict(ch=1, t=100 + k, type=2, ut=10, ct=100))
        r.append(dict(ch=3, t=100 + k, type=2, ut=5, ct=100))
        r.append(dict(ch=2, t=100 + k, type=2, ut=40, ct=1000))
        r.append(dict(ch=3, t=100 + k, type=2, ut=80, ct=1000))
    return r

class T(unittest.TestCase):
    def test_ratios(self):
        mr = features.matched_ratios(rows(), 0, 1000)
        self.assertLess(mr[2][1], mr[1][1])        # long-job channel 2 is faster for the same workload
        self.assertEqual(set(features.matched_ratios(rows(), 0, 1000, min_n=7)), {3})
        self.assertEqual(features.matched_ratios(rows(), 500, 1000), {})

    def test_errors_and_empty_excluded(self):
        r = rows() + [dict(ch=1, t=101, type=5, ut=99, ct=0), dict(ch=1, t=101, type=2, ut=99, ct=0)]
        self.assertEqual(features.matched_ratios(r, 0, 1000)[1][0], 6)

    def test_score_window(self):
        s = dict(min_n_window=5, a_p50=0.3, b_p90=0.3, c_ttft=1.0, t_ref=20.0, err_exp=2,
                 workload_match=dict(enabled=True, mix=1.0, min_n=5))
        w = dict(n=10, p50=10, p90=20, err_rate=0, slow_rate=0, mr_n=10, mr50=0.5, mr90=0.5)
        fast = decide.score_window(w, 5, s, (20, 40))
        s2 = dict(s, workload_match=dict(enabled=False))
        self.assertAlmostEqual(fast, decide.score_window(w, 5, s2, (20, 40)))   # 0.5*20=10, 0.5*40=20 equivalent
        w2 = dict(w, mr50=2.0, mr90=2.0)
        self.assertLess(decide.score_window(w2, 5, s, (20, 40)), fast)
        self.assertAlmostEqual(decide.score_window(dict(w2, mr_n=3), 5, s, (20, 40)), fast)  # too few samples falls back to raw

if __name__ == "__main__": unittest.main()
