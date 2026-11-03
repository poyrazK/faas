package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// One anonymous client could wake ~130 different public parked apps and fill
// the gateway's wake admission queue for everyone (bug hunt H75). A client
// network may now wake only GatewayColdWakeAppsPerClient different apps a
// window; more requests to an app it already woke still pass, and platform
// (private) callers are not counted.
func TestColdWakeClientBudgetLimitsDistinctAppsPerNetwork(t *testing.T) {
	b := newColdWakeClientBudget()
	client := func(xff string) string {
		r := httptest.NewRequest("GET", "http://app.example/", nil)
		r.Header.Set("X-Forwarded-For", xff)
		return coldWakeClientKey(r)
	}
	sweeper := client("203.0.113.9")
	for i := range api.GatewayColdWakeAppsPerClient {
		if ok, _ := b.allow(sweeper, fmt.Sprintf("app-%d", i)); !ok {
			t.Fatalf("wake %d refused inside the budget", i)
		}
	}
	ok, wait := b.allow(client("203.0.113.77"), "one-more")
	if ok || wait <= 0 || wait > api.GatewayColdWakeClientWindow {
		t.Fatalf("wake past the budget from the same /24: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := b.allow(sweeper, "app-0"); !ok {
		t.Fatal("a repeat wake of an app the client already woke was refused")
	}
	if ok, _ := b.allow(client("198.51.100.4"), "one-more"); !ok {
		t.Fatal("another network was refused")
	}
	if key := client("10.0.0.5"); key != "" {
		t.Fatalf("private caller charged to %q", key)
	}
	b.now = func() time.Time { return time.Now().Add(api.GatewayColdWakeClientWindow) }
	if ok, _ := b.allow(sweeper, "one-more"); !ok {
		t.Fatal("budget did not recover after the window")
	}
}

// One account could hold every wake admission slot and queue entry. It now
// runs at most GatewayWakeAdmissionRunningPerAccount at once, while another
// account's wake takes a free slot at once, and it may queue at most
// GatewayWakeAdmissionQueuedPerAccount more.
func TestWakeAdmissionQueueAccountShare(t *testing.T) {
	q := newWakeAdmissionQueue(4, 128, nil)
	policy := WakeAdmissionPolicyForPlan(api.PlanPro)
	release := make(chan struct{})
	started := make(chan string, 64)
	run := func(app, owner string) chan error {
		done := make(chan error, 1)
		go func() {
			_, _, err := q.Do(context.Background(), app, owner, "pro", policy, func(context.Context) error {
				started <- app
				<-release
				return nil
			})
			done <- err
		}()
		return done
	}
	for i := range api.GatewayWakeAdmissionRunningPerAccount {
		run(fmt.Sprintf("a-%d", i), "acct-a")
		<-started
	}
	for i := range api.GatewayWakeAdmissionQueuedPerAccount {
		run(fmt.Sprintf("a-wait-%d", i), "acct-a")
	}
	waitFor(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return q.queuedByOwner["acct-a"] == api.GatewayWakeAdmissionQueuedPerAccount
	})
	select {
	case app := <-started:
		t.Fatalf("%s started past the account's running share", app)
	default:
	}
	run("b-0", "acct-b")
	select {
	case app := <-started:
		if app != "b-0" {
			t.Fatalf("started %s, want the other account's wake", app)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("another account's wake waited behind a capped account")
	}
	_, _, err := q.Do(context.Background(), "a-over", "acct-a", "pro", policy, func(context.Context) error { return nil })
	if !errors.Is(err, ErrWakeAdmissionQueueFull) {
		t.Fatalf("account past its queued share: err=%v, want queue full", err)
	}
	close(release)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
