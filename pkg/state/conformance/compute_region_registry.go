package conformance

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testComputeRegionRegistry(t *testing.T, fx *Fixture) {
	region := state.ComputeRegion{ID: "eu-fsn", DisplayName: "Falkenstein", AcceptingNewApps: true}
	for _, invalid := range []state.ComputeRegion{{ID: "", DisplayName: "Empty"}, {ID: "EU", DisplayName: "Uppercase"}, {ID: "valid", DisplayName: "bad\nname"}, {ID: "valid", DisplayName: " space "}} {
		if _, err := fx.Store.UpsertComputeRegion(fx.Ctx, invalid); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid region accepted: %+v %v", invalid, err)
		}
	}
	app := fx.App
	app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "registry-"+uuid.NewString(), "", region.ID
	if _, err := fx.Store.CreateApp(fx.Ctx, app); api.AsProblem(err) == nil || api.AsProblem(err).Code != api.CodeComputeRegionUnknown {
		t.Fatalf("unknown region: %v", err)
	}
	saved, err := fx.Store.UpsertComputeRegion(fx.Ctx, region)
	if err != nil || saved.CreatedAt.IsZero() {
		t.Fatalf("create registry: %+v %v", saved, err)
	}
	// Regions persist independently of compute nodes and currently deployed apps.
	list, err := fx.Store.ListComputeRegions(fx.Ctx)
	if err != nil || len(list) != 2 || list[0].ID != region.ID {
		t.Fatalf("catalog: %+v %v", list, err)
	}
	created, err := fx.Store.CreateAppIfUnderQuota(fx.Ctx, app, api.MustLimitsFor(fx.Account.Plan))
	if err != nil {
		t.Fatal(err)
	}
	node := fx.Node
	node.Region = &region.ID
	node, err = fx.Store.UpsertComputeNodeFromOperator(fx.Ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	region.AcceptingNewApps, region.DisplayName = false, "Falkenstein paused"
	paused, err := fx.Store.UpsertComputeRegion(fx.Ctx, region)
	if err != nil || !paused.CreatedAt.Equal(saved.CreatedAt) || paused.AcceptingNewApps {
		t.Fatalf("pause: %+v %v", paused, err)
	}
	app.Slug = "paused-" + uuid.NewString()
	for _, quota := range []bool{false, true} {
		var err error
		if quota {
			_, err = fx.Store.CreateAppIfUnderQuota(fx.Ctx, app, api.MustLimitsFor(fx.Account.Plan))
		} else {
			_, err = fx.Store.CreateApp(fx.Ctx, app)
		}
		p := api.AsProblem(err)
		if p == nil || p.Code != api.CodeComputeRegionAdmissionPaused || p.ComputeRegion != region.ID {
			t.Fatalf("paused create quota=%t: %v", quota, err)
		}
	}
	// Existing owner claims and instance admission ignore the new-app policy.
	if err := fx.Store.SetAppNodeID(fx.Ctx, created.ID, node.ID); err != nil {
		t.Fatal(err)
	}
	dep := fx.Deployment
	dep.ID, dep.AppID = "", created.ID
	dep, err = fx.Store.CreateDeployment(fx.Ctx, dep)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.CreateInstance(fx.Ctx, created.ID, dep.ID, "running", created.RAMMB, node.ID, ""); err != nil {
		t.Fatalf("paused region rejected existing wake: %v", err)
	}
	if err := fx.Store.SetComputeNodeActive(fx.Ctx, node.ID, false); err != nil {
		t.Fatal(err)
	}
	nodes, err := fx.Store.ListAllComputeNodes(fx.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	retained := false
	for _, item := range nodes {
		if item.ID == node.ID && !item.Active {
			retained = true
		}
	}
	if !retained {
		t.Fatal("inactive regional node was removed from diagnostics")
	}
	got, err := fx.Store.ComputeRegionByID(fx.Ctx, region.ID)
	if err != nil || got.AcceptingNewApps || got.DisplayName != region.DisplayName {
		t.Fatalf("outage erased policy: %+v %v", got, err)
	}
}
