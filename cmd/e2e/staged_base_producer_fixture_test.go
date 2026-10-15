package e2e_test

// adr: 595

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// This portable fixture binds actual staged bytes and decoded OCI layer bytes.
// Its debugfs shim models filesystem contents; it supplies no native receipt.
func seedBootContractBuilderBase(t *testing.T, pool *pgxpool.Pool, storageRoot string, guestInit []byte) string {
	t.Helper()
	key := sched.BaseKeyForArch("builder", imaged.BuilderArch())
	artifact := []byte("boot-contract ext4 placeholder\n")
	path := filepath.Join(storageRoot, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	descriptor, consumption := stagedBaseLayer(t, []byte("portable builder layer"))
	config, err := json.Marshal(map[string]any{"os": "linux", "architecture": imaged.BuilderArch(), "rootfs": map[string]any{"type": "layers", "diff_ids": []string{consumption.DiffID}}})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": imagechain.Descriptor{Digest: imagechain.Digest(config), Size: int64(len(config))}, "layers": []imagechain.Descriptor{descriptor}})
	if err != nil {
		t.Fatal(err)
	}
	digest := imagechain.Digest(manifest)
	ref := "127.0.0.1:1/onebox-faas/builder-base@" + digest
	_, err = state.NewPgStore(pool).PublishBaseImageProducer(t.Context(), state.BaseImageProducerInput{
		ID: uuid.NewString(), Artifact: imagechain.BaseArtifact{StorageKey: key, Digest: imagechain.Digest(artifact), Bytes: int64(len(artifact))},
		SourceReference: ref, SourceDigest: digest, SelectedDigest: digest,
		ImageChain:    &imagechain.Evidence{SourceManifest: manifest, Config: config},
		LayoutVersion: imagechain.BaseLayoutVersion, GuestInitDigest: imagechain.Digest(guestInit), Layers: []imagechain.LayerConsumption{consumption},
	})
	if err != nil {
		t.Fatalf("publish portable staged-base producer: %v", err)
	}
	return ref
}

func stagedBaseLayer(t *testing.T, plain []byte) (imagechain.Descriptor, imagechain.LayerConsumption) {
	t.Helper()
	var blob bytes.Buffer
	z := gzip.NewWriter(&blob)
	if _, err := z.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	d := imagechain.Descriptor{Digest: imagechain.Digest(blob.Bytes()), Size: int64(blob.Len())}
	stream, err := imagechain.NewLayerStream(t.Context(), io.NopCloser(bytes.NewReader(blob.Bytes())), d, imagechain.Digest(plain), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
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
	return d, c
}
