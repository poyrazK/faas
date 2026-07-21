package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

func TestRegistryPullManifest_DecodesLayersAndConfig(t *testing.T) {
	f := newFakeRegistry(t)
	f.manifestBody = []byte(`{
        "schemaVersion": 2,
        "mediaType": "application/vnd.oci.image.manifest.v1+json",
        "config": {"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:` + hex64 + `","size":1234},
        "layers": [
            {"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:` + hex64 + `","size":5678},
            {"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:` + hex64 + `","size":9012}
        ]
    }`)
	m, err := f.client().PullManifest(context.Background(), "ghcr.io/org/app:main")
	if err != nil {
		t.Fatalf("PullManifest: %v", err)
	}
	if m.Config.Digest != "sha256:"+hex64 {
		t.Errorf("config digest = %q", m.Config.Digest)
	}
	if len(m.Layers) != 2 {
		t.Errorf("layers = %d, want 2", len(m.Layers))
	}
	if m.Layers[1].Size != 9012 {
		t.Errorf("layers[1].size = %d", m.Layers[1].Size)
	}
}

func TestRegistryPullManifest_RejectsManifestList(t *testing.T) {
	f := newFakeRegistry(t)
	f.manifestBody = []byte(`{
        "schemaVersion": 2,
        "mediaType": "application/vnd.oci.image.index.v1+json",
        "manifests": []
    }`)
	_, err := f.client().PullManifest(context.Background(), "ghcr.io/org/app:main")
	if err == nil {
		t.Fatal("manifest list should be rejected")
	}
	// ADR-021: the manifest-list rejection must lift to
	// ErrImageManifestInvalid so the imaged handler persists
	// deployments.error_code = image_manifest_invalid.
	if !errors.Is(err, ErrImageManifestInvalid) {
		t.Errorf("PullManifest manifest-list err = %v, want errors.Is(_, ErrImageManifestInvalid) true", err)
	}
}

func TestRegistryPullManifest_RejectsBadLayerDigest(t *testing.T) {
	f := newFakeRegistry(t)
	f.manifestBody = []byte(`{
        "schemaVersion": 2,
        "config": {"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:` + hex64 + `","size":1},
        "layers": [{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:short","size":1}]
    }`)
	_, err := f.client().PullManifest(context.Background(), "ghcr.io/org/app:main")
	if err == nil {
		t.Fatal("bad digest should be rejected")
	}
	// ADR-021: schema-validation failures (missing config, no layers,
	// malformed digest) all lift to ErrImageManifestInvalid so the
	// imaged handler can branch on the same code regardless of which
	// manifest-validation step rejected the body.
	if !errors.Is(err, ErrImageManifestInvalid) {
		t.Errorf("PullManifest bad-digest err = %v, want errors.Is(_, ErrImageManifestInvalid) true", err)
	}
}

func TestRegistryPullBlob_StreamsBytesAndVerifiesDigest(t *testing.T) {
	want := []byte("hello, layer world — random bytes\n")
	sum := sha256.Sum256(want)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	f := newFakeRegistry(t)
	f.blobHandler = func(repo, got string) ([]byte, error) {
		if got != digest {
			t.Errorf("requested digest = %q, want %q", got, digest)
		}
		return want, nil
	}

	rc, err := f.client().PullBlob(context.Background(), "org/app", digest)
	if err != nil {
		t.Fatalf("PullBlob: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read blob: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("blob bytes = %q, want %q", got, want)
	}
}

func TestRegistryPullBlob_RefusesBadDigestFormat(t *testing.T) {
	c := NewRegistryClient()
	if _, err := c.PullBlob(context.Background(), "org/app", "sha256:not-64-hex-chars-just-a-few"); err == nil {
		t.Fatal("bad digest format should be rejected")
	}
}

func TestRegistryPullBlob_RefusesEmptyRepo(t *testing.T) {
	c := NewRegistryClient()
	if _, err := c.PullBlob(context.Background(), "", "sha256:"+hex64); err == nil {
		t.Fatal("empty repo should be rejected")
	}
}
