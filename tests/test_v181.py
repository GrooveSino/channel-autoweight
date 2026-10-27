import os, sys, unittest
from datetime import datetime
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight.diag import vendor_of, reenable_check

E = {22: dict(disabled_at="2026-01-01T17:05:00+08:00", vendor="ACME", min_age_min=120, start_weight=3)}
CH = [dict(id=2, name="[GLOBEX]-GPT"), dict(id=21, name="[ACME]-GPT-A")]
def F(n, to60, to10=0): return {2: {"60m": dict(n=300, err_timeout=0), "10m": {}}, 21: {"60m": dict(n=n, err_timeout=to60), "10m": dict(err_timeout=to10)}}
def T_(s): return datetime.fromisoformat(s).timestamp()

class T(unittest.TestCase):
    def test_vendor(self):
        self.assertEqual(vendor_of("[ACME]-GPT-B"), "ACME"); self.assertIsNone(vendor_of("gpt"))

    def test_too_young(self):
        r = reenable_check(E, CH, F(50, 0), T_("2026-01-01T17:35:00+08:00"))[22]
        self.assertFalse(r["ready"]); self.assertEqual(r["siblings"], [21])

    def test_sibling_timeouts_block(self):
        r = reenable_check(E, CH, F(50, 2), T_("2026-01-01T19:30:00+08:00"))[22]
        self.assertFalse(r["ready"]); self.assertEqual(r["sib_timeout_60m"], 2)

    def test_ready(self):
        r = reenable_check(E, CH, F(50, 0), T_("2026-01-01T19:30:00+08:00"))[22]
        self.assertTrue(r["ready"]); self.assertEqual(r["start_weight"], 3)

    def test_thin_evidence(self):
        r = reenable_check(E, CH, F(2, 0), T_("2026-01-01T19:30:00+08:00"))[22]
        self.assertFalse(r["ready"])

    def test_vendor_from_names(self):
        e = {22: dict(disabled_at="2026-01-01T17:05:00+08:00")}
        r = reenable_check(e, CH, F(50, 0), T_("2026-01-01T19:30:00+08:00"), names={22: "[ACME]-GPT-B"})[22]
        self.assertEqual(r["vendor"], "ACME"); self.assertTrue(r["ready"])
