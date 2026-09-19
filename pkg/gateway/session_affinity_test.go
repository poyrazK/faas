package gateway

// spec: §4.9 — request routing remains fail-open when a preferred instance is unavailable.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionAffinityCookieRoundTripAndAppBinding(t *testing.T) {
	h := NewHandlerWith(nil, nil, nil)
	value, err := h.sessionAffinityCookieValue("app-1", "instance-1")
	if err != nil {
		t.Fatalf("sessionAffinityCookieValue: %v", err)
	}
	r := httptest.NewRequest("GET", "https://app.example/", nil)
	r.AddCookie(&http.Cookie{Name: sessionAffinityCookieName, Value: value})
	if got := h.affinityTargetFromRequest(r, "app-1"); got != "instance-1" {
		t.Fatalf("affinityTargetFromRequest = %q, want instance-1", got)
	}
	if got := h.affinityTargetFromRequest(r, "app-2"); got != "" {
		t.Fatalf("cross-app cookie accepted as %q", got)
	}
}

func TestPGBackendPickForInstanceFallsBackWhenStale(t *testing.T) {
	sched := NewFakeScheduler("node-1")
	b := NewPGBackend(nil, sched, nil)
	if _, _, _, err := b.Admit(context.Background(), "app-1", "", "", "", 3); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	preferred := b.PickForInstance("app-1", "i-1")
	if !preferred.OK || preferred.Target.InstanceID != "i-1" {
		t.Fatalf("preferred pick = %+v, want i-1", preferred)
	}
	fallback := b.PickForInstance("app-1", "gone")
	if !fallback.OK || fallback.Target.InstanceID != "i-1" {
		t.Fatalf("stale fallback = %+v, want i-1", fallback)
	}
}
