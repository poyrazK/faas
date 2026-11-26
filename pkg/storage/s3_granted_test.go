package storage

// adr: 20261117190000 — slice 4 without a live bucket: a fake S3 that
// honors only granted requests and the ETag conditions they carry.

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // ETags, as S3 computes them
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storagegrant"
)

type grantS3 struct {
	mu      sync.Mutex
	objects map[string]grantS3Object
	uploads map[string]map[int][]byte
	tokens  map[string]storagegrant.Request
	next    int
	srv     *httptest.Server
}

type grantS3Object struct {
	body []byte
	etag string
	meta map[string]string
}

func newGrantS3(t *testing.T) *grantS3 {
	f := &grantS3{objects: map[string]grantS3Object{}, uploads: map[string]map[int][]byte{}, tokens: map[string]storagegrant.Request{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func etagOf(b []byte) string {
	sum := md5.Sum(b) //nolint:gosec // see import
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func (f *grantS3) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/bucket/")
	g, ok := f.tokens[r.URL.Query().Get("token")]
	if !ok || g.Key != key {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	obj, exists := f.objects[key]
	condition := func() bool {
		if r.Header.Get("If-None-Match") == "*" {
			return !exists
		}
		if m := r.Header.Get("If-Match"); m != "" {
			return exists && obj.etag == m
		}
		return false // every granted write is conditioned
	}
	meta := func() map[string]string {
		m := map[string]string{}
		for name, v := range r.Header {
			if k, ok := strings.CutPrefix(strings.ToLower(name), "x-amz-meta-"); ok {
				m[k] = v[0]
			}
		}
		return m
	}
	switch g.Op {
	case storagegrant.OpGet, storagegrant.OpHead:
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("ETag", obj.etag)
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.body)))
		for k, v := range obj.meta {
			w.Header().Set("x-amz-meta-"+k, v)
		}
		if g.Op == storagegrant.OpGet {
			_, _ = w.Write(obj.body)
		}
	case storagegrant.OpCreate, storagegrant.OpReplace:
		if !condition() {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.objects[key] = grantS3Object{body: body, etag: etagOf(body), meta: meta()}
	case storagegrant.OpCreateMultipart:
		f.next++
		id := "upload-" + strconv.Itoa(f.next)
		f.uploads[id] = map[int][]byte{0: nil}
		f.tokens["meta-"+id] = storagegrant.Request{Metadata: meta()}
		_, _ = fmt.Fprintf(w, `<InitiateMultipartUploadResult><UploadId>%s</UploadId></InitiateMultipartUploadResult>`, id)
	case storagegrant.OpUploadPart:
		parts, ok := f.uploads[g.UploadID]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		parts[int(g.PartNumber)] = body
		w.Header().Set("ETag", etagOf(body))
	case storagegrant.OpCompleteMultipart:
		parts, ok := f.uploads[g.UploadID]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if !condition() {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		var manifest completeMultipartUpload
		if err := xml.NewDecoder(r.Body).Decode(&manifest); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var all []byte
		for i, p := range manifest.Parts {
			if int(p.PartNumber) != i+1 || etagOf(parts[i+1]) != p.ETag {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			all = append(all, parts[i+1]...)
		}
		f.objects[key] = grantS3Object{body: all, etag: etagOf(all), meta: f.tokens["meta-"+g.UploadID].Metadata}
		delete(f.uploads, g.UploadID)
		_, _ = io.WriteString(w, `<CompleteMultipartUploadResult/>`)
	case storagegrant.OpAbortMultipart:
		delete(f.uploads, g.UploadID)
		w.WriteHeader(http.StatusNoContent)
	}
}

type grantS3Granter struct {
	s3    *grantS3
	n     int
	asked []storagegrant.Op
}

func (g *grantS3Granter) GrantObjects(_ context.Context, reqs []storagegrant.Request) ([]storagegrant.Grant, error) {
	out := make([]storagegrant.Grant, len(reqs))
	for i, r := range reqs {
		if err := r.Validate(); err != nil {
			return nil, err
		}
		g.asked = append(g.asked, r.Op)
		g.n++
		token := strconv.Itoa(g.n)
		g.s3.mu.Lock()
		g.s3.tokens[token] = r
		g.s3.mu.Unlock()
		headers := map[string]string{}
		switch {
		case r.Op == storagegrant.OpCreate, r.Op == storagegrant.OpCompleteMultipart && r.ETag == "":
			headers["if-none-match"] = "*"
		case r.ETag != "":
			headers["if-match"] = r.ETag
		}
		for k, v := range r.Metadata {
			headers["x-amz-meta-"+k] = v
		}
		methods := map[storagegrant.Op]string{storagegrant.OpGet: "GET", storagegrant.OpHead: "HEAD", storagegrant.OpCreate: "PUT",
			storagegrant.OpReplace: "PUT", storagegrant.OpUploadPart: "PUT", storagegrant.OpCreateMultipart: "POST",
			storagegrant.OpCompleteMultipart: "POST", storagegrant.OpAbortMultipart: "DELETE"}
		out[i] = storagegrant.Grant{Method: methods[r.Op], URL: g.s3.srv.URL + "/bucket/" + r.Key + "?" + url.Values{"token": {token}}.Encode(),
			Headers: headers, Expires: time.Now().Add(storagegrant.TTL)}
	}
	return out, nil
}

func grantedS3Fixture(t *testing.T) (*S3StorageBackend, *grantS3, *grantS3Granter) {
	t.Helper()
	f := newGrantS3(t)
	g := &grantS3Granter{s3: f}
	b, err := newS3StorageBackend("bucket", snapshotCompressionNone, &awsS3ArtifactStore{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UseGrants(b, g, f.srv.Client()); err == nil {
		t.Fatal("UseGrants accepted an S3 backend that still deletes")
	}
	if _, err := DeferDeletes(b, &recordingDeferrer{}); err != nil {
		t.Fatal(err)
	}
	if n, err := UseGrants(b, g, f.srv.Client()); err != nil || n != 1 {
		t.Fatalf("UseGrants = %d, %v", n, err)
	}
	return b, f, g
}

func TestGrantedS3_SingleAndMultipart(t *testing.T) {
	for name, size := range map[string]int{"single": 1234, "multipart": s3PartSize*2 + 7} {
		t.Run(name, func(t *testing.T) {
			b, f, _ := grantedS3Fixture(t)
			key := grantedKey
			for round := byte(1); round <= 2; round++ {
				body := bytes.Repeat([]byte{round}, size)
				if err := b.Put(t.Context(), key, bytes.NewReader(body)); err != nil {
					t.Fatalf("put round %d: %v", round, err)
				}
				rc, err := b.Get(t.Context(), key)
				if err != nil {
					t.Fatal(err)
				}
				got, _ := io.ReadAll(rc)
				_ = rc.Close()
				if !bytes.Equal(got, body) {
					t.Fatalf("round %d read %d bytes", round, len(got))
				}
			}
			if len(f.uploads) != 0 {
				t.Fatalf("leftover uploads %v", f.uploads)
			}
			if ok, err := b.Exists(t.Context(), key); err != nil || !ok {
				t.Fatalf("exists = %v, %v", ok, err)
			}
		})
	}
}

// A writer racing in between loses the precondition instead of being
// overwritten, and a failed multipart upload is aborted.
func TestGrantedS3_RaceIsRefusedAndAborted(t *testing.T) {
	b, f, g := grantedS3Fixture(t)
	key := grantedKey
	if err := b.Put(t.Context(), key, strings.NewReader("first")); err != nil {
		t.Fatal(err)
	}
	// Another writer replaces the object while this one uploads parts.
	racer := &raceReader{r: bytes.NewReader(make([]byte, s3PartSize*2)), after: s3PartSize, race: func() {
		f.mu.Lock()
		f.objects[key] = grantS3Object{body: []byte("racer"), etag: etagOf([]byte("racer"))}
		f.mu.Unlock()
	}}
	err := b.Put(t.Context(), key, racer)
	if !errors.Is(err, ErrArtifactExists) {
		t.Fatalf("racing put = %v, want a refused precondition", err)
	}
	if string(f.objects[key].body) != "racer" || len(f.uploads) != 0 {
		t.Fatalf("object %q, uploads %v; want the racer kept and the upload aborted", f.objects[key].body, f.uploads)
	}
	aborted := false
	for _, op := range g.asked {
		aborted = aborted || op == storagegrant.OpAbortMultipart
	}
	if !aborted {
		t.Fatal("the failed upload was not aborted")
	}
	if err := b.CopyObject(t.Context(), key, "layers/0b8f5a8e-7c1e-4f3a-9d2b-1a2b3c4d5e70.ext4"); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("copy = %v, want ErrUnsupported so the caller streams", err)
	}
	if err := b.Delete(t.Context(), key); err != nil {
		t.Fatalf("deferred delete: %v", err)
	}
	if _, ok := f.objects[key]; !ok {
		t.Fatal("a granted node deleted")
	}
}

type raceReader struct {
	r     io.Reader
	after int
	read  int
	race  func()
	raced bool
}

func (r *raceReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.read += n
	if !r.raced && r.read > r.after {
		r.raced = true
		r.race()
	}
	return n, err
}

// ADR-20261117190000 slice 5: a granted S3 node holds no key at all. It must
// still start, and every object still flows through grants.
func TestGrantedS3_StartsWithoutAKey(t *testing.T) {
	f := newGrantS3(t)
	t.Setenv("FAAS_STORAGE_BACKEND", "s3")
	t.Setenv("FAAS_S3_BUCKET", "gregale-artifacts")
	t.Setenv("FAAS_S3_ENDPOINT", f.srv.URL)
	t.Setenv("FAAS_S3_REGION", "bhs")
	t.Setenv("FAAS_S3_ACCESS_KEY_ID", "")
	t.Setenv("FAAS_S3_SECRET_ACCESS_KEY", "")
	t.Setenv("FAAS_REQUIRE_SHARED_ARTIFACTS", "1")
	t.Setenv("FAAS_STORAGE_LOCAL_PREFIXES", "none")
	t.Setenv("FAAS_STORAGE_CACHE_DIR", "")
	t.Setenv("FAAS_STORAGE_ROOT", t.TempDir())
	t.Setenv("FAAS_STORAGE_ACCESS", "")
	if _, err := BackendFromEnv(); err == nil {
		t.Fatal("a direct S3 node started without a key")
	}
	t.Setenv("FAAS_STORAGE_ACCESS", "secretd")
	backend, err := BackendFromEnv()
	if err != nil {
		t.Fatalf("granted node without a key: %v", err)
	}
	if _, err := DeferDeletes(backend, &recordingDeferrer{}); err != nil {
		t.Fatal(err)
	}
	g := &grantS3Granter{s3: f}
	if n, err := UseGrants(backend, g, f.srv.Client()); err != nil || n != 1 {
		t.Fatalf("UseGrants = %d, %v", n, err)
	}
	if err := backend.Put(t.Context(), grantedKey, strings.NewReader("layer")); err != nil {
		t.Fatal(err)
	}
	rc, err := backend.Get(t.Context(), grantedKey)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	_ = rc.Close()
	if string(got) != "layer" || g.n == 0 {
		t.Fatalf("read %q with %d grants", got, g.n)
	}
}
