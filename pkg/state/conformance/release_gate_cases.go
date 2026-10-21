package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// testReleaseGates pins ADR-20261019071734's gate bookkeeping on both stores:
// each kind round-trips, names are unique per app, the per-app limit holds,
// deleting by name is final, and deleting an SLO deletes its gates.
func testReleaseGates(t *testing.T, fx *Fixture) {
	t.Helper()
	store, ok := fx.Store.(state.ReleaseGateStore)
	if !ok {
		t.Fatalf("%T does not implement state.ReleaseGateStore", fx.Store)
	}
	slos, ok := fx.Store.(state.SLOStore)
	if !ok {
		t.Fatalf("%T does not implement state.SLOStore", fx.Store)
	}
	acct, app := fx.Account.ID, fx.App.ID
	now := time.Now().UTC().Truncate(time.Second)
	slo, err := slos.CreateSLO(fx.Ctx, state.SLO{AccountID: acct, AppID: app, Name: "checkout", SLI: state.SLIAvailability,
		ObjectiveBP: 9990, WindowDays: 30}, 10)
	if err != nil {
		t.Fatal(err)
	}
	gates := []state.ReleaseGate{
		{AccountID: acct, AppID: app, Name: "checkout-slo", Kind: state.ReleaseGateSLO, SLOID: slo.ID, MaxBurnRate: 10, MinSamples: 20, CreatedBy: "user:conformance"},
		{AccountID: acct, AppID: app, Name: "new-errors", Kind: state.ReleaseGateNewErrors, MaxNewGroups: 0, MinEvents: 3, MinSamples: 1, CreatedBy: "user:conformance"},
		{AccountID: acct, AppID: app, Name: "orders", Kind: state.ReleaseGateCustomMetric, MetricName: "orders_per_minute", Comparison: "below", Threshold: 2.5, MinSamples: 5, CreatedBy: "user:conformance"},
	}
	for _, g := range gates {
		if _, err := store.CreateReleaseGate(fx.Ctx, g, 3, now); err != nil {
			t.Fatalf("create %s: %v", g.Name, err)
		}
	}
	listed, err := store.ListReleaseGates(fx.Ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 || listed[0].Name != "checkout-slo" || listed[1].Name != "new-errors" || listed[2].Name != "orders" {
		t.Fatalf("listed = %+v", listed)
	}
	if g := listed[0]; g.SLOID != slo.ID || g.MaxBurnRate != 10 || g.MinSamples != 20 {
		t.Fatalf("slo gate = %+v", g)
	}
	if g := listed[1]; g.MaxNewGroups != 0 || g.MinEvents != 3 {
		t.Fatalf("new_errors gate = %+v", g)
	}
	if g := listed[2]; g.MetricName != "orders_per_minute" || g.Comparison != "below" || g.Threshold != 2.5 {
		t.Fatalf("custom_metric gate = %+v", g)
	}
	if _, err := store.CreateReleaseGate(fx.Ctx, state.ReleaseGate{AccountID: acct, AppID: app, Name: "fourth", Kind: state.ReleaseGateNewErrors,
		MinEvents: 1, MinSamples: 1, CreatedBy: "user:conformance"}, 3, now); !errors.Is(err, state.ErrReleaseGateLimit) {
		t.Fatalf("fourth gate err = %v, want ErrReleaseGateLimit", err)
	}
	if _, err := store.CreateReleaseGate(fx.Ctx, state.ReleaseGate{AccountID: acct, AppID: app, Name: "orders", Kind: state.ReleaseGateNewErrors,
		MinEvents: 1, MinSamples: 1, CreatedBy: "user:conformance"}, 10, now); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate name err = %v, want ErrConflict", err)
	}
	deleted, err := store.DeleteReleaseGate(fx.Ctx, app, "orders")
	if err != nil || deleted.Name != "orders" {
		t.Fatalf("delete = %+v, %v", deleted, err)
	}
	if _, err := store.DeleteReleaseGate(fx.Ctx, app, "orders"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second delete err = %v, want ErrNotFound", err)
	}
	if err := slos.DeleteSLO(fx.Ctx, app, slo.ID); err != nil {
		t.Fatal(err)
	}
	listed, err = store.ListReleaseGates(fx.Ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Name != "new-errors" {
		t.Fatalf("after deleting the SLO = %+v", listed)
	}
}
