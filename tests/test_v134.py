import unittest, json, copy, os
from autoweight import diag, decide

B = {"2": [{"ok": True, "ttft": 7.0, "streaming": False}]}
S = {"2": [{"ok": True, "ttft": 4.0, "streaming": True}]}
HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

class Buffered(unittest.TestCase):
    def test_reused_rounds_not_double_counted(self):
        hist = [{"probes": S}, {"probes": B}, {"probes": B, "probes_reused": True}, {"probes": B}, {"probes": B, "probes_reused": True}]
        self.assertEqual(diag.buffered_streak(hist, B, [2]), {2: 3})              # dry run with a fresh probe
        self.assertEqual(diag.buffered_streak(hist[:-1], B, [2], True), {2: 2})   # formal round reusing the probe: not counted again

    def test_old_behavior_without_reuse(self):
        hist = [{"probes": B}, {"probes": B}]
        self.assertEqual(diag.buffered_streak(hist, B, [2]), {2: 3})

    def test_recovering_ignores_reused_copy(self):
        hist = [{"probes": B}, {"probes": B, "probes_reused": True}]
        self.assertEqual(diag.recovering(hist, S, [2], 2), {})    # only 1 real buffered round < 2
        hist2 = [{"probes": B}, {"probes": B}, {"probes": B, "probes_reused": True}]
        self.assertEqual(diag.recovering(hist2, S, [2], 2), {2: 2})

class ConfirmRise(unittest.TestCase):
    def test_drop_immediate(self):
        self.assertEqual(decide.confirm_rise([{5: 6}, {5: 6}, {5: 1}], {5: 6}, 3), {5: 1})

    def test_rise_needs_confirmation(self):
        self.assertEqual(decide.confirm_rise([{5: 1}, {5: 3}, {5: 6}], {5: 1}, 3), {5: 1})
        self.assertEqual(decide.confirm_rise([{5: 3}, {5: 3}, {5: 6}], {5: 1}, 3), {5: 3})
        self.assertEqual(decide.confirm_rise([{5: 6}, {5: 6}, {5: 6}], {5: 3}, 3), {5: 6})

    def test_n1_identity_and_new_channel(self):
        self.assertEqual(decide.confirm_rise([{5: 1}, {5: 12}], {5: 1}, 1), {5: 12})
        self.assertEqual(decide.confirm_rise([{5: 1}, {5: 3, 9: 12}], {5: 3}, 3), {5: 3, 9: 12})


if __name__ == "__main__":
    unittest.main()
