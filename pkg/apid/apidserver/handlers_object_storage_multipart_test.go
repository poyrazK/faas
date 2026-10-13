package apidserver_test

import (
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectMultipartLayout(t *testing.T) {
	for _, test := range []struct {
		size      int64
		partSize  int64
		partCount int32
		wantErr   bool
	}{
		{0, 0, 0, true},
		{api.DefaultMultipartPartBytes, api.DefaultMultipartPartBytes, 1, false},
		{api.DefaultMultipartPartBytes + 1, api.DefaultMultipartPartBytes, 2, false},
		{api.MaxObjectUploadBytes, 525 << 20, 9987, false},
		{api.MaxObjectUploadBytes + 1, 0, 0, true},
	} {
		partSize, partCount, err := object.MultipartLayout(test.size)
		if errors.Is(err, objectstorage.ErrInvalid) != test.wantErr || !test.wantErr && (partSize != test.partSize || partCount != test.partCount) {
			t.Fatalf("multipartLayout(%d) = (%d, %d, %v)", test.size, partSize, partCount, err)
		}
	}
	upload := state.ObjectMultipartUpload{SizeBytes: api.DefaultMultipartPartBytes + 1, PartSizeBytes: api.DefaultMultipartPartBytes, PartCount: 2}
	if size, err := object.MultipartPartSize(upload, 2); err != nil || size != 1 {
		t.Fatalf("final part size = %d, %v", size, err)
	}
}

func TestObjectMultipartUploadLifecycle(t *testing.T) {
	_, teardown := apidserver.WithTestIdentities(t)
	defer teardown()
	e := apidserver.Setup(t, api.PlanHobby)
	if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	apidserver.CreateApp(t, e, "multipart-app")
	provider := &apidserver.FakeObjectProvider{}
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, provider, &apidserver.FakeObjectProvider{}, "external"))
	bucketPath := "/v1/apps/multipart-app/buckets"
	bucket := apidserver.BucketResponse(t, e.Do(t, "POST", bucketPath, map[string]any{"name": "assets"}, nil), 201)
	apidserver.QualifyObjectAccounting(t, e, bucket.ID)
	base := bucketPath + "/" + bucket.ID + "/multipart-uploads"

	createdResponse := e.Do(t, "POST", base, api.CreateObjectMultipartUploadRequest{Key: "large file.bin", SizeBytes: 70, ContentType: "application/octet-stream"}, nil)
	if createdResponse.Code != 201 || createdResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(createdResponse.Code, createdResponse.Body.String())
	}
	var upload api.ObjectMultipartUpload
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &upload); err != nil {
		t.Fatal(err)
	}
	if upload.State != state.ObjectMultipartActive || upload.PartCount != 1 || upload.PartSizeBytes != (*(*e.S()).ObjectStorage()).MaxPartBytes {
		t.Fatal(upload)
	}
	retryResponse := e.Do(t, "POST", base, api.CreateObjectMultipartUploadRequest{Key: upload.Key, SizeBytes: upload.SizeBytes, ContentType: upload.ContentType}, nil)
	var retry api.ObjectMultipartUpload
	if retryResponse.Code != 200 || json.Unmarshal(retryResponse.Body.Bytes(), &retry) != nil || retry.ID != upload.ID {
		t.Fatal("create was not idempotent", retryResponse.Code, retryResponse.Body.String())
	}
	if got := e.Do(t, "GET", base+"/"+upload.ID, nil, nil); got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	listed := e.Do(t, "GET", base+"?limit=1", nil, nil)
	var uploadList api.ObjectMultipartUploadList
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &uploadList) != nil || len(uploadList.Items) != 1 || uploadList.Items[0].ID != upload.ID {
		t.Fatal("session list", listed.Code, listed.Body.String())
	}
	partResponse := e.Do(t, "POST", base+"/"+upload.ID+"/parts/1/signed-url", api.ObjectMultipartPartSignRequest{ExpiresIn: 60}, nil)
	var signed api.ObjectSignedRequest
	if partResponse.Code != 200 || json.Unmarshal(partResponse.Body.Bytes(), &signed) != nil || signed.Headers["Content-Length"] != strconv.FormatInt(upload.SizeBytes, 10) {
		t.Fatal(partResponse.Code, partResponse.Body.String())
	}
	(*provider.MultipartParts()) = objectstorage.MultipartPartsPage{Items: []objectstorage.MultipartPart{{PartNumber: 1, ETag: `"etag"`, SizeBytes: upload.SizeBytes, LastModified: time.Now().UTC()}}}
	partsResponse := e.Do(t, "GET", base+"/"+upload.ID+"/parts?limit=1", nil, nil)
	var partsList api.ObjectMultipartPartList
	if partsResponse.Code != 200 || json.Unmarshal(partsResponse.Body.Bytes(), &partsList) != nil || len(partsList.Items) != 1 || partsList.Items[0].ETag != `"etag"` {
		t.Fatal("provider part list", partsResponse.Code, partsResponse.Body.String())
	}
	if invalid := e.Do(t, "POST", base+"/"+upload.ID+"/parts/2/signed-url", api.ObjectMultipartPartSignRequest{}, nil); invalid.Code != 400 {
		t.Fatal("invalid part accepted", invalid.Code, invalid.Body.String())
	}
	complete := api.CompleteObjectMultipartUploadRequest{Parts: []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"etag"`}}}
	completedResponse := e.Do(t, "POST", base+"/"+upload.ID+"/complete", complete, nil)
	var completed api.ObjectMultipartUpload
	if completedResponse.Code != 200 || json.Unmarshal(completedResponse.Body.Bytes(), &completed) != nil || completed.State != state.ObjectMultipartCompleted || len((*provider.MultipartCompleted())) != 1 {
		t.Fatal(completedResponse.Code, completedResponse.Body.String(), (*provider.MultipartCompleted()))
	}
	if repeated := e.Do(t, "POST", base+"/"+upload.ID+"/complete", complete, nil); repeated.Code != 200 || len((*provider.MultipartCompleted())) != 1 {
		t.Fatal("completion retry repeated provider call", repeated.Code, len((*provider.MultipartCompleted())))
	}

	abortResponse := e.Do(t, "POST", base, api.CreateObjectMultipartUploadRequest{Key: "cancel.bin", SizeBytes: 10}, nil)
	(*provider.MultipartParts()) = objectstorage.MultipartPartsPage{}
	var abortUpload api.ObjectMultipartUpload
	if abortResponse.Code != 201 || json.Unmarshal(abortResponse.Body.Bytes(), &abortUpload) != nil {
		t.Fatal(abortResponse.Code, abortResponse.Body.String())
	}
	if aborted := e.Do(t, "DELETE", base+"/"+abortUpload.ID, nil, nil); aborted.Code != 204 || len((*provider.MultipartAborted())) != 1 {
		t.Fatal(aborted.Code, aborted.Body.String(), (*provider.MultipartAborted()))
	}
	if repeated := e.Do(t, "DELETE", base+"/"+abortUpload.ID, nil, nil); repeated.Code != 204 || len((*provider.MultipartAborted())) != 1 {
		t.Fatal("abort retry repeated provider call", repeated.Code, len((*provider.MultipartAborted())))
	}
}

func TestObjectMultipartUploadRequiresWriteGrant(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	apidserver.CreateApp(t, e, "multipart-auth")
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, &apidserver.FakeObjectProvider{}, &apidserver.FakeObjectProvider{}, "external"))
	bucketPath := "/v1/apps/multipart-auth/buckets"
	bucket := apidserver.BucketResponse(t, e.Do(t, "POST", bucketPath, map[string]any{"name": "assets"}, nil), 201)
	apidserver.QualifyObjectAccounting(t, e, bucket.ID)
	plain, hash, _ := api.GenerateAPIKey()
	key, err := (*e.Store()).CreateAPIKey(t.Context(), (*e.Acct()).ID, hash, "writer", []string{api.ScopeStorageWrite})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + plain}
	base := bucketPath + "/" + bucket.ID + "/multipart-uploads"
	request := api.CreateObjectMultipartUploadRequest{Key: "large.bin", SizeBytes: 10}
	if response := e.Do(t, "POST", base, request, headers); response.Code != 403 {
		t.Fatal("ungranted writer created upload", response.Code, response.Body.String())
	}
	var access state.ObjectBucketAccessStore = (*e.Store())
	if _, err = access.SetObjectBucketAccessGrant(t.Context(), (*e.Acct()).ID, bucket.ID, key.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	if response := e.Do(t, "POST", base, request, headers); response.Code != 201 {
		t.Fatal("granted writer denied", response.Code, response.Body.String())
	}
}

func TestObjectMultipartCompletionIntentSurvivesProviderFailure(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	apidserver.CreateApp(t, e, "multipart-recovery")
	provider := &apidserver.FakeObjectProvider{}
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, provider, &apidserver.FakeObjectProvider{}, "external"))
	bucketPath := "/v1/apps/multipart-recovery/buckets"
	bucket := apidserver.BucketResponse(t, e.Do(t, "POST", bucketPath, map[string]any{"name": "assets"}, nil), 201)
	apidserver.QualifyObjectAccounting(t, e, bucket.ID)
	base := bucketPath + "/" + bucket.ID + "/multipart-uploads"
	created := e.Do(t, "POST", base, api.CreateObjectMultipartUploadRequest{Key: "recover.bin", SizeBytes: 10}, nil)
	var upload api.ObjectMultipartUpload
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &upload) != nil {
		t.Fatal(created.Code, created.Body.String())
	}
	(*provider.MultipartErr()) = objectstorage.ErrUnavailable
	parts := []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"persisted"`}}
	failed := e.Do(t, "POST", base+"/"+upload.ID+"/complete", api.CompleteObjectMultipartUploadRequest{Parts: parts}, nil)
	if failed.Code != 503 {
		t.Fatal(failed.Code, failed.Body.String())
	}
	app, err := (*e.Store()).AppBySlug(t.Context(), "multipart-recovery")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := (*e.Store()).GetObjectMultipartUpload(t.Context(), (*e.Acct()).ID, app.ID, bucket.ID, upload.ID)
	if err != nil || stored.State != state.ObjectMultipartCompleting || len(stored.Parts) != 1 || stored.Parts[0] != parts[0] || stored.LeaseToken != "" || stored.LastErrorCode != "temporary" {
		t.Fatal(stored, err)
	}
	if retried := e.Do(t, "POST", base+"/"+upload.ID+"/complete", api.CompleteObjectMultipartUploadRequest{Parts: parts}, nil); retried.Code != 409 {
		t.Fatal("retry bypassed durable cooldown", retried.Code, retried.Body.String())
	}
}

func TestMultipartLayoutHonorsPartLimit(t *testing.T) {
	size, count, err := object.MultipartLayoutWithLimit(65<<20, 64<<20)
	if err != nil || size != 64<<20 || count != 2 {
		t.Fatalf("size=%d count=%d err=%v", size, count, err)
	}
	if _, _, err = object.MultipartLayoutWithLimit(65<<20, 1<<20); err == nil {
		t.Fatal("allowed multipart parts smaller than S3 minimum")
	}
	if _, _, err = object.MultipartLayoutWithLimit(api.MaxObjectUploadBytes, 64<<20); err == nil {
		t.Fatal("exceeded part count with a constrained part ceiling")
	}
}
