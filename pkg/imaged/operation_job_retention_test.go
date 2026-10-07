// adr: 664
package imaged

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestJobOperationRetainsImageThroughReplacementDeletionAndExpiry(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "operation-images@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "job-operation-images", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:app", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "image-owner", "Image owner", 100)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.JobCreate(ctx, account.ID, "image-export", "batch", "registry.example/export:v1", []string{"export"}, 128, 60, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldKey := sched.JobLayerAttemptKey(job.ID, "01234567-89ab-cdef-0123-456789abcdef")
	if _, err := store.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", "sha256:"+strings.Repeat("a", 64), oldKey, ""); err != nil {
		t.Fatal(err)
	}
	schema := json.RawMessage(`{"type":"object"}`)
	definition, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: deployment.Scope, DeploymentID: deployment.ID, Spec: api.OperationDefinitionSpec{Name: "export", Job: job.Name, Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, InputSchema: schema, OutputSchema: schema, ProgressStages: []string{"working"}}}})
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: account.ID, DefinitionID: definition.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "export", Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelOperation(ctx, account.ID, tenant.ID, op.ID, 1); err != nil {
		t.Fatal(err)
	}
	newImage := "registry.example/export:v2"
	if _, err := store.JobUpdate(ctx, job.ID, nil, &newImage, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	newKey := sched.JobLayerKey(job.ID)
	if _, err := store.JobSetImageMaterialization(ctx, job.ID, newImage, "ready", "sha256:"+strings.Repeat("b", 64), newKey, ""); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	h := New(store, &fakeNotifier{}, nil, nil, "", root, silentLogger()).WithStorage(mustLocalStorage(t, root))
	for _, key := range []string{oldKey, newKey} {
		if err := h.storage.Put(ctx, key, strings.NewReader("image")); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.cleanupSupersededJobArtifacts(ctx, job.ID, newKey); err != nil {
		t.Fatal(err)
	}
	if _, err := h.storage.Get(ctx, oldKey); err != nil {
		t.Fatal("replacement removed retained image", err)
	}
	if deleted, live, err := store.JobSoftDelete(ctx, job.ID); err != nil || !deleted || live {
		t.Fatal("soft delete", deleted, live, err)
	}
	if err := h.deleteJobArtifact(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.storage.Get(ctx, oldKey); err != nil {
		t.Fatal("deletion removed retained image", err)
	}
	if _, err := h.storage.Get(ctx, newKey); !storage.IsNotFound(err) {
		t.Fatal("unretained replacement leaked", err)
	}
	if count, err := store.PruneOperationState(ctx, op.ExpiresAt.Add(time.Second), 10); err != nil || count != 1 {
		t.Fatal("prune", count, err)
	}
	if err := h.ReconcileDeletedJobArtifacts(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := h.storage.Get(ctx, oldKey); !storage.IsNotFound(err) {
		t.Fatal("expired operation leaked retained image", err)
	}
}
