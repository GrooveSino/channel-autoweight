import unittest
from autoweight import features as F

def e(ch, t, sc=503): return dict(ch=ch, t=t, type=5, ut=0, ct=0, tok=1, sc=sc)
def ok(ch, t): return dict(ch=ch, t=t, type=2, ut=10, ct=300, tok=1)
RC = dict(enabled=True, span_s=60, min_ok=10, keep=0.3)

class T(unittest.TestCase):
    def test_err_burst(self):
        self.assertEqual(F.err_burst([ok(2, 1)]), (0, None, None))
        r = [e(2, 100), e(2, 126), ok(2, 90), ok(2, 130), ok(2, 140)]
        self.assertEqual(F.err_burst(r), (2, 26, 2))

    def test_recovered_burst_discounted(self):
        now = 10000
        rows = [e(2, now - 300 + i) for i in range(20)] + [ok(2, now - 200 + i) for i in range(20)]
        cfg = dict(windows_min=[60, 10], slow_s=60, score=dict(err_recover=RC))
        out = F.build(cfg, [{"id": 2}], rows, now)
        w = out[2]["10m"]
        self.assertEqual(w["err_pre_rec"], 20); self.assertEqual(w["err"], 6)
        self.assertAlmostEqual(w["err_rate"], 6 / 40, 4)

    def test_not_recovered_or_long_kept(self):
        now = 10000
        rows = [e(2, now - 300 + i) for i in range(20)] + [ok(2, now - 200 + i) for i in range(5)]   # only 5 successes
        cfg = dict(windows_min=[10], slow_s=60, score=dict(err_recover=RC))
        self.assertEqual(F.build(cfg, [{"id": 2}], rows, now)[2]["10m"]["err"], 20)
        rows = [e(2, now - 500), e(2, now - 300)] + [ok(2, now - 200 + i) for i in range(20)]       # span 200s
        self.assertEqual(F.build(cfg, [{"id": 2}], rows, now)[2]["10m"]["err"], 2)

    def test_off(self):
        now = 10000
        rows = [e(2, now - 300 + i) for i in range(5)] + [ok(2, now - 200 + i) for i in range(20)]
        cfg = dict(windows_min=[10], slow_s=60, score=dict(err_recover=dict(RC, enabled=False)))
        w = F.build(cfg, [{"id": 2}], rows, now)[2]["10m"]
        self.assertEqual(w["err"], 5); self.assertNotIn("err_pre_rec", w); self.assertEqual(w["eb_ok_after"], 20)

if __name__ == "__main__": unittest.main()
