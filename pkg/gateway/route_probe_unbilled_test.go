// adr: 20261116120000
package gateway

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

// An unbilled health probe challenge validates like any route probe and is
// the only kind reported unbilled; a scoped or plain challenge never is.
func TestUnbilledRouteProbeChallenge(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	exp := time.Now().Add(time.Minute)
	b.AuthorizeUnbilledRouteProbe("app-1", "dep-1", "health-token", exp)
	b.AuthorizeRouteProbe("app-1", "dep-1", "canary-token", exp)
	b.AuthorizeRouteProbeForScope("app-1", "dep-1", "scoped-token", "staging", exp)
	if !b.ValidateRouteProbe("app-1", "dep-1", "health-token") || !b.RouteProbeUnbilled("app-1", "dep-1", "health-token") {
		t.Fatal("unbilled health probe challenge not valid and unbilled")
	}
	for _, token := range []string{"canary-token", "scoped-token", "forged", ""} {
		if b.RouteProbeUnbilled("app-1", "dep-1", token) {
			t.Fatalf("%q reported unbilled", token)
		}
	}
	if b.RouteProbeUnbilled("app-2", "dep-1", "health-token") {
		t.Fatal("health token unbilled for another app")
	}
	if b.ValidateDeploymentSmoke("app-1", "dep-1", "health-token") {
		t.Fatal("health probe token authorized the smoke bypass")
	}
}

// Only an unbilled probe's own wake reaches the ledger recorder.
func TestRecordPlatformProbeWakeOnlyForUnbilledProbes(t *testing.T) {
	type call struct{ app, instance string }
	var calls []call
	b := NewPGBackend(nil, nil, nil).WithPlatformProbeWakeRecorder(func(_ context.Context, appID, instanceID string, wokeAt, done time.Time) {
		if wokeAt.IsZero() || done.Before(wokeAt) {
			t.Errorf("bad window %v..%v", wokeAt, done)
		}
		calls = append(calls, call{appID, instanceID})
	})
	h := &Handler{backend: b}
	woke := time.Now().Add(-2 * time.Second)

	plain := httptest.NewRequest("GET", "/", nil)
	h.recordPlatformProbeWake(plain, "app-1", "i-1", woke)
	canary := plain.WithContext(withRouteProbe(plain.Context(), "dep-1", "t", false))
	h.recordPlatformProbeWake(canary, "app-1", "i-1", woke)
	health := plain.WithContext(withRouteProbe(plain.Context(), "dep-1", "t", true))
	h.recordPlatformProbeWake(health, "app-1", "", woke)
	h.recordPlatformProbeWake(health, "app-1", "i-1", time.Time{})
	if len(calls) != 0 {
		t.Fatalf("recorded %v for a customer request, a canary probe or an incomplete wake", calls)
	}
	h.recordPlatformProbeWake(health, "app-1", "i-1", woke)
	if len(calls) != 1 || calls[0] != (call{"app-1", "i-1"}) {
		t.Fatalf("calls = %v; want the health probe's wake", calls)
	}
}
