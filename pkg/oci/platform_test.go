package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testImageMediaType = "application/vnd.oci.image.manifest.v1+json"
const testIndexMediaType = "application/vnd.oci.image.index.v1+json"

type platformDescriptor struct {
	Descriptor
	Platform *imagePlatform `json:"platform,omitempty"`
}

func platformIndex(t *testing.T, mediaType string, entries ...platformDescriptor) []byte {
	t.Helper()
	body, err := json.Marshal(struct {
		SchemaVersion int                  `json:"schemaVersion"`
		MediaType     string               `json:"mediaType"`
		Manifests     []platformDescriptor `json:"manifests"`
	}{2, mediaType, entries})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func platformImage(t *testing.T, arch string) (platformDescriptor, []byte, []byte, []byte) {
	t.Helper()
	config := []byte(fmt.Sprintf(`{"os":"linux","architecture":%q,"config":{"Cmd":["./app"]}}`, arch))
	layer := []byte("layer-" + arch)
	body, err := json.Marshal(Manifest{SchemaVersion: 2, MediaType: testImageMediaType,
		Config: Descriptor{Digest: imageContentDigest(config), Size: int64(len(config))},
		Layers: []Descriptor{{Digest: imageContentDigest(layer), Size: int64(len(layer))}}})
	if err != nil {
		t.Fatal(err)
	}
	return platformDescriptor{Descriptor{testImageMediaType, imageContentDigest(body), int64(len(body))}, &imagePlatform{OS: "linux", Architecture: arch}}, body, config, layer
}

// A private registry fixture requires bearer authentication separately on each
// metadata request. Unknown/ARM descriptors have no served content: fetching
// either would fail the test, as would a filesystem layer read during resolve.
func TestPlatformRegistryReadersAgree(t *testing.T) {
	for _, mt := range []string{testIndexMediaType, "application/vnd.docker.distribution.manifest.list.v2+json"} {
		t.Run(mt, func(t *testing.T) {
			amd, manifest, config, layer := platformImage(t, "amd64")
			arm, _, _, _ := platformImage(t, "arm64")
			artifact := platformDescriptor{Descriptor{testImageMediaType, "sha256:" + strings.Repeat("f", 64), 1}, &imagePlatform{OS: "unknown", Architecture: "unknown"}}
			index := platformIndex(t, mt, arm, artifact, amd)
			root := imageContentDigest(index)
			configDigest := imageContentDigest(config)
			layerDigest := imageContentDigest(layer)
			allowLayers := false
			tagReads := 0
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					u, p, ok := r.BasicAuth()
					if !ok || u != "user" || p != "secret" {
						t.Error("missing registry credentials")
					}
					_, _ = io.WriteString(w, `{"token":"authorized"}`)
					return
				}
				if r.Header.Get("Authorization") != "Bearer authorized" {
					w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm=%q,service="registry",scope="repository:org/app:pull"`, srv.URL+"/token"))
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				switch r.URL.Path {
				case "/v2/org/app/manifests/latest":
					tagReads++
					w.Header().Set("Content-Type", mt+"; charset=utf-8")
					_, _ = w.Write(index)
				case "/v2/org/app/manifests/" + root:
					_, _ = w.Write(index)
				case "/v2/org/app/manifests/" + amd.Digest:
					_, _ = w.Write(manifest)
				case "/v2/org/app/blobs/" + configDigest:
					_, _ = w.Write(config)
				case "/v2/org/app/blobs/" + layerDigest:
					if !allowLayers {
						t.Error("layer downloaded during metadata resolution")
					}
					_, _ = w.Write(layer)
				default:
					t.Errorf("unexpected fetch: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			c := NewRegistryClient(WithEndpoint("http", strings.TrimPrefix(srv.URL, "http://")))
			auth := &BasicAuth{Username: "user", Password: "secret"}
			ctx := context.Background()
			for _, ref := range []string{"example.com/org/app:latest", "example.com/org/app@" + root, "example.com/org/app@" + amd.Digest} {
				got, err := c.ResolveImage(ctx, ref, auth)
				if err != nil {
					t.Fatal(err)
				}
				expectedSource := root
				if strings.HasSuffix(ref, amd.Digest) {
					expectedSource = amd.Digest
				}
				if got.InputReference != ref || got.SourceDigest != expectedSource || got.SourceReference != "example.com/org/app@"+expectedSource || got.Digest != amd.Digest || got.Reference != "example.com/org/app@"+amd.Digest || got.Config.Architecture != "amd64" {
					t.Fatalf("wrong resolution: %+v", got)
				}
			}
			if tagReads != 1 {
				t.Fatalf("resolution reread mutable tag %d times", tagReads)
			}
			digest, err := c.PullDigestWithAuth(ctx, "example.com/org/app@"+root, auth)
			if err != nil || digest != root {
				t.Fatalf("signature subject changed: %s %v", digest, err)
			}
			m, err := c.PullManifestWithAuth(ctx, "example.com/org/app:latest", auth)
			if err != nil || m.Config.Digest != configDigest {
				t.Fatalf("manifest: %+v %v", m, err)
			}
			cfg, err := c.PullImageConfigWithAuth(ctx, "example.com/org/app:latest", auth)
			if err != nil || cfg.Architecture != "amd64" {
				t.Fatalf("config: %+v %v", cfg, err)
			}
			allowLayers = true
			// Full pinned references route blobs to the same repository. The
			// signing adapter also passes full source references to PullBlob.
			stream, err := c.PullBlobWithAuth(ctx, "example.com/org/app@"+amd.Digest, layerDigest, auth)
			if err != nil {
				t.Fatal(err)
			}
			blob, err := io.ReadAll(stream)
			_ = stream.Close()
			if err != nil || string(blob) != string(layer) {
				t.Fatalf("pinned blob: %q %v", blob, err)
			}
			layers, err := c.PullLayersWithAuth(ctx, "example.com/org/app:latest", auth)
			if err != nil {
				t.Fatal(err)
			}
			if layers.Digest != amd.Digest || len(layers.Layers) != 1 {
				t.Fatalf("wrong layers: %+v", layers)
			}
			for _, stream := range layers.Layers {
				body, err := io.ReadAll(stream)
				_ = stream.Close()
				if err != nil || string(body) != string(layer) {
					t.Fatalf("layer: %q %v", body, err)
				}
			}
		})
	}
}

func TestPlatformSelectionPolicy(t *testing.T) {
	amd, _, _, _ := platformImage(t, "amd64")
	arm, _, _, _ := platformImage(t, "arm64")
	other := amd
	other.Digest = "sha256:" + strings.Repeat("e", 64)
	conflict := amd
	conflict.Size++
	variant := amd
	variant.Platform = &imagePlatform{OS: "linux", Architecture: "amd64", Variant: "v2"}
	baseline := amd
	baseline.Platform = &imagePlatform{OS: "linux", Architecture: "amd64", Variant: "v1"}
	features := amd
	features.Platform = &imagePlatform{OS: "linux", Architecture: "amd64", OSFeatures: []string{"special"}}
	nested := amd
	nested.MediaType = testIndexMediaType
	missing := amd
	missing.Platform = nil
	invalid := amd
	invalid.Digest = "sha256:bad"
	for _, tc := range []struct {
		name    string
		entries []platformDescriptor
		valid   bool
	}{
		{"arm first", []platformDescriptor{arm, amd}, true},
		{"amd first", []platformDescriptor{amd, arm}, true},
		{"duplicate", []platformDescriptor{amd, amd}, true},
		{"baseline variant", []platformDescriptor{baseline}, true},
		{"no amd64", []platformDescriptor{arm}, false},
		{"ambiguous", []platformDescriptor{amd, other}, false},
		{"conflicting descriptor", []platformDescriptor{amd, conflict}, false},
		{"cpu requirement", []platformDescriptor{variant}, false},
		{"os requirement", []platformDescriptor{features}, false},
		{"nested", []platformDescriptor{nested}, false},
		{"missing platform", []platformDescriptor{missing}, false},
		{"invalid digest", []platformDescriptor{invalid}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectImagePlatform(platformIndex(t, testIndexMediaType, tc.entries...))
			if tc.valid {
				if err != nil || got.Digest != amd.Digest {
					t.Fatalf("got %+v %v", got, err)
				}
			} else if !errors.Is(err, ErrImageManifestInvalid) {
				t.Fatalf("expected invalid manifest, got %v", err)
			}
		})
	}
}

func TestPlatformResolutionRejectsTampering(t *testing.T) {
	for _, mode := range []string{"index digest", "child digest", "child size", "config digest", "config platform", "config variant", "pinned child reread", "pinned signature subject"} {
		t.Run(mode, func(t *testing.T) {
			amd, manifest, config, _ := platformImage(t, "amd64")
			switch mode {
			case "config platform":
				_, manifest, config, _ = platformImage(t, "arm64")
				amd.Digest, amd.Size = imageContentDigest(manifest), int64(len(manifest))
			case "config variant":
				old := imageContentDigest(config)
				config = []byte(`{"os":"linux","architecture":"amd64","variant":"v3","config":{"Cmd":["./app"]}}`)
				manifest = []byte(strings.ReplaceAll(string(manifest), old, imageContentDigest(config)))
				amd.Digest, amd.Size = imageContentDigest(manifest), int64(len(manifest))
			case "child size":
				amd.Size++
			}
			index := platformIndex(t, testIndexMediaType, amd)
			ref := "example.com/org/app:latest"
			if mode == "index digest" {
				ref = "example.com/org/app@sha256:" + strings.Repeat("a", 64)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/blobs/") {
					if mode == "config digest" {
						_, _ = w.Write([]byte(`{}`))
					} else {
						_, _ = w.Write(config)
					}
					return
				}
				if strings.HasSuffix(r.URL.Path, amd.Digest) {
					if mode == "child digest" || mode == "pinned child reread" || mode == "pinned signature subject" {
						_, _ = w.Write(append(manifest, ' '))
					} else {
						_, _ = w.Write(manifest)
					}
					return
				}
				_, _ = w.Write(index)
			}))
			defer srv.Close()
			c := NewRegistryClient(WithEndpoint("http", strings.TrimPrefix(srv.URL, "http://")))
			var err error
			switch mode {
			case "pinned child reread":
				_, err = c.PullManifest(context.Background(), "example.com/org/app@"+amd.Digest)
			case "pinned signature subject":
				_, err = c.PullDigest(context.Background(), "example.com/org/app@"+amd.Digest)
			default:
				_, err = c.ResolveImage(context.Background(), ref, nil)
			}
			if !errors.Is(err, ErrImageManifestInvalid) {
				t.Fatalf("accepted %s: %v", mode, err)
			}
		})
	}
}
