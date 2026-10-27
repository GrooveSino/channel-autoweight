import os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight.diag import probe_flip

FLIP = [dict(ok=True, streaming=False), dict(ok=True, streaming=True, recheck=True)]
STREAM = [dict(ok=True, streaming=True)]
BUF = [dict(ok=True, streaming=False), dict(ok=True, streaming=False, recheck=True)]

class T(unittest.TestCase):
    def test_flip_flagged(self):
        h = [dict(probes={"20": FLIP, "2": STREAM}), dict(probes={"20": STREAM, "2": STREAM})]
        r = probe_flip(h, {"20": FLIP, "2": STREAM}, [2, 20])
        self.assertEqual(r, {20: dict(flips=2, rounds=3)})
    def test_true_buffer_not_flip(self):
        h = [dict(probes={"20": BUF}), dict(probes={"20": BUF})]
        self.assertEqual(probe_flip(h, {"20": BUF}, [20]), {})
    def test_reused_skipped(self):
        h = [dict(probes={"20": FLIP}, probes_reused=True)]
        self.assertEqual(probe_flip(h, {"20": FLIP}, [20]), {})
    def test_window(self):
        h = [dict(probes={"20": FLIP})] + [dict(probes={"20": STREAM})] * 8
        self.assertEqual(probe_flip(h, {"20": FLIP}, [20], window=8), {})
    def test_empty(self):
        self.assertEqual(probe_flip([], {}, [20]), {})

if __name__ == "__main__": unittest.main()
