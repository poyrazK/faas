package imaged

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// prepareContainerImage separates the signed source from the executable image.
// A signed index authenticates its digest-verified child; it does not require
// that child to have an independent signature. The stored customer reference
// remains intact while every subsequent app config/layer read uses childRef.
func (h *Handler) prepareContainerImage(ctx context.Context, app state.App, dep state.Deployment, auth *oci.BasicAuth) (childRef, digest string, err error) {
	ref := dep.ImageDigest
	if resolver, ok := h.oci.(oci.ImageResolver); ok {
		resolved, resolveErr := resolver.ResolveImage(ctx, ref, auth)
		if resolveErr != nil {
			_ = h.markDeployFailed(ctx, dep.ID, resolveErr, "oci platform resolution")
			return "", "", fmt.Errorf("imaged: resolve container image: %w", resolveErr)
		}
		if app.RequireSigned {
			if err := h.verifyImageSignature(ctx, app, dep, resolved.SourceReference); err != nil {
				return "", "", err
			}
		}
		h.log.Info("imaged: image platform resolved", "deployment", dep.ID, "input_ref", ref, "source_ref", resolved.SourceReference, "image_ref", resolved.Reference, "image_digest", resolved.Digest)
		return resolved.Reference, resolved.Digest, nil
	}
	if app.RequireSigned {
		if err := h.verifyImageSignature(ctx, app, dep, ref); err != nil {
			return "", "", err
		}
	}
	digest, err = pullDigestWithAuth(ctx, h.oci, ref, auth)
	if err != nil {
		_ = h.markDeployFailed(ctx, dep.ID, err, "oci pull failed")
		return "", "", fmt.Errorf("imaged: oci pull: %w", err)
	}
	return ref, digest, nil
}
