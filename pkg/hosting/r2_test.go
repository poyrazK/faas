package hosting

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestStagingIsVerifiedAndPublishedImmutably(t *testing.T) {
	// adr: 20261015 — temporary grants must never authorize published objects.
	var mu sync.Mutex
	objects := map[string][]byte{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		key := strings.TrimPrefix(r.URL.Path, "/bucket/")
		switch r.Method {
		case http.MethodGet:
			body, ok := objects[key]
			if !ok {
				http.Error(w, "missing", 404)
				return
			}
			w.WriteHeader(200)
			_, _ = w.Write(body)
		case http.MethodPut:
			if _, exists := objects[key]; exists && r.Header.Get("If-None-Match") == "*" {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(412)
				_, _ = io.WriteString(w, "<Error><Code>PreconditionFailed</Code></Error>")
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			objects[key] = body
			w.WriteHeader(200)
		case http.MethodDelete:
			delete(objects, key)
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	backend, err := NewR2(server.URL, "bucket", "test", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	options := backend.client.Options()
	options.HTTPClient = server.Client()
	backend.client = s3.New(options)
	backend.signer = s3.NewPresignClient(backend.client)
	site, release := uuid.NewString(), uuid.NewString()
	body := []byte("<html>Hello</html>")
	digest := sha256.Sum256(body)
	file := api.HostingFile{Path: "index.html", Size: int64(len(body)), SHA256: hex.EncodeToString(digest[:]), ContentType: "text/html"}
	manifest := api.HostingManifest{Files: []api.HostingFile{file}}
	grant, err := backend.Upload(t.Context(), site, release, file)
	if err != nil || !strings.Contains(grant.URL, "/hosting/staging/") || strings.Contains(grant.URL, "/published/") {
		t.Fatalf("grant namespace: %v", err)
	}
	objects[stagingKey(site, release, file.Path)] = []byte("wrong")
	if err := backend.Publish(t.Context(), site, release, manifest); err == nil {
		t.Fatal("accepted corrupt staging object")
	}
	if _, exists := objects[ReleasePrefix(site, release)+"manifest.json"]; exists {
		t.Fatal("published invalid manifest")
	}
	objects[stagingKey(site, release, file.Path)] = body
	if err := backend.Publish(t.Context(), site, release, manifest); err != nil {
		t.Fatal(err)
	}
	if err := backend.Publish(t.Context(), site, release, manifest); err != nil {
		t.Fatalf("idempotent publication: %v", err)
	}
	objects[stagingKey(site, release, file.Path)] = []byte("changed after publication")
	if got := string(objects[ReleasePrefix(site, release)+"files/index.html"]); got != string(body) {
		t.Fatal("staging mutation changed published bytes")
	}
}

func TestManifestRejectsAmbiguousPathsAndLimits(t *testing.T) {
	for _, path := range []string{"../index.html", "/index.html", "a//b", "a/../b", "a%2fb", `a\b`, "_gregale/releases/x"} {
		if ValidPath(path) {
			t.Fatalf("accepted ambiguous path %q", path)
		}
	}
	if !ValidPath("_next/static/app.js") {
		t.Fatal("rejected framework asset prefix")
	}
	digest := strings.Repeat("0", 64)
	m := api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: api.HostingFileMaxBytes + 1, SHA256: digest, ContentType: "text/html"}}}
	if ValidateManifest(m) == nil {
		t.Fatal("accepted oversized artifact")
	}
	m.Files[0].Size = 1
	m.Files = append(m.Files, m.Files[0])
	if ValidateManifest(m) == nil {
		t.Fatal("accepted duplicate path")
	}
}
