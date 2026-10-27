"""v1.77: newcomer detection must use the full formal-round history (when the live shadow only read the last 10 rounds, established channels were misclassified as new and capped at 6 during dry-run-heavy periods)."""
import os, sys, json, subprocess, unittest
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, HERE); sys.path.insert(0, os.path.join(HERE, "tools"))
import shadow as SH
from autoweight import decide

CFG = {"newcomer": {"enabled": True, "min_rounds": 4, "cap": 6, "min_real_n60": 30}}
CH = [{"id": 21}, {"id": 22}]

def rnd(ts, applied, ids=(21, 22)):
    return {"ts": ts, "applied": applied, "channels": [{"id": i} for i in ids]}

class T(unittest.TestCase):
    def setUp(self):
        # 5 early write rounds + 7 mixed dry-run/write rounds (only 2 of the last 7 are writes)
        self.hist = [rnd(f"2026-10-26T0{h}:00:00+08:00", True) for h in range(5)]
        self.hist += [rnd(f"2026-10-26T1{h}:00:00+08:00", h % 3 == 0) for h in range(7)]

    def test_tail_window_misclassifies(self):
        self.assertEqual(decide.newcomer_ids(CFG, CH, self.hist[-7:]), {21, 22})   # old behaviour: misclassified

    def test_full_history_not_newcomer(self):
        self.assertEqual(decide.newcomer_ids(CFG, CH, self.hist), set())

    def test_nc_rounds_filters_future(self):
        from datetime import datetime
        now = datetime.fromisoformat("2026-10-26T02:30:00+08:00").timestamp()
        got = SH.nc_rounds(self.hist, now)
        self.assertEqual(len(got), 3)                                       # only rounds before now
        self.assertEqual(decide.newcomer_ids(CFG, CH, got), {21, 22})       # real new channels are still capped

    def test_replay_rounds_tail_flag(self):
        import shadow_replay as R
        self.assertEqual(R.ROUNDS_TAIL, 0)                                  # full history by default


if __name__ == "__main__":
    unittest.main()
