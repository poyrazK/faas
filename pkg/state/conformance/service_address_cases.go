package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testServiceAddressIndexAllocation pins the ADR-576 address contract: every
// live app holds a distinct index within its account, a lookup never crosses
// an account or reaches a tombstone, a deleted app's index is not handed to a
// new app, and a restore brings the original address back.
func testServiceAddressIndexAllocation(t *testing.T, fx *Fixture) {
	limits := api.MustLimitsFor(api.PlanPro)
	newApp := func(accountID, prefix string) state.App {
		t.Helper()
		app, err := fx.Store.CreateAppIfUnderQuota(fx.Ctx, state.App{
			AccountID: accountID, Slug: prefix + "-" + uuid.NewString()[:8],
			Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128, MaxConcurrency: 1,
		}, limits)
		if err != nil {
			t.Fatalf("CreateAppIfUnderQuota(%s): %v", prefix, err)
		}
		return app
	}
	lookup := func(accountID string, index int) (state.App, error) {
		t.Helper()
		return fx.Store.AppByServiceAddressIndex(fx.Ctx, accountID, index)
	}
	indexOf := func(app state.App) int {
		t.Helper()
		index, err := fx.Store.AppServiceAddressIndex(fx.Ctx, app.ID)
		if err != nil {
			t.Fatalf("AppServiceAddressIndex(%s): %v", app.Slug, err)
		}
		return index
	}

	first, err := fx.Store.AppByID(fx.Ctx, fx.App.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if indexOf(first) != 1 {
		t.Fatalf("first app of a new account has service address index %d, want 1", indexOf(first))
	}
	second := newApp(fx.Account.ID, "svc-addr-second")
	if indexOf(second) != 2 {
		t.Fatalf("second app has service address index %d, want 2", indexOf(second))
	}
	for _, app := range []state.App{first, second} {
		got, err := lookup(fx.Account.ID, indexOf(app))
		if err != nil || got.ID != app.ID {
			t.Fatalf("AppByServiceAddressIndex(%d) = %s, %v; want %s", indexOf(app), got.ID, err, app.ID)
		}
	}

	// Indices are per account: another account's first app also holds 1,
	// and each account's lookup returns only its own app.
	other, err := fx.Store.CreateAccount(fx.Ctx, "svc-addr-"+uuid.NewString()[:8]+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	foreign := newApp(other.ID, "svc-addr-foreign")
	if indexOf(foreign) != 1 {
		t.Fatalf("other account's first app has service address index %d, want 1", indexOf(foreign))
	}
	if got, err := lookup(other.ID, 1); err != nil || got.ID != foreign.ID {
		t.Fatalf("other account lookup(1) = %s, %v; want %s", got.ID, err, foreign.ID)
	}
	if got, err := lookup(fx.Account.ID, 1); err != nil || got.ID != first.ID {
		t.Fatalf("fixture account lookup(1) = %s, %v; want %s", got.ID, err, first.ID)
	}

	// A tombstone is unreachable and its index is not reused.
	if _, err := fx.Store.ScheduleAppDeletion(fx.Ctx, second.ID, time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatalf("ScheduleAppDeletion: %v", err)
	}
	if _, err := lookup(fx.Account.ID, indexOf(second)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("lookup of a deleted app's address: err = %v, want ErrNotFound", err)
	}
	third := newApp(fx.Account.ID, "svc-addr-third")
	if indexOf(third) != 3 {
		t.Fatalf("app created after a deletion has service address index %d, want 3", indexOf(third))
	}
	restored, err := fx.Store.RestoreApp(fx.Ctx, second.ID, limits)
	if err != nil {
		t.Fatalf("RestoreApp: %v", err)
	}
	if indexOf(restored) != indexOf(second) {
		t.Fatalf("restored app has service address index %d, want its original %d", indexOf(restored), indexOf(second))
	}
	if got, err := lookup(fx.Account.ID, indexOf(second)); err != nil || got.ID != second.ID {
		t.Fatalf("lookup after restore = %s, %v; want %s", got.ID, err, second.ID)
	}

	for _, tc := range []struct {
		name      string
		accountID string
		index     int
	}{
		{"zero index", fx.Account.ID, 0},
		{"past the range", fx.Account.ID, api.ServiceAddressIndexMax + 1},
		{"unallocated index", fx.Account.ID, 99},
		{"empty account", "", 1},
		{"unknown account", uuid.NewString(), 1},
	} {
		if _, err := lookup(tc.accountID, tc.index); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("%s: AppByServiceAddressIndex err = %v, want ErrNotFound", tc.name, err)
		}
	}
}

// testComputeNodeServiceAddressReady pins the ADR-576 rollout gate: the stamp
// is set once and kept across repeated boots while the switch stays on,
// cleared when it is turned off, and re-stamped no earlier than before when
// it comes back. Service DNS trusts only instances started after the stamp.
func testComputeNodeServiceAddressReady(t *testing.T, fx *Fixture) {
	if at, err := fx.Store.ComputeNodeServiceAddressReadyAt(fx.Ctx, fx.Node.ID); err != nil || at != nil {
		t.Fatalf("fresh node readiness = %v, %v; want nil", at, err)
	}
	first, err := fx.Store.SetComputeNodeServiceAddressReady(fx.Ctx, fx.Node.ID, true)
	if err != nil || first == nil {
		t.Fatalf("SetComputeNodeServiceAddressReady(true) = %v, %v", first, err)
	}
	again, err := fx.Store.SetComputeNodeServiceAddressReady(fx.Ctx, fx.Node.ID, true)
	if err != nil || again == nil || !again.Equal(*first) {
		t.Fatalf("a second boot moved the stamp: %v -> %v (err %v)", first, again, err)
	}
	if read, err := fx.Store.ComputeNodeServiceAddressReadyAt(fx.Ctx, fx.Node.ID); err != nil || read == nil || !read.Equal(*first) {
		t.Fatalf("ComputeNodeServiceAddressReadyAt = %v, %v; want %v", read, err, first)
	}
	if cleared, err := fx.Store.SetComputeNodeServiceAddressReady(fx.Ctx, fx.Node.ID, false); err != nil || cleared != nil {
		t.Fatalf("SetComputeNodeServiceAddressReady(false) = %v, %v; want nil", cleared, err)
	}
	if read, err := fx.Store.ComputeNodeServiceAddressReadyAt(fx.Ctx, fx.Node.ID); err != nil || read != nil {
		t.Fatalf("readiness after the switch went off = %v, %v; want nil", read, err)
	}
	restamped, err := fx.Store.SetComputeNodeServiceAddressReady(fx.Ctx, fx.Node.ID, true)
	if err != nil || restamped == nil || restamped.Before(*first) {
		t.Fatalf("re-enabled stamp = %v, %v; want at or after %v", restamped, err, first)
	}
	unknown := uuid.NewString()
	if _, err := fx.Store.SetComputeNodeServiceAddressReady(fx.Ctx, unknown, true); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("Set on an unknown node: err = %v, want ErrNotFound", err)
	}
	if _, err := fx.Store.ComputeNodeServiceAddressReadyAt(fx.Ctx, unknown); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("read of an unknown node: err = %v, want ErrNotFound", err)
	}
}

// testServiceAddressCallerByHostIP pins the ADR-576 DNS gate: a caller is
// service-address capable only when its instance row was created at or
// after its node's readiness stamp, both on the database clock.
func testServiceAddressCallerByHostIP(t *testing.T, fx *Fixture) {
	start := func(hostIP string) {
		t.Helper()
		instance, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID, string(state.StateColdBooting), 128, fx.Node.ID, "")
		if err != nil {
			t.Fatalf("CreateInstance: %v", err)
		}
		if _, err := fx.Store.PublishInstanceRuntime(fx.Ctx, instance.ID, string(state.StateColdBooting), "fc-"+hostIP, hostIP, 20021); err != nil {
			t.Fatalf("PublishInstanceRuntime: %v", err)
		}
	}
	start("10.99.0.21")
	before, err := fx.Store.ServiceAddressCallerByHostIP(fx.Ctx, fx.Node.Name, "10.99.0.21")
	if err != nil || before.AppID != fx.App.ID || before.AccountID != fx.Account.ID || before.StartedAt.IsZero() {
		t.Fatalf("caller before readiness = %+v, %v", before, err)
	}
	if before.NodeReadyAt != nil || before.ServiceAddressCapable() {
		t.Fatalf("caller on a node without readiness is capable: %+v", before)
	}
	if _, err := fx.Store.SetComputeNodeServiceAddressReady(fx.Ctx, fx.Node.ID, true); err != nil {
		t.Fatalf("SetComputeNodeServiceAddressReady: %v", err)
	}
	// An instance that predates the stamp keeps the bridge answer.
	if stale, err := fx.Store.ServiceAddressCallerByHostIP(fx.Ctx, fx.Node.Name, "10.99.0.21"); err != nil || stale.NodeReadyAt == nil || stale.ServiceAddressCapable() {
		t.Fatalf("instance created before readiness = %+v, %v; want not capable", stale, err)
	}
	start("10.99.0.22")
	fresh, err := fx.Store.ServiceAddressCallerByHostIP(fx.Ctx, "", "10.99.0.22")
	if err != nil || fresh.AppID != fx.App.ID || !fresh.ServiceAddressCapable() {
		t.Fatalf("instance created after readiness = %+v, %v; want capable", fresh, err)
	}
	for name, tc := range map[string]struct {
		node, ip string
		want     error
	}{
		"another node":   {"not-this-node.faas", "10.99.0.22", state.ErrNotFound},
		"unused address": {fx.Node.Name, "10.99.0.99", state.ErrNotFound},
		"not an address": {fx.Node.Name, "tap0", state.ErrInvalidArgument},
	} {
		if _, err := fx.Store.ServiceAddressCallerByHostIP(fx.Ctx, tc.node, tc.ip); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}
