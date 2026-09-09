package s3gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	testAccess = "GRGAAAAAAAAAAAAAAAAA"
	testSecret = "0123456789012345678901234567890123456789"
)

type gatewayTestStore struct {
	credential state.ObjectS3Credential
	bucket     state.ObjectBucket
	touched    int
	admitted   []string
}

func (s *gatewayTestStore) CreateObjectS3Credential(context.Context, state.ObjectS3Credential, int) (state.ObjectS3Credential, error) {
	panic("not used")
}
func (s *gatewayTestStore) ListObjectS3Credentials(context.Context, string, string) ([]state.ObjectS3Credential, error) {
	panic("not used")
}
func (s *gatewayTestStore) RevokeObjectS3Credential(context.Context, string, string, string) error {
	panic("not used")
}
func (s *gatewayTestStore) ResolveObjectS3Credential(_ context.Context, access string) (state.ObjectS3Credential, state.ObjectBucket, error) {
	if access != s.credential.AccessKeyID {
		return state.ObjectS3Credential{}, state.ObjectBucket{}, state.ErrNotFound
	}
	return s.credential, s.bucket, nil
}
func (s *gatewayTestStore) TouchObjectS3Credential(context.Context, string, time.Time) error {
	s.touched++
	return nil
}
func (s *gatewayTestStore) AdmitObjectURL(_ context.Context, _, _, key string, _ int64, _ bool, _ api.ObjectStoragePolicy) error {
	s.admitted = append(s.admitted, key)
	return nil
}

type gatewayTestProvider struct {
	objects objectstorage.ObjectPage
	deleted string
}

type gatewayRequestMetrics struct {
	calls int
	err   error
}

func (m *gatewayRequestMetrics) RecordObjectStorageProviderRequest(context.Context, string, time.Time) error {
	m.calls++
	return m.err
}
func (*gatewayRequestMetrics) ListObjectStorageProviderRequestMetrics(context.Context, string, string, time.Time) ([]state.ObjectStorageProviderRequestMetric, error) {
	return nil, nil
}
func (*gatewayRequestMetrics) ListObjectStorageProviderBuckets(context.Context, string, string) ([]state.ObjectBucket, error) {
	return nil, nil
}

func (*gatewayTestProvider) CreateBucket(context.Context, string) error { return nil }
func (*gatewayTestProvider) DeleteBucket(context.Context, string) error { return nil }
func (p *gatewayTestProvider) ListObjects(context.Context, string, string, string, int32) (objectstorage.ObjectPage, error) {
	return p.objects, nil
}
func (p *gatewayTestProvider) DeleteObject(_ context.Context, _ string, key string) error {
	p.deleted = key
	return nil
}
func (*gatewayTestProvider) Presign(_ context.Context, bucket string, request objectstorage.SignRequest) (objectstorage.SignedRequest, error) {
	return objectstorage.SignedRequest{URL: "https://provider.invalid/" + bucket + "/" + request.Key, Method: request.Method, Headers: map[string]string{"Content-Type": request.ContentType}}, nil
}
func (*gatewayTestProvider) EnsureMultipartUpload(context.Context, string, objectstorage.MultipartCreateRequest) (string, error) {
	panic("not used")
}
func (*gatewayTestProvider) PresignMultipartPart(context.Context, string, objectstorage.MultipartPartRequest) (objectstorage.SignedRequest, error) {
	panic("not used")
}
func (*gatewayTestProvider) ListMultipartParts(context.Context, string, objectstorage.MultipartListPartsRequest) (objectstorage.MultipartPartsPage, error) {
	panic("not used")
}
func (*gatewayTestProvider) CompleteMultipartUpload(context.Context, string, objectstorage.MultipartCompleteRequest) error {
	panic("not used")
}
func (*gatewayTestProvider) AbortMultipartUpload(context.Context, string, objectstorage.MultipartAbortRequest) error {
	panic("not used")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newGatewayTestHandler(t *testing.T, permission string, roundTrip roundTripFunc) (*Handler, *gatewayTestStore, *gatewayTestProvider) {
	t.Helper()
	provider := &gatewayTestProvider{objects: objectstorage.ObjectPage{Items: []objectstorage.Object{{Key: "folder/a.txt", Size: 3, LastModified: time.Date(2026, 9, 7, 1, 2, 3, 0, time.UTC)}}}}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{
		DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"}, MaxUploadBytes: 1024,
		Backends: []objectstorage.BackendConfig{{ID: "test", Driver: "test", Region: "us-east-1", Namespace: "fixture"}},
	}, func(string) string { return "" }, map[string]objectstorage.Factory{"test": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
		return provider, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	store := &gatewayTestStore{
		credential: state.ObjectS3Credential{ID: "credential", AccountID: "account", BucketID: "bucket-id", AccessKeyID: testAccess, SecretSealed: []byte("sealed"), Permission: permission, Status: state.ObjectS3CredentialStatusActive},
		bucket:     state.ObjectBucket{ID: "bucket-id", AccountID: "account", Name: "assets", PhysicalName: "gregale-physical", State: "ready", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
	}
	handler, err := New(Config{
		Registry: registry, Store: store, Host: "s3.gregale.dev", Region: "us-east-1", SpoolDir: t.TempDir(),
		OpenSecret: func(sealed []byte) (string, error) {
			if string(sealed) != "sealed" {
				t.Fatal("unexpected sealed secret")
			}
			return testSecret, nil
		},
		HTTPClient: &http.Client{Transport: roundTrip, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		Now:        func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, store, provider
}

func signedGatewayRequest(t *testing.T, method, target string, body []byte, payloadHash string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Host = "s3.gregale.dev"
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	request.Header.Set("X-Amz-Date", "20260907T120000Z")
	if method == http.MethodPut {
		request.Header.Set("Content-Type", "application/octet-stream")
	}
	if err := awsv4.NewSigner().SignHTTP(context.Background(), aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, request, payloadHash, "s3", "us-east-1", time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return request
}

func TestGatewayPutAndGetHideProvider(t *testing.T) {
	var uploaded []byte
	handler, store, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut {
			uploaded, _ = io.ReadAll(r.Body)
			header := make(http.Header)
			header.Set("ETag", `"etag"`)
			header.Set("x-goog-generation", "provider-leak")
			return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		header := make(http.Header)
		header.Set("Content-Type", "text/plain")
		header.Set("Content-Length", "5")
		header.Set("x-goog-generation", "provider-leak")
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader("hello")), Request: r}, nil
	})
	body := []byte("hello")
	sum := sha256.Sum256(body)
	put := signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/folder/hello.txt?x-id=PutObject", body, hex.EncodeToString(sum[:]))
	putRecorder := httptest.NewRecorder()
	handler.ServeHTTP(putRecorder, put)
	if putRecorder.Code != http.StatusOK || string(uploaded) != "hello" || putRecorder.Header().Get("ETag") != `"etag"` {
		t.Fatalf("PUT = %d headers=%v uploaded=%q body=%s", putRecorder.Code, putRecorder.Header(), uploaded, putRecorder.Body.String())
	}
	if putRecorder.Header().Get("x-goog-generation") != "" {
		t.Fatal("provider header escaped branded endpoint")
	}

	get := signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets/folder/hello.txt?x-id=GetObject", nil, "UNSIGNED-PAYLOAD")
	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, get)
	if getRecorder.Code != http.StatusOK || getRecorder.Body.String() != "hello" || getRecorder.Header().Get("x-goog-generation") != "" {
		t.Fatalf("GET = %d headers=%v body=%q", getRecorder.Code, getRecorder.Header(), getRecorder.Body.String())
	}
	if len(store.admitted) != 2 || store.admitted[0] != "folder/hello.txt" || store.touched != 1 {
		t.Fatalf("admission=%v touched=%d", store.admitted, store.touched)
	}
}

func TestGatewayBlocksProviderCallWhenRequestMetricCannotBeRecorded(t *testing.T) {
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, func(*http.Request) (*http.Response, error) {
		t.Fatal("provider must not be called when request metrics fail")
		return nil, nil
	})
	metrics := &gatewayRequestMetrics{err: state.ErrConflict}
	handler.requestMetrics = metrics
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets/key", nil, "UNSIGNED-PAYLOAD"))
	if recorder.Code != http.StatusServiceUnavailable || metrics.calls != 1 || !strings.Contains(recorder.Body.String(), "ServiceUnavailable") {
		t.Fatalf("response = %d %s calls=%d", recorder.Code, recorder.Body.String(), metrics.calls)
	}
}

func TestGatewayListsOnlyCredentialBucket(t *testing.T) {
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, func(*http.Request) (*http.Response, error) {
		t.Fatal("list must use provider interface, not HTTP")
		return nil, nil
	})
	for _, target := range []string{"https://s3.gregale.dev/", "https://s3.gregale.dev/assets?list-type=2&max-keys=10&x-id=ListObjectsV2"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodGet, target, nil, "UNSIGNED-PAYLOAD"))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "assets") {
			t.Fatalf("GET %s = %d %s", target, recorder.Code, recorder.Body.String())
		}
		if err := xml.Unmarshal(recorder.Body.Bytes(), new(any)); err != nil {
			t.Fatalf("invalid XML: %v", err)
		}
	}
}

func TestGatewayRejectsBadBodyAndWrongBucket(t *testing.T) {
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) {
		t.Fatal("rejected request reached provider")
		return nil, nil
	})
	other := sha256.Sum256([]byte("other"))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/bad.txt", []byte("body"), hex.EncodeToString(other[:])))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "XAmzContentSHA256Mismatch") {
		t.Fatalf("bad digest = %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/other/key", nil, "UNSIGNED-PAYLOAD"))
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "NoSuchBucket") {
		t.Fatalf("wrong bucket = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestGatewayBoundsConcurrentPutStaging(t *testing.T) {
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionWrite, func(*http.Request) (*http.Response, error) {
		t.Fatal("throttled request reached provider")
		return nil, nil
	})
	for range cap(handler.putSlots) {
		handler.putSlots <- struct{}{}
	}
	body := []byte("body")
	sum := sha256.Sum256(body)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/key", body, hex.EncodeToString(sum[:])))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "SlowDown") {
		t.Fatalf("concurrent PUT limit = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestGatewayPermissionAndUnsupportedMultipart(t *testing.T) {
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, func(*http.Request) (*http.Response, error) {
		t.Fatal("rejected request reached provider")
		return nil, nil
	})
	body := []byte("body")
	sum := sha256.Sum256(body)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/key", body, hex.EncodeToString(sum[:])))
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "AccessDenied") {
		t.Fatalf("permission = %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, signedGatewayRequest(t, http.MethodPost, "https://s3.gregale.dev/assets/key?uploads=", nil, "UNSIGNED-PAYLOAD"))
	if recorder.Code != http.StatusNotImplemented || !strings.Contains(recorder.Body.String(), "NotImplemented") {
		t.Fatalf("multipart = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestGatewayRejectsTamperedSignatureAndReportsPresignGap(t *testing.T) {
	handler, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, func(*http.Request) (*http.Response, error) {
		t.Fatal("rejected request reached provider")
		return nil, nil
	})

	tampered := signedGatewayRequest(t, http.MethodGet, "https://s3.gregale.dev/assets/file.txt", nil, "UNSIGNED-PAYLOAD")
	tampered.Header.Set("Authorization", strings.Replace(tampered.Header.Get("Authorization"), "Signature=", "Signature=00", 1))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, tampered)
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "SignatureDoesNotMatch") {
		t.Fatalf("tampered signature = %d %s", recorder.Code, recorder.Body.String())
	}

	presigned := httptest.NewRequest(http.MethodGet, "https://s3.gregale.dev/assets/file.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256", nil)
	presigned.Host = "s3.gregale.dev"
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, presigned)
	if recorder.Code != http.StatusNotImplemented || !strings.Contains(recorder.Body.String(), "NotImplemented") {
		t.Fatalf("presigned query = %d %s", recorder.Code, recorder.Body.String())
	}

	streaming := httptest.NewRequest(http.MethodPut, "https://s3.gregale.dev/assets/file.txt", strings.NewReader("chunked"))
	streaming.Host = "s3.gregale.dev"
	streaming.Header.Set("X-Amz-Content-Sha256", "STREAMING-AWS4-HMAC-SHA256-PAYLOAD")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, streaming)
	if recorder.Code != http.StatusNotImplemented || !strings.Contains(recorder.Body.String(), "NotImplemented") {
		t.Fatalf("streaming payload = %d %s", recorder.Code, recorder.Body.String())
	}
}
