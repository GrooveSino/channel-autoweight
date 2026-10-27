import os, sys, unittest
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from autoweight import features

def row(t, typ, sc=None, ut=10):
    d = dict(ch=1, t=t, type=typ, ut=ut, ct=200, tok=0)
    if sc is not None: d["sc"] = sc
    return d

class T(unittest.TestCase):
    def test_is_client_err(self):
        self.assertTrue(features.is_client_err(row(1, 5, 413)))
        self.assertTrue(features.is_client_err(row(1, 5, 400)))
        self.assertFalse(features.is_client_err(row(1, 5, 429)))
        self.assertFalse(features.is_client_err(row(1, 5, 408)))
        self.assertFalse(features.is_client_err(row(1, 5, 524)))
        self.assertFalse(features.is_client_err(row(1, 5)))
        self.assertFalse(features.is_client_err(row(1, 2, 400)))

    def test_window_and_drop(self):
        rows = [row(100 + i, 2) for i in range(8)] + [row(110, 5, 413), row(111, 5, 413), row(112, 5, 502)]
        w = features.window(rows, 200, 60, 60)
        self.assertEqual((w["n"], w["err"], w["err_client"]), (11, 3, 2))
        f = features.drop_client_errors({1: {"60m": dict(w)}})
        x = f[1]["60m"]
        self.assertEqual((x["n"], x["err"], x["err_raw"], x["n_raw"]), (9, 1, 3, 11))
        self.assertAlmostEqual(x["err_rate"], round(1 / 9, 4))
        # idempotent: a second call does not deduct again
        features.drop_client_errors(f)
        self.assertEqual(f[1]["60m"]["n"], 9)

    def test_old_records_untouched(self):
        w = dict(n=10, err=2, err_rate=0.2)
        f = features.drop_client_errors({1: {"60m": w}})
        self.assertEqual(f[1]["60m"]["err"], 2)

if __name__ == "__main__": unittest.main()
