package imaged

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

var errEnvironmentScanUnavailable = errors.New("imaged: environment scan unavailable")
var errEnvironmentScanRejected = errors.New("imaged: environment base scan refused publication")

// Environment bases use the same retained OCI consumption proof, exact-byte
// scan and fail-closed boot sidecar as platform runtime bases. Customer locks
// never constitute publication or vulnerability evidence on their own.
func (h *Handler) publishExecutionEnvironmentBase(ctx context.Context, backend storage.StorageBackend, row state.ExecutionEnvironment, proof localOCIProvenance, base rootfs.BaseBuildResult) error {
	store, ok := h.store.(state.BaseImageProducerStore)
	if !ok {
		return fmt.Errorf("imaged: base producer store unavailable")
	}
	consumed := make([]imagechain.LayerConsumption, 0, len(proof.Streams))
	for _, stream := range proof.Streams {
		value, err := stream.Consumption()
		if err != nil {
			return err
		}
		consumed = append(consumed, value)
	}
	ref := "gregale.invalid/run-environments/" + row.ID + "@" + proof.Digest
	producer, err := store.PublishBaseImageProducer(ctx, state.BaseImageProducerInput{ID: uuid.NewString(), Artifact: imagechain.BaseArtifact{StorageKey: row.BaseKey, Digest: base.ArtifactDigest, Bytes: base.ArtifactBytes}, SourceReference: ref, SourceDigest: proof.Digest, SelectedDigest: proof.Digest, ImageChain: proof.Evidence, LayoutVersion: baseLayoutVersion, GuestInitDigest: base.GuestInitDigest, ContentBytes: base.SizeBytes, Layers: consumed})
	if err != nil {
		return err
	}
	if err := h.writeBaseContentSidecar(ctx, backend, row.BaseKey); err != nil {
		return err
	}
	if err := h.writeBaseDigestSidecar(ctx, backend, row.BaseKey+".digest", proof.Image.Config.Digest, base.GuestInitDigest, ref); err != nil {
		return err
	}
	scan, err := h.ensureProducedBaseScan(ctx, backend, producer, base.ImagePath)
	if err != nil {
		return fmt.Errorf("%w: %w", errEnvironmentScanUnavailable, err)
	}
	if scan.Input.Status != "complete" {
		return errEnvironmentScanUnavailable
	}
	result, err := scanResultFromAPI(scan.Result)
	if err != nil {
		return fmt.Errorf("%w: %w", errEnvironmentScanUnavailable, err)
	}
	if result.fixAvailableToMap()[SeverityCritical] > 0 {
		return errEnvironmentScanRejected
	}
	return nil
}
