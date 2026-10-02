package imaged

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Exercise imaged's production key wiring and provenance update through the
// remote export path. Rootfs publication itself has separate hermetic tests.
type sbomPublishingBuilder struct{ fakeBuilder }

func (b *sbomPublishingBuilder) Build(ctx context.Context, in rootfs.BuildInput) (rootfs.BuildResult, error) {
	result, err := b.fakeBuilder.Build(ctx, in)
	if err != nil || in.SBOMRun == nil || in.SBOMStorageKey == "" {
		return result, err
	}
	body, err := in.SBOMRun(ctx, ".")
	if err != nil {
		return result, nil //nolint:nilerr // Match observational SBOM failure semantics.
	}
	if err := in.Storage.Put(ctx, in.SBOMStorageKey, strings.NewReader(string(body))); err != nil {
		return result, err
	}
	result.SBOMKey = in.SBOMStorageKey
	return result, nil
}

func TestRegionalExportPublishesSBOM(t *testing.T) {
	fx, h, _, _, _ := regionalImageExportFixture(t)
	b := &sbomPublishingBuilder{fakeBuilder: fakeBuilder{bytesOut: 4096}}
	h.builder = b
	body := []byte(`{"bomFormat":"CycloneDX","specVersion":"1.5","version":1}`)
	h.WithSyftRun(func(context.Context, string) ([]byte, error) { return body, nil })
	if err := h.HandleNotification(t.Context(), exportNotification(fx.dep)); err != nil {
		t.Fatal(err)
	}
	build, err := fx.store.BuildByDeployment(t.Context(), fx.dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	key := region.BuildSBOMKey(fx.app.ComputeRegion, build.ID)
	prov, err := fx.store.BuildProvenanceByBuildID(t.Context(), build.ID)
	if err != nil || prov.SBOMStorageKey != key {
		t.Fatalf("provenance=%+v err=%v", prov, err)
	}
	r, err := h.storage.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil || string(payload) != string(body) {
		t.Fatalf("SBOM=%s err=%v", payload, err)
	}
	if r, err := h.storage.Get(t.Context(), BuildSBOMKey(build.ID)); !errors.Is(err, storage.ErrNotFound) {
		if r != nil {
			_ = r.Close()
		}
		t.Fatalf("global SBOM exists: %v", err)
	}
	h.updateBuildProvenanceSBOM(t.Context(), fx.dep.ID, "")
	prov, err = fx.store.BuildProvenanceByBuildID(t.Context(), build.ID)
	if err != nil || prov.SBOMStorageKey != key {
		t.Fatalf("failed retry lost SBOM: %+v %v", prov, err)
	}
	refs, err := fx.store.ListAppDeletionArtifacts(t.Context(), fx.app.ID)
	found := false
	for _, ref := range refs {
		if ref.Key == key {
			found = true
		}
	}
	if err != nil || !found {
		t.Fatalf("deletion refs=%+v err=%v", refs, err)
	}
	if err := h.storage.Delete(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if r, err := h.storage.Get(t.Context(), key); !errors.Is(err, storage.ErrNotFound) {
		if r != nil {
			_ = r.Close()
		}
		t.Fatalf("SBOM delete: %v", err)
	}
}

func TestWriteRegionalSBOMRequiresRoute(t *testing.T) {
	legacy := mustLocalStorage(t, t.TempDir())
	eu := mustLocalStorage(t, t.TempDir())
	const build = "550e8400-e29b-41d4-a716-446655440000"
	h := &Handler{storage: legacy, log: silentLogger()}
	h.WithSyftRun(func(context.Context, string) ([]byte, error) { return []byte(`{"bomFormat":"CycloneDX"}`), nil })
	if key, err := h.writeBuildSBOM(t.Context(), "eu-sbom", build, t.TempDir()); err != nil || key != "" {
		t.Fatalf("unrouted SBOM: %q %v", key, err)
	}
	router, err := storage.NewRegionRouter(map[string]storage.StorageBackend{"eu-sbom": eu}, legacy)
	if err != nil {
		t.Fatal(err)
	}
	h.WithStorage(router)
	want := region.BuildSBOMKey("eu-sbom", build)
	if key, err := h.writeBuildSBOM(t.Context(), "eu-sbom", build, t.TempDir()); err != nil || key != want {
		t.Fatalf("routed SBOM: %q %v", key, err)
	}
}
