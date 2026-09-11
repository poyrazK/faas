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
	head, err := p.Presign(context.Background(), "gregale-test", SignRequest{Method: http.MethodHead, Key: "index.html", ExpiresIn: 60})
	if err != nil {
		t.Fatal(err)
	}
	headURL, _ := url.Parse(head.URL)
	if head.Method != http.MethodHead || headURL.Query().Has("response-content-disposition") || headURL.Query().Has("response-content-type") {
		t.Fatalf("invalid HEAD request: %+v", head)
	}
	size := int64(5)
	metadata, err := p.Presign(context.Background(), "gregale-test", SignRequest{
		Method: http.MethodPut, Key: "metadata.txt", SizeBytes: &size, ContentType: "text/plain", CacheControl: "max-age=60",
		Metadata: map[string]string{"owner": "platform"}, Tags: map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatal(err)
	}
	metadataURL, _ := url.Parse(metadata.URL)
	if !strings.Contains(metadataURL.Query().Get("X-Amz-SignedHeaders"), "x-amz-meta-owner") || !strings.Contains(metadataURL.Query().Get("X-Amz-SignedHeaders"), "x-amz-tagging") || headerValue(metadata.Headers, "X-Amz-Tagging") != "env=prod" || headerValue(metadata.Headers, "X-Amz-Meta-Owner") != "platform" {
		t.Fatalf("metadata/tagging was not signed: url=%s headers=%v", metadata.URL, metadata.Headers)
	}
}

func headerValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func TestS3ObjectTags(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("tagging") {
			switch r.Method {
			case http.MethodGet:
				_, _ = io.WriteString(w, `<Tagging><TagSet><Tag><Key>env</Key><Value>prod</Value></Tag></TagSet></Tagging>`)
			case http.MethodPut:
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), `<Key>team</Key>`) {
					t.Errorf("tagging body = %s", body)
				}
			case http.MethodDelete:
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()
	c := testBackend()
	c.Endpoint = upstream.URL
	p, err := NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	tags, err := p.(ObjectTagger).GetObjectTags(context.Background(), "gregale-test", "file.txt")
	if err != nil || tags["env"] != "prod" {
		t.Fatalf("get tags = %v err=%v", tags, err)
	}
	if err := p.(ObjectTagger).PutObjectTags(context.Background(), "gregale-test", "file.txt", map[string]string{"team": "core"}); err != nil {
		t.Fatal(err)
	}
	if err := p.(ObjectTagger).DeleteObjectTags(context.Background(), "gregale-test", "file.txt"); err != nil {
		t.Fatal(err)
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
		case r.Method == http.MethodGet && r.URL.Query().Get("uploadId") == "provider-id":
			if r.URL.Query().Get("part-number-marker") != "1" || r.URL.Query().Get("max-parts") != "2" {
				t.Errorf("list parts pagination missing: %s", r.URL.RequestURI())
			}
			_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>true</IsTruncated><NextPartNumberMarker>3</NextPartNumberMarker><Part><PartNumber>2</PartNumber><ETag>&quot;etag-2&quot;</ETag><Size>10</Size><LastModified>2026-09-05T00:00:00Z</LastModified></Part></ListPartsResult>`)
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
	page, err := provider.ListMultipartParts(context.Background(), "gregale-test", MultipartListPartsRequest{Key: "large.bin", ProviderUploadID: providerID, PartNumberMarker: 1, Limit: 2})
	if err != nil || len(page.Items) != 1 || page.Items[0].PartNumber != 2 || page.Items[0].ETag != `"etag-2"` || page.NextPartNumberMarker != 3 {
		t.Fatal("list parts", page, err)
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

func TestS3CopyObjectAndDelimitedListing(t *testing.T) {
	var copyHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2":
			if r.URL.Query().Get("delimiter") != "/" {
				t.Errorf("delimiter not forwarded: %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>root.txt</Key><Size>4</Size><LastModified>2026-09-05T00:00:00Z</LastModified></Contents><CommonPrefixes><Prefix>photos/</Prefix></CommonPrefixes></ListBucketResult>`)
		case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
			copyHeader = r.Header.Get("X-Amz-Copy-Source")
			_, _ = io.WriteString(w, `<CopyObjectResult><LastModified>2026-09-07T00:00:00Z</LastModified><ETag>&quot;copy-etag&quot;</ETag></CopyObjectResult>`)
		case r.Method == http.MethodHead:
			w.Header().Set("Content-Length", "12")
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.RequestURI())
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	c := testBackend()
	c.Endpoint = upstream.URL
	p, err := NewS3(c, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	lister, ok := p.(DelimitedObjectLister)
	if !ok {
		t.Fatal("S3 provider does not expose delimiter listing")
	}
	page, err := lister.ListObjectsDelimited(context.Background(), "gregale-test", "", "/", "", 10)
	if err != nil || len(page.Items) != 1 || len(page.CommonPrefixes) != 1 || page.CommonPrefixes[0] != "photos/" {
		t.Fatalf("delimited page = %+v err=%v", page, err)
	}
	copier, ok := p.(ObjectCopier)
	if !ok {
		t.Fatal("S3 provider does not expose CopyObject")
	}
	result, err := copier.CopyObject(context.Background(), "gregale-test", CopyObjectRequest{SourceKey: "source.txt", DestinationKey: "copy.txt", MetadataDirective: "REPLACE", Metadata: ObjectMetadata{ContentType: "text/plain", Metadata: map[string]string{"owner": "platform"}}})
	if err != nil || result.ETag != `"copy-etag"` || !strings.Contains(copyHeader, "gregale-test") || !strings.Contains(copyHeader, "source.txt") {
		t.Fatalf("copy result = %+v err=%v header=%q", result, err, copyHeader)
	}
	sizer, ok := p.(ObjectSizer)
	if !ok {
		t.Fatal("S3 provider does not expose object sizing")
	}
	size, err := sizer.ObjectSize(context.Background(), "gregale-test", "copy.txt")
	if err != nil || size != 12 {
		t.Fatalf("size = %d err=%v", size, err)
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

func TestPublicS3EndpointDefaultsAndValidation(t *testing.T) {
	backend := testBackend()
	base := Config{DefaultRegion: backend.Region, Defaults: map[string]string{backend.Region: backend.ID}, Backends: []BackendConfig{backend}}
	registry, err := NewRegistry(base, testCredentials, map[string]Factory{"s3": NewS3})
	if err != nil {
		t.Fatal(err)
	}
	if registry.PublicEndpoint != "https://s3.gregale.dev" || registry.PublicRegion != "us-east-1" {
		t.Fatalf("public defaults = %q %q", registry.PublicEndpoint, registry.PublicRegion)
	}

	for _, endpoint := range []string{"http://s3.gregale.dev", "https://user@s3.gregale.dev", "https://s3.gregale.dev/path", "https://s3.gregale.dev?secret=x", "https://:443"} {
		config := base
		config.PublicEndpoint = endpoint
		if _, err := NewRegistry(config, testCredentials, map[string]Factory{"s3": NewS3}); err == nil {
			t.Fatalf("accepted public endpoint %q", endpoint)
		}
	}
	base.PublicEndpoint = "https://storage.gregale.dev/"
	base.PublicRegion = "eu-west-3"
	registry, err = NewRegistry(base, testCredentials, map[string]Factory{"s3": NewS3})
	if err != nil || registry.PublicEndpoint != "https://storage.gregale.dev" || registry.PublicRegion != "eu-west-3" {
		t.Fatalf("custom public endpoint = %#v, %v", registry, err)
	}
}
