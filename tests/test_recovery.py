import os, sys, unittest, json, copy
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from autoweight import diag, decide

def P(stream, ok=True):
    return [dict(ok=ok, streaming=stream, ttft=3.0)]

CFG = json.load(open(os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "tests/fixtures/config.test.json")))

def W(n, p50, p90):
    return dict(n=n, err=0, err_rate=0.0, p50=p50, p90=p90, slow_rate=0.0, empty_rate=0.0)

class T(unittest.TestCase):
    def test_recovering_detect(self):
        hist = [dict(probes={"2": P(False)}), dict(probes={"2": P(False)})]
        self.assertEqual(diag.recovering(hist, {"2": P(True)}, [2], 2), {2: 2})
        # buffered for only 1 round: no trigger
        self.assertEqual(diag.recovering(hist[-1:], {"2": P(True)}, [2], 2), {})
        # still buffered this round: no trigger
        self.assertEqual(diag.recovering(hist, {"2": P(False)}, [2], 2), {})
        # exits automatically on the second round after recovering
        h2 = hist + [dict(probes={"2": P(True)})]
        self.assertEqual(diag.recovering(h2, {"2": P(True)}, [2], 2), {})
        self.assertEqual(diag.recovering([], {"2": P(True)}, [2], 2), {})

    def test_decide_caps_recovering(self):
        cfg = copy.deepcopy(CFG); cfg["soft_caps"] = {}; cfg["recovery"]["enabled"] = True
        chans = [dict(id=1), dict(id=2)]
        feats = {1: {"60m": W(100, 10, 20), "10m": W(10, 10, 20)}, 2: {"60m": W(100, 9, 19), "10m": W(10, 9, 19)}}
        probes = {1: P(True), 2: P(True)}
        base = decide.decide(cfg, chans, feats, probes)["target"]
        self.assertGreater(base[2], cfg["recovery"]["cap"])
        d = decide.decide(cfg, chans, feats, probes, recovering={2: 6})
        self.assertEqual(d["target"][2], cfg["recovery"]["cap"])
        self.assertTrue(any("just resumed streaming" in n for n in d["notes"]))
        cfg["recovery"]["enabled"] = False
        self.assertEqual(decide.decide(cfg, chans, feats, probes, recovering={2: 6})["target"], base)

if __name__ == "__main__":
    unittest.main()
