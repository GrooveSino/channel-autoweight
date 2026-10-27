import unittest, sys, os
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight import toggle as TG

def F(n, n_user=None):
    f = {"60m": {"n": n, "err_rate": 0.0}}
    if n_user is not None: f["mix_60m"] = {"n_user": n_user, "n_probe": n - n_user}
    return f

class TestV196(unittest.TestCase):
    def test_n60_of_real_uses_n_user(self):
        self.assertEqual(TG.n60_of(F(30, 4)), 4)
        self.assertEqual(TG.n60_of(F(30, 4), real=False), 30)

    def test_n60_of_fallback_old_records(self):
        self.assertEqual(TG.n60_of(F(25)), 25)
        self.assertIsNone(TG.n60_of({}))

    def test_probe_only_traffic_does_not_count_for_last_streak(self):
        # 3 channels with 30 requests each in 1h, but only 2 real ones: below min_n60=20 on the real-traffic basis, so no "ranked last"
        rounds = []
        for i in range(6):
            rounds.append(dict(ts=f"2026-10-27T0{i}:00:00+08:00", dry_run=False, old={"2": 3, "5": 3, "20": 3},
                               decision={"scores": {"2": {"score": 0.1}, "5": {"score": 0.5}, "20": {"score": 0.9}}},
                               features={"2": F(30, 2), "5": F(30, 2), "20": F(30, 2)}))
        fm_real = TG.formal_from_rounds(rounds)
        fm_raw = TG.formal_from_rounds(rounds, real=False)
        self.assertEqual(TG.last_streak(fm_real, 2, 20)[0], 0)
        self.assertEqual(TG.last_streak(fm_raw, 2, 20)[0], 6)

    def test_default_on(self):
        self.assertTrue(TG.P({})["real_n60"])

if __name__ == "__main__": unittest.main()
