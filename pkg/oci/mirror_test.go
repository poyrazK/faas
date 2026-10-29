package oci

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// mirrorTestImage serves one linux/amd64 image at /v2/library/nginx and
// counts the requests it answers; missing makes it answer 404 instead.
type mirrorTestImage struct {
	srv      *httptest.Server
	hits     atomic.Int32
	missing  atomic.Bool
	digest   string
	manifest string
	config   []byte
}

func newMirrorTestImage(t *testing.T) *mirrorTestImage {
	t.Helper()
	img := &mirrorTestImage{config: []byte(`{"os":"linux","architecture":"amd64","rootfs":{"type":"layers","diff_ids":["sha256:` + strings.Repeat("b", 64) + `"]},"config":{"Cmd":["nginx"]}}`)}
	configDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(img.config))
	img.manifest = fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":%q,"size":%d},"layers":[{"digest":"sha256:%s","size":1}]}`, configDigest, len(img.config), strings.Repeat("a", 64))
	img.digest = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(img.manifest)))
	img.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		img.hits.Add(1)
		if img.missing.Load() {
			http.Error(w, "unknown", http.StatusNotFound)
			return
		}
		switch r.URL.Path {
		case "/v2/library/nginx/manifests/latest", "/v2/library/nginx/manifests/" + img.digest:
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			_, _ = fmt.Fprint(w, img.manifest)
		case "/v2/library/nginx/blobs/" + configDigest:
			_, _ = w.Write(img.config)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(img.srv.Close)
	return img
}

// hostRoutes sends each registry host to a test server.
type hostRoutes map[string]*httptest.Server

func (h hostRoutes) RoundTrip(req *http.Request) (*http.Response, error) {
	if srv, ok := h[req.URL.Host]; ok {
		req = req.Clone(req.Context())
		req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
		req.Host = req.URL.Host
	}
	return http.DefaultTransport.RoundTrip(req)
}

func mirrorTestClient(routes hostRoutes, fallbacks *[]string) *RegistryClient {
	c := NewRegistryClient(
		WithHTTPClient(&http.Client{Transport: routes}),
		WithDockerHubMirrors([]string{"mirror.test"}, func(mirror string, _ error) { *fallbacks = append(*fallbacks, mirror) }),
	)
	c.scheme = "http"
	return c
}

// Docker Hub limits anonymous pulls per IP; a configured mirror serves them
// (ADR-20261025114554), and the resolution still names Docker Hub.
func TestDockerHubMirrorServesAnonymousPulls(t *testing.T) {
	hub, mirror := newMirrorTestImage(t), newMirrorTestImage(t)
	var fallbacks []string
	c := mirrorTestClient(hostRoutes{"registry-1.docker.io": hub.srv, "mirror.test": mirror.srv}, &fallbacks)
	got, err := c.ResolveImage(context.Background(), "nginx:latest", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reference != "docker.io/library/nginx@"+mirror.digest || !strings.HasPrefix(got.SourceReference, "docker.io/library/nginx@") {
		t.Fatalf("references name the mirror: %+v", got)
	}
	if _, err := c.PullManifest(context.Background(), "nginx:latest"); err != nil {
		t.Fatal(err)
	}
	if digest, err := c.PullDigest(context.Background(), "nginx:latest"); err != nil || digest != mirror.digest {
		t.Fatalf("PullDigest = %q, %v", digest, err)
	}
	if hub.hits.Load() != 0 || mirror.hits.Load() == 0 || len(fallbacks) != 0 {
		t.Fatalf("hub hits = %d, mirror hits = %d, fallbacks = %v", hub.hits.Load(), mirror.hits.Load(), fallbacks)
	}
}

func TestDockerHubMirrorFallsBackToDockerHub(t *testing.T) {
	hub, mirror := newMirrorTestImage(t), newMirrorTestImage(t)
	mirror.missing.Store(true)
	var fallbacks []string
	c := mirrorTestClient(hostRoutes{"registry-1.docker.io": hub.srv, "mirror.test": mirror.srv}, &fallbacks)
	got, err := c.ResolveImage(context.Background(), "nginx:latest", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Digest != hub.digest || hub.hits.Load() == 0 {
		t.Fatalf("Docker Hub did not serve the fallback: %+v (hub hits %d)", got, hub.hits.Load())
	}
	if len(fallbacks) != 1 || fallbacks[0] != "mirror.test" {
		t.Fatalf("fallbacks = %v, want [mirror.test]", fallbacks)
	}
}

// A credential is never sent to a mirror, and other registries are not
// mirrored.
func TestDockerHubMirrorSkipsCredentialsAndOtherRegistries(t *testing.T) {
	hub, mirror := newMirrorTestImage(t), newMirrorTestImage(t)
	var fallbacks []string
	c := mirrorTestClient(hostRoutes{"registry-1.docker.io": hub.srv, "ghcr.io": hub.srv, "mirror.test": mirror.srv}, &fallbacks)
	if _, err := c.ResolveImage(context.Background(), "nginx:latest", &BasicAuth{Username: "u", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PullDigest(context.Background(), "ghcr.io/library/nginx:latest"); err != nil {
		t.Fatal(err)
	}
	if mirror.hits.Load() != 0 || hub.hits.Load() == 0 {
		t.Fatalf("mirror hits = %d, hub hits = %d", mirror.hits.Load(), hub.hits.Load())
	}
}

func TestDockerHubMirrorStopsOnCancel(t *testing.T) {
	hub, mirror := newMirrorTestImage(t), newMirrorTestImage(t)
	var fallbacks []string
	c := mirrorTestClient(hostRoutes{"registry-1.docker.io": hub.srv, "mirror.test": mirror.srv}, &fallbacks)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.PullDigest(ctx, "nginx:latest"); err == nil {
		t.Fatal("cancelled pull succeeded")
	}
	if hub.hits.Load() != 0 || len(fallbacks) != 0 {
		t.Fatalf("cancelled pull moved on: hub hits %d, fallbacks %v", hub.hits.Load(), fallbacks)
	}
}
