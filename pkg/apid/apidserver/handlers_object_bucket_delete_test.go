package apidserver_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 151
// Hunt #9 (production-us): deleting a bucket that still held objects revoked
// the app's compute binding first, the provider then refused the deletion and
// the bucket returned to ready — with the app's credentials and sealed
// GREGALE_S3_* settings gone. A non-empty bucket is now refused before any
// binding changes.
func TestBucketDeleteRefusesNonEmptyBucketBeforeRevokingBindings(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	e := apidserver.Setup(t, api.PlanHobby)
	if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	apidserver.CreateApp(t, e, "bucket-delete-app")
	provider := &apidserver.FakeObjectProvider{}
	*provider.Objects() = map[string][]byte{"kept.txt": []byte("data")}
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, provider, &apidserver.FakeObjectProvider{}, "external"))
	bucketPath := "/v1/apps/bucket-delete-app/buckets"
	bucket := apidserver.BucketResponse(t, e.Do(t, "POST", bucketPath, map[string]any{"name": "assets"}, nil), 201)
	bindingPath := bucketPath + "/" + bucket.ID + "/compute-bindings"
	if r := e.Do(t, "POST", bindingPath, api.CreateObjectStorageComputeBindingRequest{Permission: api.ObjectBucketPermissionReadWrite}, nil); r.Code != 201 {
		t.Fatalf("create compute binding = %d %s", r.Code, r.Body.String())
	}

	r := e.Do(t, "DELETE", bucketPath+"/"+bucket.ID, nil, nil)
	var problem api.Problem
	if r.Code != 409 || json.Unmarshal(r.Body.Bytes(), &problem) != nil || problem.Code != "bucket_not_empty" {
		t.Fatalf("delete non-empty bucket = %d %s, want 409 bucket_not_empty", r.Code, r.Body.String())
	}
	listed := e.Do(t, "GET", bindingPath, nil, nil)
	var bindings api.ObjectStorageComputeBindingList
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &bindings) != nil || len(bindings.Items) != 1 {
		t.Fatalf("bindings after refused delete = %d %s, want the binding kept", listed.Code, listed.Body.String())
	}

	*provider.Objects() = map[string][]byte{}
	if r := e.Do(t, "DELETE", bucketPath+"/"+bucket.ID, nil, nil); r.Code != 204 {
		t.Fatalf("delete empty bucket = %d %s", r.Code, r.Body.String())
	}
}
