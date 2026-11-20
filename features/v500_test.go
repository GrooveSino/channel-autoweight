package features

import (
	"testing"

	m "autoweight/model"
)

// v5.00 rule 1: client-side failures (adaptive thinking, client disconnect,
// signature 400, unsupported parameter) are not channel faults.
func TestClientContentNotFault(t *testing.T) {
	cfg := m.M{"probe_token_ids": []any{1318.0}}
	var rows []m.Row
	for k := 0; k < 50; k++ {
		rows = append(rows, m.Row{Ch: 72, T: int64(k), Type: 2, Tok: 10 + k%5})
	}
	rows = append(rows,
		m.Row{Ch: 72, T: 60, Type: 5, Tok: 2434, Cl: 1}, // Client connection closed
		m.Row{Ch: 72, T: 61, Type: 5, Tok: 2434, Cl: 1},
		m.Row{Ch: 72, T: 62, Type: 5, Tok: 3027, Cl: 1},
		m.Row{Ch: 72, T: 63, Type: 5, Tok: 1438, To: 1}, // one real timeout
	)
	s := ChanStats(cfg, rows, 0, 2, 0.8)[72]
	if s.Client != 3 || s.Err != 1 || s.Eff != 1 {
		t.Fatalf("got %+v", *s)
	}
	if !IsClientErr(m.Row{Type: 5, Cl: 1}) {
		t.Error("Cl row not a client error")
	}
	if IsClientErr(m.Row{Type: 5, To: 1}) || IsClientErr(m.Row{Type: 2, Cl: 1}) {
		t.Error("timeout / success counted as client error")
	}
}

// 404 model_not_found is a client error only when one token holds it.
func TestNotFoundSingleToken(t *testing.T) {
	cfg := m.M{}
	ok := func(ch int) []m.Row {
		var r []m.Row
		for k := 0; k < 40; k++ {
			r = append(r, m.Row{Ch: ch, T: int64(k), Type: 2, Tok: 1 + k%4})
		}
		return r
	}
	rows := append(ok(73), m.Row{Ch: 73, T: 50, Type: 5, Tok: 9, Nf: 1}, m.Row{Ch: 73, T: 51, Type: 5, Tok: 9, Nf: 1},
		m.Row{Ch: 73, T: 52, Type: 5, Tok: 9, Nf: 1})
	if s := ChanStats(cfg, rows, 0, 2, 0.8)[73]; s.Client != 3 || s.Err != 0 {
		t.Fatalf("single-token 404 counted: %+v", *s)
	}
	rows = append(ok(73), m.Row{Ch: 73, T: 50, Type: 5, Tok: 7, Nf: 1}, m.Row{Ch: 73, T: 51, Type: 5, Tok: 8, Nf: 1},
		m.Row{Ch: 73, T: 52, Type: 5, Tok: 9, Nf: 1})
	if s := ChanStats(cfg, rows, 0, 2, 0.8)[73]; s.Err != 3 {
		t.Fatalf("spread 404 not counted: %+v", *s)
	}
}

// v5.00 rule 3: timeouts concentrated on one token are discounted like other
// single-token errors; 429 / concurrency limits still always count.
func TestTimeoutSingleTokenDiscount(t *testing.T) {
	cfg := m.M{}
	sc := func(c int) *int { return &c }
	var rows []m.Row
	for k := 0; k < 60; k++ {
		rows = append(rows, m.Row{Ch: 68, T: int64(k), Type: 2, Tok: 1 + k%6})
	}
	for k := 0; k < 5; k++ {
		rows = append(rows, m.Row{Ch: 68, T: int64(70 + k), Type: 5, Tok: 1438, To: 1})
	}
	rows = append(rows, m.Row{Ch: 68, T: 80, Type: 5, Tok: 1438, Sc: sc(524), To: 1})
	if s := ChanStats(cfg, rows, 0, 2, 0.8)[68]; s.Err != 6 || s.Eff != 2 || s.TopTok != 1438 {
		t.Fatalf("single-token timeouts not discounted: %+v", *s)
	}
	for k := 0; k < 4; k++ {
		rows = append(rows, m.Row{Ch: 68, T: int64(90 + k), Type: 5, Tok: 1438, Sc: sc(429)})
	}
	rows = append(rows, m.Row{Ch: 68, T: 95, Type: 5, Tok: 1438, Cap: 1})
	if s := ChanStats(cfg, rows, 0, 2, 0.8)[68]; s.Eff != 7 {
		t.Fatalf("429/concurrency discounted: %+v", *s)
	}
}
