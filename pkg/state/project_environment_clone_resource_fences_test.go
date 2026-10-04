// adr: 568
package state_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneResourceFenceStore interface {
	state.Store
	state.ProjectEnvironmentCloneOperationStore
	state.ProjectEnvironmentCloneObjectManifestStore
}

func TestMemCloneResourceInventoryFencesPublication(t *testing.T) {
	testCloneResourceInventoryFences(t, state.NewMemStore())
}

func testCloneResourceInventoryFences(t *testing.T, store cloneResourceFenceStore) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "clone-resource-fences@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "clone-resource-fences"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(target string) state.ProjectEnvironmentCloneOperation {
		op, err := store.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{
			AccountID: account.ID, ProjectID: project.ID, SourceEnvironment: "production", TargetEnvironment: target,
			IdempotencyKey: target, SourceRevisionHash: strings.Repeat("a", 64),
		})
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	advance := func(op state.ProjectEnvironmentCloneOperation, next string, resources []state.ProjectEnvironmentCloneResource, code string) (state.ProjectEnvironmentCloneOperation, error) {
		return store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, next, op.Revision, resources, code)
	}
	op := create("staging")
	root := state.ProjectEnvironmentCloneResource{Kind: "source_revision", Name: "production", SourceVersion: op.SourceRevisionHash, Status: "captured"}
	at := time.Date(2026, 9, 30, 0, 0, 0, 123456789, time.UTC)
	resource := state.ProjectEnvironmentCloneResource{Kind: "object_storage", Name: "assets", SourceID: uuid.NewString(), TargetID: uuid.NewString(), CapturePoint: at.Format(time.RFC3339Nano), Status: "capturing"}
	resources := []state.ProjectEnvironmentCloneResource{root, resource}
	op, err = advance(op, state.CloneOperationCapturing, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func([]state.ProjectEnvironmentCloneResource) []state.ProjectEnvironmentCloneResource
		want error
	}{
		{"omitted resource", func(r []state.ProjectEnvironmentCloneResource) []state.ProjectEnvironmentCloneResource { return r[:1] }, state.ErrConflict},
		{"rebound target", func(r []state.ProjectEnvironmentCloneResource) []state.ProjectEnvironmentCloneResource {
			r[1].TargetID = uuid.NewString()
			return r
		}, state.ErrConflict},
		{"new source", func(r []state.ProjectEnvironmentCloneResource) []state.ProjectEnvironmentCloneResource {
			r[1].SourceID = uuid.NewString()
			return r
		}, state.ErrConflict},
		{"changed capture point", func(r []state.ProjectEnvironmentCloneResource) []state.ProjectEnvironmentCloneResource {
			r[1].CapturePoint = at.Add(time.Second).Format(time.RFC3339Nano)
			return r
		}, state.ErrConflict},
		{"duplicate identity", func(r []state.ProjectEnvironmentCloneResource) []state.ProjectEnvironmentCloneResource {
			return append(r, r[1])
		}, state.ErrInvalidProjectEnvironmentCloneOperation},
		{"unknown status", func(r []state.ProjectEnvironmentCloneResource) []state.ProjectEnvironmentCloneResource {
			r[1].Status = "ignored"
			return r
		}, state.ErrInvalidProjectEnvironmentCloneOperation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := tc.edit(append([]state.ProjectEnvironmentCloneResource(nil), resources...))
			if _, err := advance(op, state.CloneOperationCapturing, changed, ""); !errors.Is(err, tc.want) {
				t.Fatalf("invalid inventory update accepted: %v", err)
			}
			loaded, err := store.ProjectEnvironmentCloneOperationByID(ctx, account.ID, project.ID, op.ID)
			if err != nil || loaded.Revision != op.Revision || !reflect.DeepEqual(loaded.Resources, resources) {
				t.Fatalf("rejected update changed inventory: %+v, %v", loaded, err)
			}
		})
	}
	version := state.ProjectEnvironmentCloneObjectVersion{Key: "data.json", VersionID: "v1", Size: 3, LastModified: at.Add(-time.Second)}
	hash, err := state.ProjectEnvironmentCloneObjectManifestHash([]state.ProjectEnvironmentCloneObjectVersion{version})
	if err != nil {
		t.Fatal(err)
	}
	manifest := state.ProjectEnvironmentCloneObjectManifest{OperationID: op.ID, SourceBucketID: resource.SourceID, TargetBucketID: resource.TargetID,
		CapturedAt: at, Hash: hash, Objects: []state.ProjectEnvironmentCloneObjectCheckpoint{{Source: version}}}
	if _, err := store.PutProjectEnvironmentCloneObjectManifest(ctx, account.ID, project.ID, manifest); err != nil {
		t.Fatal(err)
	}
	resources[0].Status, resources[1].Status, resources[1].SourceVersion = "ready", "ready", hash
	op, err = advance(op, state.CloneOperationCopying, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := advance(op, state.CloneOperationPublishing, resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unverified object copy published: %v", err)
	}
	if err := store.MarkProjectEnvironmentCloneObjectCopied(ctx, account.ID, project.ID, op.ID, resource.SourceID, version.Key, version.VersionID, "target-etag", strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	extra := append(append([]state.ProjectEnvironmentCloneResource(nil), resources...), state.ProjectEnvironmentCloneResource{Kind: "variables", Name: "added-after-capture", Status: "ready"})
	if _, err := advance(op, state.CloneOperationPublishing, extra, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("late inventory expansion accepted: %v", err)
	}
	unfinished := append([]state.ProjectEnvironmentCloneResource(nil), resources...)
	unfinished[1].Status = "unsupported"
	if _, err := advance(op, state.CloneOperationPublishing, unfinished, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unsupported resource published: %v", err)
	}
	op, err = advance(op, state.CloneOperationPublishing, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := advance(op, state.CloneOperationReady, resources, "verification_failed"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("operation with error became ready: %v", err)
	}
	op, err = advance(op, state.CloneOperationReady, resources, "")
	if err != nil || op.Status != state.CloneOperationReady {
		t.Fatalf("verified inventory could not become ready: %+v, %v", op, err)
	}
	// Even an empty project needs the captured root revision in its receipt.
	empty := create("empty-inventory")
	empty, err = advance(empty, state.CloneOperationCapturing, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	empty, err = advance(empty, state.CloneOperationCopying, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := advance(empty, state.CloneOperationPublishing, nil, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("empty inventory published: %v", err)
	}
	unknown := create("unknown-resource")
	unknownResources := []state.ProjectEnvironmentCloneResource{
		{Kind: "source_revision", Name: "production", SourceVersion: unknown.SourceRevisionHash, Status: "ready"},
		{Kind: "future_integration", Name: "integration", Status: "ready"},
	}
	unknown, err = advance(unknown, state.CloneOperationCapturing, unknownResources, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := advance(unknown, state.CloneOperationCopying, unknownResources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unimplemented resource strategy entered copying: %v", err)
	}
	missing := create("missing-manifest")
	missing, err = advance(missing, state.CloneOperationCapturing, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	missing, err = advance(missing, state.CloneOperationCopying, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := advance(missing, state.CloneOperationPublishing, resources, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("bucket with no operation-owned copy manifest published: %v", err)
	}
	// Compensation preserves identities in the historical receipt.
	compensating := create("failed-stage")
	compensating, err = advance(compensating, state.CloneOperationCapturing, []state.ProjectEnvironmentCloneResource{root}, "")
	if err != nil {
		t.Fatal(err)
	}
	compensating, err = advance(compensating, state.CloneOperationCompensating, []state.ProjectEnvironmentCloneResource{root}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := advance(compensating, state.CloneOperationCompensated, nil, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("compensation erased inventory: %v", err)
	}
	root.Status = "compensated"
	if _, err := advance(compensating, state.CloneOperationCompensated, []state.ProjectEnvironmentCloneResource{root}, ""); err != nil {
		t.Fatal(err)
	}
}
