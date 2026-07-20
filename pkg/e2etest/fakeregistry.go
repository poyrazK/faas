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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
// produces an app layer containing a single regular file at app/hello.txt with
// body `helloBody`. The Cmd it advertises is `["/bin/sh","-c","cat app/hello.txt"]`
// — that's what `manifestFromImageConfig` will pick up as the Entrypoint, so
// the resulting layer is bootable by guest-init if it ever gets that far (the
// quota test never lets it get that far; the metal test does).
//
// The image has exactly one layer whose diff_id is the hardcoded constant
// `helloLayerDiffID` (sha256:bbbb…). Tests that need the two-drive scheme to
// work pair this with `BaseLayerImage` as the deploy-time base — base's
// diff_ids prefix the app's, above-base is exactly the app layer.
func HelloImage(repo, helloBody string) (fakeImage, string) {
	return layeredHelloImage(repo, helloBody, false)
}

// HelloImageAboveBase returns an image identical to HelloImage except it has
// TWO layers: the hardcoded helloLayerDiffID (matching BaseLayerImage's
// layer) followed by an additional layer whose diff_id is computed from the
// tar blob. Use this with BaseLayerImage as the deploy-time base — the base's
// single layer prefixes the app's two layers, so oci.LayersAboveBase puts
// the second (above-base) layer into `above`.
func HelloImageAboveBase(repo, helloBody string) (fakeImage, string) {
	return layeredHelloImage(repo, helloBody, true)
}

func layeredHelloImage(repo, helloBody string, aboveBase bool) (fakeImage, string) {
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
			"Cmd":          []string{"/bin/sh", "-c", "cat app/hello.txt"},
			"Env":          []string{},
			"WorkingDir":   "/",
			"ExposedPorts": map[string]any{"8080/tcp": struct{}{}},
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
}

// NewFakeRegistry returns a running registry bound to 127.0.0.1. The caller
// must Close() it.
func NewFakeRegistry() *FakeRegistry {
	f := &FakeRegistry{images: map[string]fakeImage{}}
	mux := http.NewServeMux()

	// Public endpoints the OCI client hits. No auth — anon public pull.
	mux.HandleFunc("/v2/", f.route)
	mux.HandleFunc("/v2", f.route)

	f.srv = httptest.NewServer(mux)
	return f
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
