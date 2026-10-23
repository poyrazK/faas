package edgewaf

import (
	"net/http"
	"slices"
	"testing"
	"time"

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
		for range i.InlineSlots() {
			i.inlineSlots <- struct{}{}
		}
		start := time.Now()
		res := i.CheckInline(attack)
		if res.Outcome != gateway.WAFInlineSkipped || res.SkipReason != gateway.WAFInlineSkipBusy {
			t.Fatalf("outcome = %s (%s), want skipped (busy)", res.Outcome, res.SkipReason)
		}
		// It waited for a slot before giving up (amendment 11).
		if waited := time.Since(start); waited < api.EdgeWAFInlineSlotWait {
			t.Errorf("skipped after %v, want at least the %v slot wait", waited, api.EdgeWAFInlineSlotWait)
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
		if res := i.CheckInline(attack); res.Outcome != gateway.WAFInlineSkipped || res.SkipReason != gateway.WAFInlineSkipBudget {
			t.Fatalf("outcome = %s (%s), want skipped (budget)", res.Outcome, res.SkipReason)
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

// adr: 831 amendment 11 — a check that finds every slot busy waits for one
// instead of failing open at once, and its seconds include the wait.
func TestCheckInlineWaitsForASlot(t *testing.T) {
	attack := withUA(sample(http.MethodGet, "/", nil, ""), "sqlmap/1.7.2")
	i := New(&recordingObserver{}, nil)
	if _, err := i.inlineWAF(); err != nil {
		t.Fatal(err)
	}
	// A generous wait keeps the test independent of scheduler latency.
	i.inlineWait = time.Second
	for range i.InlineSlots() {
		i.inlineSlots <- struct{}{}
	}
	const hold = 20 * time.Millisecond
	go func() {
		time.Sleep(hold)
		<-i.inlineSlots
	}()
	res := i.CheckInline(attack)
	if res.Outcome != gateway.WAFInlineDetected {
		t.Fatalf("outcome = %s (%s), want detected once a slot freed", res.Outcome, res.SkipReason)
	}
	if res.Seconds < hold.Seconds() {
		t.Errorf("seconds = %v, want the wait for the slot (%v) included", res.Seconds, hold)
	}
	// The app is charged the check, not the wait.
	if spent := api.EdgeWAFInlineMsPerAppBurst - i.inlineBudgets.m[attack.AppID].balance; spent >= float64(hold/time.Millisecond) {
		t.Errorf("budget charged %.1f ms, want the check's time without the %v wait", spent, hold)
	}

	i.inlineWait = 0
	for len(i.inlineSlots) < i.InlineSlots() {
		i.inlineSlots <- struct{}{}
	}
	start := time.Now()
	if res := i.CheckInline(attack); res.SkipReason != gateway.WAFInlineSkipBusy || time.Since(start) > hold {
		t.Errorf("no wait: %s (%s) after %v, want an immediate busy skip", res.Outcome, res.SkipReason, time.Since(start))
	}
}
