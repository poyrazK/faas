package storage

import (
	"bytes"
	"crypto/md5" // #nosec G501 -- verifies the S3 Content-MD5 wire checksum in a fake server.
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeS3 is a minimal path-style S3 server: enough of the protocol for the
// artifact store (objects with user metadata, multipart uploads, copy, list,
// head, delete) and strict about Content-MD5, so the real SDK client is
// exercised on the wire.
type fakeS3 struct {
	mu       sync.Mutex
	objects  map[string]fakeS3Object
	uploads  map[string]map[int][]byte
	aborted  int
	failPart int // UploadPart with this number is refused (403)
	nextID   int
	requests []string
}

type fakeS3Object struct {
	body     []byte
	metadata map[string]string
}

func newFakeS3(t *testing.T) (*fakeS3, *httptest.Server) {
	t.Helper()
	f := &fakeS3{objects: map[string]fakeS3Object{}, uploads: map[string]map[int][]byte{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/")
	bucket, rawKey, _ := strings.Cut(path, "/")
	key, _ := url.PathUnescape(rawKey)
	q := r.URL.Query()
	f.requests = append(f.requests, r.Method+" "+key)
	body, _ := io.ReadAll(r.Body)
	if r.Header.Get("Content-Md5") != "" || (r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") == "") {
		sum := md5.Sum(body) // #nosec G401 -- test of a protocol checksum.
		if r.Header.Get("Content-Md5") != base64.StdEncoding.EncodeToString(sum[:]) {
			s3Error(w, http.StatusBadRequest, "BadDigest")
			return
		}
	}
	name := bucket + "/" + key
	switch {
	case r.Method == http.MethodGet && key == "" && q.Get("list-type") == "2":
		type content struct{ Key string }
		var out struct {
			XMLName     xml.Name  `xml:"ListBucketResult"`
			Contents    []content `xml:"Contents"`
			IsTruncated bool
		}
		var keys []string
		for n := range f.objects {
			if strings.HasPrefix(n, bucket+"/"+q.Get("prefix")) {
				keys = append(keys, strings.TrimPrefix(n, bucket+"/"))
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			out.Contents = append(out.Contents, content{Key: k})
		}
		writeXML(w, out)
	case r.Method == http.MethodPost && q.Has("uploads"):
		f.nextID++
		id := strconv.Itoa(f.nextID)
		f.uploads[id] = map[int][]byte{0: nil}
		meta := userMetadata(r.Header)
		f.objects["\x00meta/"+id] = fakeS3Object{metadata: meta}
		writeXML(w, struct {
			XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
			Bucket   string
			Key      string
			UploadId string
		}{Bucket: bucket, Key: key, UploadId: id})
	case r.Method == http.MethodPut && q.Has("uploadId"):
		number, _ := strconv.Atoi(q.Get("partNumber"))
		if number == f.failPart {
			s3Error(w, http.StatusForbidden, "AccessDenied")
			return
		}
		f.uploads[q.Get("uploadId")][number] = body
		w.Header().Set("ETag", fmt.Sprintf("\"part-%d\"", number))
	case r.Method == http.MethodPost && q.Has("uploadId"):
		id := q.Get("uploadId")
		var done struct {
			Parts []struct{ PartNumber int } `xml:"Part"`
		}
		if err := xml.Unmarshal(body, &done); err != nil {
			s3Error(w, http.StatusBadRequest, "MalformedXML")
			return
		}
		var joined []byte
		for _, part := range done.Parts {
			joined = append(joined, f.uploads[id][part.PartNumber]...)
		}
		f.objects[name] = fakeS3Object{body: joined, metadata: f.objects["\x00meta/"+id].metadata}
		delete(f.objects, "\x00meta/"+id)
		delete(f.uploads, id)
		writeXML(w, struct {
			XMLName xml.Name `xml:"CompleteMultipartUploadResult"`
			Key     string
		}{Key: key})
	case r.Method == http.MethodDelete && q.Has("uploadId"):
		delete(f.uploads, q.Get("uploadId"))
		delete(f.objects, "\x00meta/"+q.Get("uploadId"))
		f.aborted++
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
		source, _ := url.PathUnescape(r.Header.Get("X-Amz-Copy-Source"))
		src, ok := f.objects[strings.TrimPrefix(source, "/")]
		if !ok {
			s3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		f.objects[name] = src
		writeXML(w, struct {
			XMLName xml.Name `xml:"CopyObjectResult"`
			ETag    string
		}{ETag: "\"copy\""})
	case r.Method == http.MethodPut:
		f.objects[name] = fakeS3Object{body: body, metadata: userMetadata(r.Header)}
		w.Header().Set("ETag", "\"put\"")
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		object, ok := f.objects[name]
		if !ok {
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			s3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		for k, v := range object.metadata {
			w.Header().Set("X-Amz-Meta-"+k, v)
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(object.body)))
		if r.Method == http.MethodGet {
			_, _ = w.Write(object.body)
		}
	case r.Method == http.MethodDelete:
		delete(f.objects, name)
		w.WriteHeader(http.StatusNoContent)
	default:
		s3Error(w, http.StatusNotImplemented, "NotImplemented")
	}
}

func userMetadata(h http.Header) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		if lower := strings.ToLower(k); strings.HasPrefix(lower, "x-amz-meta-") {
			out[strings.TrimPrefix(lower, "x-amz-meta-")] = v[0]
		}
	}
	return out
}

func writeXML(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(v)
}

func s3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<Error><Code>%s</Code><Message>%s</Message></Error>", code, code)
}

func newTestS3Backend(t *testing.T, endpoint, compression string) *S3StorageBackend {
	t.Helper()
	backend, err := NewS3StorageBackend(S3Config{
		Bucket: "gregale-artifacts", Endpoint: endpoint, Region: "bhs",
		AccessKeyID: "AK", SecretAccessKey: "SK", PathStyle: true,
	}, compression)
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

func readAllAndClose(t *testing.T, rc io.ReadCloser) []byte {
	t.Helper()
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestS3BackendRoundTripOverWire(t *testing.T) {
	fake, srv := newFakeS3(t)
	backend := newTestS3Backend(t, srv.URL, "")
	ctx := t.Context()

	if err := backend.Put(ctx, "kernel/1.17.0", strings.NewReader("kernel")); err != nil {
		t.Fatal(err)
	}
	if got := readAllAndClose(t, mustGet(t, backend, "kernel/1.17.0")); string(got) != "kernel" {
		t.Fatalf("Get = %q", got)
	}
	if ok, err := backend.Exists(ctx, "kernel/1.17.0"); err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	if ok, err := backend.Exists(ctx, "kernel/missing"); err != nil || ok {
		t.Fatalf("Exists(missing) = %v, %v", ok, err)
	}
	if _, err := backend.Get(ctx, "kernel/missing"); !IsNotFound(err) {
		t.Fatalf("Get(missing) err = %v, want ErrNotFound", err)
	}
	if err := backend.Put(ctx, "kernel/a b+c", strings.NewReader("odd")); err != nil {
		t.Fatal(err)
	}
	if err := backend.CopyObject(ctx, "kernel/a b+c", "layers/copy"); err != nil {
		t.Fatal(err)
	}
	if got := readAllAndClose(t, mustGet(t, backend, "layers/copy")); string(got) != "odd" {
		t.Fatalf("copy = %q", got)
	}
	keys, err := backend.List(ctx, "kernel/")
	if err != nil || strings.Join(keys, ",") != "kernel/1.17.0,kernel/a b+c" {
		t.Fatalf("List = %v, %v", keys, err)
	}
	if err := backend.Delete(ctx, "kernel/1.17.0"); err != nil {
		t.Fatal(err)
	}
	if err := backend.Delete(ctx, "kernel/1.17.0"); err != nil {
		t.Fatalf("second Delete = %v, want idempotent", err)
	}
	if _, ok := fake.objects["gregale-artifacts/kernel/1.17.0"]; ok {
		t.Fatal("object survived Delete")
	}
}

func mustGet(t *testing.T, b StorageBackend, key string) io.ReadCloser {
	t.Helper()
	rc, err := b.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	return rc
}

func TestS3BackendCompressesSnapshotMemoryWithMetadata(t *testing.T) {
	fake, srv := newFakeS3(t)
	backend := newTestS3Backend(t, srv.URL, "zstd")
	key := "snap/550e8400-e29b-41d4-a716-446655440000/mem"
	payload := bytes.Repeat([]byte{0}, 4<<20)
	copy(payload, "guest memory")
	if err := backend.Put(t.Context(), key, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	stored := fake.objects["gregale-artifacts/"+key]
	if stored.metadata[remoteEncodingMetadata] != snapshotCompressionZstd || stored.metadata[remoteUncompressedSizeMetadata] != strconv.Itoa(len(payload)) {
		t.Fatalf("metadata = %v", stored.metadata)
	}
	if len(stored.body) >= len(payload)/10 {
		t.Fatalf("stored %d bytes for %d zero-heavy bytes; not compressed", len(stored.body), len(payload))
	}
	if got := readAllAndClose(t, mustGet(t, backend, key)); !bytes.Equal(got, payload) {
		t.Fatal("decoded memory differs")
	}
}

func TestS3BackendMultipartUploadForLargeArtifact(t *testing.T) {
	fake, srv := newFakeS3(t)
	backend := newTestS3Backend(t, srv.URL, "")
	payload := make([]byte, 2*s3PartSize+12345)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	// An io.Reader without a length: the upload must not need one.
	if err := backend.Put(t.Context(), "layers/big", io.MultiReader(bytes.NewReader(payload))); err != nil {
		t.Fatal(err)
	}
	if got := readAllAndClose(t, mustGet(t, backend, "layers/big")); !bytes.Equal(got, payload) {
		t.Fatal("multipart object differs")
	}
	parts := 0
	for _, req := range fake.requests {
		if req == "PUT layers/big" {
			parts++
		}
	}
	if parts != 3 {
		t.Fatalf("uploaded %d parts, want 3 (requests %v)", parts, fake.requests)
	}
}

func TestS3BackendExactPartSizeCompletesWithOnePart(t *testing.T) {
	_, srv := newFakeS3(t)
	backend := newTestS3Backend(t, srv.URL, "")
	payload := bytes.Repeat([]byte{9}, s3PartSize)
	if err := backend.Put(t.Context(), "layers/exact", bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	if got := readAllAndClose(t, mustGet(t, backend, "layers/exact")); !bytes.Equal(got, payload) {
		t.Fatal("object differs")
	}
}

func TestS3BackendAbortsFailedMultipartUpload(t *testing.T) {
	fake, srv := newFakeS3(t)
	fake.failPart = 2
	backend := newTestS3Backend(t, srv.URL, "")
	payload := bytes.Repeat([]byte{1}, 2*s3PartSize)
	if err := backend.Put(t.Context(), "layers/broken", bytes.NewReader(payload)); err == nil {
		t.Fatal("Put succeeded despite a failed part")
	}
	if fake.aborted != 1 || len(fake.uploads) != 0 {
		t.Fatalf("aborted=%d open uploads=%d, want the upload aborted", fake.aborted, len(fake.uploads))
	}
	if _, ok := fake.objects["gregale-artifacts/layers/broken"]; ok {
		t.Fatal("a failed upload left a visible object")
	}
}

func TestS3BackendReadsGCSEncodedObject(t *testing.T) {
	// A bucket copied from GCS with its metadata must read back unchanged:
	// both backends share one encoding vocabulary.
	gcsStore := newMemoryGCSStore()
	gcsBackend, err := newGCSStorageBackend("gregale-artifacts", "zstd", gcsStore)
	if err != nil {
		t.Fatal(err)
	}
	key := "snap/550e8400-e29b-41d4-a716-446655440000/mem"
	payload := bytes.Repeat([]byte("page"), 1<<16)
	if err := gcsBackend.Put(t.Context(), key, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	fake, srv := newFakeS3(t)
	object := gcsStore.objects["gregale-artifacts/"+key]
	fake.objects["gregale-artifacts/"+key] = fakeS3Object{body: object.body, metadata: object.metadata}
	backend := newTestS3Backend(t, srv.URL, "")
	if got := readAllAndClose(t, mustGet(t, backend, key)); !bytes.Equal(got, payload) {
		t.Fatal("GCS-encoded object did not decode through S3")
	}
}

func TestS3ConfigValidation(t *testing.T) {
	good := S3Config{Bucket: "gregale-artifacts", Endpoint: "https://s3.bhs.io.cloud.ovh.net", Region: "bhs", AccessKeyID: "a", SecretAccessKey: "s"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*S3Config){
		"plain http":       func(c *S3Config) { c.Endpoint = "http://s3.bhs.io.cloud.ovh.net" },
		"endpoint path":    func(c *S3Config) { c.Endpoint = "https://s3.example/bucket" },
		"endpoint creds":   func(c *S3Config) { c.Endpoint = "https://u:p@s3.example" },
		"no region":        func(c *S3Config) { c.Region = "" },
		"no secret":        func(c *S3Config) { c.SecretAccessKey = "" },
		"uppercase bucket": func(c *S3Config) { c.Bucket = "Gregale" },
		"ip bucket":        func(c *S3Config) { c.Bucket = "10.0.0.1" },
	} {
		c := good
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: Validate accepted %+v", name, c)
		}
	}
	loopback := good
	loopback.Endpoint = "http://127.0.0.1:9000"
	if err := loopback.Validate(); err != nil {
		t.Fatalf("loopback http rejected: %v", err)
	}
}

func TestBackendFromEnvSelectsS3InSharedMode(t *testing.T) {
	_, srv := newFakeS3(t)
	t.Setenv("FAAS_STORAGE_BACKEND", "s3")
	t.Setenv("FAAS_S3_BUCKET", "gregale-artifacts")
	t.Setenv("FAAS_S3_ENDPOINT", srv.URL)
	t.Setenv("FAAS_S3_REGION", "bhs")
	t.Setenv("FAAS_S3_ACCESS_KEY_ID", "AK")
	t.Setenv("FAAS_S3_SECRET_ACCESS_KEY", "SK")
	t.Setenv("FAAS_S3_PATH_STYLE", "1")
	t.Setenv("FAAS_REQUIRE_SHARED_ARTIFACTS", "1")
	t.Setenv("FAAS_STORAGE_LOCAL_PREFIXES", "none")
	t.Setenv("FAAS_STORAGE_CACHE_DIR", "")
	t.Setenv("FAAS_STORAGE_ROOT", t.TempDir())
	backend, err := BackendFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Put(t.Context(), "kernel/1.17.0", strings.NewReader("k")); err != nil {
		t.Fatal(err)
	}
	if got := readAllAndClose(t, mustGet(t, backend, "kernel/1.17.0")); string(got) != "k" {
		t.Fatalf("Get = %q", got)
	}

	t.Setenv("FAAS_S3_SECRET_ACCESS_KEY", "")
	if _, err := BackendFromEnv(); err == nil {
		t.Fatal("shared mode accepted S3 without credentials")
	}
	t.Setenv("FAAS_S3_SECRET_ACCESS_KEY", "SK")
	t.Setenv("FAAS_STORAGE_FALLBACK_BACKEND", "local")
	if _, err := BackendFromEnv(); err == nil {
		t.Fatal("accepted an unsupported S3 fallback")
	}
}

func TestS3IsRemoteBackendKind(t *testing.T) {
	if !IsRemoteBackendKind("s3") || !IsRemoteBackendKind(" S3 ") {
		t.Fatal("s3 is not a remote backend kind")
	}
}
