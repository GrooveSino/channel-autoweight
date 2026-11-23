package regress

import (
	"reflect"
	"testing"
)

// Independent expectations: 128 completed requests per channel, all slow on
// 102 but no channel errors. The old finalizer therefore retains both twenties.
func TestSyntheticRound(t *testing.T) {
	for _, sets := range [][]string{nil, {"score.log_ttft.thin_pool_k=20", "score.empty_weight=2"}, {"toggle.skip_dormant_base=true"}} {
		out, _, err := GoEval("../testdata/paired", sets...)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(out.Target, map[string]int{"101": 20, "102": 20}) || out.Scores["101"] <= out.Scores["102"] {
			t.Fatal(out)
		}
	}
}
func TestProbeOnlyExemption(t *testing.T) {
	for _, v := range []struct {
		sets    []string
		actions int
	}{{nil, 0}, {[]string{"toggle.err_real_only=false"}, 0}, {[]string{"toggle.err_real_only=false", "toggle.acct_disable.exempt=[]"}, 1}} {
		out, _, err := GoEval("../testdata/plumbing", v.sets...)
		if err != nil {
			t.Fatal(err)
		}
		if out.Toggle == nil || len(out.Toggle.Actions) != v.actions {
			t.Fatal(out.Toggle)
		}
		if v.actions == 1 && out.Toggle.Actions[0].ID != 26 {
			t.Fatal(out.Toggle.Actions)
		}
	}
}
