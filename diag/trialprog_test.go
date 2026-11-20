package diag

import (
	"strings"
	"testing"
)

func TestTrialProgFlag(t *testing.T) {
	cases := []struct {
		p    TrialProg
		want string
	}{
		{TrialProg{Ch: 28, N: 100, ElapsedH: 10}, "进行中"},
		{TrialProg{Ch: 28, N: 20, ElapsedH: 10}, "可能不足"},
		{TrialProg{Ch: 28, N: 100, Err: 3, ElapsedH: 10}, "错误率"},
		{TrialProg{Ch: 28, N: 100, Empty: 1, ElapsedH: 10}, "空回复，不应加档"},
		{TrialProg{Ch: 28, N: 350, ElapsedH: 48.5}, "已达标"},
		{TrialProg{Ch: 28, N: 200, ElapsedH: 49}, "继续保持"},
	}
	for _, c := range cases {
		if s := TrialProgFlag(c.p, 48, 300, 0.01); !strings.Contains(s, c.want) {
			t.Errorf("%+v: %s", c.p, s)
		}
	}
}

func TestTrialDemandFlag(t *testing.T) {
	if s := TrialDemandFlag(28, 60, 51, 0, 1, 0, 3, 470, 2, 31); !strings.Contains(s, "会话亲和") || strings.Contains(s, "可留意") {
		t.Fatal(s)
	}
	if s := TrialDemandFlag(28, 60, 3, 0, 3, 0, 1, 80, 2, 38); !strings.Contains(s, "低谷期") {
		t.Error(s)
	}
	if s := TrialDemandFlag(28, 60, 200, 1, 100, 1, 9, 5, 2, 20); !strings.Contains(s, "可留意") {
		t.Error(s)
	}
	if s := TrialDemandFlag(28, 60, 200, 20, 100, 10, 9, 5, 2, 20); !strings.Contains(s, "相当") {
		t.Error(s)
	}
}
