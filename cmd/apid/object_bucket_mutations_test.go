// adr: 590
package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func mutationAPIFixture(t *testing.T) (testEnv, *fakeObjectProvider, state.ObjectBucket, string) {
	t.Helper()
	_, restoreIdentities := withTestIdentities(t)
	t.Cleanup(restoreIdentities)
	e := setup(t, api.PlanPro)
	setS3Flag(t, e, true)
	app := createApp(t, e, "write-source")
	provider := &fakeObjectProvider{}
	e.s.WithObjectStorage(objectRegistry(t, provider, &fakeObjectProvider{}, "external"))
	path := "/v1/apps/write-source/buckets"
	b := bucketResponse(t, e.do(t, "POST", path, map[string]any{"name": "assets"}, nil), 201)
	qualifyObjectAccounting(t, e, b.ID)
	bucket, err := e.store.GetObjectBucket(context.Background(), e.acct.ID, app.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	return e, provider, bucket, path + "/" + b.ID
}

func TestObjectBucketMutationAPIUploadGrantAndFence(t *testing.T) {
	e, provider, b, path := mutationAPIFixture(t)
	request := map[string]any{"method": "PUT", "key": "file", "size_bytes": 10}
	if response := e.do(t, "POST", path+"/signed-url", request, nil); response.Code != 200 {
		t.Fatalf("sign = %d %s", response.Code, response.Body.String())
	}
	token := uuid.NewString()
	fence, err := e.store.AcquireObjectBucketWriteFence(context.Background(), b, token)
	if err != nil || fence.Requests != 0 || fence.NativeGrants != 0 {
		t.Fatalf("broker URL unexpectedly created an outstanding native grant: %+v %v", fence, err)
	}
	calls := len(provider.accessed)
	for _, test := range []struct {
		method, path string
		body         any
	}{
		{"POST", path + "/signed-url", request},
		{"DELETE", path + "/objects?key=file", nil},
		{"POST", path + "/multipart-uploads", api.CreateObjectMultipartUploadRequest{Key: "parts", SizeBytes: 10}},
	} {
		response := e.do(t, test.method, test.path, test.body, nil)
		if response.Code != 503 || !strings.Contains(response.Body.String(), "object_storage_checkpoint_active") || len(provider.accessed) != calls {
			t.Fatalf("fenced API reached provider: %s HTTP=%d calls=%d/%d %s", test.path, response.Code, len(provider.accessed), calls, response.Body.String())
		}
	}
	if response := e.do(t, "POST", path+"/signed-url", map[string]any{"method": "GET", "key": "file"}, nil); response.Code != 200 {
		t.Fatalf("fence blocked read: %d %s", response.Code, response.Body.String())
	}
	fence, err = e.store.ReadObjectBucketWriteFence(context.Background(), b, token)
	if err != nil || fence.NativeGrants != 0 || fence.Requests != 0 {
		t.Fatalf("failed writes or read changed grant count: %+v %v", fence, err)
	}
}

func TestObjectUploadGrantAPIMultipartSigningDoesNotContactProvider(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "provider-available", true: "provider-unavailable"}[failed], func(t *testing.T) {
			e, provider, b, path := mutationAPIFixture(t)
			base := path + "/multipart-uploads"
			response := e.do(t, "POST", base, api.CreateObjectMultipartUploadRequest{Key: "multipart", SizeBytes: 10}, nil)
			var upload api.ObjectMultipartUpload
			if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &upload) != nil {
				t.Fatalf("create = %d %s", response.Code, response.Body.String())
			}
			if failed {
				provider.multipartErr = objectstorage.ErrUnavailable
			}
			response = e.do(t, "POST", base+"/"+upload.ID+"/parts/1/signed-url", api.ObjectMultipartPartSignRequest{ExpiresIn: 60}, nil)
			want := 200
			if response.Code != want {
				t.Fatalf("sign = %d %s", response.Code, response.Body.String())
			}
			token := uuid.NewString()
			fence, err := e.store.AcquireObjectBucketWriteFence(context.Background(), b, token)
			if err != nil || fence.Requests != 1 || fence.Multipart != 1 || fence.NativeGrants != 0 {
				t.Fatalf("broker signing changed original session custody: %+v %v", fence, err)
			}
			calls := len(provider.accessed)
			response = e.do(t, "POST", base+"/"+upload.ID+"/parts/1/signed-url", api.ObjectMultipartPartSignRequest{ExpiresIn: 60}, nil)
			if response.Code != 503 || len(provider.accessed) != calls {
				t.Fatalf("fence allowed new native part capability: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
