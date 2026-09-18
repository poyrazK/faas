// fakeregistry.go — minimal OCI v2 registry for the e2e harness.
//
// Serves a single image, digest-pinned, with:
//   - one OCI image manifest
//   - one image-config blob (with a non-empty Cmd, so manifestFromImageConfig
//     produces a valid AppManifest)
//   - one layer blob (gzip'd tar containing a single regular file the
//     rootfs.Builder can unpack into the app-layer ext4)
//
// The harness points imaged at this registry via FAAS_OCI_INSECURE=1 (test
// only — the egress guard denies loopback by design, see pkg/oci/egress.go).
//
// Spec coverage: §5 (imaged pull → image-config → layer pull → app layer),
// ADR-005 (snapshot restore), ADR-018 (image digest pinned).

package e2etest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// fakeImage is the in-memory state of one image served by a FakeRegistry. The
// registry can serve multiple images; the e2e test wires up one and pins a
// reference to it.
type fakeImage struct {
	configDigest string
	configBytes  []byte

	// layerBlobs is the ordered list of layer blob digests → bytes. For
	// single-layer images it has one entry; for HelloImageAboveBase it has
	// two (base layer + app layer). imaged's PullManifest returns the
	// compressed-blob digests in order; the registry serves each by digest
	// lookup. Kept as a slice (rather than the legacy single layerDigest/
	// layerBytes fields) so multi-layer images round-trip cleanly.
	layerBlobs []blobEntry

	manifestDigest string // sha256 of the manifest body, served as Docker-Content-Digest
	manifestBytes  []byte
	manifestMT     string
}

type blobEntry struct {
	digest string
	bytes  []byte
}

// HelloImage returns an image that, when fed through imaged's pull pipeline,
// produces an app layer containing /hello-server (a static binary, see
// testdata/helloserver) and app/hello.txt with body `helloBody`. The Cmd it
// advertises is `["/hello-server"]`, which serves that body on :8080 and
// answers /healthz — so the image is a real, if minimal, app: what a
// scratch-based customer image looks like, with no shell and no libc.
//
// The image has exactly one layer whose diff_id is the hardcoded constant
// `helloLayerDiffID` (sha256:bbbb…). Tests that need the two-drive scheme to
// work pair this with `BaseLayerImage` as the deploy-time base — base's
// diff_ids prefix the app's, above-base is exactly the app layer.
func HelloImage(repo, helloBody string) (fakeImage, string) {
	return layeredHelloImageOnPort(repo, helloBody, false, 8080)
}

// HelloImageOnPort returns the same scratch-style image as HelloImage, but
// advertises a non-default TCP listener. The image's process has no baked-in
// port argument; it binds the PORT environment variable that guest-init
// injects from this OCI ExposedPorts declaration.
func HelloImageOnPort(repo, helloBody string, port int) (fakeImage, string) {
	return layeredHelloImageOnPort(repo, helloBody, false, port)
}

// HelloImageAboveBase returns an image identical to HelloImage except it has
// TWO layers: the hardcoded helloLayerDiffID (matching BaseLayerImage's
// layer) followed by an additional layer whose diff_id is computed from the
// tar blob. Use this with BaseLayerImage as the deploy-time base — the base's
// single layer prefixes the app's two layers, so oci.LayersAboveBase puts
// the second (above-base) layer into `above`.
func HelloImageAboveBase(repo, helloBody string) (fakeImage, string) {
	return layeredHelloImageOnPort(repo, helloBody, true, 8080)
}

// CPUBoundImage returns a single-layer image whose entrypoint is hello-server
// in -spin mode: it serves as usual and burns one CPU in a goroutine. Used by the cpu-fairness e2e
// (cmd/e2e/cpu_fairness_test.go, issue #301 / ADR-044) to drive a
// sustained 100% CPU workload on a single Hobby-tier VM so the per-plan
// cpu.max = 200ms/100ms cap engages and the test can measure the
// starvation it imposes on neighbouring Hobby-tier VMs.
//
// Layer shape matches HelloImage so this image pairs with the same
// BaseLayerImage("onebox-faas/deploy-base", ...) the rest of the e2e
// suite uses — `oci.LayersAboveBase` sees a matching diff_id prefix and
// the above-base layer (here: a noop regular file) lands in `above`.
//
// The spin is a pure userland loop, not `dd` or `openssl speed`: the goal
// is *CPU saturation*, not I/O or crypto throughput, and the kernel
// scheduler's throttle ratio is the only thing that can throttle it —
// exactly the signal the issue's `vmmd_cpu_throttle_ratio{slice}` gauge
// measures. (It used to be a `/bin/sh` busy-loop; the image has no shell.)
//
// Pair with HelloImage for the quiet side of the experiment:
//   - CPUBoundImage (hot)        on plan=Hobby → saturates tenant-hobby.slice
//   - HelloImage      (quiet ×5) on plan=Hobby → measurement baseline
//
// The 200ms/100ms cpu.max is the tightest quota in the §1 model
// (Hobby), so a hot Hobby app at its ceiling preempts the 5 quiet
// Hobby apps via the cpu.weight ratio (4 vs 4 — equal weights, so
// the throttle is the only signal). If the hot app were Pro/Scale,
// the cpu.weight differential (4 vs 8 / 4 vs 16) would dominate and
// the test would not isolate cpu.max enforcement.
func CPUBoundImage(repo string) (fakeImage, string) {
	return layeredHelloImageWithCmd(repo, []string{"/hello-server", "-spin"})
}

// WedgedLoopImage (issue #554 / ADR-079, AC #1 metal test) is the
// busy-loop variant that ALSO ignores SIGTERM and never binds :8080,
// so a "graceful" shutdown signal can't reach the process. vmmd's
// liveness probe classifies the wedged process as
// `conn_refused` (no :8080 listener) and the consecutive-fail
// counter increments; after 3 cycles the engine parks the
// deployment and the next wake must cold-boot.
//
// Sibling of CPUBoundImage — same shape (one base layer, no
// above-base) — but with the SIGTERM trap prepended so a strict
// subset of "wedged" is exercised (the busy loop never receives
// a signal that would let it shut down). Used by
// cmd/vmmd/liveness_metal_test.go (TestMetalLivenessCycle_AC1).
func WedgedLoopImage(repo string) (fakeImage, string) {
	return layeredHelloImageWithCmd(repo, []string{"/hello-server", "-spin", "-ignore-term", "-no-listen"})
}

// layeredHelloImageWithCmd is layeredHelloImageOnPort with a custom Cmd.
// Kept as a separate helper so the CPU/wedged fixtures can change Cmd without
// adding command-shape branches to the ordinary image builder.
func layeredHelloImageWithCmd(repo string, cmd []string) (fakeImage, string) {
	// Layer shape identical to layeredHelloImage (one base layer with
	// the hardcoded diff_id; no above-base layer — pairs with the
	// shared deploy-base via the same prefix trick). Re-use
	// buildHelloLayer("") so the diff_id stays helloLayerDiffID.
	baseBlob := buildHelloLayer("")
	baseSum := sha256.Sum256(baseBlob)
	baseDigest := "sha256:" + hex.EncodeToString(baseSum[:])

	cfg := map[string]any{
		"architecture": "amd64",
		"os":           "linux",
		"config": map[string]any{
			"Cmd":          cmd,
			"Env":          []string{},
			"WorkingDir":   "/",
			"ExposedPorts": map[string]any{"8080/tcp": struct{}{}},
		},
		"rootfs": map[string]any{
			"type":     "layers",
			"diff_ids": []string{helloLayerDiffID},
		},
	}
	cfgBytes, _ := json.Marshal(cfg)
	cfgSum := sha256.Sum256(cfgBytes)
	cfgDigest := "sha256:" + hex.EncodeToString(cfgSum[:])

	manifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest":    cfgDigest,
			"size":      len(cfgBytes),
		},
		"layers": []map[string]any{{
			"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
			"digest":    baseDigest,
			"size":      len(baseBlob),
		}},
	}
	manifestBytes, _ := json.Marshal(manifest)
	mSum := sha256.Sum256(manifestBytes)
	manifestDigest := "sha256:" + hex.EncodeToString(mSum[:])

	img := fakeImage{
		configDigest:   cfgDigest,
		configBytes:    cfgBytes,
		layerBlobs:     []blobEntry{{digest: baseDigest, bytes: baseBlob}},
		manifestDigest: manifestDigest,
		manifestBytes:  manifestBytes,
		manifestMT:     "application/vnd.oci.image.manifest.v1+json",
	}
	ref := fmt.Sprintf("%s@%s", repo, manifestDigest)
	return img, ref
}

func layeredHelloImageOnPort(repo, helloBody string, aboveBase bool, port int) (fakeImage, string) {
	// Build the layer blob list. The "base" layer (always present) advertises
	// the hardcoded helloLayerDiffID — a fake that the deploy-time base image
	// (BaseLayerImage) repeats so oci.LayersAboveBase sees a matching prefix.
	// The "above-base" layer (only when aboveBase=true) is the actual
	// hello.txt content; its diff_id is whatever sha256(uncompressed tar)
	// yields, which doesn't need to match anything because LayersAboveBase
	// only compares prefixes, not tails.
	baseBlob := buildHelloLayer("")
	baseSum := sha256.Sum256(baseBlob)
	baseDigest := "sha256:" + hex.EncodeToString(baseSum[:])

	type layerRec struct {
		blob   []byte
		digest string
		diffID string
	}
	layers := []layerRec{{blob: baseBlob, digest: baseDigest, diffID: helloLayerDiffID}}

	if aboveBase {
		appBlob := buildHelloLayer(helloBody)
		appSum := sha256.Sum256(appBlob)
		appDigest := "sha256:" + hex.EncodeToString(appSum[:])
		// diff_ids are rootfs-level (uncompressed-tar sha256), not
		// blob-level (compressed). We don't actually compute the
		// uncompressed sha256 because LayersAboveBase only checks
		// string equality of the listed diff_ids — picking a unique
		// label per call is enough.
		layers = append(layers, layerRec{
			blob: appBlob, digest: appDigest,
			diffID: "sha256:" + hex.EncodeToString(appSum[:]) + "a", // unique marker
		})
	}

	// Image config (OCI v1). Cmd is required for AppManifest.Validate.
	diffIDs := make([]string, len(layers))
	for i, l := range layers {
		diffIDs[i] = l.diffID
	}
	cfg := map[string]any{
		"architecture": "amd64",
		"os":           "linux",
		"config": map[string]any{
			"Cmd":          []string{"/hello-server"},
			"Env":          []string{},
			"WorkingDir":   "/",
			"ExposedPorts": map[string]any{fmt.Sprintf("%d/tcp", port): struct{}{}},
		},
		"rootfs": map[string]any{
			"type":     "layers",
			"diff_ids": diffIDs,
		},
	}
	cfgBytes, _ := json.Marshal(cfg)
	cfgSum := sha256.Sum256(cfgBytes)
	cfgDigest := "sha256:" + hex.EncodeToString(cfgSum[:])

	// Manifest layers in the same bottom-to-top order as diff_ids.
	manifestLayers := make([]map[string]any, len(layers))
	for i, l := range layers {
		manifestLayers[i] = map[string]any{
			"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
			"digest":    l.digest,
			"size":      len(l.blob),
		}
	}
	manifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest":    cfgDigest,
			"size":      len(cfgBytes),
		},
		"layers": manifestLayers,
	}
	manifestBytes, _ := json.Marshal(manifest)
	mSum := sha256.Sum256(manifestBytes)
	manifestDigest := "sha256:" + hex.EncodeToString(mSum[:])

	// The fakeImage struct tracks layer blobs as a slice. imaged's
	// PullManifest returns the compressed-blob digests in order; the
	// registry serves each by digest lookup. The last layer is the
	// above-base layer (when present) — that's the layer imaged actually
	// streams into the app ext4 after LayersAboveBase filters out the
	// base prefix.
	img := fakeImage{
		configDigest:   cfgDigest,
		configBytes:    cfgBytes,
		layerBlobs:     make([]blobEntry, len(layers)),
		manifestDigest: manifestDigest,
		manifestBytes:  manifestBytes,
		manifestMT:     "application/vnd.oci.image.manifest.v1+json",
	}
	for i, l := range layers {
		img.layerBlobs[i] = blobEntry{digest: l.digest, bytes: l.blob}
	}

	// Reference of the form "<host>/<repo>@sha256:<digest>". The test passes
	// this to apid's CreateDeployment; imaged pulls from the same host.
	ref := fmt.Sprintf("%s@%s", repo, manifestDigest)
	return img, ref
}

// helloLayerDiffID is the uncompressed-tar (diff_id) of the single layer
// HelloImage and BaseLayerImage advertise. Both helpers use buildHelloLayer
// to construct the gzip'd tar blob, so the diff_id is identical — and that
// is the property oci.LayersAboveBase relies on for the deploy-time base
// prefix test.
const helloLayerDiffID = "sha256:" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// buildHelloLayer returns the gzipped tar blob that HelloImage and
// BaseLayerImage serve as their single layer. Centralising the encoder
// here means the two helpers produce bit-identical layer bytes — and
// therefore identical diff_ids — without copy-pasting the gzip+tar dance.
func buildHelloLayer(body string) []byte {
	var layerBuf bytes.Buffer
	zw := gzip.NewWriter(&layerBuf)
	tw := tar.NewWriter(zw)
	// The app itself: a static server (see testdata/helloserver) that serves
	// app/hello.txt on / and answers /healthz. The image contains nothing
	// else — no shell, no libc — so the platform must run it as the
	// scratch-based customer image it is. It rides in every layer this
	// helper builds, so each image variant (hello, above-base, cpu-bound,
	// wedged) carries it regardless of which layer ends up above the base.
	server, err := helloServerBinary()
	if err != nil {
		panic(fmt.Sprintf("fakeregistry: build hello-server fixture: %v", err))
	}
	if err := tw.WriteHeader(&tar.Header{Name: "hello-server", Mode: 0o755, Size: int64(len(server)), Typeflag: tar.TypeReg}); err != nil {
		panic(fmt.Sprintf("fakeregistry: write server header: %v", err))
	}
	if _, err := tw.Write(server); err != nil {
		panic(fmt.Sprintf("fakeregistry: write server: %v", err))
	}
	hdr := &tar.Header{
		Name:     "app/hello.txt",
		Mode:     0o644,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		panic(fmt.Sprintf("fakeregistry: write tar header: %v", err))
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		panic(fmt.Sprintf("fakeregistry: write tar body: %v", err))
	}
	if err := tw.Close(); err != nil {
		panic(fmt.Sprintf("fakeregistry: close tar: %v", err))
	}
	if err := zw.Close(); err != nil {
		panic(fmt.Sprintf("fakeregistry: close gzip: %v", err))
	}
	return layerBuf.Bytes()
}

// BaseLayerImage returns a one-layer image whose layer blob is the SAME blob
// as HelloImage's app/hello.txt layer for the same `body` — and whose
// diff_id therefore matches. Pairs with HelloImage to satisfy
// oci.LayersAboveBase (base's diff_ids must be a prefix of the app's): when
// imaged aboveBaseLayers them together, the shared layer drops out as base
// and only the app layer stays in `above`.
//
// Cmd/Entrypoint fields are deliberately empty — this image stands in for a
// real runner base; the deployed app's manifest is what guest-init executes.
// LayersAboveBase doesn't look at Cmd, and the empty Entrypoint never reaches
// a real guest because the test's deploy never wakes the builder VM
// (builderd is M6).
func BaseLayerImage(repo, body string) (fakeImage, string) {
	layerBytes := buildHelloLayer(body)
	layerSum := sha256.Sum256(layerBytes)
	layerDigest := "sha256:" + hex.EncodeToString(layerSum[:])

	cfg := map[string]any{
		"architecture": "amd64",
		"os":           "linux",
		"config":       map[string]any{"Entrypoint": []string{"/bin/true"}, "Env": []string{}},
		"rootfs": map[string]any{
			"type":     "layers",
			"diff_ids": []string{"sha256:" + repeat("b", 64)}, // == helloLayerDiffID
		},
	}
	cfgBytes, _ := json.Marshal(cfg)
	cfgSum := sha256.Sum256(cfgBytes)
	cfgDigest := "sha256:" + hex.EncodeToString(cfgSum[:])

	manifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest":    cfgDigest,
			"size":      len(cfgBytes),
		},
		"layers": []map[string]any{{
			"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
			"digest":    layerDigest,
			"size":      len(layerBytes),
		}},
	}
	manifestBytes, _ := json.Marshal(manifest)
	mSum := sha256.Sum256(manifestBytes)
	manifestDigest := "sha256:" + hex.EncodeToString(mSum[:])

	img := fakeImage{
		configDigest:   cfgDigest,
		configBytes:    cfgBytes,
		layerBlobs:     []blobEntry{{digest: layerDigest, bytes: layerBytes}},
		manifestDigest: manifestDigest,
		manifestBytes:  manifestBytes,
		manifestMT:     "application/vnd.oci.image.manifest.v1+json",
	}
	ref := fmt.Sprintf("%s@%s", repo, manifestDigest)
	return img, ref
}

// FakeRegistry serves one or more images on an httptest server.
type FakeRegistry struct {
	srv    *httptest.Server
	images map[string]fakeImage // repo → image (one image per repo, the e2e test only uses one)

	// Auth gate (issue #461 / ADR-062). When set, every /v2/* call must
	// carry a Bearer token issued by /token; /token requires the matching
	// Basic Auth. Nil = anonymous, the existing posture.
	mu       sync.Mutex
	authUser string
	authPass string
	tokens   map[string]struct{} // issued bearer tokens; opaque random strings
}

// NewFakeRegistry returns a running registry bound to 127.0.0.1. The caller
// must Close() it.
func NewFakeRegistry() *FakeRegistry {
	f := &FakeRegistry{images: map[string]fakeImage{}, tokens: map[string]struct{}{}}
	mux := http.NewServeMux()

	// Public endpoints the OCI client hits. No auth — anon public pull.
	mux.HandleFunc("/v2/", f.route)
	mux.HandleFunc("/v2", f.route)
	// Bearer-token exchange endpoint. Only wired (rejects anonymous) when
	// RequireBasicAuth is set; before that, the path returns 404 so an
	// anonymous pull that accidentally probes /token doesn't change
	// behavior.
	mux.HandleFunc("/token", f.token)

	f.srv = httptest.NewServer(mux)
	return f
}

// RequireBasicAuth installs a per-FakeRegistry Basic Auth gate. After this
// call:
//
//   - /v2/... returns 401 with a Bearer challenge whose realm is
//     "<FakeRegistry.URL()>/token", service "fake-registry", and a scope
//     matching the requested repo. The challenge drives imaged's
//     pkg/oci.RegistryClient.fetchToken path.
//   - /token requires `Authorization: Basic base64(user:pass)` matching
//     the user/pass arguments; on success it issues a fresh random bearer
//     token (kept in memory) and returns the distribution-spec
//     `{"token": "..."}` envelope.
//   - /v2/... with a valid Bearer token then serves the manifest / blob
//     normally.
//
// Used by the registry-auth e2e to assert imaged's transient unseal + auth
// threading end-to-end. Per-call gates are intentionally NOT modelled —
// the e2e harness uses one credential per (FakeRegistry, image) tuple.
func (f *FakeRegistry) RequireBasicAuth(user, pass string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authUser = user
	f.authPass = pass
}

// authEnabled reports whether the Basic Auth gate is on. Reads under mu.
func (f *FakeRegistry) authEnabled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authUser != "" || f.authPass != ""
}

// issueToken mints a fresh random bearer token and records it. Locked.
func (f *FakeRegistry) issueToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	tok := base64.RawURLEncoding.EncodeToString(b[:])
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[tok] = struct{}{}
	return tok
}

// tokenValid reports whether the bearer token was issued by this server.
func (f *FakeRegistry) tokenValid(t string) bool {
	if t == "" {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.tokens[t]
	return ok
}

// token handles GET /token. Only wired when RequireBasicAuth was called
// (anonymous calls return 404 so the public path stays unaffected). On
// success emits `{"token": "<random>"}` — the distribution-spec shape
// pkg/oci/auth.go::FetchToken consumes.
func (f *FakeRegistry) token(w http.ResponseWriter, r *http.Request) {
	if !f.authEnabled() {
		http.NotFound(w, r)
		return
	}
	user, pass, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="fake-registry"`)
		http.Error(w, "missing basic auth", http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	wantUser, wantPass := f.authUser, f.authPass
	f.mu.Unlock()
	if user != wantUser || pass != wantPass {
		w.Header().Set("WWW-Authenticate", `Basic realm="fake-registry"`)
		http.Error(w, "bad credentials", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token":      f.issueToken(),
		"expires_in": 60,
	})
}

// bearerChallenge returns a `WWW-Authenticate: Bearer ...` value with
// realm=server.URL + /token, service="fake-registry", scope keyed to
// the requested repo. Used by route() when the gate is on and the
// incoming request has no valid Bearer token.
func (f *FakeRegistry) bearerChallenge(scope string) string {
	realm := strings.TrimSuffix(f.srv.URL, "/") + "/token"
	if scope == "" {
		return fmt.Sprintf(`Bearer realm=%q,service="fake-registry"`, realm)
	}
	return fmt.Sprintf(`Bearer realm=%q,service="fake-registry",scope=%q`, realm, scope)
}

// URL is the host:port the OCI client should connect to. Pass to imaged via
// oci.WithEndpoint("http", host) in unit tests; in the e2e harness, imaged
// reads the reference as-is and dials this URL.
func (f *FakeRegistry) URL() string { return f.srv.URL }

// Host returns just the host:port (no scheme) — what oci.WithEndpoint wants.
func (f *FakeRegistry) Host() string {
	// srv.URL is like "http://127.0.0.1:51234"; strip the scheme.
	u := f.srv.URL
	for i := 0; i < len(u)-2; i++ {
		if u[i] == ':' && u[i+1] == '/' && u[i+2] == '/' {
			return u[i+3:]
		}
	}
	return u
}

// AddImage installs an image under repo (e.g. "library/hello"). Returns the
// digest-pinned reference the e2e test passes to apid.
func (f *FakeRegistry) AddImage(repo string, img fakeImage) string {
	f.images[repo] = img
	return fmt.Sprintf("%s/%s@%s", f.Host(), repo, img.manifestDigest)
}

// Close shuts down the httptest server.
func (f *FakeRegistry) Close() { f.srv.Close() }

// route dispatches /v2/<repo>/manifests/<ref> and /v2/<repo>/blobs/<digest>.
// No auth — the harness is local-only.
func (f *FakeRegistry) route(w http.ResponseWriter, r *http.Request) {
	// Auth gate (issue #461 / ADR-062). When RequireBasicAuth was
	// installed, every /v2/* request must carry a valid Bearer token
	// issued by /token. Missing / invalid → 401 with the Bearer
	// challenge that drives pkg/oci.RegistryClient.fetchToken. The
	// challenge's scope is `repository:<repo>` to mirror the
	// distribution-spec convention.
	if f.authEnabled() {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !f.tokenValid(bearer) {
			path := r.URL.Path
			repo, _, _ := parseOCIPath(path)
			scope := "repository:" + repo
			w.Header().Set("WWW-Authenticate", f.bearerChallenge(scope))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	path := r.URL.Path
	repo, kind, ref := parseOCIPath(path)
	if repo == "" {
		http.NotFound(w, r)
		return
	}
	img, ok := f.images[repo]
	if !ok {
		http.Error(w, "unknown repo", http.StatusNotFound)
		return
	}
	switch kind {
	case "manifests":
		// Accept either a tag or a digest match.
		if ref != img.manifestDigest && !isTagRef(ref) {
			http.Error(w, "unknown manifest", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", img.manifestMT)
		w.Header().Set("Docker-Content-Digest", img.manifestDigest)
		_, _ = w.Write(img.manifestBytes)
	case "blobs":
		switch ref {
		case img.configDigest:
			w.Header().Set("Content-Type", "application/vnd.oci.image.config.v1+json")
			w.Header().Set("Docker-Content-Digest", img.configDigest)
			_, _ = w.Write(img.configBytes)
		default:
			// Walk the layer-blob table — multi-layer images (HelloImageAboveBase)
			// have multiple entries; single-layer images have one. Look up by
			// digest and stream the matching blob.
			for _, lb := range img.layerBlobs {
				if lb.digest == ref {
					w.Header().Set("Content-Type", "application/vnd.oci.image.layer.v1.tar+gzip")
					w.Header().Set("Docker-Content-Digest", lb.digest)
					_, _ = w.Write(lb.bytes)
					return
				}
			}
			http.Error(w, "unknown blob", http.StatusNotFound)
		}
	default:
		http.NotFound(w, r)
	}
}

// parseOCIPath extracts (repo, kind, ref) from /v2/<repo>/<kind>/<ref>.
// Repo may contain slashes (e.g. "library/hello").
func parseOCIPath(path string) (repo, kind, ref string) {
	const prefix = "/v2/"
	if len(path) < len(prefix) || path[:len(prefix)] != prefix {
		return "", "", ""
	}
	rest := path[len(prefix):]
	// Find the LAST "/manifests/" or "/blobs/" so the repo can include slashes.
	for _, k := range []string{"/manifests/", "/blobs/"} {
		if i := lastIndex(rest, k); i >= 0 {
			return rest[:i], k[1 : len(k)-1], rest[i+len(k):]
		}
	}
	return "", "", ""
}

func lastIndex(s, sub string) int {
	for i := len(s) - len(sub); i >= 0; i-- {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// isTagRef reports whether ref is a tag (alphanumeric + dot, dash, underscore)
// rather than a digest. Used to accept tag-based GETs against the same image.
func isTagRef(ref string) bool {
	if len(ref) == 0 {
		return false
	}
	for _, c := range ref {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '.', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
