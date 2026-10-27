import json, os, sys
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight.decide import strat_rank

CFG = json.load(open(os.path.join(os.path.dirname(__file__), "..", "tests/fixtures/config.test.json")))

import unittest

def _p(**kw):
    p = dict(CFG["strategies"]["rank"]); p.update(kw); return p

class TestV188(unittest.TestCase):
    def test_co_lead_off_default_matches_old(self):
        sc = {2: 0.40, 22: 0.386, 20: 0.30, 21: 0.20}
        w = strat_rank([2, 22, 20, 21], sc, _p(), CFG)
        assert w[2] == 12 and w[22] == 12 * CFG["strategies"]["rank"]["close_factor"]

    def test_co_lead_gives_both_top(self):
        sc = {2: 0.40, 22: 0.386, 20: 0.30, 21: 0.20}
        w = strat_rank([2, 22, 20, 21], sc, _p(co_lead_ratio=0.95), CFG)
        assert w[2] == 12 and w[22] == 12 and w[20] == CFG["strategies"]["rank"]["mid_w"]

    def test_co_lead_below_ratio_unchanged(self):
        sc = {2: 0.40, 22: 0.36, 20: 0.30, 21: 0.10}
        w = strat_rank([2, 22, 20, 21], sc, _p(co_lead_ratio=0.95), CFG)
        assert w[22] == 12 * CFG["strategies"]["rank"]["close_factor"]
