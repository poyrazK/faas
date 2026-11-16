// adr: 20261031193635
package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// branchObjectFixture is a project whose production environment binds one
// bucket, with a pending branch pr-9.
type branchObjectFixture struct {
	e             *apidserver.TestEnv
	srv           *apidserver.Server
	store         *state.MemStore
	acct          state.Account
	project       state.Project
	app           state.App
	source        api.ObjectBucket
	parentBinding api.ObjectStorageComputeBinding
	parentObjects map[string][]byte
}

func newBranchObjectFixture(t *testing.T) branchObjectFixture {
	t.Helper()
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
	return branchObjectFixture{e: &e, srv: srv, store: store, acct: acct, project: project, app: app, source: source, parentBinding: parentBinding, parentObjects: parentObjects}
}

// Slice 3: a branch of an environment that binds object storage gets an
// empty overlay bucket on each parent bucket, bound into its own scope with
// its own credential, and the bucket is emptied and deleted after the branch.
// A source without versioning is read live; the branch credential may copy
// from it (copy-through).
func TestEnvironmentBranchOverlaysParentBuckets(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	f := newBranchObjectFixture(t)
	srv, store, acct, project, app, source, parentBinding, parentObjects := f.srv, f.store, f.acct, f.project, f.app, f.source, f.parentBinding, f.parentObjects
	ctx := t.Context()

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
	grants, err := store.ListObjectS3CopySources(ctx, acct.ID, copyID, credentials[0].ID)
	if err != nil || len(grants) != 1 || grants[0].SourceBucketID != source.ID || grants[0].Prefix != "" {
		t.Fatalf("branch credential copy grants = %+v, %v", grants, err)
	}
	keys := object.ObjectStorageBindingSecretKeys(parentBinding.Prefix)
	if secret, err := store.GetAppSecretInScope(ctx, acct.ID, app.ID, "pr-9", keys.Bucket); err != nil || secret.ManagedObjectStorageCredentialID != credentials[0].ID {
		t.Fatalf("branch bucket secret = %+v, %v", secret, err)
	}
	if len(parentObjects) != 0 {
		t.Fatalf("branching copied objects: %v", parentObjects)
	}

	req, rec := apidserver.ProjectRequest(http.MethodDelete, "/v1/projects/shop/environments/pr-9", "shop", nil)
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

// versionedSourceStore reports versioning on and settled for one bucket.
type versionedSourceStore struct {
	MemStore *state.MemStore
	bucket   string
}

func (s *versionedSourceStore) GetObjectBucketVersioning(ctx context.Context, account, app, bucket string) (state.ObjectBucketVersioning, error) {
	if bucket != s.bucket {
		return s.MemStore.GetObjectBucketVersioning(ctx, account, app, bucket)
	}
	return state.ObjectBucketVersioning{ObjectBucketVersioning: api.ObjectBucketVersioning{BucketID: bucket, ObservedStatus: "Enabled", DesiredStatus: "Enabled", State: "ready"}}, nil
}

// A source with versioning on is read as of the branch point.
func TestEnvironmentBranchReadsAVersionedSourceAsOfTheBranchPoint(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	f := newBranchObjectFixture(t)
	*f.srv.Store() = statetest.NewMem(&versionedSourceStore{MemStore: f.store, bucket: f.source.ID})
	ctx := t.Context()
	if err := f.srv.ReconcileEnvironmentBranchData(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	branch, err := f.store.ProjectEnvironmentBranchBySlug(ctx, f.acct.ID, f.project.ID, "pr-9")
	if err != nil || branch.DataState != state.EnvironmentBranchDataReady {
		t.Fatalf("branch after reconcile = %+v, %v", branch, err)
	}
	owned, err := f.store.ListProjectEnvironmentBranchBuckets(ctx, branch.EnvironmentID)
	if err != nil || len(owned) != 1 {
		t.Fatalf("branch buckets = %+v, %v", owned, err)
	}
	overlay, err := f.store.ObjectBucketOverlay(ctx, owned[0].CopyBucketID)
	if err != nil || overlay.Consistency != state.ObjectBucketOverlayAsOf || !overlay.AsOf.Equal(branch.BranchPoint) {
		t.Fatalf("overlay = %+v, %v", overlay, err)
	}
	view := f.e.Do(t, http.MethodGet, "/v1/projects/shop/environments/pr-9/branch", nil, nil)
	var got api.ProjectEnvironmentResponse
	if view.Code != http.StatusOK || json.Unmarshal(view.Body.Bytes(), &got) != nil || got.Branch == nil || len(got.Branch.Buckets) != 1 {
		t.Fatalf("branch view = %d %s", view.Code, view.Body.String())
	}
	if b := got.Branch.Buckets[0]; b.SourceBucketID != f.source.ID || b.Name != "assets" || b.CopyBucketID != owned[0].CopyBucketID ||
		b.Consistency != state.ObjectBucketOverlayAsOf || b.AsOf != branch.BranchPoint.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("branch view bucket = %+v", b)
	}
}

func TestEnvironmentBranchSourceVersioned(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Minute), now.Add(time.Minute)
	for _, tc := range []struct {
		name string
		v    api.ObjectBucketVersioning
		want bool
	}{
		{"never versioned", api.ObjectBucketVersioning{State: "ready"}, false},
		{"on and settled", api.ObjectBucketVersioning{ObservedStatus: "Enabled", State: "ready", PropagationUntil: &past}, true},
		{"on, no propagation recorded", api.ObjectBucketVersioning{ObservedStatus: "Enabled", State: "ready"}, true},
		{"still propagating", api.ObjectBucketVersioning{ObservedStatus: "Enabled", State: "ready", PropagationUntil: &future}, false},
		{"transition in flight", api.ObjectBucketVersioning{ObservedStatus: "Enabled", State: "waiting", PropagationUntil: &past}, false},
		{"suspended", api.ObjectBucketVersioning{ObservedStatus: "Suspended", State: "ready", PropagationUntil: &past}, false},
	} {
		if got := apidserver.EnvironmentBranchSourceVersioned(state.ObjectBucketVersioning{ObjectBucketVersioning: tc.v}, now); got != tc.want {
			t.Errorf("%s: versioned = %v, want %v", tc.name, got, tc.want)
		}
	}
}
