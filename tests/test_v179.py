import os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight.features import window
from autoweight.decide import score_window

S = dict(min_n_window=3, t_ref=20.0, a_p50=0.3, b_p90=0.3, c_ttft=1.0)

def rows(now):
    r = [dict(t=now - 10 - i, type=2, ut=10.0, ct=300) for i in range(8)]
    r += [dict(t=now - 5, type=5, ut=100.0, sc=524), dict(t=now - 6, type=5, ut=1.0, sc=503)]
    return r

class T(unittest.TestCase):
    def test_window_counts_timeouts(self):
        now = 100000; w = window(rows(now), now, 10, 60)
        self.assertEqual(w["err_timeout"], 1)
        self.assertAlmostEqual(w["slow_rate"], 0.0)
        self.assertAlmostEqual(w["slow_rate_t"], round(1 / 9, 4))

    def test_no_sc_gives_none(self):
        now = 100000; r = [dict(t=now - 5, type=5, ut=100.0)] + [dict(t=now - 9, type=2, ut=5.0)] * 3
        w = window(r, now, 10, 60)
        self.assertIsNone(w["err_timeout"]); self.assertIsNone(w["slow_rate_t"])

    def test_score_option(self):
        now = 100000; w = window(rows(now), now, 10, 60)
        off = score_window(w, 5.0, S); on = score_window(w, 5.0, dict(S, timeout_slow=True))
        self.assertLess(on, off)
        self.assertAlmostEqual(on / off, 1 - round(1 / 9, 4), places=4)

if __name__ == "__main__":
    unittest.main()
