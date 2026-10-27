import os, sys
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..")); sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "tools"))
import pbest, unittest

def _rows(ch, uts, ct=200, t=100, typ=2):
    return [{"ch": ch, "t": t, "type": typ, "ct": ct, "ut": u} for u in uts]

class TestPBest(unittest.TestCase):
    def test_clear_winner(self):
        rows = _rows(1, [5] * 30) + _rows(2, [20] * 30)
        p = pbest.pbest(pbest.per_request(rows, 0, 200), B=300)
        assert p[1] == 1.0 and p[2] == 0.0

    def test_errors_penalised(self):
        rows = _rows(1, [5] * 20) + _rows(1, [0] * 15, typ=5) + _rows(2, [6] * 30)
        p = pbest.pbest(pbest.per_request(rows, 0, 200), B=300)
        assert p[2] > p[1]

    def test_min_n_and_tie(self):
        rows = _rows(1, [10] * 30) + _rows(2, [10] * 30) + _rows(3, [1] * 3)
        p = pbest.pbest(pbest.per_request(rows, 0, 200), B=200)
        assert 3 not in p and abs(sum(p.values()) - 1) < 1e-9
