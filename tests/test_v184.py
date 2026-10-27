import unittest, sys, os
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight import decide
CH = [{"id": 2}, {"id": 22}]
def R(ids): return {"applied": True, "channels": [{"id": i} for i in ids]}
class T(unittest.TestCase):
    def cfg(self, gap): return {"newcomer": {"enabled": True, "min_rounds": 4, "reset_gap_rounds": gap}}
    def test_off_keeps_old(self):
        h = [R([2, 22])]*5 + [R([2])]*3 + [R([2, 22])]
        self.assertEqual(decide.newcomer_ids(self.cfg(0), CH, h), set())
    def test_reset_after_gap(self):
        h = [R([2, 22])]*5 + [R([2])]*3 + [R([2, 22])]
        self.assertEqual(decide.newcomer_ids(self.cfg(2), CH, h), {22})
    def test_graduates_again(self):
        h = [R([2, 22])]*5 + [R([2])]*3 + [R([2, 22])]*4
        self.assertEqual(decide.newcomer_ids(self.cfg(2), CH, h), set())
    def test_short_gap_no_reset(self):
        h = [R([2, 22])]*5 + [R([2])] + [R([2, 22])]
        self.assertEqual(decide.newcomer_ids(self.cfg(2), CH, h), set())
    def test_dryrun_absence_counts(self):
        h = [R([2, 22])]*5 + [{"applied": False, "channels": [{"id": 2}]}]*3 + [R([2, 22])]
        self.assertEqual(decide.newcomer_ids(self.cfg(2), CH, h), {22})
if __name__ == "__main__": unittest.main()
