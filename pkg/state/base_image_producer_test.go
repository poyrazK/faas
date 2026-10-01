package state

// adr: 393

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/imagechain"
)

// The fixture obtains consumption from actual decoded streams. The store
// intake test does not claim that these byte fixtures are native ext4 mounts.
func baseProducerFixture(t *testing.T, key string, plain ...string) BaseImageProducerInput {
	t.Helper()
	var descriptors []imagechain.Descriptor
	var diffs []string
	var consumed []imagechain.LayerConsumption
	for i, value := range plain {
		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		if _, err := z.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		d := imagechain.Descriptor{Digest: imagechain.Digest(compressed.Bytes()), Size: int64(compressed.Len())}
		diff := imagechain.Digest([]byte(value))
		stream, err := imagechain.NewLayerStream(t.Context(), io.NopCloser(bytes.NewReader(compressed.Bytes())), d, diff, i)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := gzip.NewReader(stream)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, stream.VerifyingUncompressedReader(decoded)); err != nil {
			t.Fatal(err)
		}
		if err := decoded.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, stream); err != nil {
			t.Fatal(err)
		}
		c, err := stream.Consumption()
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
		descriptors = append(descriptors, d)
		diffs = append(diffs, diff)
		consumed = append(consumed, c)
	}
	config, _ := json.Marshal(map[string]any{"os": "linux", "architecture": "amd64", "rootfs": map[string]any{"type": "layers", "diff_ids": diffs}})
	manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": imagechain.Descriptor{Digest: imagechain.Digest(config), Size: int64(len(config))}, "layers": descriptors})
	digest := imagechain.Digest(manifest)
	artifact := []byte("produced complete artifact " + key)
	return BaseImageProducerInput{ID: uuid.NewString(), Artifact: imagechain.BaseArtifact{StorageKey: key, Digest: imagechain.Digest(artifact), Bytes: int64(len(artifact))}, SourceReference: "registry.example/base@" + digest, SourceDigest: digest, SelectedDigest: digest, ImageChain: &imagechain.Evidence{SourceManifest: manifest, Config: config}, LayoutVersion: imagechain.BaseLayoutVersion, GuestInitDigest: imagechain.Digest([]byte("init")), ContentBytes: 8, Layers: consumed}
}
func baseProducerLifecycle(t *testing.T, s BaseImageProducerStore) {
	t.Helper()
	root := baseProducerFixture(t, "base/parent.ext4", "parent")
	parent, err := s.PublishBaseImageProducer(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	child := baseProducerFixture(t, "base/child.ext4", "parent", "delta", "delta")
	child.ParentProducerID, child.ParentInputHash, child.LayerStart = parent.ID, parent.InputHash, 1
	child.Layers = child.Layers[1:]
	child.ParentMaterialization = &imagechain.ParentMaterialization{Artifact: parent.Input.Artifact, TargetDir: "/dev/shm/faas-base-staging/child"}
	stored, err := s.PublishBaseImageProducer(t.Context(), child)
	if err != nil {
		t.Fatal(err)
	}
	child.ImageChain.Config[0] ^= 1
	child.Layers[0].DiffID = "changed"
	child.ParentMaterialization.TargetDir = "changed"
	got, err := s.GetBaseImageProducerByID(t.Context(), stored.ID)
	if err != nil || got.InputHash != stored.InputHash {
		t.Fatalf("aliased input: %v", err)
	}
	retry, err := s.PublishBaseImageProducer(t.Context(), got.Input)
	if err != nil || !retry.PublishedAt.Equal(stored.PublishedAt) {
		t.Fatalf("exact retry changed clock: %v", err)
	}
	replacement := root
	replacement.ID = uuid.NewString()
	if _, err := s.PublishBaseImageProducer(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishBaseImageProducer(t.Context(), got.Input); err != nil {
		t.Fatalf("historical parent broke exact child retry: %v", err)
	}
	candidate := got.Input
	candidate.ID = uuid.NewString()
	if _, err := s.PublishBaseImageProducer(t.Context(), candidate); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatalf("new child used superseded parent: %v", err)
	}
	if _, err := s.PublishBaseImageProducer(t.Context(), root); !errors.Is(err, ErrConflict) {
		t.Fatalf("old selection reactivated: %v", err)
	}
	current, err := s.GetCurrentBaseImageProducer(t.Context(), root.Artifact.StorageKey)
	if err != nil || current.ID != replacement.ID {
		t.Fatalf("failed retry changed current: %v", err)
	}
	current.Input.ImageChain.Config[0] ^= 1
	if _, err := s.GetCurrentBaseImageProducer(t.Context(), root.Artifact.StorageKey); err != nil {
		t.Fatalf("returned evidence aliased: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.PublishBaseImageProducer(ctx, baseProducerFixture(t, "base/canceled.ext4", "parent")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled publication: %v", err)
	}
}
func baseProducerRefusals(t *testing.T, s BaseImageProducerStore) {
	root := baseProducerFixture(t, "base/source.ext4", "parent")
	parent, err := s.PublishBaseImageProducer(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing consumption", "changed DiffID", "layout", "key", "artifact size", "parent hash", "parent bytes", "parent prefix"} {
		t.Run(mode, func(t *testing.T) {
			in := baseProducerFixture(t, "base/rejected.ext4", "parent", "delta")
			switch mode {
			case "missing consumption":
				in.Layers = nil
			case "changed DiffID":
				in.Layers[0].DiffID = imagechain.Digest([]byte("other"))
			case "layout":
				in.LayoutVersion = "old"
			case "key":
				in.Artifact.StorageKey = "base/../other.ext4"
			case "artifact size":
				in.Artifact.Bytes = 0
			default:
				in.ParentProducerID, in.ParentInputHash, in.LayerStart = parent.ID, parent.InputHash, 1
				in.Layers = in.Layers[1:]
				in.ParentMaterialization = &imagechain.ParentMaterialization{Artifact: parent.Input.Artifact, TargetDir: "/dev/shm/faas-base-staging/test"}
				if mode == "parent hash" {
					in.ParentInputHash = imagechain.Digest([]byte("other"))[7:]
				}
				if mode == "parent bytes" {
					in.ParentMaterialization.Artifact.Bytes++
				}
				if mode == "parent prefix" {
					different := baseProducerFixture(t, "base/rejected.ext4", "other", "delta")
					in.ImageChain, in.SourceReference, in.SourceDigest, in.SelectedDigest = different.ImageChain, different.SourceReference, different.SourceDigest, different.SelectedDigest
				}
			}
			if _, err := s.PublishBaseImageProducer(t.Context(), in); err == nil {
				t.Fatal("invalid producer published")
			}
			if _, err := s.GetCurrentBaseImageProducer(t.Context(), "base/rejected.ext4"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("failed producer selected: %v", err)
			}
		})
	}
}
func TestMemBaseProducerLifecycle(t *testing.T) { baseProducerLifecycle(t, NewMemStore()) }
func TestMemBaseProducerRefusals(t *testing.T)  { baseProducerRefusals(t, NewMemStore()) }
