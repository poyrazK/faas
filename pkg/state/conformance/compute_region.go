package conformance

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testComputeRegionPlacement(t *testing.T, fx *Fixture) {
	source := fx.Node
	source.Region = regionPointer("eu-fsn")
	var err error
	source, err = fx.Store.UpsertComputeNodeFromOperator(fx.Ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	peer := source
	peer.ID, peer.Name = "", "region-peer-"+uuid.NewString()
	peer, err = fx.Store.CreateComputeNode(fx.Ctx, peer)
	if err != nil {
		t.Fatal(err)
	}
	other := peer
	other.ID, other.Name, other.Region = "", "other-region-"+uuid.NewString(), regionPointer("eu-hel")
	other, err = fx.Store.CreateComputeNode(fx.Ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	app := fx.App
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "regional-"+uuid.NewString(), "", "eu-fsn"
	app, err = fx.Store.CreateApp(fx.Ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	quotaApp := app
	quotaApp.ID, quotaApp.Slug = "", "regional-quota-"+uuid.NewString()
	quotaApp, err = fx.Store.CreateAppIfUnderQuota(fx.Ctx, quotaApp, api.MustLimitsFor(fx.Account.Plan))
	if err != nil || quotaApp.ComputeRegion != "eu-fsn" {
		t.Fatalf("quota create lost placement: %+v, %v", quotaApp, err)
	}
	if err := fx.Store.SetAppNodeID(fx.Ctx, app.ID, other.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("out-of-region owner claim = %v", err)
	}
	if err := fx.Store.SetAppNodeID(fx.Ctx, app.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.ReassignAppOwner(fx.Ctx, app.ID, source.ID, other.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("out-of-region ownership transfer = %v", err)
	}
	got, err := fx.Store.AppByID(fx.Ctx, app.ID)
	if err != nil || got.ComputeRegion != "eu-fsn" || got.NodeID != source.ID {
		t.Fatalf("placement was not persisted: %+v, %v", got, err)
	}
	changed := source
	changed.Region = other.Region
	if _, err := fx.Store.UpsertComputeNodeFromOperator(fx.Ctx, changed); err == nil {
		t.Fatal("node region changed under a bound app")
	}
	dep := fx.Deployment
	dep.ID, dep.AppID = "", app.ID
	dep, err = fx.Store.CreateDeployment(fx.Ctx, dep)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.CreateInstance(fx.Ctx, app.ID, dep.ID, "running", app.RAMMB, other.ID, ""); err == nil {
		t.Fatal("out-of-region instance admitted")
	}
	instance, err := fx.Store.CreateInstance(fx.Ctx, app.ID, dep.ID, "running", app.RAMMB, source.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if err := fx.Store.MarkInstanceMigrating(fx.Ctx, instance.ID, source.ID, token); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.MigrateInstanceOwner(fx.Ctx, instance.ID, source.ID, other.ID, token); err == nil {
		t.Fatal("out-of-region live migration committed")
	}
	if err := fx.Store.MigrateInstanceOwner(fx.Ctx, instance.ID, source.ID, peer.ID, token); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.SetComputeNodeActive(fx.Ctx, source.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.ReassignOrphanedAppOwner(fx.Ctx, app.ID, source.ID, other.ID, -1); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("out-of-region recovery = %v", err)
	}
	if err := fx.Store.ReassignOrphanedAppOwner(fx.Ctx, app.ID, source.ID, peer.ID, -1); err != nil {
		t.Fatal(err)
	}
	got, err = fx.Store.AppByID(fx.Ctx, app.ID)
	if err != nil || got.NodeID != peer.ID || got.ComputeRegion != "eu-fsn" {
		t.Fatalf("same-region recovery failed: %+v, %v", got, err)
	}
	if _, err := fx.Store.SoftDeleteAppCascade(fx.Ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.SoftDeleteAppCascade(fx.Ctx, quotaApp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.UpsertComputeNodeFromOperator(fx.Ctx, changed); err == nil {
		t.Fatal("node was relabeled during app restore window")
	}
	restored, err := fx.Store.RestoreApp(fx.Ctx, app.ID, api.MustLimitsFor(fx.Account.Plan))
	if err != nil || restored.ComputeRegion != "eu-fsn" {
		t.Fatalf("restore lost compute region: %+v, %v", restored, err)
	}
}

func regionPointer(region string) *string { return &region }
