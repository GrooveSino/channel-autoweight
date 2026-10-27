import os, sys, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight import verify

def R(i, w, ab, st=1, p=10):
    return dict(id=i, name="x", status=st, priority=p, weight=w, abilities=ab)

class T(unittest.TestCase):
    def test_ok(self):
        self.assertEqual(verify.check([R(2, 1, [(1, 10)]), R(14, 1, [(5, 3)], st=2)], 10, {2: 1}), [])
    def test_mismatch(self):
        p = verify.check([R(2, 1, [(3, 10)])], 10)
        self.assertEqual(len(p), 1); self.assertIn("disagree", p[0])
    def test_priority_and_target(self):
        p = verify.check([R(5, 6, [(6, 9)], p=9)], 10, {5: 12})
        self.assertEqual(len(p), 2)  # priority is not 10 + weight differs from target; abilities agree with channels

if __name__ == "__main__":
    unittest.main()
