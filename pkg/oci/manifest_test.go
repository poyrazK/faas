package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
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

func TestRegistryPullManifest_NoCompatiblePlatform(t *testing.T) {
	f := newFakeRegistry(t)
	f.manifestBody = []byte(`{
        "schemaVersion": 2,
        "mediaType": "application/vnd.oci.image.index.v1+json",
        "manifests": []
    }`)
	_, err := f.client().PullManifest(context.Background(), "ghcr.io/org/app:main")
	if err == nil {
		t.Fatal("empty index should be rejected")
	}
	// ADR-021: platform selection failures must lift to
	// ErrImageManifestInvalid so the imaged handler persists
	// deployments.error_code = image_manifest_invalid.
	if !errors.Is(err, ErrImageManifestInvalid) {
		t.Errorf("PullManifest platform selection err = %v, want errors.Is(_, ErrImageManifestInvalid) true", err)
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
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read blob: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("close blob: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("blob bytes = %q, want %q", got, want)
	}
}

func TestRegistryPullBlob_RejectsTamperedStream(t *testing.T) {
	want := []byte("expected layer bytes")
	digest := digestOf(want)
	f := newFakeRegistry(t)
	f.blobHandler = func(_, got string) ([]byte, error) {
		if got != digest {
			t.Errorf("requested digest = %q, want %q", got, digest)
		}
		return []byte("tampered layer bytes"), nil
	}

	rc, err := f.client().PullBlob(context.Background(), "org/app", digest)
	if err != nil {
		t.Fatalf("PullBlob: %v", err)
	}
	got, err := io.ReadAll(rc)
	if err == nil {
		t.Fatal("expected digest mismatch while reading blob")
	}
	if !errors.Is(err, ErrImageManifestInvalid) {
		t.Fatalf("read error = %v, want ErrImageManifestInvalid", err)
	}
	if !strings.Contains(err.Error(), "blob digest mismatch") {
		t.Fatalf("read error = %v, want blob digest mismatch", err)
	}
	if string(got) != "tampered layer bytes" {
		t.Fatalf("blob bytes = %q, want tampered response for verification test", got)
	}
	if closeErr := rc.Close(); !errors.Is(closeErr, ErrImageManifestInvalid) {
		t.Fatalf("close error = %v, want ErrImageManifestInvalid", closeErr)
	}
}

func TestRegistryPullLayers_RejectsTamperedConfigAndLayer(t *testing.T) {
	t.Run("config", func(t *testing.T) {
		f := newFakeRegistry(t)
		config := []byte(`{"Cmd":["x"]}`)
		manifestDigest := f.withImageManifest(t, config, []byte("layer"))
		f.layerBlobs[digestOf(config)] = []byte(`{"Cmd":["tampered"]}`)

		_, err := f.client().PullLayers(context.Background(), "ghcr.io/org/app@"+manifestDigest)
		if err == nil {
			t.Fatal("expected tampered config to fail")
		}
		if !errors.Is(err, ErrImageManifestInvalid) || !strings.Contains(err.Error(), "blob digest mismatch") {
			t.Fatalf("tampered config error = %v, want image-manifest digest mismatch", err)
		}
	})

	t.Run("layer", func(t *testing.T) {
		f := newFakeRegistry(t)
		config := []byte(`{"Cmd":["x"]}`)
		layer := []byte("expected layer")
		manifestDigest := f.withImageManifest(t, config, layer)
		f.layerBlobs[digestOf(layer)] = []byte("tampered layer")

		res, err := f.client().PullLayers(context.Background(), "ghcr.io/org/app@"+manifestDigest)
		if err != nil {
			t.Fatalf("PullLayers: %v", err)
		}
		if len(res.Layers) != 1 {
			t.Fatalf("layers = %d, want 1", len(res.Layers))
		}
		got, readErr := io.ReadAll(res.Layers[0])
		if readErr == nil {
			t.Fatal("expected tampered layer to fail while reading")
		}
		if !errors.Is(readErr, ErrImageManifestInvalid) || !strings.Contains(readErr.Error(), "blob digest mismatch") {
			t.Fatalf("tampered layer read error = %v, want image-manifest digest mismatch", readErr)
		}
		if string(got) != "tampered layer" {
			t.Fatalf("layer bytes = %q, want tampered response for verification test", got)
		}
		if closeErr := res.Layers[0].Close(); !errors.Is(closeErr, ErrImageManifestInvalid) {
			t.Fatalf("tampered layer close error = %v, want ErrImageManifestInvalid", closeErr)
		}
	})
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
