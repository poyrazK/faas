package objectstorage

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/smithy-go"
)

func testCredentials(name string) string {
	if name == "KEY" {
		return "test-key"
	}
	if name == "SECRET" {
		return "test-secret"
	}
	return ""
}

func TestS3RecoveryErrorClassification(t *testing.T) {
	for _, tt := range []struct {
		code string
		want error
	}{
		{"AccessDenied", ErrConfiguration}, {"InvalidAccessKeyId", ErrConfiguration},
		{"SignatureDoesNotMatch", ErrConfiguration}, {"ExpiredToken", ErrConfiguration},
		{"InvalidToken", ErrConfiguration}, {"AuthorizationHeaderMalformed", ErrConfiguration},
		{"InvalidRequest", ErrInvalid}, {"InvalidPart", ErrInvalid}, {"InvalidPartOrder", ErrInvalid},
		{"EntityTooSmall", ErrInvalid}, {"OperationAborted", ErrConflict}, {"SlowDown", ErrUnavailable},
	} {
		got := normalize(&smithy.GenericAPIError{Code: tt.code, Message: "secret-provider-detail"})
		if !errors.Is(got, tt.want) || strings.Contains(got.Error(), "secret-provider-detail") {
			t.Fatal(tt.code, got)
		}
	}
}

func TestS3RecoveryAfterPartialProvisioning(t *testing.T) {
	var created bool
	var corsCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Path != "/gregale-recovery" {
			t.Error("changed physical name", r.URL.Path)
		}
		switch {
		case r.Method == "DELETE":
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchBucket</Code></Error>`)
		case r.URL.Query().Has("cors"):
			corsCalls++
			if corsCalls == 1 {
				w.WriteHeader(403)
				_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>private-detail</Message></Error>`)
			}
		case created:
			w.WriteHeader(409)
			_, _ = io.WriteString(w, `<Error><Code>BucketAlreadyOwnedByYou</Code></Error>`)
		default:
			created = true
		}
	}))
	defer upstream.Close()
	c := testBackend()
	c.Endpoint = upstream.URL
	c.AllowedOrigins = []string{"https://console.example.test"}
	p, err := NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CreateBucket(context.Background(), "gregale-recovery"); !errors.Is(err, ErrConfiguration) {
		t.Fatal(err)
	}
	// Restarting the driver loses process state, but repeats the durable name.
	p, err = NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CreateBucket(context.Background(), "gregale-recovery"); err != nil {
		t.Fatal(err)
	}
	if !created || corsCalls != 2 {
		t.Fatal(created, corsCalls)
	}
	if err := p.DeleteBucket(context.Background(), "gregale-recovery"); err != nil {
		t.Fatal("lost delete response not idempotent", err)
	}
}
func testBackend() BackendConfig {
	return BackendConfig{ID: "external-a", Driver: "s3", Region: "us-east-1", Namespace: "test-project", Endpoint: "https://s3.example.test", S3Region: "us-east-1", PathStyle: true, AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET"}
}

func TestS3Presign(t *testing.T) {
	p, err := NewS3(testBackend(), testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int64{0, 1, 100 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			out, err := p.Presign(context.Background(), "gregale-test", SignRequest{Method: "PUT", Key: "folder/hello +世界.txt", SizeBytes: &size})
			if err != nil {
				t.Fatalf("size %d: %v", size, err)
			}
			u, err := url.Parse(out.URL)
			if err != nil {
				t.Fatal(err)
			}
			header := "content-length"
			if size == 0 {
				header = "content-md5"
			}
			if !strings.Contains(u.Query().Get("X-Amz-SignedHeaders"), header) {
				t.Fatal("unsigned upload length", out.Headers)
			}
			if strings.Contains(out.URL, "test-secret") || u.Path != "/gregale-test/folder/hello +世界.txt" {
				t.Fatal("incorrect URL")
			}
		})
	}
	out, err := p.Presign(context.Background(), "gregale-test", SignRequest{Method: "GET", Key: "index.html", ExpiresIn: 60})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(out.URL)
	if u.Query().Get("response-content-disposition") != "attachment" || u.Query().Get("X-Amz-Expires") != "60" {
		t.Fatal("unsafe download")
	}
}

func TestS3MultipartProtocolAndCompletionRecovery(t *testing.T) {
	var completeCalls, abortCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Has("uploads"):
			_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
		case r.Method == http.MethodPost && r.URL.Query().Has("uploads"):
			if r.Header.Get("X-Amz-Meta-Gregale-Upload-Id") != "session-1" {
				t.Errorf("missing recovery metadata: %q", r.Header.Get("X-Amz-Meta-Gregale-Upload-Id"))
			}
			_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>gregale-test</Bucket><Key>large.bin</Key><UploadId>provider-id</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == http.MethodPost && r.URL.Query().Get("uploadId") == "provider-id":
			completeCalls++
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
		case r.Method == http.MethodHead:
			w.Header().Set("Content-Length", "10")
			w.Header().Set("X-Amz-Meta-Gregale-Upload-Id", "session-1")
		case r.Method == http.MethodDelete && r.URL.Query().Get("uploadId") == "provider-id":
			abortCalls++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected multipart request: %s %s", r.Method, r.URL.RequestURI())
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	c := testBackend()
	c.Endpoint = upstream.URL
	provider, err := NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	providerID, err := provider.EnsureMultipartUpload(context.Background(), "gregale-test", MultipartCreateRequest{SessionID: "session-1", Key: "large.bin", SizeBytes: 10})
	if err != nil || providerID != "provider-id" {
		t.Fatal(providerID, err)
	}
	part, err := provider.PresignMultipartPart(context.Background(), "gregale-test", MultipartPartRequest{Key: "large.bin", ProviderUploadID: providerID, PartNumber: 1, SizeBytes: 10, ExpiresIn: 60})
	if err != nil {
		t.Fatal(err)
	}
	partURL, _ := url.Parse(part.URL)
	if partURL.Query().Get("uploadId") != providerID || partURL.Query().Get("partNumber") != "1" || !strings.Contains(partURL.Query().Get("X-Amz-SignedHeaders"), "content-length") {
		t.Fatal("invalid part capability", part.URL, part.Headers)
	}
	if err = provider.CompleteMultipartUpload(context.Background(), "gregale-test", MultipartCompleteRequest{
		SessionID: "session-1", Key: "large.bin", ProviderUploadID: providerID, SizeBytes: 10,
		Parts: []CompletedPart{{PartNumber: 1, ETag: `"etag"`}},
	}); err != nil {
		t.Fatal("lost completion response not recovered", err)
	}
	if err = provider.AbortMultipartUpload(context.Background(), "gregale-test", MultipartAbortRequest{Key: "large.bin", ProviderUploadID: providerID}); err != nil {
		t.Fatal(err)
	}
	if completeCalls != 1 || abortCalls != 1 {
		t.Fatal(completeCalls, abortCalls)
	}
}

func TestS3MultipartAdoptsSingleExactKeyUpload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated><Upload><Key>large.bin</Key><UploadId>recovered-id</UploadId></Upload></ListMultipartUploadsResult>`)
	}))
	defer upstream.Close()
	c := testBackend()
	c.Endpoint = upstream.URL
	provider, err := NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	id, err := provider.EnsureMultipartUpload(context.Background(), "gregale-test", MultipartCreateRequest{SessionID: "session-1", Key: "large.bin", SizeBytes: 10})
	if err != nil || id != "recovered-id" {
		t.Fatal(id, err)
	}
}

func TestS3ProtocolAndErrors(t *testing.T) {
	var calls []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("Authorization") == "" {
			t.Error("missing upstream signature")
		}
		switch {
		case r.Method == "PUT":
			body, _ := io.ReadAll(r.Body)
			if r.URL.Query().Has("cors") {
				digest := md5.Sum(body)
				if r.Header.Get("Content-MD5") != base64.StdEncoding.EncodeToString(digest[:]) {
					t.Error("missing CORS checksum")
				}
				if r.Header.Get("X-Amz-Checksum-Crc32") != "" || r.Header.Get("X-Amz-Sdk-Checksum-Algorithm") != "" {
					t.Error("AWS-only CORS checksum")
				}
			}
			if r.URL.RawQuery == "" && strings.Contains(string(body), "LocationConstraint") {
				t.Error("unexpected AWS us-east-1 location")
			}
			w.WriteHeader(200)
		case r.Method == "GET":
			w.Header().Set("Content-Type", "application/xml")
			io.WriteString(w, `<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>next</NextContinuationToken><Contents><Key>folder/file</Key><Size>3</Size><LastModified>2026-09-05T00:00:00Z</LastModified></Contents></ListBucketResult>`)
		case r.Method == "DELETE":
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(409)
			io.WriteString(w, `<Error><Code>BucketNotEmpty</Code><Message>private-provider-diagnostic</Message></Error>`)
		}
	}))
	defer upstream.Close()
	c := testBackend()
	c.Endpoint = upstream.URL
	c.AllowedOrigins = []string{"https://console.example.test"}
	p, err := NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.CreateBucket(context.Background(), "gregale-test"); err != nil {
		t.Fatal(err)
	}
	page, err := p.ListObjects(context.Background(), "gregale-test", "folder/", "opaque+cursor", 10)
	if err != nil || len(page.Items) != 1 || page.Items[0].Size != 3 || page.NextCursor != "next" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if err = p.DeleteBucket(context.Background(), "gregale-test"); !errors.Is(err, ErrNotEmpty) {
		t.Fatal(err)
	}
	if len(calls) != 4 || !strings.Contains(calls[1], "cors") || !strings.Contains(calls[2], "continuation-token=opaque%2Bcursor") {
		t.Fatal(calls)
	}
}

func TestRegistryProviderSwitch(t *testing.T) {
	a := testBackend()
	b := a
	b.ID = "self-hosted"
	b.Namespace = "ceph-one"
	b.Endpoint = "https://ceph.example.test"
	c := Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": a.ID}, Backends: []BackendConfig{a, b}}
	factories := map[string]Factory{"s3": NewS3}
	r, err := NewRegistry(c, testCredentials, factories)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := r.Default("us-east-1")
	c.Defaults["us-east-1"] = b.ID
	r, err = NewRegistry(c, testCredentials, factories)
	if err != nil {
		t.Fatal(err)
	}
	newBackend, _ := r.Default("us-east-1")
	if newBackend.ID != b.ID {
		t.Fatal(newBackend)
	}
	resolved, err := r.Resolve(old.ID, old.Fingerprint)
	if err != nil || resolved.ID != a.ID {
		t.Fatal("old bucket moved", err)
	}
	c.Backends[0].Endpoint = "https://wrong.example.test"
	r, err = NewRegistry(c, testCredentials, factories)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Resolve(old.ID, old.Fingerprint); !errors.Is(err, ErrUnavailable) {
		t.Fatal("placement change did not fence old buckets")
	}
}

func TestValidation(t *testing.T) {
	for _, endpoint := range []string{"http://s3.example.test", "https://user:secret@s3.example.test", "https://s3.example.test/path", "https://s3.example.test?secret=x"} {
		c := testBackend()
		c.Endpoint = endpoint
		_, err := NewRegistry(Config{DefaultRegion: c.Region, Defaults: map[string]string{c.Region: c.ID}, Backends: []BackendConfig{c}}, testCredentials, map[string]Factory{"s3": NewS3})
		if err == nil {
			t.Fatal("accepted", endpoint)
		}
	}
	n := int64(101)
	for _, req := range []SignRequest{{Method: "PUT", Key: "k"}, {Method: "PUT", Key: "k", SizeBytes: &n}, {Method: "DELETE", Key: "k"}, {Method: "GET", Key: ""}, {Method: "GET", Key: "a\n"}, {Method: "GET", Key: "k", ExpiresIn: 901}, {Method: "GET", Key: "k", SizeBytes: &n}} {
		if req.Validate(100) == nil {
			t.Fatalf("accepted %+v", req)
		}
	}
}
