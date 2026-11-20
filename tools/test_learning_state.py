"""Offline tests: temporary synthetic run artifacts only; never a DB or runner."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import learning_state as ledger

META = {"candidate_improvements": ["CASE-AW-001"], "change_kind": "tests",
        "tests": ["synthetic offline"], "commit": None, "note": "regression"}


class LedgerTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.base = Path(self.tmp.name)
        self.root = self.base / "runs"; self.root.mkdir()
        self.state = self.base / "learning" / "state.json"

    def tearDown(self):
        self.tmp.cleanup()

    def run_artifact(self, name, start="2026-11-21T02:16:00+08:00", state="verified", applied=False, mode="run", result=True):
        p = self.root / name; p.mkdir()
        info = {"mode": mode, "started": start, "state": state, "applied": applied}
        (p / "status.json").write_text(json.dumps(info))
        if result: (p / "result.json").write_text(json.dumps(info))
        return p

    def read(self):
        return json.loads(self.state.read_text())

    def test_init_seeds_seven_but_counts_none_and_preserves_state(self):
        for n in range(7): self.run_artifact(str(n), start="2026-11-21T01:00:00+08:00")
        self.run_artifact("check", mode="check-only")
        new = self.run_artifact("new")
        self.assertEqual(ledger.initialize(self.state, self.root)["historical_seed_count"], 7)
        self.assertEqual(self.read()["rounds"], {})
        ledger.record(self.state, self.root, new, META)
        ledger.initialize(self.state, self.root)
        self.assertEqual(len(self.read()["rounds"]), 1)
        self.assertEqual(self.state.stat().st_mode & 0o777, 0o600)

    def test_duplicate_and_canonical_symlink_do_not_count_twice(self):
        p = self.run_artifact("one")
        ledger.record(self.state, self.root, p, META)
        alias = self.root / "alias"; alias.symlink_to(p, target_is_directory=True)
        before = self.state.read_bytes()
        value = ledger.record(self.state, self.root, alias, {**META, "note": "duplicate notification"})
        self.assertTrue(value["duplicate"])
        self.assertEqual(value["counted_rounds"], 1)
        self.assertEqual(self.state.read_bytes(), before)

    def test_failures_count_and_every_four_unique_runs_needs_review(self):
        for n, status in enumerate(["verified", "failed", "uncertain", "verified"]):
            p = self.run_artifact(str(n), state=status, applied=n == 0)
            result = ledger.record(self.state, self.root, p, META)
        self.assertEqual(result["review_due"], [1])
        labels = [r["outcome"] for r in self.read()["rounds"].values()]
        self.assertEqual(labels, ["verified_write", "failed", "uncertain", "verified_noop"])
        self.assertIsNone(list(self.read()["rounds"].values())[2]["applied"])
        self.assertEqual(ledger.review(self.state, 1, "复核无新增案例，保持原行为", []) ["review_due"], [])
        self.assertTrue(ledger.review(self.state, 1, "重复通知", [])["duplicate_review"])
        with self.assertRaises(ValueError): ledger.review(self.state, 2, "too early", [])

    def test_old_check_only_unfinished_and_naive_time_are_rejected(self):
        for n, kwargs in enumerate([{"start": "2026-11-21T01:42:59+08:00"}, {"mode": "check-only"},
                                    {"state": "running", "result": False}, {"start": "2026-11-21T02:16:00"}]):
            p = self.run_artifact(str(n), **kwargs)
            with self.assertRaises((ValueError, TypeError)): ledger.record(self.state, self.root, p, META)
        p = self.root / "2"
        result = ledger.record(self.state, self.root, p, META, outcome="failed")
        self.assertEqual(result["round"]["outcome"], "failed")

    def test_annotate_is_explicit_and_never_changes_ordinal(self):
        p = self.run_artifact("one"); ledger.record(self.state, self.root, p, META)
        ledger.annotate(self.state, p, {**META, "commit": "abcdef0123456789"})
        entry = next(iter(self.read()["rounds"].values()))
        self.assertEqual(entry["ordinal"], 1)
        self.assertEqual(len(entry["improvement_history"]), 1)

    def test_failed_atomic_replace_preserves_old_state(self):
        p = self.run_artifact("one"); ledger.record(self.state, self.root, p, META)
        before = self.state.read_bytes()
        with patch.object(ledger.os, "replace", side_effect=OSError("injected replace failure")):
            with self.assertRaises(OSError): ledger.annotate(self.state, p, META)
        self.assertEqual(before, self.state.read_bytes())
        self.assertEqual(list(self.state.parent.glob(".learning-*")), [])

    def test_concurrent_processes_assign_one_ordinal_per_unique_run(self):
        paths = [self.run_artifact(str(n)) for n in range(4)]
        script = Path(ledger.__file__)
        jobs = [subprocess.Popen([sys.executable, str(script), "--state-file", str(self.state),
                "--runs-root", str(self.root), "record", "--run-dir", str(p)],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) for p in paths * 3]
        for job in jobs:
            stdout, stderr = job.communicate(timeout=20)
            self.assertEqual(job.returncode, 0, stderr)
        state = self.read()
        self.assertEqual(len(state["rounds"]), 4)
        self.assertEqual(sorted(r["ordinal"] for r in state["rounds"].values()), [1, 2, 3, 4])
        self.assertEqual(ledger.summary(state)["review_due"], [1])

    def test_corrupt_or_other_schema_is_not_overwritten(self):
        self.state.parent.mkdir(); self.state.write_text('{"schema_version":99}')
        before = self.state.read_bytes()
        with self.assertRaises(ValueError): ledger.initialize(self.state, self.root)
        self.assertEqual(before, self.state.read_bytes())

    def test_manual_maintenance_does_not_count_a_round(self):
        ledger.maintenance(self.state, "bootstrap", META)
        ledger.maintenance(self.state, "bootstrap", META)
        self.assertEqual(len(self.read()["maintenance"]), 1)
        self.assertEqual(ledger.summary(self.read())["counted_rounds"], 0)


if __name__ == "__main__":
    unittest.main()
