package imaged

// adr: 387

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// A signed source may be an index. Every executable read must use its resolved
// child, while the persisted deployment keeps the customer's original intent.
func (h *Handler) prepareContainerImage(ctx context.Context, app state.App, dep state.Deployment, auth *oci.BasicAuth) (string, string, error) {
	selected, err := h.prepareContainerWorkloadImage(ctx, app, dep, "", dep.ImageDigest, auth)
	return selected.Reference, selected.Digest, err
}

func (h *Handler) prepareContainerWorkloadImage(ctx context.Context, app state.App, dep state.Deployment, workload, ref string, auth *oci.BasicAuth) (oci.ImageResolution, error) {
	resolved, err := h.resolveContainerWorkloadImage(ctx, ref, auth)
	if err == nil && (app.RequireSigned || app.SecurityPolicy.RequiresSignedImage()) {
		err = h.recordContainerWorkloadSignature(ctx, app, dep, workload, ref, resolved, auth)
	}
	if err != nil {
		_ = h.markDeployFailed(ctx, dep.ID, err, "container image admission")
		return oci.ImageResolution{}, err
	}
	h.log.Info("imaged: image platform resolved", "deployment", dep.ID, "workload", workload, "input_ref", ref,
		"source_ref", resolved.SourceReference, "image_ref", resolved.Reference, "image_digest", resolved.Digest)
	return resolved, nil
}

func (h *Handler) resolveContainerWorkloadImage(ctx context.Context, ref string, auth *oci.BasicAuth) (oci.ImageResolution, error) {
	if resolver, ok := h.oci.(oci.ImageResolver); ok {
		resolved, err := resolver.ResolveImage(ctx, ref, auth)
		if err != nil {
			return resolved, fmt.Errorf("imaged: resolve container image: %w", err)
		}
		return resolved, nil
	}
	digest, err := pullDigestWithAuth(ctx, h.oci, ref, auth)
	if err != nil {
		return oci.ImageResolution{}, fmt.Errorf("imaged: oci pull: %w", err)
	}
	pinned := ref
	// Preserve legacy unsigned puller behavior when no immutable digest is
	// available. Signed inputs require canonical evidence; production resolves
	// content with ImageResolver above.
	if parsed, err := oci.ParseReference(ref); err == nil {
		parsed.Tag, parsed.Digest = "", digest
		if _, err := oci.ParseReference(parsed.String()); err == nil {
			pinned = parsed.String()
		}
	}
	return oci.ImageResolution{SourceReference: pinned, SourceDigest: digest, Reference: pinned, Digest: digest}, nil
}

func (h *Handler) recordContainerWorkloadSignature(ctx context.Context, app state.App, dep state.Deployment, workload, ref string, selected oci.ImageResolution, auth *oci.BasicAuth) error {
	proof, err := h.verifyImageSignature(ctx, app, dep, selected.SourceReference, selected.SourceDigest, auth)
	if err != nil {
		return err
	}
	store, ok := h.store.(state.DeploymentRegistryVerificationStore)
	if !ok {
		return fmt.Errorf("imaged: durable registry verification store unavailable")
	}
	stored, err := store.RecordDeploymentRegistryVerification(ctx, state.DeploymentRegistryVerificationInput{
		ID: uuid.NewString(), AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID,
		WorkloadName: workload, ImageReference: ref, SourceReference: selected.SourceReference,
		SelectedReference: selected.Reference, SelectedDigest: selected.Digest, Proof: proof,
	})
	if err != nil {
		if errors.Is(err, cosign.ErrSignatureInvalid) {
			h.emitSignatureAudit(ctx, "app.signature_invalid", app, dep, selected.SourceReference, "")
		}
		return fmt.Errorf("imaged: persist current publisher verification: %w", err)
	}
	h.log.Info("image publisher verification stored", "app", app.Slug, "deployment", dep.ID, "workload", workload,
		"verification", stored.ID, "signer", proof.PublisherName, "publisher_key_sha256", proof.PublisherKeySHA256,
		"subject_digest", proof.SubjectDigest, "expires_at", stored.ExpiresAt)
	return nil
}
