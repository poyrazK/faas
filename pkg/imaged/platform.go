package imaged

// adr: 435

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/ociref"
	"github.com/onebox-faas/faas/pkg/state"
)

// preparedContainerWorkload retains the exact storage-issued verification
// identity belonging to this conversion, rather than selecting a later record.
type preparedContainerWorkload struct {
	oci.ImageResolution
	Verification state.DeploymentRegistryVerification
	LayerStart   int
	Layers       []imagechain.LayerConsumption
	BaseProducer state.BaseImageProducer `json:"-"`
}

// A signed source may be an index. Executable reads use its resolved child,
// while the deployment is pinned to the immutable source index for retries.
func (h *Handler) prepareContainerImage(ctx context.Context, app state.App, dep state.Deployment, auth *oci.BasicAuth) (string, string, error) {
	selected, err := h.prepareContainerWorkloadImage(ctx, app, dep, "", dep.ImageDigest, auth)
	return selected.Reference, selected.Digest, err
}

func (h *Handler) prepareContainerWorkloadImage(ctx context.Context, app state.App, dep state.Deployment, workload, ref string, auth *oci.BasicAuth) (preparedContainerWorkload, error) {
	resolved, err := h.resolveContainerWorkloadImage(ctx, ref, auth)
	prepared := preparedContainerWorkload{ImageResolution: resolved}
	verificationRef := ref
	_, resolverBacked := h.oci.(oci.ImageResolver)
	// Only the deployment's primary image has a mutable database column to
	// pin. Sidecar intent lives in its own declaration, so its signature must
	// remain bound to that declared reference. Legacy pullers do not return
	// a verifiable immutable source and keep their historical behavior.
	if err == nil && resolverBacked && workload == "" && !api.ValidDeploymentImage(ref) {
		pins, ok := h.store.(state.DeploymentImageReferenceStore)
		if !ok {
			err = fmt.Errorf("imaged: image reference pinning unavailable")
		} else if pinErr := pins.PinDeploymentImageReference(ctx, dep.ID, ref, resolved.SourceReference); pinErr != nil {
			err = fmt.Errorf("imaged: pin image source: %w", pinErr)
		} else {
			// The state record is checked against the now-pinned deployment
			// row. Bind it to that immutable source, not the mutable tag that
			// was replaced by the compare-and-set above.
			verificationRef = resolved.SourceReference
		}
	}
	if err == nil && (app.RequireSigned || app.SecurityPolicy.RequiresSignedImage()) {
		prepared.Verification, err = h.recordContainerWorkloadSignature(ctx, app, dep, workload, verificationRef, resolved, auth)
	}
	if err != nil {
		_ = h.markDeployFailed(ctx, dep.ID, err, "container image admission")
		return preparedContainerWorkload{}, err
	}
	h.log.Info("imaged: image platform resolved", "deployment", dep.ID, "workload", workload, "input_ref", ref,
		"source_ref", resolved.SourceReference, "image_ref", resolved.Reference, "image_digest", resolved.Digest)
	return prepared, nil
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
	if parsed, err := oci.ParseReference(ref); err == nil && ociref.ValidateDigest(digest) == nil {
		parsed.Tag, parsed.Digest = "", digest
		pinned = parsed.String()
	}
	return oci.ImageResolution{SourceReference: pinned, SourceDigest: digest, Reference: pinned, Digest: digest}, nil
}

func (h *Handler) recordContainerWorkloadSignature(ctx context.Context, app state.App, dep state.Deployment, workload, ref string, selected oci.ImageResolution, auth *oci.BasicAuth) (state.DeploymentRegistryVerification, error) {
	proof, err := h.verifyImageSignature(ctx, app, dep, selected.SourceReference, selected.SourceDigest, auth)
	if err != nil {
		return state.DeploymentRegistryVerification{}, err
	}
	store, ok := h.store.(state.DeploymentRegistryVerificationStore)
	if !ok {
		return state.DeploymentRegistryVerification{}, fmt.Errorf("imaged: durable registry verification store unavailable")
	}
	stored, err := store.RecordDeploymentRegistryVerification(ctx, state.DeploymentRegistryVerificationInput{
		ID: uuid.NewString(), AccountID: app.AccountID, OrgID: app.OrgID, AppID: app.ID, DeploymentID: dep.ID,
		WorkloadName: workload, ImageReference: ref, SourceReference: selected.SourceReference,
		SelectedReference: selected.Reference, SelectedDigest: selected.Digest, Proof: proof,
		ImageChain: selected.Evidence,
	})
	if err != nil {
		if errors.Is(err, cosign.ErrSignatureInvalid) {
			h.emitSignatureAudit(ctx, "app.signature_invalid", app, dep, selected.SourceReference, "")
		}
		return state.DeploymentRegistryVerification{}, fmt.Errorf("imaged: persist current publisher verification: %w", err)
	}
	h.log.Info("image publisher verification stored", "app", app.Slug, "deployment", dep.ID, "workload", workload,
		"verification", stored.ID, "signer", proof.PublisherName, "publisher_key_sha256", proof.PublisherKeySHA256,
		"subject_digest", proof.SubjectDigest, "expires_at", stored.ExpiresAt)
	return stored, nil
}
