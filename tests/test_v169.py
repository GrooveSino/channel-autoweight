import unittest
from autoweight.features import log_stream_veto

P = [{"ok": True, "streaming": False}]
F = {"60m": {"ls_n": 30, "ls_buf": 0.05}}

class T(unittest.TestCase):
    def test_default_both(self):
        lv = {"enabled": True, "min_n": 20, "max_buf": 0.2}
        self.assertTrue(log_stream_veto(P, F, lv, use="score"))
        self.assertTrue(log_stream_veto(P, F, lv, use="trust"))
        self.assertTrue(log_stream_veto(P, F, lv))
    def test_trust_only(self):
        lv = {"enabled": True, "min_n": 20, "max_buf": 0.2, "scope": "trust"}
        self.assertFalse(log_stream_veto(P, F, lv, use="score"))
        self.assertTrue(log_stream_veto(P, F, lv, use="trust"))
    def test_score_only(self):
        lv = {"enabled": True, "min_n": 20, "max_buf": 0.2, "scope": "score"}
        self.assertTrue(log_stream_veto(P, F, lv, use="score"))
        self.assertFalse(log_stream_veto(P, F, lv, use="trust"))
    def test_disabled(self):
        self.assertFalse(log_stream_veto(P, F, {"enabled": False, "scope": "trust"}, use="trust"))

if __name__ == "__main__":
    unittest.main()
