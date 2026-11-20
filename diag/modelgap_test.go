package diag

import (
	"strings"
	"testing"

	m "autoweight/model"
)

func TestModelGapFlags(t *testing.T) {
	rows := []m.Row{}
	for i := 0; i < 10; i++ {
		rows = append(rows, m.Row{Ch: 1, T: 100, Type: 2, Ut: 4, Mdl: "gpt-x"},
			m.Row{Ch: 2, T: 100, Type: 2, Ut: 20, Mdl: "gpt-x"},
			m.Row{Ch: 2, T: 100, Type: 2, Ut: 2, Mdl: "gpt-y"})
	}
	out := ModelGapFlags(rows, map[int]int{2: 5}, 0, 8, 2.5)
	if len(out) != 1 {
		t.Fatalf("want 1 flag for #2 gpt-x, got %v", out)
	}
	if len(ModelGapFlags(rows, nil, 0, 11, 2.5)) != 0 {
		t.Fatal("below min_n must not flag")
	}
	// same per-token speed, channel 2 just writes 5x longer answers
	rows = nil
	for i := 0; i < 10; i++ {
		rows = append(rows, m.Row{Ch: 1, Type: 2, Ut: 4, Ct: 100, Mdl: "gpt-x"},
			m.Row{Ch: 2, Type: 2, Ut: 20, Ct: 500, Mdl: "gpt-x"})
	}
	out = ModelGapFlags(rows, nil, 0, 8, 2.5)
	if len(out) != 1 || !strings.Contains(out[0], "负载差异") {
		t.Fatalf("want load-difference note, got %v", out)
	}
}
