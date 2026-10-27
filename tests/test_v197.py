import os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight import decide

def rnd(ts, ids, nu=None, applied=True):
    ft = {str(i): {"mix_60m": {"n_user": (nu or {}).get(i, 5)}} for i in ids}
    return {"ts": ts, "applied": applied, "channels": [{"id": i} for i in ids], "features": ft}

CH = [{"id": 2}, {"id": 23}]

def cfg(grad):
    return {"newcomer": {"enabled": True, "min_rounds": 4, "cap": 6, "min_real_n60": 30, "graduate_real_n60": grad}}

class T(unittest.TestCase):
    def hist(self, nu23=5):
        return [rnd(f"2026-10-27T0{h}:00:00+08:00", [2, 23], {2: 40, 23: nu23}) for h in range(6)]
    def test_off_graduates_by_rounds(self):
        self.assertEqual(decide.newcomer_ids(cfg(0), CH, self.hist()), set())
    def test_unproven_stays_newcomer(self):
        self.assertEqual(decide.newcomer_ids(cfg(30), CH, self.hist()), {23})
    def test_proven_graduates(self):
        h = self.hist(); h[2]["features"]["23"]["mix_60m"]["n_user"] = 31
        self.assertEqual(decide.newcomer_ids(cfg(30), CH, h), set())
    def test_dry_run_rounds_do_not_prove(self):
        h = self.hist() + [rnd("2026-10-27T07:00:00+08:00", [2, 23], {2: 40, 23: 50}, applied=False)]
        self.assertEqual(decide.newcomer_ids(cfg(30), CH, h), {23})
    def test_missing_n_user_not_proven(self):
        h = self.hist()
        for r in h: r["features"]["23"]["mix_60m"] = {}
        self.assertEqual(decide.newcomer_ids(cfg(30), CH, h), {23})

if __name__ == "__main__": unittest.main()
