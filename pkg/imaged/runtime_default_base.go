package imaged

// adr: 435. Bind the actual runtime-default drive without inventing a prefix.

import (
	"context"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) bindContainerRuntimeBase(ctx context.Context, app state.App, prepared preparedContainerWorkload, kind string) (preparedContainerWorkload, error) {
	if kind != "full-rootfs" || prepared.Verification.ID == "" || prepared.Evidence == nil {
		return prepared, nil // Historical/unproved conversions gain no base evidence.
	}
	base, err := h.runtimeDefaultBaseProducer(ctx, app.Runtime)
	prepared.BaseProducer = base
	return prepared, err
}

func (h *Handler) runtimeDefaultBaseProducer(ctx context.Context, runtime string) (state.BaseImageProducer, error) {
	store, ok := h.store.(state.BaseImageProducerStore)
	if !ok {
		return state.BaseImageProducer{}, fmt.Errorf("imaged: runtime-default base producer store unavailable")
	}
	base, err := store.GetCurrentBaseImageProducer(ctx, state.RuntimeBaseKeyForArch(runtime, oci.ImageArchitecture))
	if err != nil {
		return state.BaseImageProducer{}, err
	}
	ref, err := h.runtimeDefaultBaseReference(ctx, runtime)
	if err != nil {
		return state.BaseImageProducer{}, err
	}
	if base.Input.SourceReference != ref || base.Input.GuestInitDigest == "" {
		return state.BaseImageProducer{}, fmt.Errorf("imaged: runtime-default base intent changed")
	}
	return h.checkedContainerBase(ctx, base)
}

func (h *Handler) runtimeDefaultBaseReference(ctx context.Context, runtime string) (string, error) {
	ref := h.deployBaseRefOverride
	var err error
	if ref == "" {
		ref, err = resolveDeployBaseRef(runtime, os.Getenv)
	}
	parsed, parseErr := oci.ParseReference(ref)
	if err != nil || parseErr != nil {
		return "", fmt.Errorf("imaged: runtime-default base reference unavailable")
	}
	if parsed.Digest != "" {
		return parsed.String(), nil
	}
	resolver, ok := h.oci.(oci.ImageResolver)
	if !ok {
		return "", fmt.Errorf("imaged: runtime-default base resolver unavailable")
	}
	resolved, err := resolver.ResolveImage(ctx, ref, nil)
	if err != nil {
		return "", fmt.Errorf("imaged: resolve runtime-default base: %w", err)
	}
	return resolved.SourceReference, nil
}
