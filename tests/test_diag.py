import os, sys, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight import diag

def P(stream, ok=True):
    return [dict(ok=ok, streaming=stream)]

class T(unittest.TestCase):
    def test_chanset(self):
        prev = dict(channels=[{"id": 2}, {"id": 5}, {"id": 14}])
        self.assertEqual(diag.channel_set_change(prev, [{"id": 2}, {"id": 5}, {"id": 20}]),
                         dict(added=[20], removed=[14]))
        self.assertIsNone(diag.channel_set_change(None, [{"id": 2}]))

    def test_streak(self):
        hist = [dict(probes={"2": P(True)}), dict(probes={"2": P(False)}), dict(probes={"2": P(None)})]
        # this round False, previous None skipped, the one before False, the one before that True stops -> 2
        self.assertEqual(diag.buffered_streak(hist, {"2": P(False)}, [2]), {2: 2})
        self.assertEqual(diag.buffered_streak(hist, {"2": P(True)}, [2]), {})
        # failed probes are not counted
        self.assertEqual(diag.buffered_streak([], {"2": P(False, ok=False)}, [2]), {})

if __name__ == "__main__":
    unittest.main()
