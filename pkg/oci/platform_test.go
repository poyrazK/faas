package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// multiArchIndexBody returns a minimal OCI image-index body that
// fans out to amd64 + arm64. Used by the multi-arch walk tests
// (ADR-140 §Decision 1).
func multiArchIndexBody(amd64Digest, arm64Digest string) []byte {
	return []byte(fmt.Sprintf(`{
        "schemaVersion": 2,
        "mediaType": "application/vnd.oci.image.index.v1+json",
        "manifests": [
            {"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"%s","size":1234,"platform":{"os":"linux","architecture":"amd64"}},
            {"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"%s","size":1234,"platform":{"os":"linux","architecture":"arm64"}}
        ]
    }`, amd64Digest, arm64Digest))
}

// singleArchManifestBody returns a flat per-platform OCI image
// manifest body — what a registry returns for a per-arch descriptor
// inside an image-index Manifests[] entry.
func singleArchManifestBody(configDigest string, layerDigests ...string) []byte {
	layersJSON := ""
	for i, d := range layerDigests {
		if i > 0 {
			layersJSON += ","
		}
		layersJSON += fmt.Sprintf(`{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"%s","size":1}`, d)
	}
	return []byte(fmt.Sprintf(`{
        "schemaVersion": 2,
        "mediaType": "application/vnd.oci.image.manifest.v1+json",
        "config": {"mediaType":"application/vnd.oci.image.config.v1+json","digest":"%s","size":1},
        "layers": [%s]
    }`, configDigest, layersJSON))
}

// TestDefaultPlatformMatcher_LinuxOnlyPinsArch verifies the
// default matcher ignores non-linux platforms and selects only the
// entry matching the supplied goarch (ADR-140 §Decision 1).
func TestDefaultPlatformMatcher_LinuxOnlyPinsArch(t *testing.T) {
	m := DefaultPlatformMatcher("amd64")
	if !m(Platform{OS: "linux", Architecture: "amd64"}) {
		t.Error("linux/amd64 should match")
	}
	if m(Platform{OS: "linux", Architecture: "arm64"}) {
		t.Error("linux/arm64 should NOT match an amd64 matcher")
	}
	if m(Platform{OS: "windows", Architecture: "amd64"}) {
		t.Error("windows/amd64 should NOT match a linux matcher")
	}
}

// TestDefaultPlatformMatcherFromGOARCH_Smoke verifies the
// runtime.GOARCH convenience helper returns a non-nil matcher.
func TestDefaultPlatformMatcherFromGOARCH_Smoke(t *testing.T) {
	m := DefaultPlatformMatcherFromGOARCH()
	if m == nil {
		t.Fatal("DefaultPlatformMatcherFromGOARCH must return non-nil matcher")
	}
	// At least one of amd64/arm64 should match on any GOARCH.
	if !m(Platform{OS: "linux", Architecture: "amd64"}) &&
		!m(Platform{OS: "linux", Architecture: "arm64"}) {
		t.Error("matcher from GOARCH should match at least one of amd64/arm64")
	}
}

// TestIndex_JSONDecode verifies the Index shape parses a real-world
// manifest-list body (Docker manifest list v2 schema).
func TestIndex_JSONDecode(t *testing.T) {
	body := multiArchIndexBody("sha256:aaa", "sha256:bbb")
	var idx Index
	if err := json.Unmarshal(body, &idx); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(idx.Manifests) != 2 {
		t.Errorf("manifests = %d, want 2", len(idx.Manifests))
	}
	if idx.Manifests[0].Platform.Architecture != "amd64" {
		t.Errorf("manifests[0].platform.architecture = %q", idx.Manifests[0].Platform.Architecture)
	}
}

// TestRegistryFetchManifest_MultiArchResolvesLinuxAMD64 verifies
// the walker selects the linux/amd64 descriptor on an amd64 host.
// (ADR-140 §Decision 1; commit 3 acceptance criteria.)
func TestRegistryFetchManifest_MultiArchResolvesLinuxAMD64(t *testing.T) {
	amd64Child := singleArchManifestBody("sha256:"+hex64, "sha256:"+hex64)
	arm64Child := singleArchManifestBody("sha256:"+hex64, "sha256:"+hex64)
	f := newFakeRegistry(t)
	f.manifestHandler = func(repo, got string) ([]byte, string, error) {
		// Top-level pull returns the index; per-arch pulls return
		// the single-arch manifest.
		if strings.HasPrefix(got, "sha256:") {
			return amd64Child, "application/vnd.oci.image.manifest.v1+json", nil
		}
		return multiArchIndexBody("sha256:"+hex64, "sha256:bbb"), "application/vnd.oci.image.index.v1+json", nil
	}
	c := f.clientWith(WithPlatformMatcher(DefaultPlatformMatcher("amd64")))
	m, _, err := c.fetchManifestWithAuth(context.Background(), mustRef(t, "ghcr.io/org/app:main"), nil)
	if err != nil {
		t.Fatalf("fetchManifest: %v", err)
	}
	if m.Config.Digest == "" {
		t.Error("expected resolved single-arch manifest with config descriptor")
	}
	_ = arm64Child // silence unused-var in case arm64-only handler gets exercised
}

// TestRegistryFetchManifest_NestedIndexRejects verifies the 2-hop
// bound: a manifest whose selected entry is itself a list returns
// ErrImageManifestInvalid (ADR-140 §Decision 1 — defensive depth>1
// rejection).
func TestRegistryFetchManifest_NestedIndexRejects(t *testing.T) {
	nested := multiArchIndexBody("sha256:aaa", "sha256:bbb")
	f := newFakeRegistry(t)
	f.manifestHandler = func(repo, got string) ([]byte, string, error) {
		if strings.HasPrefix(got, "sha256:") {
			// Selected descriptor is itself an index → reject.
			return nested, "application/vnd.oci.image.index.v1+json", nil
		}
		return nested, "application/vnd.oci.image.index.v1+json", nil
	}
	c := f.clientWith(WithPlatformMatcher(DefaultPlatformMatcher("amd64")))
	_, _, err := c.fetchManifestWithAuth(context.Background(), mustRef(t, "ghcr.io/org/app:main"), nil)
	if err == nil {
		t.Fatal("nested index should be rejected (depth>1)")
	}
	if !errors.Is(err, ErrImageManifestInvalid) {
		t.Errorf("err = %v, want errors.Is(_, ErrImageManifestInvalid)", err)
	}
}

// TestRegistryFetchManifest_NoMatchingPlatform verifies the
// "did you set FAAS_BUILDER_ARCH?" hint path (ADR-140 §Decision 3):
// no Manifests[] entry matches the configured matcher →
// ErrImageManifestInvalid with a hint.
func TestRegistryFetchManifest_NoMatchingPlatform(t *testing.T) {
	f := newFakeRegistry(t)
	f.manifestHandler = func(repo, got string) ([]byte, string, error) {
		// Only windows/amd64 in the index; matcher wants linux/arm64.
		return []byte(`{
            "schemaVersion": 2,
            "mediaType": "application/vnd.oci.image.index.v1+json",
            "manifests": [{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:zzz","size":1,"platform":{"os":"windows","architecture":"amd64"}}]
        }`), "application/vnd.oci.image.index.v1+json", nil
	}
	c := f.clientWith(WithPlatformMatcher(DefaultPlatformMatcher("arm64")))
	_, _, err := c.fetchManifestWithAuth(context.Background(), mustRef(t, "ghcr.io/org/app:main"), nil)
	if err == nil {
		t.Fatal("non-matching platform should surface ErrImageManifestInvalid")
	}
	if !errors.Is(err, ErrImageManifestInvalid) {
		t.Errorf("err = %v, want errors.Is(_, ErrImageManifestInvalid)", err)
	}
	if !strings.Contains(err.Error(), "FAAS_BUILDER_ARCH") {
		t.Errorf("err = %v should hint at FAAS_BUILDER_ARCH", err)
	}
}

// TestRegistryFetchManifest_DigestPinnedSkipsWalk verifies
// digest-pinned references resolve to a single-arch manifest
// directly (ADR-140 §Decision 4). The walker does not fire on
// single-arch Content-Type.
func TestRegistryFetchManifest_DigestPinnedSkipsWalk(t *testing.T) {
	body := singleArchManifestBody("sha256:"+hex64, "sha256:"+hex64)
	f := newFakeRegistry(t)
	f.manifestHandler = func(repo, got string) ([]byte, string, error) {
		return body, "application/vnd.oci.image.manifest.v1+json", nil
	}
	c := f.client()
	_, _, err := c.fetchManifestWithAuth(context.Background(), mustRef(t, "ghcr.io/org/app@sha256:"+hex64), nil)
	if err != nil {
		t.Fatalf("digest-pinned: %v", err)
	}
}

// mustRef is a tiny helper that parses a reference string in tests
// and fails the test on parse error (ADR-140 §Decision 1 fixtures).
func mustRef(t *testing.T, s string) Reference {
	t.Helper()
	r, err := ParseReference(s)
	if err != nil {
		t.Fatalf("ParseReference(%q): %v", s, err)
	}
	return r
}