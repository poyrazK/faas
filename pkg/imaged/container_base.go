package imaged

// adr: 429

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) containerBaseProducer(ctx context.Context, prepared preparedContainerWorkload, runtime string, start int) (state.BaseImageProducer, error) {
	if prepared.Evidence == nil {
		return state.BaseImageProducer{}, nil
	}
	store, ok := h.store.(state.BaseImageProducerStore)
	if !ok {
		return state.BaseImageProducer{}, fmt.Errorf("imaged: base producer store unavailable")
	}
	base, err := store.GetCurrentBaseImageProducer(ctx, sched.BaseKeyForArch(runtime, oci.ImageArchitecture))
	if err != nil {
		return state.BaseImageProducer{}, err
	}
	prefix, err := imagechain.Validate(base.Input.ImageChain, base.Input.SourceDigest, base.Input.SelectedDigest)
	if err != nil {
		return state.BaseImageProducer{}, err
	}
	full, err := imagechain.Validate(prepared.Evidence, prepared.SourceDigest, prepared.Digest)
	if err != nil || len(prefix.DiffIDs) != start || len(full.DiffIDs) < start {
		return state.BaseImageProducer{}, fmt.Errorf("imaged: base producer prefix does not match selected image")
	}
	for i, diff := range prefix.DiffIDs {
		if diff != full.DiffIDs[i] {
			return state.BaseImageProducer{}, fmt.Errorf("imaged: base producer DiffID mismatch")
		}
	}
	guest, err := guestInitBinaryDigest(h.guestInitPath)
	if err != nil {
		return state.BaseImageProducer{}, err
	}
	if base.Input.LayoutVersion != baseLayoutVersion || base.Input.GuestInitDigest != baseGuestDigest(guest) {
		return state.BaseImageProducer{}, fmt.Errorf("imaged: base boot layout changed")
	}
	be, err := h.storageFor()
	if err != nil {
		return state.BaseImageProducer{}, err
	}
	if err := checkStoredBaseArtifact(ctx, be, base.Input.Artifact); err != nil {
		return state.BaseImageProducer{}, err
	}
	return base, nil
}
