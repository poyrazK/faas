package apidserver_test

import (
	"encoding/json"
	"strings"
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

// 11-03 review: deletion revoked the app's compute bindings before claiming
// the bucket, and the claim refuses (409) while a multipart upload is open, a
// write is pending or a fence exists. The bucket stayed ready with the app's
// credentials gone. Bindings now go only once the provider deleted the bucket.
func TestBucketDeleteRefusedByOpenUploadKeepsBindings(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	_, restore := apidserver.WithTestIdentities(t)
	defer restore()
	e := apidserver.Setup(t, api.PlanHobby)
	if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	app := apidserver.CreateApp(t, e, "bucket-upload-app")
	provider := &apidserver.FakeObjectProvider{}
	*provider.Objects() = map[string][]byte{}
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, provider, &apidserver.FakeObjectProvider{}, "external"))
	bucketPath := "/v1/apps/bucket-upload-app/buckets"
	bucket := apidserver.BucketResponse(t, e.Do(t, "POST", bucketPath, map[string]any{"name": "assets"}, nil), 201)
	apidserver.QualifyObjectAccounting(t, e, bucket.ID)
	bindingPath := bucketPath + "/" + bucket.ID + "/compute-bindings"
	created := e.Do(t, "POST", bindingPath, api.CreateObjectStorageComputeBindingRequest{Permission: api.ObjectBucketPermissionReadWrite}, nil)
	if created.Code != 201 {
		t.Fatalf("create compute binding = %d %s", created.Code, created.Body.String())
	}
	uploads := bucketPath + "/" + bucket.ID + "/multipart-uploads"
	started := e.Do(t, "POST", uploads, api.CreateObjectMultipartUploadRequest{Key: "big.bin", SizeBytes: 70, ContentType: "application/octet-stream"}, nil)
	var upload api.ObjectMultipartUpload
	if started.Code != 201 || json.Unmarshal(started.Body.Bytes(), &upload) != nil {
		t.Fatalf("start upload = %d %s", started.Code, started.Body.String())
	}

	refused := e.Do(t, "DELETE", bucketPath+"/"+bucket.ID, nil, nil)
	var problem api.Problem
	if refused.Code != 409 || json.Unmarshal(refused.Body.Bytes(), &problem) != nil || problem.Code == "bucket_not_empty" {
		t.Fatalf("delete with an open upload = %d %s, want the claim's 409", refused.Code, refused.Body.String())
	}
	listed := e.Do(t, "GET", bindingPath, nil, nil)
	var bindings api.ObjectStorageComputeBindingList
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &bindings) != nil || len(bindings.Items) != 1 {
		t.Fatalf("bindings after refused delete = %d %s, want the binding kept", listed.Code, listed.Body.String())
	}
	store := *e.Store()
	s3Secrets := func() int {
		secrets, err := store.ListAppSecrets(t.Context(), (*e.Acct()).ID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, secret := range secrets {
			if strings.HasPrefix(secret.Key, "GREGALE_S3_") {
				n++
			}
		}
		return n
	}
	if n := s3Secrets(); n == 0 {
		t.Fatal("the refused delete removed the app's GREGALE_S3_* secrets")
	}

	if r := e.Do(t, "DELETE", uploads+"/"+upload.ID, nil, nil); r.Code != 204 {
		t.Fatalf("abort upload = %d %s", r.Code, r.Body.String())
	}
	if r := e.Do(t, "DELETE", bucketPath+"/"+bucket.ID, nil, nil); r.Code != 204 {
		t.Fatalf("delete bucket = %d %s", r.Code, r.Body.String())
	}
	if n := s3Secrets(); n != 0 {
		t.Fatalf("%d GREGALE_S3_* secrets left after the bucket was deleted", n)
	}
}
