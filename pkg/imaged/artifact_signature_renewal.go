package imaged

// adr: 430

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// The source/index bytes and selected child are retained by the exact original
// conversion. Renewal reads attachments for that digest, never a mutable tag.
type retainedSignaturePuller struct{ *ociImageSignaturePuller }

func (p *retainedSignaturePuller) ResolveDigest(ctx context.Context, ref string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	r, err := oci.ParseReference(ref)
	if err != nil || r.Digest == "" || r.Tag != "" {
		return "", cosign.ErrSignatureInvalid
	}
	return r.Digest, nil
}

func (h *Handler) renewProducedSignature(ctx context.Context, app state.App, dep state.Deployment, root state.DeploymentRegistryRootfs) (state.DeploymentRegistryVerification, error) {
	store, ok := h.store.(state.DeploymentRegistryVerificationStore)
	if !ok {
		return state.DeploymentRegistryVerification{}, fmt.Errorf("imaged: registry evidence store unavailable")
	}
	origin, err := store.GetDeploymentRegistryVerificationByID(ctx, app.AccountID, app.ID, dep.ID, root.Input.RegistryVerificationID)
	if err != nil {
		return state.DeploymentRegistryVerification{}, err
	}
	if origin.InputHash != root.Input.RegistryInputHash || origin.Input.WorkloadName != root.Input.WorkloadName || origin.Input.ImageChain == nil {
		return state.DeploymentRegistryVerification{}, state.ErrApplicationStandardRuntimeStale
	}
	if _, err := imagechain.Validate(origin.Input.ImageChain, origin.Input.Proof.SubjectDigest, origin.Input.SelectedDigest); err != nil {
		return state.DeploymentRegistryVerification{}, err
	}
	in := origin.Input
	in.ID = uuid.NewString()
	// This is a new cryptographic verification under the current stored key,
	// not a lease extension. Retained immutable bytes keep a registry outage
	// from preventing renewal when the approved signer remains valid.
	value, err := store.RecordDeploymentRegistryVerification(ctx, in)
	if err == nil || !errors.Is(err, cosign.ErrSignatureInvalid) {
		return value, err
	}
	proof, err := h.fetchRetainedSourceSignature(ctx, app, origin)
	if err != nil {
		return state.DeploymentRegistryVerification{}, err
	}
	in.Proof = proof
	return store.RecordDeploymentRegistryVerification(ctx, in)
}

func (h *Handler) fetchRetainedSourceSignature(ctx context.Context, app state.App, origin state.DeploymentRegistryVerification) (cosign.ImageSignatureProof, error) {
	pubs, err := h.imageSignaturePublishers(app)
	if err != nil {
		return cosign.ImageSignatureProof{}, err
	}
	ref, err := oci.ParseReference(origin.Input.SourceReference)
	if err != nil {
		return cosign.ImageSignatureProof{}, err
	}
	auth, err := h.resolveRegistryAuth(ctx, app, ref.APIHost())
	if err != nil {
		return cosign.ImageSignatureProof{}, err
	}
	if auth != nil {
		defer func() { auth.Password = "" }()
	}
	return cosign.VerifyImageSignatureAttachments(ctx, &retainedSignaturePuller{&ociImageSignaturePuller{oci: h.oci, auth: auth}}, origin.Input.SourceReference, pubs)
}

func (h *Handler) renewProducedDeploymentSignatures(ctx context.Context, app state.App, dep state.Deployment) (bool, error) {
	store, ok := h.store.(state.DeploymentRegistryRootfsStore)
	if !ok {
		return false, nil
	}
	main, err := store.GetCurrentDeploymentRegistryRootfs(ctx, app.AccountID, app.ID, dep.ID, "")
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if len(main.Input.Layers) == 0 && main.Input.BaseProducerID == "" {
		return false, nil // old unproved conversions gain no renewal authority
	}
	if _, err := h.renewProducedSignature(ctx, app, dep, main); err != nil {
		return true, err
	}
	var sidecars api.Sidecars
	if len(dep.Sidecars) > 0 && json.Unmarshal(dep.Sidecars, &sidecars) != nil {
		return true, state.ErrInvalidArgument
	}
	for _, sc := range sidecars {
		if sc.Image == "" {
			continue
		}
		root, err := store.GetCurrentDeploymentRegistryRootfs(ctx, app.AccountID, app.ID, dep.ID, sc.Name)
		if err != nil {
			return true, err
		}
		if _, err := h.renewProducedSignature(ctx, app, dep, root); err != nil {
			return true, err
		}
	}
	return true, nil
}
