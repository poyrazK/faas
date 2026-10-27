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
		fillInlineSlots(i)
		start := time.Now()
		res := i.CheckInline(attack)
		if res.Outcome != gateway.WAFInlineSkipped || res.SkipReason != gateway.WAFInlineSkipBusy {
			t.Fatalf("outcome = %s (%s), want skipped (busy)", res.Outcome, res.SkipReason)
		}
		// It waited for a slot before giving up (amendment 12).
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

// adr: 831 amendment 16 — one account's flood across its apps holds every
// check slot, yet another account's check is served the next freed slot
// ahead of the flood's own waiting checks, instead of failing open as busy.
func TestCheckInlineAccountShare(t *testing.T) {
	flood := withUA(sample(http.MethodGet, "/", nil, ""), "sqlmap/1.7.2")
	flood.AccountID = "acct-flood"
	other := flood
	other.AccountID, other.AppID = "acct-other", "app-other"
	i := New(&recordingObserver{}, nil)
	if _, err := i.inlineWAF(); err != nil {
		t.Fatal(err)
	}
	// Alone, the account takes every slot: the pool is work-conserving.
	for n := range i.InlineSlots() {
		if ok, _ := i.inlineSlots.acquire(flood.AccountID, 0); !ok {
			t.Fatalf("slot %d of %d refused to the only account using the pool", n+1, i.InlineSlots())
		}
	}
	// Its next check waits behind the full pool and is skipped as over its
	// share, not as a node capacity shortfall.
	if res := i.CheckInline(flood); res.Outcome != gateway.WAFInlineSkipped || res.SkipReason != gateway.WAFInlineSkipAccountShare {
		t.Fatalf("flood check on a full pool = %s (%s), want skipped (account_share)", res.Outcome, res.SkipReason)
	}

	i.inlineWait = time.Second
	floodRes, otherRes := make(chan gateway.WAFInlineResult, 1), make(chan gateway.WAFInlineResult, 1)
	go func() { floodRes <- i.CheckInline(flood) }()
	waitForWaiters(t, i.inlineSlots, 0, 1)
	go func() { otherRes <- i.CheckInline(other) }()
	waitForWaiters(t, i.inlineSlots, 1, 1)

	// The freed slot goes to the other account although the flood's check
	// queued first; the flood's check gets the slot the other one frees.
	i.inlineSlots.release(flood.AccountID)
	waitForWaiters(t, i.inlineSlots, 0, 1)
	if res := <-otherRes; res.Outcome != gateway.WAFInlineDetected {
		t.Fatalf("the other account's check = %s (%s), want detected on the first freed slot", res.Outcome, res.SkipReason)
	}
	if res := <-floodRes; res.Outcome != gateway.WAFInlineDetected {
		t.Fatalf("the flood's waiting check = %s (%s), want detected on the next freed slot", res.Outcome, res.SkipReason)
	}
}

// fillInlineSlots takes every free check slot for a tenant no sample uses.
func fillInlineSlots(i *Inspector) {
	for {
		if ok, _ := i.inlineSlots.acquire("test-filler", 0); !ok {
			return
		}
	}
}

// waitForWaiters waits until the pool has the given within-share and
// over-share waiters queued.
func waitForWaiters(t *testing.T, p *slotPool, within, over int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.mu.Lock()
		w, o := len(p.within), len(p.over)
		p.mu.Unlock()
		if w == within && o == over {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiters = %d within, %d over; want %d, %d", w, o, within, over)
		}
		time.Sleep(time.Millisecond)
	}
}

func withUA(s gateway.WAFSample, ua string) gateway.WAFSample {
	s.Header = s.Header.Clone()
	s.Header.Set("User-Agent", ua)
	return s
}

// adr: 831 amendment 12 — a check that finds every slot busy waits for one
// instead of failing open at once, and its seconds include the wait.
func TestCheckInlineWaitsForASlot(t *testing.T) {
	attack := withUA(sample(http.MethodGet, "/", nil, ""), "sqlmap/1.7.2")
	i := New(&recordingObserver{}, nil)
	if _, err := i.inlineWAF(); err != nil {
		t.Fatal(err)
	}
	// A generous wait keeps the test independent of scheduler latency.
	i.inlineWait = time.Second
	fillInlineSlots(i)
	const hold = 20 * time.Millisecond
	go func() {
		time.Sleep(hold)
		i.inlineSlots.release("test-filler")
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
	fillInlineSlots(i)
	start := time.Now()
	if res := i.CheckInline(attack); res.SkipReason != gateway.WAFInlineSkipBusy || time.Since(start) > hold {
		t.Errorf("no wait: %s (%s) after %v, want an immediate busy skip", res.Outcome, res.SkipReason, time.Since(start))
	}
}
