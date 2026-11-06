package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/object"
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
}
