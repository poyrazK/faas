package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeRegistry is an httptest registry that implements just enough of the v2
// API to exercise the anonymous-token flow, digest resolution, and the
// PullLayers path (manifests, image-config blobs, layer blobs).
type fakeRegistry struct {
	srv          *httptest.Server
	token        string
	manifestBody []byte
	manifestMT   string // content-type to serve the manifest with; "" → default to OCI image manifest
	digestHeader string // when "", the server omits Docker-Content-Digest
	requireToken bool
	tokenHits    int

	// layerBlobs: digest → body. The /blobs/<digest> handler serves bytes
	// from this map; missing digest returns 404. Also serves the image-config
	// blob under the config descriptor's digest (same wire path).
	layerBlobs  map[string][]byte
	blobsHits   int
	manifestHit int
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	f := &fakeRegistry{
		token:        "tok-abc",
		manifestBody: []byte(`{"schemaVersion":2}`),
		manifestMT:   "application/vnd.oci.image.manifest.v1+json",
		requireToken: true,
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		f.tokenHits++
		if r.URL.Query().Get("scope") == "" {
			http.Error(w, "missing scope", http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, `{"token":%q}`, f.token)
	})

	// /v2/ — must disambiguate manifests/ from blobs/.
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.Contains(path, "/blobs/"):
			f.blobsHits++
			if f.requireToken && r.Header.Get("Authorization") != "Bearer "+f.token {
				w.Header().Set("Www-Authenticate",
					fmt.Sprintf(`Bearer realm=%q,service="registry",scope="repository:org/app:pull"`,
						f.srv.URL+"/token"))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			// /v2/<repo>/blobs/<digest>
			idx := strings.Index(path, "/blobs/")
			digest := path[idx+len("/blobs/"):]
			// Strip any trailing path segments (forward refs may include
			// a sub-mount, but our client uses the plain digest form).
			if i := strings.IndexByte(digest, '/'); i >= 0 {
				digest = digest[:i]
			}
			body, ok := f.layerBlobs[digest]
			if !ok {
				http.Error(w, "blob not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			if f.digestHeader != "" {
				w.Header().Set("Docker-Content-Digest", f.digestHeader)
			}
			_, _ = w.Write(body)
		case strings.Contains(path, "/manifests/"):
			f.manifestHit++
			if f.requireToken && r.Header.Get("Authorization") != "Bearer "+f.token {
				w.Header().Set("Www-Authenticate",
					fmt.Sprintf(`Bearer realm=%q,service="registry",scope="repository:org/app:pull"`,
						f.srv.URL+"/token"))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			mt := f.manifestMT
			if mt == "" {
				mt = "application/vnd.oci.image.manifest.v1+json"
			}
			w.Header().Set("Content-Type", mt)
			if f.digestHeader != "" {
				w.Header().Set("Docker-Content-Digest", f.digestHeader)
			}
			_, _ = w.Write(f.manifestBody)
		default:
			http.NotFound(w, r)
		}
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// client returns a RegistryClient pinned at the fake registry.
func (f *fakeRegistry) client() *RegistryClient {
	u, _ := url.Parse(f.srv.URL)
	return NewRegistryClient(WithEndpoint("http", u.Host))
}

func TestRegistryPullDigest_TokenFlowAndHeader(t *testing.T) {
	f := newFakeRegistry(t)
	f.digestHeader = "sha256:" + hex64
	got, err := f.client().PullDigest(context.Background(), "ghcr.io/org/app:main")
	if err != nil {
		t.Fatalf("PullDigest: %v", err)
	}
	if got != "sha256:"+hex64 {
		t.Errorf("digest = %q, want header value", got)
	}
	if f.tokenHits != 1 {
		t.Errorf("token endpoint hits = %d, want 1", f.tokenHits)
	}
}

func TestRegistryPullDigest_ComputesWhenHeaderAbsent(t *testing.T) {
	f := newFakeRegistry(t)
	f.digestHeader = "" // force the client to hash the body itself
	sum := sha256.Sum256(f.manifestBody)
	want := "sha256:" + hex.EncodeToString(sum[:])

	got, err := f.client().PullDigest(context.Background(), "ghcr.io/org/app:main")
	if err != nil {
		t.Fatalf("PullDigest: %v", err)
	}
	if got != want {
		t.Errorf("digest = %q, want computed %q", got, want)
	}
}

func TestRegistryPullDigest_NotFound(t *testing.T) {
	f := newFakeRegistry(t)
	f.requireToken = false
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unknown manifest", http.StatusNotFound)
	})
	_, err := f.client().PullDigest(context.Background(), "ghcr.io/org/missing:tag")
	if err == nil {
		t.Fatal("expected error for 404 manifest")
	}
}

func TestRegistryPullDigest_BadReference(t *testing.T) {
	c := NewRegistryClient()
	if _, err := c.PullDigest(context.Background(), "app@sha256:short"); err == nil {
		t.Fatal("expected parse error for bad digest")
	}
}

func TestParseChallenge(t *testing.T) {
	ch := parseChallenge(`Bearer realm="https://auth.example/token",service="registry",scope="repository:org/app:pull,push"`)
	if ch.realm != "https://auth.example/token" {
		t.Errorf("realm = %q", ch.realm)
	}
	if ch.service != "registry" {
		t.Errorf("service = %q", ch.service)
	}
	// The scope contains a comma; it must not be split into two params.
	if ch.scope != "repository:org/app:pull,push" {
		t.Errorf("scope = %q", ch.scope)
	}
}

var _ Puller = (*RegistryClient)(nil)

// --- PullLayers tests -------------------------------------------------------
//
// The tests below extend fakeRegistry to serve image manifests + config +
// layer blobs, exercising the streaming PullLayers path.

// digestOf hashes a body and returns the "sha256:<hex>" form the registry
// uses to address it.
func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// withImageManifest reconfigures f to serve a synthetic image manifest with
// the given config + layers. Returns the canonical manifest digest. When
// manifestMT is empty it defaults to the Docker v2 form.
func (f *fakeRegistry) withImageManifest(t *testing.T, config []byte, layerBodies ...[]byte) string {
	t.Helper()
	cfgDigest := digestOf(config)
	if f.layerBlobs == nil {
		f.layerBlobs = map[string][]byte{cfgDigest: config}
	} else {
		f.layerBlobs[cfgDigest] = config
	}
	type desc struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int    `json:"size"`
	}
	layers := make([]desc, 0, len(layerBodies))
	for _, b := range layerBodies {
		d := digestOf(b)
		f.layerBlobs[d] = b
		layers = append(layers, desc{
			MediaType: "application/vnd.docker.image.rootfs.diff.tar.gzip",
			Digest:    d, Size: len(b),
		})
	}
	manifest := struct {
		SchemaVersion int    `json:"schemaVersion"`
		MediaType     string `json:"mediaType"`
		Config        desc   `json:"config"`
		Layers        []desc `json:"layers"`
	}{
		SchemaVersion: 2,
		MediaType:     "application/vnd.docker.distribution.manifest.v2+json",
		Config: desc{
			MediaType: "application/vnd.docker.container.image.v1+json",
			Digest:    cfgDigest, Size: len(config),
		},
		Layers: layers,
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	f.manifestBody = body
	f.manifestMT = "application/vnd.docker.distribution.manifest.v2+json"
	return digestOf(body)
}

// TestRegistryPullLayers_HappyPath wires a single image with two layers and
// asserts the client streams them bottom-to-top with the parsed config.
func TestRegistryPullLayers_HappyPath(t *testing.T) {
	f := newFakeRegistry(t)
	layer1 := []byte("layer1-bytes")
	layer2 := []byte("layer2-bytes")
	config := []byte(`{"Cmd":["sh","-c","echo hi"],"Env":["FOO=bar"],"WorkingDir":"/app"}`)
	wantManifestDigest := f.withImageManifest(t, config, layer1, layer2)

	res, err := f.client().PullLayers(context.Background(),
		"ghcr.io/org/app@sha256:"+strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("PullLayers: %v", err)
	}
	t.Cleanup(func() {
		for _, r := range res.Layers {
			_ = r.Close()
		}
	})

	if len(res.Layers) != 2 {
		t.Fatalf("layers = %d, want 2", len(res.Layers))
	}
	got1, _ := io.ReadAll(res.Layers[0])
	got2, _ := io.ReadAll(res.Layers[1])
	if string(got1) != "layer1-bytes" {
		t.Errorf("layer[0] = %q, want layer1", got1)
	}
	if string(got2) != "layer2-bytes" {
		t.Errorf("layer[1] = %q, want layer2", got2)
	}
	if res.Config.Cmd[0] != "sh" || res.Config.WorkingDir != "/app" {
		t.Errorf("config parsed wrong: %+v", res.Config)
	}
	if res.Config.Env["FOO"] != "bar" {
		t.Errorf("env[FOO] = %q, want bar", res.Config.Env["FOO"])
	}
	if res.Digest != wantManifestDigest {
		t.Errorf("manifest digest = %q, want %q", res.Digest, wantManifestDigest)
	}
}

// TestRegistryPullLayers_ManifestListRejected asserts a manifest list / index
// response is rejected (M5 contract: digest-pinned single arch).
func TestRegistryPullLayers_ManifestListRejected(t *testing.T) {
	f := newFakeRegistry(t)
	f.manifestBody = []byte(`{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.list.v2+json","manifests":[]}`)
	f.manifestMT = "application/vnd.docker.distribution.manifest.list.v2+json"

	_, err := f.client().PullLayers(context.Background(),
		"ghcr.io/org/app@sha256:"+strings.Repeat("a", 64))
	if err == nil {
		t.Fatal("expected error for manifest list")
	}
	if !strings.Contains(err.Error(), "manifest list") {
		t.Errorf("error should mention manifest list: %v", err)
	}
}

// TestRegistryPullLayers_LayerMissing closes any already-opened layers when
// a later layer's blob fetch fails.
func TestRegistryPullLayers_LayerMissing(t *testing.T) {
	f := newFakeRegistry(t)
	config := []byte(`{"Cmd":["x"]}`)
	_ = f.withImageManifest(t, config, []byte("layer1"))
	// withImageManifest registers the layer body; evict it so the server 404s.
	for k := range f.layerBlobs {
		if k != digestOf(config) {
			delete(f.layerBlobs, k)
		}
	}

	_, err := f.client().PullLayers(context.Background(),
		"ghcr.io/org/app@sha256:"+strings.Repeat("a", 64))
	if err == nil {
		t.Fatal("expected error when a layer blob is missing")
	}
}

// TestRegistryPullLayers_NoCmdOK asserts an image without a Cmd is accepted
// at this layer — validation belongs to imaged (it merges with app config).
func TestRegistryPullLayers_NoCmdOK(t *testing.T) {
	f := newFakeRegistry(t)
	layer := []byte("layer")
	config := []byte(`{"WorkingDir":"/srv"}`)
	_ = f.withImageManifest(t, config, layer)

	res, err := f.client().PullLayers(context.Background(),
		"ghcr.io/org/app@sha256:"+strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("PullLayers: %v", err)
	}
	for _, r := range res.Layers {
		_ = r.Close()
	}
	if res.Config.Cmd != nil {
		t.Errorf("expected nil Cmd, got %v", res.Config.Cmd)
	}
	if res.Config.WorkingDir != "/srv" {
		t.Errorf("WorkingDir = %q, want /srv", res.Config.WorkingDir)
	}
}

// TestParseImageConfig_OciNestedConfig asserts the parser handles the OCI
// shape where Cmd/Env/WorkingDir are nested under "config".
func TestParseImageConfig_OciNestedConfig(t *testing.T) {
	in := []byte(`{"config":{"Cmd":["node","server.js"],"Env":["PORT=8080"]},"created":"2025-01-01"}`)
	got, err := parseImageConfig(in)
	if err != nil {
		t.Fatalf("parseImageConfig: %v", err)
	}
	if got.Cmd[0] != "node" || got.Env["PORT"] != "8080" {
		t.Errorf("parsed wrong: %+v", got)
	}
}
