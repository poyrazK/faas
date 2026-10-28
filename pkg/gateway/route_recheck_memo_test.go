package gateway

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A warm custom-domain route re-read the registry on every request. The
// memo keeps a successful recheck for GatewayRouteRecheckInterval, per host
// and app, and a flush drops it.
func TestRouteRecheckMemo(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	m := newRouteRecheckMemo()
	m.now = func() time.Time { return now }

	if _, ok := m.domain("shop.example.com", "app-1"); ok {
		t.Fatal("empty memo answered")
	}
	m.rememberDomain("shop.example.com", "app-1", true)
	if active, ok := m.domain("shop.example.com", "app-1"); !ok || !active {
		t.Fatalf("fresh recheck = %v, %v", active, ok)
	}
	if _, ok := m.domain("shop.example.com", "app-2"); ok {
		t.Fatal("a recheck for another app answered")
	}
	m.rememberTenant("shop.example.com", PlatformTenantHostBinding{TenantID: "t1", Active: true}, true)
	if b, found, ok := m.tenant("shop.example.com"); !ok || !found || b.TenantID != "t1" {
		t.Fatalf("fresh tenant recheck = %+v, %v, %v", b, found, ok)
	}

	now = now.Add(api.GatewayRouteRecheckInterval)
	if _, ok := m.domain("shop.example.com", "app-1"); ok {
		t.Fatal("a recheck older than the interval answered")
	}

	m.rememberDomain("shop.example.com", "app-1", true)
	m.reset()
	if _, ok := m.domain("shop.example.com", "app-1"); ok {
		t.Fatal("a flushed recheck answered")
	}
}
