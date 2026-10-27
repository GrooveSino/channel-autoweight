"""v1.6 traffic_mix diagnostics: real user/probe split, token concentration, compatibility with old data."""
import os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import features
import evaluate

def L(t, tok, ch=5, typ=2):
    return dict(ch=ch, t=t, type=typ, ut=3, ct=10, tok=tok)

class T(unittest.TestCase):
    def test_mix_basic(self):
        rows = [L(1000, 7)] * 8 + [L(1000, 9), L(1000, 900)]
        m = features.traffic_mix(rows, 1100, 60, [900])
        self.assertEqual((m["n_user"], m["n_probe"], m["tokens"]), (9, 1, 2))
        self.assertAlmostEqual(m["top_tok_share"], 0.889)

    def test_mix_window_and_legacy(self):
        self.assertEqual(features.traffic_mix([L(10, 7)], 10000, 60, [900])["n_user"], 0)
        self.assertIsNone(features.traffic_mix([dict(ch=5, t=1, type=2, ut=1, ct=1)], 2, 60, [900]))

    def test_build_has_mix_and_scores_untouched(self):
        cfg = dict(windows_min=[60, 10], slow_s=60, probe_token_ids=[900], score={})
        f = features.build(cfg, [dict(id=5)], [L(1000, 7), L(1000, 900)], 1100)
        self.assertEqual(f[5]["60m"]["n"], 2)          # decision basis unchanged (probes still counted)
        self.assertEqual(f[5]["mix_60m"]["n_user"], 1)

    def test_evaluate_shows_mix(self):
        a = dict(ts="2026-10-24T01:00:00+08:00", old={}, applied=True, decision=dict(target={"5": 3}))
        b = dict(ts="2026-10-24T02:00:00+08:00", old={}, applied=False,
                 features={"5": {"60m": dict(n=10, p50=2, p90=4, err_rate=0), "mix_60m": dict(n_user=9, n_probe=1, tokens=2, top_tok_share=0.9)}},
                 decision=dict(target={}, scores={"5": 0.5}))
        self.assertEqual(evaluate.evaluate_pair(a, b)["channels"]["5"]["top_tok_share"], 0.9)

if __name__ == "__main__": unittest.main()
