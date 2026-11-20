package features

import (
	"testing"

	m "autoweight/model"
)

// v5.01: a transmission failure in the [ReverixAI] format (Row.Df) that the same
// token hit on another channel is cross-channel; Uf/Ec without status code are client.
func TestV501ErrorFormats(t *testing.T) {
	var rows []m.Row
	for k := 0; k < 40; k++ {
		rows = append(rows, m.Row{Ch: 73, T: int64(k), Type: 2, Tok: 10 + k%4}, m.Row{Ch: 26, T: int64(k), Type: 2, Tok: 10 + k%4})
	}
	rows = append(rows,
		m.Row{Ch: 73, T: 100, Type: 5, Tok: 7, Df: 1},
		m.Row{Ch: 26, T: 101, Type: 5, Tok: 7, Df: 1},
		m.Row{Ch: 73, T: 300, Type: 5, Tok: 8, Df: 1}, // alone: the channel's fault
		m.Row{Ch: 73, T: 400, Type: 5, Tok: 9, Uf: 1},
		m.Row{Ch: 73, T: 401, Type: 5, Tok: 9, Ec: 1},
	)
	s := ChanStats(m.M{}, rows, 0, 2, 0.8)[73]
	if s.Client != 3 || s.Err != 1 {
		t.Fatalf("got %+v", *s)
	}
}

func TestPairOf(t *testing.T) {
	cfg := m.M{}
	var rows []m.Row
	for tok := 1; tok <= 4; tok++ {
		for k := 0; k < 5; k++ {
			rows = append(rows,
				m.Row{Ch: 78, T: 100, Type: 2, Tok: tok, Ct: 600, Ut: 40, Mdl: "gpt-x"},
				m.Row{Ch: 26, T: 100, Type: 2, Tok: tok, Ct: 600, Ut: 20, Mdl: "gpt-x"},
				m.Row{Ch: 26, T: 100, Type: 2, Tok: tok, Ct: 100, Ut: 2, Mdl: "gpt-x"}, // other bucket, unpaired
			)
		}
	}
	p := PairOf(cfg, rows, 200, 60)
	if p[78] == nil || p[78].Ratio != 2 || p[78].Tokens != 4 || p[78].N != 20 {
		t.Fatalf("78: %+v", p[78])
	}
	if p[26] == nil || p[26].Ratio != 0.5 {
		t.Fatalf("26: %+v", p[26])
	}
	if len(PairOf(cfg, rows, 200+3600, 60)) != 0 {
		t.Fatal("rows outside the window paired")
	}
}
