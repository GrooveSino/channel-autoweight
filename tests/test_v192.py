import os, sys, unittest
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight.toggle import toggle_sql

class T(unittest.TestCase):
    def test_enable_then_disable_sync_abilities(self):
        sql, ok, rej = toggle_sql([dict(op="disable", id=21, why="ranked last for 6 consecutive rounds (218 min)"), dict(op="enable", id=5, weight=2)], [2, 20, 21, 22, 23])
        self.assertEqual([a["id"] for a in ok], [5, 21]); self.assertEqual(rej, [])
        self.assertIn("status=1, priority=10, weight=2 where id=5", sql)
        self.assertIn("status=2, weight=0 where id=21", sql)
        self.assertIn("enabled=(c.status=1)", sql); self.assertIn("c.id in (5,21)", sql)
        self.assertTrue(sql.startswith("BEGIN;") and sql.rstrip().endswith("COMMIT;"))
    def test_rank_disable_refused_at_min_enabled(self):
        sql, ok, rej = toggle_sql([dict(op="disable", id=21, why="ranked last for 6 consecutive rounds")], [2, 20, 21, 22])
        self.assertIsNone(sql); self.assertEqual(len(rej), 1)
    def test_error_disable_allowed_to_hard_min_not_below(self):
        why = "persistent errors: 1h error rate >= 20% for 2 consecutive rounds at weight 0"
        sql, ok, _ = toggle_sql([dict(op="disable", id=21, why=why)], [2, 20, 21, 22])
        self.assertIsNotNone(sql)
        sql, ok, rej = toggle_sql([dict(op="disable", id=21, why=why)], [2, 20, 21])
        self.assertIsNone(sql); self.assertIn("floor 3", rej[0]["reject"])
    def test_enable_counts_toward_floor(self):
        sql, ok, rej = toggle_sql([dict(op="disable", id=21, why="ranked last"), dict(op="enable", id=5, weight=2)], [2, 20, 21, 22])
        self.assertEqual(len(ok), 2)
    def test_noop_and_bad_targets(self):
        sql, ok, rej = toggle_sql([dict(op="enable", id=2, weight=2), dict(op="disable", id=9, why="x")], [2, 20])
        self.assertIsNone(sql); self.assertEqual(len(rej), 2)

if __name__ == "__main__": unittest.main()
