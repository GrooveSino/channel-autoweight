"""v1.13 watch list verdict"""
import os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, HERE)
from autoweight import watch
C = dict(max_ttft=10.0, min_n=10, max_err=0.05, max_empty=0.05)
G = dict(ok=True, ttft=3.0, streaming=True, status=200, deltas=150)
class T(unittest.TestCase):
    def test_good(self): self.assertEqual(watch.verdict([G, G], None, C), (True, []))
    def test_fail(self): self.assertFalse(watch.verdict([dict(ok=False, status=403)], None, C)[0])
    def test_buffered(self): self.assertFalse(watch.verdict([dict(G, streaming=False)], None, C)[0])
    def test_slow(self): self.assertFalse(watch.verdict([dict(G, ttft=14)], None, C)[0])
    def test_unknown_stream(self): self.assertFalse(watch.verdict([dict(G, streaming=None)], None, C)[0])
    def test_empty_rate(self):
        self.assertFalse(watch.verdict([G], dict(n=20, err_rate=0, empty_rate=0.2), C)[0])
        self.assertTrue(watch.verdict([G], dict(n=5, err_rate=0.5, empty_rate=0.5), C)[0])
    def test_none(self): self.assertFalse(watch.verdict([], None, C)[0])
if __name__ == "__main__": unittest.main()
