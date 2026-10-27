import os, sys, unittest
from datetime import datetime, timezone, timedelta
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight.probe import reuse_age
from autoweight.shadow import probe_new

TZ = timezone(timedelta(hours=8))
def ts(m): return datetime(2026, 10, 26, 7, m, tzinfo=TZ)

class T(unittest.TestCase):
    def test_age_of_latest_probe(self):
        rounds = [{"ts": ts(47).isoformat(), "probes": {"2": {}}}]
        self.assertAlmostEqual(reuse_age(rounds, ts(49).timestamp()), 2.0)

    def test_skips_reused_copies(self):
        rounds = [{"ts": ts(40).isoformat(), "probes": {"2": {}}},
                  {"ts": ts(47).isoformat(), "probes": {"2": {}}, "probes_reused": True}]
        self.assertAlmostEqual(reuse_age(rounds, ts(49).timestamp()), 9.0)

    def test_none_without_probes(self):
        self.assertIsNone(reuse_age([{"ts": ts(40).isoformat(), "probes": {}}], ts(49).timestamp()))

    def test_dryrun_probe_counts_as_new_after_shadow_point(self):
        # shadow point 07:40, dry-run probe 07:47, apply reuses it at 07:49: age 2 min < gap 9 min -> new evidence
        age = reuse_age([{"ts": ts(47).isoformat(), "probes": {"2": {}}}], ts(49).timestamp())
        self.assertTrue(probe_new(age, 9.0))
        # probe older than the shadow point (age 12 > gap 9) -> already counted by the shadow point, not counted twice
        age = reuse_age([{"ts": ts(37).isoformat(), "probes": {"2": {}}}], ts(49).timestamp())
        self.assertFalse(probe_new(age, 9.0))

if __name__ == "__main__": unittest.main()
