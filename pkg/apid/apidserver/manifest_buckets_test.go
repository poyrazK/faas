package apidserver_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261106105015 §1: a project apply creates the gregale.yaml buckets that
// say `create`, binds them once, and a retried apply reuses both.
func TestProjectManifestBucketsCreateBindAndRetry(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	e := apidserver.SetupSecrets(t, api.PlanPro)
	server, store, account := *e.S(), *e.Store(), *e.Acct()
	if err := (*server.RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	provider := newStorageTestObjectProvider(map[string][]byte{}, nil)
	server.WithObjectStorage(apidserver.ObjectRegistry(t, provider, newStorageTestObjectProvider(nil, nil), "external"))
	if rec := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "store"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", rec.Code, rec.Body.String())
	}
	app, err := store.AppBySlug(t.Context(), "store")
	if err != nil {
		t.Fatal(err)
	}
	segments := object.ManifestBucket{Name: "segments", Scope: api.DefaultEnvScope, Prefix: "GREGALE_S3_SEGMENTS",
		Permission: api.ObjectBucketPermissionReadWrite, Label: "compute", Create: true}
	for attempt := 0; attempt < 2; attempt++ {
		if problem := server.ApplyProjectManifestBucketsForTest(t.Context(), account, app, []object.ManifestBucket{segments}); problem != nil {
			t.Fatalf("attempt %d: %+v", attempt, problem)
		}
	}
	buckets, err := store.ListObjectBuckets(t.Context(), account.ID, app.ID)
	if err != nil || len(buckets) != 1 || buckets[0].Name != "segments" || buckets[0].State != "ready" {
		t.Fatalf("buckets %+v err %v", buckets, err)
	}
	credentials, err := store.ListObjectS3Credentials(t.Context(), account.ID, buckets[0].ID)
	if err != nil || len(credentials) != 1 || credentials[0].ManagedPrefix != "GREGALE_S3_SEGMENTS" || credentials[0].ManagedAppID != app.ID {
		t.Fatalf("credentials %+v err %v; want one binding after a retried apply", credentials, err)
	}
	if len(*provider.Created()) != 1 {
		t.Fatalf("provider buckets created %d times", len(*provider.Created()))
	}

	// A different permission on the same prefix is a conflict, not a second binding.
	readOnly := segments
	readOnly.Create, readOnly.Permission = false, api.ObjectBucketPermissionRead
	problem := server.ApplyProjectManifestBucketsForTest(t.Context(), account, app, []object.ManifestBucket{readOnly})
	if problem == nil || problem.Status != http.StatusConflict {
		t.Fatalf("problem %+v, want a permission conflict", problem)
	}

	missing := object.ManifestBucket{Name: "exports", Scope: api.DefaultEnvScope, Prefix: "GREGALE_S3_EXPORTS",
		Permission: api.ObjectBucketPermissionReadWrite, Label: "compute"}
	problem = server.ApplyProjectManifestBucketsForTest(t.Context(), account, app, []object.ManifestBucket{missing})
	if problem == nil || problem.Status != http.StatusNotFound {
		t.Fatalf("problem %+v, want not found for a bucket without create", problem)
	}

	// Review finding 5: after the binding is deleted ("delete the binding and
	// redeploy"), the next apply binds again instead of colliding with the
	// revoked row's stable ID.
	bucketID := buckets[0].ID
	if rec := e.Do(t, http.MethodDelete, "/v1/apps/store/buckets/"+bucketID+"/compute-bindings/"+credentials[0].ID, nil, nil); rec.Code >= 300 {
		t.Fatalf("delete binding: %d %s", rec.Code, rec.Body.String())
	}
	if problem := server.ApplyProjectManifestBucketsForTest(t.Context(), account, app, []object.ManifestBucket{segments}); problem != nil {
		t.Fatalf("re-bind after delete: %+v", problem)
	}
	active := 0
	credentials, _ = store.ListObjectS3Credentials(t.Context(), account.ID, bucketID)
	for _, credential := range credentials {
		if credential.Status == "active" {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("active bindings %d after re-bind, want 1", active)
	}

	// Review finding 4: a deleted bucket's name can be declared again.
	if rec := e.Do(t, http.MethodDelete, "/v1/apps/store/buckets/"+bucketID, nil, nil); rec.Code >= 300 {
		t.Fatalf("delete bucket: %d %s", rec.Code, rec.Body.String())
	}
	if problem := server.ApplyProjectManifestBucketsForTest(t.Context(), account, app, []object.ManifestBucket{segments}); problem != nil {
		t.Fatalf("re-create after delete: %+v", problem)
	}
	buckets, _ = store.ListObjectBuckets(t.Context(), account.ID, app.ID)
	recreated := false
	for _, bucket := range buckets {
		recreated = recreated || bucket.Name == "segments" && bucket.State == "ready" && bucket.ID != bucketID
	}
	if !recreated {
		t.Fatalf("buckets %+v, want a new ready segments bucket", buckets)
	}
}

// Review findings 3 and 6: limit and ownership problems stop the plan before
// anything is created.
func TestCheckManifestBucketsRefusesBeforeMutation(t *testing.T) {
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	e := apidserver.SetupSecrets(t, api.PlanPro)
	server, store, account := *e.S(), *e.Store(), *e.Acct()
	if err := (*server.RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	provider := newStorageTestObjectProvider(map[string][]byte{}, nil)
	server.WithObjectStorage(apidserver.ObjectRegistry(t, provider, newStorageTestObjectProvider(nil, nil), "external"))
	limit := server.ObjectStorageMaxBucketsPerAppForTest()
	var many []object.ManifestBucket
	for i := 0; i <= limit; i++ {
		name := fmt.Sprintf("bucket-%d", i)
		many = append(many, object.ManifestBucket{Name: name, Scope: api.DefaultEnvScope, Prefix: api.DefaultObjectStorageBindingPrefix(name),
			Permission: api.ObjectBucketPermissionReadWrite, Create: true})
	}
	var limitErr *state.ObjectStorageLimitError
	if err := server.CheckManifestBucketsForTest(t.Context(), account, nil, many); !errors.As(err, &limitErr) {
		t.Fatalf("err = %v, want the per-app limit for %d new buckets", err, len(many))
	}
	if len(*provider.Created()) != 0 {
		t.Fatal("created buckets while checking")
	}
	orgApp := state.App{ID: "org-app", AccountID: "another-account", Slug: "org-store"}
	if err := server.CheckManifestBucketsForTest(t.Context(), account, &orgApp, many[:1]); !errors.Is(err, object.ErrManifestBucketsOrganization) {
		t.Fatalf("err = %v, want organization-owned apps refused", err)
	}
	if buckets, _ := store.ListObjectBuckets(t.Context(), account.ID, "org-app"); len(buckets) != 0 {
		t.Fatal("reserved a bucket while checking")
	}
}
