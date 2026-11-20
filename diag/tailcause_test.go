package diag

import (
	"strings"
	"testing"

	m "autoweight/model"
)

func TestTailCauseLoadVsSlow(t *testing.T) {
	load, slow := []m.Row{}, []m.Row{}
	for i := 0; i < 18; i++ {
		load = append(load, m.Row{Ch: 1, T: 10, Type: 2, Ut: 5, Ct: 500, Mdl: "a"})
		slow = append(slow, m.Row{Ch: 1, T: 10, Type: 2, Ut: 5, Ct: 500, Mdl: "a"})
	}
	for i := 0; i < 2; i++ {
		load = append(load, m.Row{Ch: 1, T: 10, Type: 2, Ut: 50, Ct: 5000, Mdl: "b"})
		slow = append(slow, m.Row{Ch: 1, T: 10, Type: 2, Ut: 50, Ct: 500, Mdl: "b"})
	}
	if got := TailCause(load, 1, 0); !strings.Contains(got, "请求负载") || !strings.Contains(got, "b 占 2") {
		t.Fatalf("load: %s", got)
	}
	if got := TailCause(slow, 1, 0); !strings.Contains(got, "渠道排队") {
		t.Fatalf("slow: %s", got)
	}
	hung := append([]m.Row{}, slow[:18]...)
	hung = append(hung, m.Row{Ch: 1, T: 10, Type: 2, Ut: 170}, m.Row{Ch: 1, T: 10, Type: 2, Ut: 180})
	if got := TailCause(hung, 1, 0); !strings.Contains(got, "0 输出") {
		t.Fatalf("hung: %s", got)
	}
	if TailCause(load[:10], 1, 0) != "" {
		t.Fatal("small sample should be empty")
	}
}
