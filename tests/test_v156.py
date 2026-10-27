import unittest
from autoweight.features import log_stream, log_stream_veto, window

def row(t, span=None, chunks=None, typ=2):
    d = dict(ch=2, t=t, type=typ, ut=10, ct=300, tok=0)
    if span is not None: d.update(span=span, chunks=chunks)
    return d

class T(unittest.TestCase):
    def test_log_stream(self):
        ok = [row(1, 5.0, 80)] * 9 + [row(1, 0.1, 80)] + [row(1, 0.0, 1)]
        self.assertEqual(log_stream(ok), (10, 0.1))
        self.assertEqual(log_stream([row(1)]), (0, None))

    def test_window_fields(self):
        w = window([row(990, 4.0, 50), row(995, 0.1, 50)], 1000, 60, 60)
        self.assertEqual((w["ls_n"], w["ls_buf"]), (2, 0.5))

    def test_veto(self):
        lv = dict(enabled=True, min_n=20, max_buf=0.2)
        buf = [dict(ok=True, streaming=False, ttft=20)]
        f = {"60m": dict(ls_n=30, ls_buf=0.05)}
        self.assertTrue(log_stream_veto(buf, f, lv))
        self.assertFalse(log_stream_veto(buf, f, dict(lv, enabled=False)))
        self.assertFalse(log_stream_veto(buf, {"60m": dict(ls_n=10, ls_buf=0.0)}, lv))
        self.assertFalse(log_stream_veto(buf, {"60m": dict(ls_n=30, ls_buf=0.5)}, lv))
        self.assertFalse(log_stream_veto([dict(ok=True, streaming=True)], f, lv))

    def test_score_and_trust(self):
        import json, copy
        from autoweight import decide, trust
        cfg = json.load(open("tests/fixtures/config.test.json")); cfg["log_stream_veto"]["enabled"] = False   # on by default since v1.69; compare off/on explicitly here
        f = {"60m": dict(n=100, err=0, err_rate=0.0, p50=12, p90=36, slow_rate=0.05, ls_n=50, ls_buf=0.05, lt_n=50, lt50=8.0),
             "10m": dict(n=20, err=0, err_rate=0.0, p50=12, p90=36, slow_rate=0.05)}
        pr = [dict(ok=True, streaming=False, ttft=25.0)]
        off = decide.score(cfg, f, pr)
        on_cfg = copy.deepcopy(cfg); on_cfg["log_stream_veto"]["enabled"] = True
        on = decide.score(on_cfg, f, pr)
        self.assertIs(off[2], False); self.assertIsNone(on[2]); self.assertGreater(on[0], off[0])
        p_off, p_on = trust.params(cfg), trust.params(on_cfg)
        self.assertTrue(any("buffered" in w for _, w, _ in trust.severity_parts(f, pr, p_off)))
        self.assertFalse(any("buffered" in w for _, w, _ in trust.severity_parts(f, pr, p_on)))

if __name__ == "__main__": unittest.main()
