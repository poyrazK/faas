package edgewaf

import (
	"net/http"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestCheckInline(t *testing.T) {
	i := New(&recordingObserver{}, nil)
	for _, tc := range []struct {
		name     string
		s        gateway.WAFSample
		want     string
		wantRule int
	}{
		{name: "scanner user agent", s: withUA(sample(http.MethodGet, "/api/items?id=1", nil, ""), "sqlmap/1.7.2#stable (https://sqlmap.org)"),
			want: gateway.WAFInlineDetected, wantRule: 913100},
		{name: "CRLF injection in query", s: sample(http.MethodGet, "/login?next=%0d%0aSet-Cookie:%20session=attacker", nil, ""),
			want: gateway.WAFInlineDetected, wantRule: 921160},
		{name: "SQLi in query", s: sample(http.MethodGet, "/api/items?q=1%27%20OR%201%3D1--", nil, ""),
			want: gateway.WAFInlineDetected, wantRule: 942100},
		{name: "benign request", s: sample(http.MethodGet, "/api/orders?page=2&sort=created", nil, ""),
			want: gateway.WAFInlineClean},
		// Bodies are never checked in-path, even when the sample carries one.
		{name: "attack only in body", s: sample(http.MethodPost, "/api/comments", nil, `{"text":"<script>alert(document.cookie)</script>"}`),
			want: gateway.WAFInlineClean},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := i.CheckInline(tc.s)
			if res.Outcome != tc.want {
				t.Fatalf("outcome = %s (rules %v), want %s", res.Outcome, res.RuleIDs, tc.want)
			}
			if tc.wantRule != 0 && !slices.Contains(res.RuleIDs, tc.wantRule) {
				t.Errorf("rules = %v, want %d", res.RuleIDs, tc.wantRule)
			}
			if res.Seconds <= 0 {
				t.Errorf("seconds = %v, want the measured check time", res.Seconds)
			}
		})
	}
}

func TestCheckInlineFailsOpen(t *testing.T) {
	attack := withUA(sample(http.MethodGet, "/", nil, ""), "sqlmap/1.7.2")

	t.Run("no free slot", func(t *testing.T) {
		i := New(&recordingObserver{}, nil)
		for range api.EdgeWAFInlineConcurrency {
			i.inlineSlots <- struct{}{}
		}
		if res := i.CheckInline(attack); res.Outcome != gateway.WAFInlineSkipped {
			t.Fatalf("outcome = %s, want skipped", res.Outcome)
		}
		// The skipped check is refunded to the app's budget.
		if b := i.inlineBudgets.m[attack.AppID].balance; b != api.EdgeWAFInlineMsPerAppBurst {
			t.Errorf("balance = %.1f, want the full burst back", b)
		}
	})

	t.Run("app budget exhausted", func(t *testing.T) {
		i, _ := newClockedInspector(&recordingObserver{})
		for i.admitInline(attack.AppID) {
		}
		if res := i.CheckInline(attack); res.Outcome != gateway.WAFInlineSkipped {
			t.Fatalf("outcome = %s, want skipped", res.Outcome)
		}
		other := attack
		other.AppID = "app-2"
		if res := i.CheckInline(other); res.Outcome != gateway.WAFInlineDetected {
			t.Errorf("another app's check = %s, want detected (budgets are per app)", res.Outcome)
		}
	})
}

func withUA(s gateway.WAFSample, ua string) gateway.WAFSample {
	s.Header = s.Header.Clone()
	s.Header.Set("User-Agent", ua)
	return s
}
