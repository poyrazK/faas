// adr: 20261031193635
package apidserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/state"
)

// Slice 3: a branch of an environment that binds object storage gets an
// empty overlay bucket on each parent bucket, bound into its own scope with
// its own credential, and the bucket is emptied and deleted after the branch.
func TestEnvironmentBranchOverlaysParentBuckets(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	e := apidserver.SetupSecrets(t, api.PlanPro)
	srv, store, acct := *e.S(), *e.Store(), *e.Acct()
	ctx := t.Context()
	for _, key := range []string{apidserver.RuntimeConfigS3, apidserver.RuntimeConfigEnvironmentBranches} {
		if err := (*srv.RuntimeConfig()).Apply(key, json.RawMessage("true")); err != nil {
			t.Fatal(err)
		}
	}
	parentObjects := map[string][]byte{}
	srv.WithObjectStorage(apidserver.ObjectRegistry(t, newStorageTestObjectProvider(parentObjects, nil), newStorageTestObjectProvider(nil, nil), "external"))
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-web", WorkloadName: "web", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	created := e.Do(t, http.MethodPost, "/v1/apps/shop-web/buckets", map[string]any{"name": "assets", "scope": "production"}, nil)
	var source api.ObjectBucket
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &source) != nil {
		t.Fatalf("create bucket = %d %s", created.Code, created.Body.String())
	}
	bound := e.Do(t, http.MethodPost, "/v1/apps/shop-web/buckets/"+source.ID+"/compute-bindings", api.CreateObjectStorageComputeBindingRequest{Permission: api.ObjectBucketPermissionReadWrite}, nil)
	var parentBinding api.ObjectStorageComputeBinding
	if bound.Code != http.StatusCreated || json.Unmarshal(bound.Body.Bytes(), &parentBinding) != nil || parentBinding.Scope != "production" {
		t.Fatalf("bind bucket = %d %s", bound.Code, bound.Body.String())
	}
	parent, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:web"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, parent.ID, "/web.ext4", "layers/web.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}

	req, rec := apidserver.ProjectRequest(http.MethodPost, "/v1/projects/shop/environments/production/branches", "shop", []byte(`{"slug":"pr-9","ttl":"2h"}`))
	req.SetPathValue("environment", "production")
	srv.CreateProjectEnvironmentBranch(rec, req, acct)
	var response api.ProjectEnvironmentResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &response) != nil || response.Branch == nil || response.Branch.Data != state.EnvironmentBranchDataPending {
		t.Fatalf("branch = %d %s", rec.Code, rec.Body.String())
	}
	if rows, err := store.ListAppSecretsInScope(ctx, acct.ID, app.ID, "pr-9"); err != nil || len(rows) != 0 {
		t.Fatalf("the parent's bucket credential was copied into the branch: %+v, %v", rows, err)
	}

	for range 2 { // the second pass finds nothing left to do
		if err := srv.ReconcileEnvironmentBranchData(ctx, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	branch, err := store.ProjectEnvironmentBranchBySlug(ctx, acct.ID, project.ID, "pr-9")
	if err != nil || branch.DataState != state.EnvironmentBranchDataReady {
		t.Fatalf("branch after reconcile = %+v, %v", branch, err)
	}
	owned, err := store.ListProjectEnvironmentBranchBuckets(ctx, branch.EnvironmentID)
	if err != nil || len(owned) != 1 || owned[0].SourceBucketID != source.ID || owned[0].CopyBucketID == "" {
		t.Fatalf("branch buckets = %+v, %v", owned, err)
	}
	copyID := owned[0].CopyBucketID
	overlayBucket, err := store.GetObjectBucket(ctx, acct.ID, app.ID, copyID)
	if err != nil || overlayBucket.State != "ready" || overlayBucket.Scope != "pr-9" || overlayBucket.EnvironmentCloneSourceBucketID != source.ID {
		t.Fatalf("overlay bucket = %+v, %v", overlayBucket, err)
	}
	overlay, err := store.ObjectBucketOverlay(ctx, copyID)
	if err != nil || overlay.SourceBucketID != source.ID || overlay.Source.ID != source.ID || overlay.Consistency != state.ObjectBucketOverlayLiveParent || !overlay.AsOf.Equal(branch.BranchPoint) {
		t.Fatalf("overlay = %+v, %v", overlay, err)
	}
	credentials, err := store.ListObjectS3Credentials(ctx, acct.ID, copyID)
	if err != nil || len(credentials) != 1 || credentials[0].ManagedScope != "pr-9" || credentials[0].ManagedPrefix != parentBinding.Prefix ||
		credentials[0].Permission != state.ObjectBucketPermissionReadWrite {
		t.Fatalf("branch credentials = %+v, %v", credentials, err)
	}
	keys := object.ObjectStorageBindingSecretKeys(parentBinding.Prefix)
	if secret, err := store.GetAppSecretInScope(ctx, acct.ID, app.ID, "pr-9", keys.Bucket); err != nil || secret.ManagedObjectStorageCredentialID != credentials[0].ID {
		t.Fatalf("branch bucket secret = %+v, %v", secret, err)
	}
	if len(parentObjects) != 0 {
		t.Fatalf("branching copied objects: %v", parentObjects)
	}

	req, rec = apidserver.ProjectRequest(http.MethodDelete, "/v1/projects/shop/environments/pr-9", "shop", nil)
	req.SetPathValue("environment", "pr-9")
	srv.DeleteProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete branch = %d %s", rec.Code, rec.Body.String())
	}
	if err := srv.ReconcileEnvironmentBranchData(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if b, err := store.GetObjectBucket(ctx, acct.ID, app.ID, copyID); err == nil && b.State != "deleted" {
		t.Fatalf("branch bucket after delete = %+v", b)
	}
	if _, _, err := store.ResolveObjectS3Credential(ctx, credentials[0].AccessKeyID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("branch credential resolves after delete: %v", err)
	}
	if _, err := store.ObjectBucketOverlay(ctx, copyID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("overlay after delete: %v", err)
	}
	if owned, err := store.ListProjectEnvironmentBranchBuckets(ctx, branch.EnvironmentID); err != nil || len(owned) != 0 {
		t.Fatalf("deleted branch bucket still recorded: %+v, %v", owned, err)
	}
	if b, err := store.GetObjectBucket(ctx, acct.ID, app.ID, source.ID); err != nil || b.State != "ready" {
		t.Fatalf("parent bucket after branch delete = %+v, %v", b, err)
	}
	if _, _, err := store.ResolveObjectS3Credential(ctx, parentBinding.Credential.AccessKeyID); err != nil {
		t.Fatalf("parent credential after branch delete: %v", err)
	}
}
