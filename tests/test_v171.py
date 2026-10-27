import unittest
from autoweight import features as F

def e(ch, t, tok, sc): return dict(ch=ch, t=t, type=5, ut=1, ct=0, tok=tok, sc=sc)
def ok(ch, t, tok=1): return dict(ch=ch, t=t, type=2, ut=10, ct=300, tok=tok)

class T(unittest.TestCase):
    def test_cross_channel_tokens(self):
        rows = [e(2, 100, 9, 403), e(22, 200, 9, 403), e(21, 150, 7, 400), e(21, 160, 7, 400), e(20, 170, 8, 524)]
        self.assertEqual(F.cross_channel_tokens(rows, 0, 300), {9})
        self.assertEqual(F.cross_channel_tokens(rows, 0, 150), set())

    def test_drop_field(self):
        feats = {2: {"60m": dict(n=10, err=3, err_rate=0.3, err_client=3, err_user=2)}}
        F.drop_client_errors(feats, "err_user")
        self.assertEqual(feats[2]["60m"]["err"], 1); self.assertEqual(feats[2]["60m"]["n"], 8)

    def test_build_cross_mode(self):
        cfg = dict(windows_min=[60, 10], slow_s=60, score=dict(client_err=dict(enabled=True, mode="cross_channel")))
        now = 10000
        rows = [ok(2, now - 100), ok(22, now - 100), e(2, now - 50, 9, 403), e(22, now - 900, 9, 403), e(2, now - 40, 5, 400), e(21, now - 30, 6, 524), ok(21, now - 20)]
        out = F.build(cfg, [{"id": 2}, {"id": 21}, {"id": 22}], rows, now)
        self.assertEqual(out[2]["60m"]["err"], 1)      # cross-channel 403 removed, single-channel 400 kept
        self.assertEqual(out[2]["60m"]["err_raw"], 2)
        self.assertEqual(out[22]["60m"]["err"], 0)
        self.assertEqual(out[22]["10m"]["err"], 0)
        self.assertEqual(out[21]["60m"]["err"], 1)     # 524 is not a client error

    def test_off_keeps_errors(self):
        cfg = dict(windows_min=[60], slow_s=60, score=dict(client_err=dict(enabled=False)))
        rows = [e(2, 9950, 9, 403), e(22, 9900, 9, 403)]
        out = F.build(cfg, [{"id": 2}, {"id": 22}], rows, 10000)
        self.assertEqual(out[2]["60m"]["err"], 1); self.assertEqual(out[2]["60m"]["err_user"], 1)

if __name__ == "__main__": unittest.main()
