package core

import (
	"testing"
	"time"

	m "autoweight/model"
)

// Duplicate records per round must not shrink the established-channel lookback.
func TestWithinKeepsTimeWindow(t *testing.T) {
	now := time.Unix(1793571180, 0)
	rs := []*m.Rec{}
	for _, ago := range []int{400, 300, 200, 200, 200, 90, 90, 45, 45, 45, 0} {
		rs = append(rs, &m.Rec{TsTime: now.Add(-time.Duration(ago) * time.Minute)})
	}
	if got := len(within(rs, now, 6*time.Hour)); got != 10 {
		t.Fatalf("within 6h = %d, want 10", got)
	}
	if got := len(tail(rs, 8)); got != 8 {
		t.Fatalf("tail = %d", got)
	}
}
