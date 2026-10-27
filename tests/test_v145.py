import os, sys, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__))); sys.path.insert(0, os.path.join(HERE, "tools"))
import override_eval as E

def rnd(sysw, fin, applied=True, dry=False):
    return {"ts": "2026-10-25T10:00:00+08:00", "dry_run": dry, "applied": applied,
            "decision": {"target": fin, "override": {"system": sysw, "reason": "x"}}}

class T(unittest.TestCase):
    def test_pairs_only_applied_and_different(self):
        rs = [rnd({"2": 12}, {"2": 6}), rnd({"2": 12}, {"2": 12}), rnd({"2": 12}, {"2": 6}, dry=True), rnd({"2": 12}, {"2": 6}, applied=False)]
        p = E.pairs(rs); assert len(p) == 1 and p[0][1] == {2: 12} and p[0][2] == {2: 6}

    def test_verdict_p90_first_then_p50_tie(self):
        assert E.verdict({"p50": 10, "p90": 40}, {"p50": 12, "p90": 30}) == "F"
        assert E.verdict({"p50": 10, "p90": 30}, {"p50": 12, "p90": 30.2}) == "S"
        assert E.verdict({"p50": 10, "p90": 30}, {"p50": 10.1, "p90": 30.1}) == "="

    def test_score_uses_after_window(self):
        srecs = [{"ts": "2026-10-25T10:10:00+08:00", "features": {"2": {"10m": {"n": 5, "p50": 10, "p90": 20, "err_rate": 0}}, "5": {"10m": {"n": 5, "p50": 30, "p90": 60, "err_rate": 0}}}}]
        rows = E.score(srecs, [("2026-10-25T10:00:00+08:00", {2: 1, 5: 1}, {2: 1, 5: 0}, "")], 45)
        assert rows and rows[0][5]["p50"] == 10 and rows[0][4]["p50"] == 20

if __name__ == '__main__': unittest.main()
