package state

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

// TestCoverageSlice15StaticEgressIPSet drives the new
// SetStaticEgressIP branch in memstore.go::UpdateApp (ADR-119). The
// Set-bit convention is identical to SetOverflowNode / SetCORS*:
// SetStaticEgressIP=true with a non-nil pointer writes the IP and
// stamps SetAt; SetStaticEgressIP=true with nil clears both.
func TestCoverageSlice15StaticEgressIPSet(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)

	ip := netip.MustParseAddr("203.0.113.42")
	updated, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    &ip,
	})
	if err != nil {
		t.Fatalf("UpdateApp set static: %v", err)
	}
	if updated.StaticEgressIP == nil {
		t.Fatal("StaticEgressIP nil after set")
	}
	if updated.StaticEgressIP.String() != ip.String() {
		t.Errorf("StaticEgressIP = %s, want %s", updated.StaticEgressIP, ip)
	}
	if updated.StaticEgressIPSetAt == nil {
		t.Error("StaticEgressIPSetAt nil after set")
	}
}

// TestCoverageSlice15StaticEgressIPClear drives the nil-pointer
// clears-both-columns branch. Used by the DELETE wire shape.
func TestCoverageSlice15StaticEgressIPClear(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)

	ip := netip.MustParseAddr("203.0.113.42")
	if _, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    &ip,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	updated, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    nil,
	})
	if err != nil {
		t.Fatalf("UpdateApp clear static: %v", err)
	}
	if updated.StaticEgressIP != nil {
		t.Errorf("StaticEgressIP = %s after clear, want nil", updated.StaticEgressIP)
	}
	if updated.StaticEgressIPSetAt != nil {
		t.Errorf("StaticEgressIPSetAt = %s after clear, want nil", updated.StaticEgressIPSetAt)
	}
}

// TestCoverageSlice15StaticEgressIPNoTouch drives the
// SetStaticEgressIP=false (don't touch) branch. The columns must
// remain at their pre-PATCH values.
func TestCoverageSlice15StaticEgressIPNoTouch(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)

	ip := netip.MustParseAddr("203.0.113.42")
	if _, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    &ip,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, err := m.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}

	// PATCH with SetStaticEgressIP=false (the default-zero path).
	other := netip.MustParseAddr("198.51.100.7")
	if _, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{
		StaticEgressIP: &other, // ignored when Set is false
	}); err != nil {
		t.Fatalf("UpdateApp no-touch: %v", err)
	}
	after, err := m.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if after.StaticEgressIP == nil || after.StaticEgressIP.String() != before.StaticEgressIP.String() {
		t.Errorf("StaticEgressIP changed during no-touch PATCH: %s vs %s", after.StaticEgressIP, before.StaticEgressIP)
	}
}

// TestCoverageSlice15StaticEgressIPDefaultIsZero pins the fixture
// invariant: a fresh app has StaticEgressIP=nil + SetAt=nil. The
// migration 00325 default is NULL on both columns, and the
// MemStore's CreateApp mirrors that.
func TestCoverageSlice15StaticEgressIPDefaultIsZero(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)
	got, err := m.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.StaticEgressIP != nil {
		t.Errorf("fresh StaticEgressIP = %s, want nil", got.StaticEgressIP)
	}
	if got.StaticEgressIPSetAt != nil {
		t.Errorf("fresh StaticEgressIPSetAt = %s, want nil", got.StaticEgressIPSetAt)
	}
}

// TestCoverageSlice15StaticEgressIPCrossAppConflict drives the
// same-account cross-app conflict branch in memstore.go::UpdateApp.
// Mirrors the pgstore's apps_static_egress_ip_key partial unique
// index — the apid handler branches on errors.Is(err, ErrConflict)
// and the index-name substring to return 403 plan_static_egress_ip_quota.
func TestCoverageSlice15StaticEgressIPCrossAppConflict(t *testing.T) {
	m, ctx, _, app, _ := memCoverageFixture(t)

	ip := netip.MustParseAddr("203.0.113.42")
	if _, err := m.UpdateApp(ctx, app.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    &ip,
	}); err != nil {
		t.Fatalf("first UpdateApp: %v", err)
	}

	// Second app on the same account tries to pin the same IP.
	second, err := m.CreateApp(ctx, App{
		AccountID: app.AccountID,
		Slug:      "second-app-" + app.Slug,
		RAMMB:     256,
		Status:    AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp second: %v", err)
	}
	_, err = m.UpdateApp(ctx, second.ID, UpdateAppParams{
		SetStaticEgressIP: true,
		StaticEgressIP:    &ip,
	})
	if err == nil {
		t.Fatal("expected ErrConflict on cross-app same-IP, got nil")
	}
	if !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
	if !strings.Contains(err.Error(), "apps_static_egress_ip_key") {
		t.Errorf("err %q missing index name (apId handler branch)", err.Error())
	}
}
