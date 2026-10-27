import os, sys, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight import decide
CH=[{"id":2},{"id":5},{"id":20}]; CFG={"w_min":1,"w_max":20}
class T(unittest.TestCase):
    def test_final_full(self):
        self.assertEqual(decide.parse_final("2=12,5=3,20=3",CH,CFG),{2:12,5:3,20:3})
    def test_final_missing(self):
        with self.assertRaises(ValueError): decide.parse_final("2=12,5=3",CH,CFG)
    def test_final_disabled_rejected(self):
        with self.assertRaises(ValueError): decide.parse_final("2=12,5=3,20=3,14=1",CH,CFG)
    def test_drift(self):
        self.assertEqual(decide.system_drift({2:12,20:6},{2:12,5:6,20:10}),{20:(6,10)})
        self.assertEqual(decide.system_drift({2:12},{2:12,5:6}),{})
if __name__=="__main__": unittest.main()
