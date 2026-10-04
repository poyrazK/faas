// adr: 532 — primary secret intent and sidecar delivery have separate evidence.
package conformance

import (
	"errors"
	"maps"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func testRuntimeInputReceiptSidecarSecretAccess(t *testing.T, fx *Fixture) {
	project, err := fx.Store.CreateProject(fx.Ctx, state.Project{AccountID: fx.Account.ID, Slug: "sidecar-receipts"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID, ProjectID: project.ID,
		Slug: "sidecar-api", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: app.ID, Scope: "production",
		Kind: state.DeploymentKindImage, ImageDigest: "sha256:sidecar-receipts", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"MAIN_TOKEN", "SIDECAR_TOKEN", "UNDELIVERED_TOKEN"} {
		if err := fx.Store.UpsertAppSecretInScope(fx.Ctx, fx.Account.ID, app.ID, dep.Scope, key, []byte("sealed")); err != nil {
			t.Fatal(err)
		}
	}
	refs := fx.Store.(state.AppEnvironmentSecretReferenceStore)
	for _, key := range []string{"SIDECAR_TOKEN", "UNDELIVERED_TOKEN"} {
		if err := refs.DeleteAppEnvironmentSecretReference(fx.Ctx, fx.Account.ID, app.ID, dep.Scope, key); err != nil {
			t.Fatal(err)
		}
	}
	boundary, _, err := state.RuntimeConfigChangedAtForScope(fx.Ctx, fx.Store, app.ID, dep.Scope)
	if err != nil {
		t.Fatal(err)
	}
	inputs := state.RuntimeConfigInputs{Scope: dep.Scope, Boundary: boundary, AllSecrets: true,
		Variables: map[string]string{}, SecretRefs: map[string]string{"MAIN_TOKEN": "secret:MAIN_TOKEN"},
		SecretVersions:        map[string]int64{"production/MAIN_TOKEN": 1, "production/SIDECAR_TOKEN": 1},
		SidecarSecretVersions: map[string]int64{"production/SIDECAR_TOKEN": 1}}
	receipts := fx.Store.(state.RuntimeConfigReceiptStore)
	for _, tc := range []struct {
		name string
		edit func(*state.RuntimeConfigInputs)
		want bool
	}{
		{"explicit sidecar access", func(*state.RuntimeConfigInputs) {}, true},
		{"shared primary and sidecar source", func(i *state.RuntimeConfigInputs) { i.SidecarSecretVersions["production/MAIN_TOKEN"] = 1 }, true},
		{"legacy receipt without sidecar evidence", func(i *state.RuntimeConfigInputs) { i.SidecarSecretVersions = nil }, false},
		{"primary suppression still applies", func(i *state.RuntimeConfigInputs) { i.SecretRefs["SIDECAR_TOKEN"] = "secret:SIDECAR_TOKEN" }, false},
		{"unattested extra delivery", func(i *state.RuntimeConfigInputs) { i.SecretVersions["production/UNDELIVERED_TOKEN"] = 1 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := inputs
			candidate.SecretVersions, candidate.SidecarSecretVersions = maps.Clone(inputs.SecretVersions), maps.Clone(inputs.SidecarSecretVersions)
			candidate.SecretRefs = maps.Clone(inputs.SecretRefs)
			tc.edit(&candidate)
			if fresh, err := receipts.RuntimeConfigInputsFresh(fx.Ctx, app.ID, candidate); err != nil || fresh != tc.want {
				t.Fatalf("fresh=%v err=%v want=%v", fresh, err, tc.want)
			}
		})
	}
	invalid := inputs
	invalid.SidecarSecretVersions = map[string]int64{"production/SIDECAR_TOKEN": 2}
	if _, err := receipts.RuntimeConfigInputsFresh(fx.Ctx, app.ID, invalid); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("sidecar evidence outside delivered versions: %v", err)
	}
	instance, err := fx.Store.CreateInstance(fx.Ctx, app.ID, dep.ID, string(state.StateRunning), 256, fx.Node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := receipts.RecordInstanceRuntimeConfigReceipt(fx.Ctx, instance.ID, instance.WakeID, inputs); err != nil {
		t.Fatal(err)
	}
	legacy := inputs
	legacy.SidecarSecretVersions = nil
	if err := receipts.RecordInstanceRuntimeConfigReceipt(fx.Ctx, instance.ID, instance.WakeID, legacy); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("same wake changed its sidecar evidence: %v", err)
	}
	snapshot, err := fx.Store.PublishSnapshotIfRuntimeFresh(fx.Ctx, state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0", Tier: state.SnapshotTierWarm,
		StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierWarm, uuid.NewString()),
	}, instance.ID, instance.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	inputs.SidecarSecretVersions["production/SIDECAR_TOKEN"] = 99
	stored, exists, err := receipts.InstanceRuntimeConfigReceipt(fx.Ctx, instance.ID)
	if err != nil || !exists || stored.SidecarSecretVersions["production/SIDECAR_TOKEN"] != 1 {
		t.Fatalf("caller changed stored sidecar evidence: %+v %v %v", stored, exists, err)
	}
	stored.SidecarSecretVersions["production/SIDECAR_TOKEN"] = 99
	captured, exists, err := receipts.SnapshotRuntimeConfigReceipt(fx.Ctx, snapshot.ID)
	if err != nil || !exists || captured.SidecarSecretVersions["production/SIDECAR_TOKEN"] != 1 {
		t.Fatalf("snapshot lost sidecar evidence: %+v %v %v", captured, exists, err)
	}
	if fresh, err := receipts.RuntimeConfigInputsFresh(fx.Ctx, app.ID, captured); err != nil || !fresh {
		t.Fatalf("captured sidecar inputs are stale: %v %v", fresh, err)
	}
	if err := fx.Store.UpsertAppSecretInScope(fx.Ctx, fx.Account.ID, app.ID, dep.Scope, "SIDECAR_TOKEN", []byte("rotated")); err != nil {
		t.Fatal(err)
	}
	if fresh, err := receipts.RuntimeConfigInputsFresh(fx.Ctx, app.ID, captured); err != nil || fresh {
		t.Fatalf("sidecar rotation left captured inputs fresh: %v %v", fresh, err)
	}
}
