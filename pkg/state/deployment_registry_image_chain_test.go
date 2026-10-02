package state

// adr: 429

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/imagechain"
)

func registryChainFixture(t *testing.T) (*imagechain.Evidence, imagechain.LayerConsumption) {
	t.Helper()
	plain := append([]byte("layer bytes"), make([]byte, 1024)...)
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	_, err := z.Write(plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	config := []byte(fmt.Sprintf(`{"os":"linux","architecture":"amd64","rootfs":{"type":"layers","diff_ids":[%q]}}`, imagechain.Digest(plain)))
	d := imagechain.Descriptor{Digest: imagechain.Digest(compressed.Bytes()), Size: int64(compressed.Len())}
	manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": imagechain.Descriptor{Digest: imagechain.Digest(config), Size: int64(len(config))}, "layers": []imagechain.Descriptor{d}})
	index := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":%q,"size":%d,"platform":{"os":"linux","architecture":"amd64"}}]}`, imagechain.Digest(manifest), len(manifest)))
	stream, err := imagechain.NewLayerStream(t.Context(), io.NopCloser(bytes.NewReader(compressed.Bytes())), d, imagechain.Digest(plain), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	reader, err := gzip.NewReader(stream)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := io.Copy(io.Discard, stream.VerifyingUncompressedReader(reader)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, stream); err != nil {
		t.Fatal(err)
	}
	consumed, err := stream.Consumption()
	if err != nil {
		t.Fatal(err)
	}
	return &imagechain.Evidence{SourceManifest: index, SelectedManifest: manifest, Config: config}, consumed
}

func registryImageChainPublication(t *testing.T, s registryRootfsTestStore) {
	for _, kind := range []string{"app-layer", "full-rootfs", "sidecar-layer"} {
		t.Run(kind, func(t *testing.T) {
			chain, consumed := registryChainFixture(t)
			input, app, dep := registryVerificationFixtureWithChain(t, s, kind == "sidecar-layer", chain)
			parent, err := s.RecordDeploymentRegistryVerification(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			// Input and returned chain buffers cannot mutate stored signed bytes.
			input.ImageChain.Config[0] ^= 1
			parent.Input.ImageChain.SourceManifest[0] ^= 1
			parent, err = s.GetLatestDeploymentRegistryVerification(t.Context(), app.AccountID, app.ID, dep.ID, input.WorkloadName)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := imagechain.Validate(parent.Input.ImageChain, parent.Input.Proof.SubjectDigest, parent.Input.SelectedDigest); err != nil {
				t.Fatalf("stored chain aliased caller: %v", err)
			}
			root := DeploymentRegistryRootfsInput{ID: uuid.NewString(), RegistryVerificationID: parent.ID, RegistryInputHash: parent.InputHash, AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID, WorkloadName: input.WorkloadName, Scope: dep.Scope, Kind: kind, StorageKey: "apps/" + app.Slug + "/" + dep.ID + ".ext4", RootfsPath: "/srv/" + dep.ID + ".ext4", ArtifactDigest: "sha256:" + strings.Repeat("f", 64), ArtifactBytes: 4096, ContentBytes: 123, Layers: []imagechain.LayerConsumption{consumed}}
			if kind == "sidecar-layer" {
				root.RootfsPath = ""
			}
			for _, field := range []string{"missing", "index", "digest", "compressed size", "DiffID", "negative uncompressed", "reordered range"} {
				t.Run(field, func(t *testing.T) {
					candidate := root
					candidate.ID = uuid.NewString()
					candidate.Layers = append([]imagechain.LayerConsumption(nil), root.Layers...)
					switch field {
					case "missing":
						candidate.Layers = nil
					case "index":
						candidate.Layers[0].Index = 1
					case "digest":
						candidate.Layers[0].Digest = imagechain.Digest([]byte("other"))
					case "compressed size":
						candidate.Layers[0].CompressedBytes++
					case "DiffID":
						candidate.Layers[0].DiffID = imagechain.Digest([]byte("other"))
					case "negative uncompressed":
						candidate.Layers[0].UncompressedBytes = -1
					case "reordered range":
						candidate.LayerStart = 1
					}
					if _, err := s.PublishDeploymentRegistryRootfs(t.Context(), candidate); err == nil {
						t.Fatalf("accepted %s consumption substitution", field)
					}
				})
			}
			assertNoRegistryRootfs(t, s, root)
			value, err := s.PublishDeploymentRegistryRootfs(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			root.Layers[0].Digest = "mutated"
			value.Input.Layers[0].DiffID = "mutated"
			got, err := s.GetCurrentDeploymentRegistryRootfs(t.Context(), app.AccountID, app.ID, dep.ID, input.WorkloadName)
			if err != nil || len(got.Input.Layers) != 1 || got.Input.Layers[0] != consumed {
				t.Fatalf("producer consumption alias/loss: %+v %v", got, err)
			}
		})
	}
}

func registryImageChainRejectsTamperedMetadata(t *testing.T, s registryRootfsTestStore) {
	chain, _ := registryChainFixture(t)
	in, _, _ := registryVerificationFixtureWithChain(t, s, false, chain)
	for _, field := range []string{"source", "selected", "config"} {
		t.Run(field, func(t *testing.T) {
			candidate := cloneRegistryVerificationInput(in)
			candidate.ID = uuid.NewString()
			switch field {
			case "source":
				candidate.ImageChain.SourceManifest[0] ^= 1
			case "selected":
				candidate.ImageChain.SelectedManifest[0] ^= 1
			case "config":
				candidate.ImageChain.Config[0] ^= 1
			}
			if _, err := s.RecordDeploymentRegistryVerification(t.Context(), candidate); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("accepted mutated %s: %v", field, err)
			}
		})
	}
	if _, err := s.GetLatestDeploymentRegistryVerification(t.Context(), in.AccountID, in.AppID, in.DeploymentID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad metadata left evidence: %v", err)
	}
}

func TestMemRegistryImageChainPublication(t *testing.T) {
	registryImageChainPublication(t, NewMemStore())
}
func TestMemRegistryImageChainMetadata(t *testing.T) {
	registryImageChainRejectsTamperedMetadata(t, NewMemStore())
}
