import json, os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import shadow as S

CFG = json.load(open(os.path.join(HERE, "tests/fixtures/config.test.json")))
def feat(n=20):
    w = {"n": n, "err": 0, "err_rate": 0.0, "slow_rate": 0.0, "p50": 5, "p90": 10}
    return {"60m": dict(w), "10m": dict(w, n=5)}
BUF = [{"ok": True, "ttft": 7.0, "streaming": False}]

class T(unittest.TestCase):
    def test_probe_ages(self):
        self.assertEqual(S.probe_ages({"2": "probe", "5": "reuse 12.5m", "20": "missing"}), {2: 0.0, 5: 12.5})
    def test_probe_new(self):
        self.assertTrue(S.probe_new(0.0, 5)); self.assertTrue(S.probe_new(1.9, 5))
        self.assertFalse(S.probe_new(6.9, 5)); self.assertTrue(S.probe_new(None, 5))
    def test_reused_buffer_not_recounted(self):
        out = S.trust_step(CFG, [2], {2: feat()}, {2: BUF}, {2: 0.85}, 5.0, {2: 6.9})
        self.assertEqual(out[2]["sev_eff"], 0.0); self.assertGreaterEqual(out[2]["trust"], 0.85)
    def test_new_buffer_counted_once_full(self):
        out = S.trust_step(CFG, [2], {2: feat()}, {2: BUF}, {2: 1.0}, 5.0, {2: 1.9})
        self.assertEqual(out[2]["sev_eff"], 0.5); self.assertLess(out[2]["trust"], 1.0)
    def test_off_keeps_old_behaviour(self):
        cfg = json.loads(json.dumps(CFG)); cfg["shadow"]["probe_once"] = False
        out = S.trust_step(cfg, [2], {2: feat()}, {2: BUF}, {2: 1.0}, 5.0, {2: 30.0})
        self.assertEqual(out[2]["sev_eff"], 0.25)

if __name__ == "__main__": unittest.main()
