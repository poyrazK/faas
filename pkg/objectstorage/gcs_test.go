package objectstorage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
)

type fakeGCSStore struct {
	createErr, reconcileErr, deleteBucketErr, deleteObjectErr error
	createdSpec                                               gcsBucketSpec
	bucketState                                               gcsBucketState
	objects                                                   []gcsObjectState
	next                                                      string
	object                                                    gcsObjectState
	reconciled                                                bool
}

func (s *fakeGCSStore) CreateBucket(_ context.Context, _ string, spec gcsBucketSpec) error {
	s.createdSpec = spec
	return s.createErr
}

func (s *fakeGCSStore) BucketState(context.Context, string) (gcsBucketState, error) {
	return s.bucketState, nil
}

func (s *fakeGCSStore) ReconcileBucket(_ context.Context, _ string, spec gcsBucketSpec) error {
	s.createdSpec = spec
	s.reconciled = true
	return s.reconcileErr
}

func (s *fakeGCSStore) DeleteBucket(context.Context, string) error { return s.deleteBucketErr }

func (s *fakeGCSStore) ListObjects(context.Context, string, string, string, int32) ([]gcsObjectState, string, error) {
	return s.objects, s.next, nil
}

func (s *fakeGCSStore) DeleteObject(context.Context, string, string) error {
	return s.deleteObjectErr
}

func (s *fakeGCSStore) ObjectState(context.Context, string, string) (gcsObjectState, error) {
	return s.object, nil
}

func testGCS(endpoint string, store gcsStore) *GCS {
	u, _ := url.Parse(endpoint)
	return &GCS{
		store: store, httpClient: http.DefaultClient, endpoint: u,
		serviceAccount: "gregale-storage@example.iam.gserviceaccount.com",
		location:       "EUROPE-WEST3", storageClass: "STANDARD", projectID: "project-a", managedLabel: "backend-label",
		origins: []string{"https://console.example.test"},
		sign:    func(context.Context, []byte) ([]byte, error) { return []byte("test-signature"), nil },
		now:     func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) },
	}
}

func TestGCSBucketSafetyRecoveryAndErrors(t *testing.T) {
	store := &fakeGCSStore{}
	p := testGCS(gcsDefaultEndpoint, store)
	if err := p.CreateBucket(context.Background(), "gregale-test"); err != nil {
		t.Fatal(err)
	}
	if store.createdSpec.ProjectID != "project-a" || store.createdSpec.Location != "EUROPE-WEST3" || store.createdSpec.ManagedLabel != "backend-label" {
		t.Fatalf("unsafe create spec: %+v", store.createdSpec)
	}
	attrs := gcsCreateBucketAttrs(store.createdSpec)
	if !attrs.UniformBucketLevelAccess.Enabled || attrs.PublicAccessPrevention != storage.PublicAccessPreventionEnforced || attrs.SoftDeletePolicy == nil || attrs.SoftDeletePolicy.RetentionDuration != 0 || len(attrs.CORS) != 1 {
		t.Fatalf("unsafe create attrs: %+v", attrs)
	}

	store.createErr = &gcsHTTPError{status: http.StatusConflict}
	store.bucketState = gcsBucketState{Location: "europe-west3", ManagedLabel: "backend-label"}
	if err := p.CreateBucket(context.Background(), "gregale-test"); err != nil || !store.reconciled {
		t.Fatal("lost create response was not recovered", err)
	}
	store.bucketState.ManagedLabel = "someone-else"
	if err := p.CreateBucket(context.Background(), "gregale-test"); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign bucket adopted", err)
	}

	store.deleteBucketErr = &gcsHTTPError{status: http.StatusNotFound}
	if err := p.DeleteBucket(context.Background(), "gregale-test"); err != nil {
		t.Fatal("missing bucket delete is not idempotent", err)
	}
	store.deleteBucketErr = &gcsHTTPError{status: http.StatusConflict, code: "BucketNotEmpty"}
	if err := p.DeleteBucket(context.Background(), "gregale-test"); !errors.Is(err, ErrNotEmpty) {
		t.Fatal(err)
	}
	store.deleteObjectErr = &gcsHTTPError{status: http.StatusNotFound}
	if err := p.DeleteObject(context.Background(), "gregale-test", "missing"); err != nil {
		t.Fatal("missing object delete is not idempotent", err)
	}
}

func TestGCSPresignBindsUploadAndMultipartShape(t *testing.T) {
	p := testGCS(gcsDefaultEndpoint, &fakeGCSStore{})
	for _, size := range []int64{0, 1, 100 << 20} {
		out, err := p.Presign(context.Background(), "gregale-test", SignRequest{Method: http.MethodPut, Key: "folder/hello +世界.txt", SizeBytes: &size})
		if err != nil {
			t.Fatal(size, err)
		}
		u, err := url.Parse(out.URL)
		if err != nil || u.Path != "/gregale-test/folder/hello +世界.txt" || u.Query().Get("X-Goog-Algorithm") != "GOOG4-RSA-SHA256" {
			t.Fatal("invalid signed URL", out.URL, err)
		}
		signedHeaders := u.Query().Get("X-Goog-SignedHeaders")
		if size == 0 && (!strings.Contains(signedHeaders, "content-md5") || out.Headers["Content-MD5"] != "1B2M2Y8AsgTpgAmY7PhCfg==") {
			t.Fatal("empty upload is not integrity-bound", out)
		}
		if size > 0 && (!strings.Contains(signedHeaders, "content-length") || out.Headers["Content-Length"] == "") {
			t.Fatal("upload length is not signed", out)
		}
	}
	download, err := p.Presign(context.Background(), "gregale-test", SignRequest{Method: http.MethodGet, Key: "hello", ExpiresIn: 60})
	if err != nil {
		t.Fatal(err)
	}
	query, _ := url.Parse(download.URL)
	if query.Query().Get("response-content-disposition") != "attachment" || query.Query().Get("response-content-type") != "application/octet-stream" {
		t.Fatal("unsafe download response", download.URL)
	}
	head, err := p.Presign(context.Background(), "gregale-test", SignRequest{Method: http.MethodHead, Key: "hello", ExpiresIn: 60})
	if err != nil {
		t.Fatal(err)
	}
	headURL, _ := url.Parse(head.URL)
	if head.Method != http.MethodHead || headURL.Query().Has("response-content-disposition") || headURL.Query().Has("response-content-type") {
		t.Fatalf("invalid HEAD request: %+v", head)
	}
	part, err := p.PresignMultipartPart(context.Background(), "gregale-test", MultipartPartRequest{Key: "large.bin", ProviderUploadID: "provider+id", PartNumber: 3, SizeBytes: 10, ExpiresIn: 60})
	if err != nil {
		t.Fatal(err)
	}
	partURL, _ := url.Parse(part.URL)
	if partURL.Query().Get("uploadId") != "provider+id" || partURL.Query().Get("partNumber") != "3" || !strings.Contains(partURL.Query().Get("X-Goog-SignedHeaders"), "content-length") {
		t.Fatal("multipart capability lost shape", part)
	}

	p.sign = func(context.Context, []byte) ([]byte, error) {
		return nil, &gcsHTTPError{status: http.StatusForbidden, code: "private-provider-detail"}
	}
	if _, err := p.Presign(context.Background(), "gregale-test", SignRequest{Method: http.MethodGet, Key: "hello"}); !errors.Is(err, ErrConfiguration) || strings.Contains(err.Error(), "private") {
		t.Fatal("signing error was not sanitized", err)
	}
}

func TestGCSMultipartOAuthProtocolAndCompletionRecovery(t *testing.T) {
	var initiated bool
	var initiateCalls, completeCalls, abortCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Has("uploads"):
			if initiated {
				_, _ = io.WriteString(w, `<ListBucketMultipartUploadsResult><IsTruncated>false</IsTruncated><Upload><Key>folder/large.bin</Key><UploadId>provider-id</UploadId></Upload></ListBucketMultipartUploadsResult>`)
			} else {
				_, _ = io.WriteString(w, `<ListBucketMultipartUploadsResult><IsTruncated>false</IsTruncated></ListBucketMultipartUploadsResult>`)
			}
		case r.Method == http.MethodPost && r.URL.Query().Has("uploads"):
			initiateCalls++
			initiated = true
			if r.URL.Path != "/gregale-test/folder/large.bin" || r.Header.Get("x-goog-meta-gregale-upload-id") != "session-1" || r.Header.Get("Content-Type") != "application/octet-stream" {
				t.Errorf("bad initiate request: %s %#v", r.URL.RequestURI(), r.Header)
			}
			_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>provider-id</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == http.MethodGet && r.URL.Query().Get("uploadId") == "provider-id":
			if r.URL.Query().Get("part-number-marker") != "1" || r.URL.Query().Get("max-parts") != "2" {
				t.Errorf("bad part listing: %s", r.URL.RequestURI())
			}
			_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>true</IsTruncated><NextPartNumberMarker>3</NextPartNumberMarker><Part><PartNumber>2</PartNumber><ETag>&quot;etag-2&quot;</ETag><Size>10</Size><LastModified>2026-09-07T00:00:00Z</LastModified></Part></ListPartsResult>`)
		case r.Method == http.MethodPost && r.URL.Query().Get("uploadId") == "provider-id":
			completeCalls++
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `<PartNumber>1</PartNumber><ETag>&#34;etag-1&#34;</ETag>`) {
				t.Errorf("bad completion body: %s", body)
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code><Message>private-detail</Message></Error>`)
		case r.Method == http.MethodDelete && r.URL.Query().Get("uploadId") == "provider-id":
			abortCalls++
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	store := &fakeGCSStore{object: gcsObjectState{Size: 10, Metadata: map[string]string{multipartSessionMetadata: "session-1"}}}
	p := testGCS(upstream.URL, store)
	p.httpClient = upstream.Client()

	id, err := p.EnsureMultipartUpload(context.Background(), "gregale-test", MultipartCreateRequest{SessionID: "session-1", Key: "folder/large.bin", SizeBytes: 10})
	if err != nil || id != "provider-id" {
		t.Fatal(id, err)
	}
	recovered, err := p.EnsureMultipartUpload(context.Background(), "gregale-test", MultipartCreateRequest{SessionID: "session-1", Key: "folder/large.bin", SizeBytes: 10})
	if err != nil || recovered != id || initiateCalls != 1 {
		t.Fatal("multipart initiation was not recoverable", recovered, err, initiateCalls)
	}
	page, err := p.ListMultipartParts(context.Background(), "gregale-test", MultipartListPartsRequest{Key: "folder/large.bin", ProviderUploadID: id, PartNumberMarker: 1, Limit: 2})
	if err != nil || len(page.Items) != 1 || page.Items[0].ETag != `"etag-2"` || page.NextPartNumberMarker != 3 {
		t.Fatal(page, err)
	}
	if err := p.CompleteMultipartUpload(context.Background(), "gregale-test", MultipartCompleteRequest{SessionID: "session-1", Key: "folder/large.bin", ProviderUploadID: id, SizeBytes: 10, Parts: []CompletedPart{{PartNumber: 1, ETag: `"etag-1"`}}}); err != nil {
		t.Fatal("lost completion response not recovered", err)
	}
	if err := p.AbortMultipartUpload(context.Background(), "gregale-test", MultipartAbortRequest{Key: "folder/large.bin", ProviderUploadID: id}); err != nil {
		t.Fatal("missing abort was not idempotent", err)
	}
	if completeCalls != 1 || abortCalls != 1 {
		t.Fatal(completeCalls, abortCalls)
	}
}

func TestGCSIAMBlobSigner(t *testing.T) {
	var gotPayload string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/serviceAccounts/gregale-storage@example.iam.gserviceaccount.com:signBlob") {
			t.Error(r.URL.Path)
		}
		var request struct {
			Payload string `json:"payload"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid JSON")
		}
		decoded, _ := base64.StdEncoding.DecodeString(request.Payload)
		gotPayload = string(decoded)
		_ = json.NewEncoder(w).Encode(map[string]string{"signedBlob": base64.StdEncoding.EncodeToString([]byte("signature"))})
	}))
	defer server.Close()
	signer := &gcsIAMBlobSigner{client: server.Client(), serviceAccount: "gregale-storage@example.iam.gserviceaccount.com", endpoint: server.URL}
	signed, err := signer.Sign(context.Background(), []byte("string-to-sign"))
	if err != nil || string(signed) != "signature" || gotPayload != "string-to-sign" {
		t.Fatal(string(signed), gotPayload, err)
	}
}

func TestGCSRegistryValidationAndFingerprint(t *testing.T) {
	backend := BackendConfig{
		ID: "gcs-frankfurt-v1", Driver: "gcs", Region: "eu-central-1", Namespace: "project-a",
		GCSLocation: "EUROPE-WEST3", GCSServiceAccount: "gregale-storage@example.iam.gserviceaccount.com",
	}
	factory := func(BackendConfig, func(string) string) (Provider, error) { return &GCS{}, nil }
	config := Config{DefaultRegion: backend.Region, Defaults: map[string]string{backend.Region: backend.ID}, Backends: []BackendConfig{backend}}
	if _, err := NewRegistry(config, func(string) string { return "" }, map[string]Factory{"gcs": factory}); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*BackendConfig){
		"location":        func(b *BackendConfig) { b.GCSLocation = "bad location" },
		"mixed_driver":    func(b *BackendConfig) { b.S3Region = "us-east-1" },
		"service_account": func(b *BackendConfig) { b.GCSServiceAccount = "not-an-account" },
		"storage_class":   func(b *BackendConfig) { b.GCSStorageClass = "HOT" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := backend
			mutate(&bad)
			config.Backends = []BackendConfig{bad}
			if _, err := NewRegistry(config, func(string) string { return "" }, map[string]Factory{"gcs": factory}); err == nil {
				t.Fatal("accepted invalid GCS config")
			}
		})
	}

	originalFingerprint := fingerprint(backend)
	rotated := backend
	rotated.GCSServiceAccount = "rotated@example.iam.gserviceaccount.com"
	if fingerprint(rotated) != originalFingerprint {
		t.Fatal("credential rotation changed placement")
	}
	explicitEndpoint := backend
	explicitEndpoint.Endpoint = gcsDefaultEndpoint
	if fingerprint(explicitEndpoint) != originalFingerprint {
		t.Fatal("default GCS endpoint was not canonicalized")
	}
	moved := backend
	moved.GCSLocation = "US-EAST1"
	if fingerprint(moved) == originalFingerprint {
		t.Fatal("GCS location change did not fence old buckets")
	}

	s3 := testBackend()
	want := sha256.Sum256([]byte(s3.Endpoint + "\x00" + s3.S3Region + "\x00" + s3.Namespace + "\x00" + s3.Driver + "\x00" + s3.Region))
	if fingerprint(s3) != hex.EncodeToString(want[:]) {
		t.Fatal("existing S3 placement fingerprint changed")
	}
}
