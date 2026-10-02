package imaged

// adr: 430

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
)

type baseResolutionPuller struct {
	*minimalManifestPuller
	resolution   oci.ImageResolution
	resolveCalls int
	resolveErr   error
}

func (p *baseResolutionPuller) ResolveImage(_ context.Context, _ string, _ *oci.BasicAuth) (oci.ImageResolution, error) {
	p.resolveCalls++
	return p.resolution, p.resolveErr
}
func verifiedBaseFixture(t *testing.T, mode string) (*Handler, *state.MemStore, *baseResolutionPuller, *recordingRunner, string) {
	t.Helper()
	layer := gzTar(t, map[string]string{"usr/share/source": "contents"})
	diff := imagechain.Digest(layerPlainBytes(t, layer))
	if mode == "wrong DiffID" {
		diff = imagechain.Digest([]byte("other"))
	}
	if mode == "bad CRC" {
		layer[len(layer)-8] ^= 1
	}
	config, _ := json.Marshal(map[string]any{"os": "linux", "architecture": "amd64", "rootfs": map[string]any{"type": "layers", "diff_ids": []string{diff}}})
	manifest := oci.Manifest{SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json", Config: oci.Descriptor{Digest: imagechain.Digest(config), Size: int64(len(config))}, Layers: []oci.Descriptor{{Digest: imagechain.Digest(layer), Size: int64(len(layer))}}}
	raw, _ := json.Marshal(manifest)
	digest := imagechain.Digest(raw)
	ref := "registry.example/base@" + digest
	puller := &baseResolutionPuller{minimalManifestPuller: &minimalManifestPuller{manifest: manifest, layers: map[string][]byte{imagechain.Digest(layer): layer}}, resolution: oci.ImageResolution{SourceReference: ref, Reference: ref, SourceDigest: digest, Digest: digest, Evidence: &imagechain.Evidence{SourceManifest: raw, Config: config}}}
	store := state.NewMemStore()
	runner := &recordingRunner{}
	var builder LayerBuilder = rootfs.NewBuilder(runner)
	if mode == "consumer omitted" {
		builder = &fakeBuilder{bytesOut: 1024}
	}
	root := t.TempDir()
	guest := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(guest, []byte("ACTUAL INIT"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := New(store, &fakeNotifier{}, puller, builder, guest, root, silentLogger()).WithStorage(mustLocalStorage(t, root))
	return h, store, puller, runner, ref
}
func TestVerifiedBaseRealBuilderLineage(t *testing.T) {
	for _, mode := range []string{"complete", "wrong DiffID", "bad CRC", "consumer omitted"} {
		t.Run(mode, func(t *testing.T) {
			h, store, puller, runner, ref := verifiedBaseFixture(t, mode)
			result, err := h.EnsureBaseExt4(t.Context(), ref, "base/verified.ext4", "base/verified.digest", "", "", "")
			stored, getErr := store.GetCurrentBaseImageProducer(t.Context(), "base/verified.ext4")
			if mode != "complete" {
				if err == nil || !errors.Is(getErr, state.ErrNotFound) {
					t.Fatalf("unverified producer published: %v %v", err, getErr)
				}
				return
			}
			if err != nil || getErr != nil || result.Producer.ID != stored.ID {
				t.Fatalf("producer missing: %v %v", err, getErr)
			}
			if stored.Input.Artifact.Digest != imagechain.Digest(bytes.Repeat([]byte{0}, 1024)) || stored.Input.Artifact.Bytes != 1024 || stored.Input.GuestInitDigest != imagechain.Digest([]byte("ACTUAL INIT")) {
				t.Fatalf("actual output binding lost: %+v", stored.Input.Artifact)
			}
			if len(stored.Input.Layers) != 1 || len(runner.argv) == 0 {
				t.Fatal("real builder did not consume source")
			}
			puller.resolveErr = errors.New("registry unavailable")
			reused, err := h.EnsureBaseExt4(t.Context(), ref, "base/verified.ext4", "base/verified.digest", "", "", "")
			if err != nil || !reused.Skipped || reused.Producer.ID != stored.ID || puller.resolveCalls != 1 {
				t.Fatalf("exact local proof did not survive registry outage: %v", err)
			}
			be, err := h.storageFor()
			if err != nil {
				t.Fatal(err)
			}
			mutated := bytes.Repeat([]byte{0}, 1024)
			mutated[len(mutated)-1] = 1
			if err := be.Put(t.Context(), "base/verified.ext4", bytes.NewReader(mutated)); err != nil {
				t.Fatal(err)
			}
			if _, err := h.EnsureBaseExt4(t.Context(), ref, "base/verified.ext4", "base/verified.digest", "", "", ""); err == nil {
				t.Fatal("changed artifact reused during outage")
			}
			puller.resolveErr = nil
			rebuilt, err := h.EnsureBaseExt4(t.Context(), ref, "base/verified.ext4", "base/verified.digest", "", "", "")
			if err != nil || rebuilt.Skipped || rebuilt.Producer.ID == stored.ID {
				t.Fatalf("changed artifact not rebuilt: %v", err)
			}
		})
	}
}
func TestVerifiedBaseRejectsLegacySidecarAuthority(t *testing.T) {
	h, store, puller, _, ref := verifiedBaseFixture(t, "complete")
	be, err := h.storageFor()
	if err != nil {
		t.Fatal(err)
	}
	if err := be.Put(t.Context(), "base/verified.ext4", bytes.NewReader([]byte("old filesystem"))); err != nil {
		t.Fatal(err)
	}
	guest, err := guestInitBinaryDigest(h.guestInitPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.writeBaseDigestSidecar(t.Context(), be, "base/verified.digest", puller.manifest.Config.Digest, guest, ref); err != nil {
		t.Fatal(err)
	}
	puller.resolveErr = errors.New("registry unavailable")
	if _, err := h.EnsureBaseExt4(t.Context(), ref, "base/verified.ext4", "base/verified.digest", "", "", ""); err == nil {
		t.Fatal("legacy sidecar fabricated producer evidence")
	}
	if _, err := store.GetCurrentBaseImageProducer(t.Context(), "base/verified.ext4"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("legacy artifact gained authority: %v", err)
	}
}

func TestVerifiedChildBaseRequiresNativeCapability(t *testing.T) {
	h, store, puller, _, parentRef := verifiedBaseFixture(t, "complete")
	parent, err := h.EnsureBaseExt4(t.Context(), parentRef, "base/parent.ext4", "base/parent.digest", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	chain, err := imagechain.Validate(parent.Producer.Input.ImageChain, parent.Producer.Input.SourceDigest, parent.Producer.Input.SelectedDigest)
	if err != nil {
		t.Fatal(err)
	}
	delta := gzTar(t, map[string]string{"usr/share/child": "delta"})
	config, _ := json.Marshal(map[string]any{"os": "linux", "architecture": "amd64", "rootfs": map[string]any{"type": "layers", "diff_ids": append(chain.DiffIDs, imagechain.Digest(layerPlainBytes(t, delta)))}})
	manifest := oci.Manifest{SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json", Config: oci.Descriptor{Digest: imagechain.Digest(config), Size: int64(len(config))}, Layers: []oci.Descriptor{{Digest: chain.Layers[0].Digest, Size: chain.Layers[0].Size}, {Digest: imagechain.Digest(delta), Size: int64(len(delta))}}}
	raw, _ := json.Marshal(manifest)
	digest := imagechain.Digest(raw)
	ref := "registry.example/child@" + digest
	puller.resolution = oci.ImageResolution{SourceReference: ref, Reference: ref, SourceDigest: digest, Digest: digest, Evidence: &imagechain.Evidence{SourceManifest: raw, Config: config}}
	h.vmmClient = &fakeVMMClient{}
	if _, err := h.EnsureBaseExt4(t.Context(), ref, "base/child.ext4", "base/child.digest", "", parentRef, "base/parent.ext4"); err == nil {
		t.Fatal("legacy materialization minted a child producer")
	}
	if _, err := store.GetCurrentBaseImageProducer(t.Context(), "base/child.ext4"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed child selected: %v", err)
	}
}

func TestBaseResultsDoNotSerializePrivateEvidence(t *testing.T) {
	private := state.BaseImageProducer{Input: state.BaseImageProducerInput{ImageChain: &imagechain.Evidence{Config: []byte("PRIVATE IMAGE CONFIG")}}}
	for _, value := range []any{BaseStageResult{Producer: private}, EnsureBasesResult{Producer: private}, preparedContainerWorkload{BaseProducer: private}} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("PRIVATE")) || bytes.Contains(raw, []byte("image_chain")) || bytes.Contains(raw, []byte("Producer")) {
			t.Fatal("stage result serialized private image configuration")
		}
	}
}
