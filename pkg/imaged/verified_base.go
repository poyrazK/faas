package imaged

// adr: 393

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type verifiedBaseRequest struct {
	ref, key, digestKey, out, parentRef, parentKey, guestDigest string
	be                                                          storage.StorageBackend
	mp                                                          oci.ManifestPuller
}
type verifiedBaseBuild struct {
	prepared        preparedContainerWorkload
	result          rootfs.BaseBuildResult
	parent          state.BaseImageProducer
	materialization *imagechain.ParentMaterialization
}

func baseGuestDigest(raw string) string {
	if raw == "" {
		return ""
	}
	return "sha256:" + raw
}

func (h *Handler) ensureVerifiedBaseExt4(ctx context.Context, r verifiedBaseRequest, resolver oci.ImageResolver) (BaseStageResult, error) {
	if err := writeProducedBaseScanCompatibility(ctx, r.be, r.key, r.ref, r.out, state.BaseImageScan{}); err != nil {
		return BaseStageResult{}, err
	}
	store, ok := h.store.(state.BaseImageProducerStore)
	if !ok {
		return BaseStageResult{}, fmt.Errorf("imaged: base producer store unavailable")
	}
	if cached, ok, err := h.reuseVerifiedBase(ctx, r, store, r.ref); err != nil || ok {
		return cached, err
	}
	resolved, err := resolver.ResolveImage(ctx, r.ref, nil)
	if err != nil {
		return BaseStageResult{}, fmt.Errorf("imaged: resolve base image: %w", err)
	}
	if _, err := imagechain.Validate(resolved.Evidence, resolved.SourceDigest, resolved.Digest); err != nil {
		return BaseStageResult{}, err
	}
	if cached, ok, err := h.reuseVerifiedBase(ctx, r, store, resolved.SourceReference); err != nil || ok {
		return cached, err
	}
	build := verifiedBaseBuild{prepared: preparedContainerWorkload{ImageResolution: resolved}}
	if r.parentRef == "" {
		build, err = h.buildVerifiedBase(ctx, r, build)
	} else {
		build, err = h.buildVerifiedChildBase(ctx, r, store, build)
	}
	if err != nil {
		return BaseStageResult{}, err
	}
	producer, err := h.publishVerifiedBase(ctx, r, store, build)
	if err != nil {
		return BaseStageResult{}, err
	}
	image, err := imagechain.Validate(resolved.Evidence, resolved.SourceDigest, resolved.Digest)
	if err != nil {
		return BaseStageResult{}, err
	}
	return h.finishVerifiedBase(ctx, r, producer, image.Config.Digest, false)
}

func (h *Handler) reuseVerifiedBase(ctx context.Context, r verifiedBaseRequest, store state.BaseImageProducerStore, source string) (BaseStageResult, bool, error) {
	producer, err := store.GetCurrentBaseImageProducer(ctx, r.key)
	if errors.Is(err, state.ErrNotFound) {
		return BaseStageResult{}, false, nil
	}
	if err != nil {
		return BaseStageResult{}, false, err
	}
	in := producer.Input
	if in.SourceReference != source || in.LayoutVersion != baseLayoutVersion || in.GuestInitDigest != baseGuestDigest(r.guestDigest) {
		return BaseStageResult{}, false, nil
	}
	if !baseCacheParentMatches(ctx, store, in, r) {
		return BaseStageResult{}, false, nil
	}
	if err := checkStoredBaseArtifact(ctx, r.be, in.Artifact); err != nil {
		return BaseStageResult{}, false, nil
	}
	if err := h.validateBaseArtifact(ctx, r.be, r.key); err != nil {
		return BaseStageResult{}, false, nil
	}
	image, err := imagechain.Validate(in.ImageChain, in.SourceDigest, in.SelectedDigest)
	if err != nil {
		return BaseStageResult{}, false, err
	}
	result, err := h.finishVerifiedBase(ctx, r, producer, image.Config.Digest, true)
	return result, err == nil, err
}

func baseCacheParentMatches(ctx context.Context, store state.BaseImageProducerStore, in state.BaseImageProducerInput, r verifiedBaseRequest) bool {
	if r.parentRef == "" {
		return in.ParentProducerID == ""
	}
	if in.ParentProducerID == "" {
		return false
	}
	parent, err := store.GetBaseImageProducerByID(ctx, in.ParentProducerID)
	return err == nil && parent.InputHash == in.ParentInputHash && parent.Input.SourceReference == r.parentRef && parent.Input.Artifact.StorageKey == r.parentKey
}

func checkStoredBaseArtifact(ctx context.Context, be storage.StorageBackend, expected imagechain.BaseArtifact) error {
	if !expected.Valid() {
		return fmt.Errorf("imaged: invalid complete base identity")
	}
	rc, err := be.Get(ctx, expected.StorageKey)
	if err != nil {
		return err
	}
	identity, readErr := rootfs.ReadArtifactIdentity(ctx, io.LimitReader(rc, expected.Bytes+1))
	closeErr := rc.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	if identity.Digest != expected.Digest || identity.Bytes != expected.Bytes {
		return fmt.Errorf("imaged: stored base differs from produced artifact")
	}
	return nil
}

func pullVerifiedBaseLayers(ctx context.Context, r verifiedBaseRequest, prepared preparedContainerWorkload, start int) ([]io.ReadCloser, error) {
	image, err := imagechain.Validate(prepared.Evidence, prepared.SourceDigest, prepared.Digest)
	if err != nil {
		return nil, err
	}
	if start < 0 || start > len(image.Layers) {
		return nil, fmt.Errorf("imaged: invalid base suffix")
	}
	readers := make([]io.ReadCloser, 0, len(image.Layers)-start)
	for _, desc := range image.Layers[start:] {
		rc, err := r.mp.PullBlob(ctx, repoWithHost(prepared.Reference), desc.Digest)
		if err != nil {
			closeContainerLayers(readers)
			return nil, err
		}
		readers = append(readers, rc)
	}
	verified, err := wrapContainerLayerReaders(ctx, prepared, readers, start)
	if err != nil {
		closeContainerLayers(readers)
		return nil, err
	}
	return verified, nil
}

func (h *Handler) buildVerifiedBase(ctx context.Context, r verifiedBaseRequest, build verifiedBaseBuild) (verifiedBaseBuild, error) {
	readers, err := pullVerifiedBaseLayers(ctx, r, build.prepared, 0)
	if err != nil {
		return build, err
	}
	defer closeContainerLayers(readers)
	build.result, err = h.builder.BuildBase(ctx, rootfs.BaseBuildInput{Layers: layersAsReaders(readers), Storage: r.be, StorageKey: r.key, GuestInitPath: h.guestInitPath})
	if err != nil {
		return build, err
	}
	build.prepared, err = consumedContainerLayers(build.prepared, layersAsReaders(readers), 0)
	return build, err
}

func (h *Handler) buildVerifiedChildBase(ctx context.Context, r verifiedBaseRequest, store state.BaseImageProducerStore, build verifiedBaseBuild) (verifiedBaseBuild, error) {
	parent, err := store.GetCurrentBaseImageProducer(ctx, r.parentKey)
	if err != nil {
		return build, err
	}
	if parent.Input.SourceReference != r.parentRef || parent.Input.LayoutVersion != baseLayoutVersion || parent.Input.GuestInitDigest != baseGuestDigest(r.guestDigest) {
		return build, fmt.Errorf("imaged: selected parent producer does not match base intent")
	}
	base, err := imagechain.Validate(parent.Input.ImageChain, parent.Input.SourceDigest, parent.Input.SelectedDigest)
	if err != nil {
		return build, err
	}
	child, err := imagechain.Validate(build.prepared.Evidence, build.prepared.SourceDigest, build.prepared.Digest)
	if err != nil {
		return build, err
	}
	if _, err := oci.LayersAboveBase(base.DiffIDs, child.DiffIDs); err != nil {
		return build, err
	}
	owner, ok := h.vmmClient.(VerifiedParentMaterializer)
	if !ok {
		return build, fmt.Errorf("imaged: verified parent materialization unavailable")
	}
	staging, err := rootfs.MkdirBaseStaging()
	if err != nil {
		return build, err
	}
	defer os.RemoveAll(staging)
	expected := imagechain.ParentMaterialization{Artifact: parent.Input.Artifact, TargetDir: staging}
	actual, err := owner.MaterializeVerifiedParentExt4(ctx, expected)
	if err != nil {
		return build, err
	}
	if actual != expected || ctx.Err() != nil {
		return build, fmt.Errorf("imaged: parent materialization binding mismatch")
	}
	build.parent, build.materialization = parent, &actual
	return h.applyVerifiedBaseDelta(ctx, r, build, staging, len(base.DiffIDs))
}

func (h *Handler) applyVerifiedBaseDelta(ctx context.Context, r verifiedBaseRequest, build verifiedBaseBuild, staging string, start int) (verifiedBaseBuild, error) {
	readers, err := pullVerifiedBaseLayers(ctx, r, build.prepared, start)
	if err != nil {
		return build, err
	}
	defer closeContainerLayers(readers)
	for _, reader := range readers {
		if err := rootfs.ApplyLayerGz(staging, reader); err != nil {
			return build, err
		}
	}
	build.prepared, err = consumedContainerLayers(build.prepared, layersAsReaders(readers), start)
	if err != nil {
		return build, err
	}
	build.result, err = h.builder.BuildBaseFromStaging(ctx, staging, rootfs.BaseBuildInput{Storage: r.be, StorageKey: r.key, GuestInitPath: h.guestInitPath})
	return build, err
}

func (h *Handler) publishVerifiedBase(ctx context.Context, r verifiedBaseRequest, store state.BaseImageProducerStore, build verifiedBaseBuild) (state.BaseImageProducer, error) {
	result, prepared := build.result, build.prepared
	artifact := imagechain.BaseArtifact{StorageKey: r.key, Digest: result.ArtifactDigest, Bytes: result.ArtifactBytes}
	if result.ImageKey != r.key || result.GuestInitDigest != baseGuestDigest(r.guestDigest) {
		return state.BaseImageProducer{}, fmt.Errorf("imaged: base conversion binding mismatch")
	}
	if err := checkStoredBaseArtifact(ctx, r.be, artifact); err != nil {
		return state.BaseImageProducer{}, err
	}
	if err := h.validateBaseArtifact(ctx, r.be, r.key); err != nil {
		return state.BaseImageProducer{}, err
	}
	return store.PublishBaseImageProducer(ctx, state.BaseImageProducerInput{ID: uuid.NewString(), Artifact: artifact, SourceReference: prepared.SourceReference, SourceDigest: prepared.SourceDigest, SelectedDigest: prepared.Digest, ImageChain: prepared.Evidence, LayoutVersion: baseLayoutVersion, GuestInitDigest: result.GuestInitDigest, ContentBytes: result.SizeBytes, LayerStart: prepared.LayerStart, Layers: prepared.Layers, ParentProducerID: build.parent.ID, ParentInputHash: build.parent.InputHash, ParentMaterialization: build.materialization})
}

func (h *Handler) finishVerifiedBase(ctx context.Context, r verifiedBaseRequest, producer state.BaseImageProducer, configDigest string, skipped bool) (BaseStageResult, error) {
	if err := h.writeBaseDigestSidecar(ctx, r.be, r.digestKey, configDigest, r.guestDigest, producer.Input.SourceReference); err != nil {
		return BaseStageResult{}, err
	}
	if err := h.writeProducedBaseScanSidecar(ctx, r, producer); err != nil {
		return BaseStageResult{}, fmt.Errorf("imaged: publish produced shared-base scan: %w", err)
	}
	generation := baseDigestSidecarValueWithSource(configDigest, r.guestDigest, producer.Input.SourceReference)
	if err := markCachedBaseGeneration(r.be, r.key, r.digestKey, generation); err != nil {
		return BaseStageResult{}, err
	}
	return BaseStageResult{OutImage: r.out, StorageKey: r.key, ConfigDigest: configDigest, Skipped: skipped, Producer: producer}, nil
}
