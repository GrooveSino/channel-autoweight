import unittest
from autoweight import trust

class TestSlowBayes(unittest.TestCase):
    def test_posterior_matches_binomial_closed_form(self):
        from math import comb
        n, k, thr = 18, 3, 0.15
        exact = sum(comb(n+1, j) * thr**j * (1-thr)**(n+1-j) for j in range(k+1))
        self.assertAlmostEqual(trust.slow_posterior(n, k/n, thr), exact, places=3)

    def test_monotone_in_k_and_n(self):
        self.assertLess(trust.slow_posterior(18, 2/18, .15), trust.slow_posterior(18, 4/18, .15))
        self.assertLess(trust.slow_posterior(20, .2, .15), trust.slow_posterior(200, .2, .15))

    def test_small_sample_softened(self):
        p = trust.params({"trust": {"slow_bayes": True, "slow_prior_a": 0.8, "slow_prior_b": 9.2}})
        f = {"60m": {"n": 18, "slow_rate": 3/18}, "10m": {"n": 0}}
        self.assertEqual(trust.severity_parts(f, None, p), [])
        f = {"60m": {"n": 200, "slow_rate": 0.25}, "10m": {"n": 0}}
        s = trust.severity_parts(f, None, p)
        self.assertTrue(s and 0 < s[0][0] <= p["slow_sev"])

    def test_off_unchanged(self):
        p = trust.params({})
        f = {"60m": {"n": 18, "slow_rate": 3/18}, "10m": {"n": 0}}
        self.assertEqual(trust.severity_parts(f, None, p)[0][0], p["slow_sev"])

if __name__ == "__main__":
    unittest.main()
