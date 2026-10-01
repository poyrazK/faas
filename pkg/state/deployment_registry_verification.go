package state

// adr: 393

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
	"github.com/onebox-faas/faas/pkg/ociref"
)

// DeploymentRegistryVerification records verified registry source bytes and
// the resolver-selected child. It is private historical evidence, not authority
// for a converted rootfs, scan, native boot or observed standard adoption.
type DeploymentRegistryVerification struct {
	ID, InputHash         string
	Input                 DeploymentRegistryVerificationInput
	VerifiedAt, ExpiresAt time.Time
}

type DeploymentRegistryVerificationInput struct {
	ID                string                             `json:"-"`
	AccountID         string                             `json:"account_id"`
	OrgID             string                             `json:"org_id"`
	AppID             string                             `json:"app_id"`
	DeploymentID      string                             `json:"deployment_id"`
	WorkloadName      string                             `json:"workload_name"`
	ImageReference    string                             `json:"image_reference"`
	SourceReference   string                             `json:"source_reference"`
	SelectedReference string                             `json:"selected_reference"`
	SelectedDigest    string                             `json:"selected_digest"`
	Proof             imagepublisher.ImageSignatureProof `json:"proof"`
	ImageChain        *imagechain.Evidence               `json:"image_chain,omitempty"`
}

type DeploymentRegistryVerificationStore interface {
	RecordDeploymentRegistryVerification(context.Context, DeploymentRegistryVerificationInput) (DeploymentRegistryVerification, error)
	// This returns historical evidence even after expiry or publisher revocation.
	// A consumer must reverify current approval and bind the resulting rootfs.
	GetLatestDeploymentRegistryVerification(context.Context, string, string, string, string) (DeploymentRegistryVerification, error)
	// Exact retained origin, scoped to the current owner and workload intent.
	// Historical expiry or key revocation does not prevent retrieval.
	GetDeploymentRegistryVerificationByID(context.Context, string, string, string, string) (DeploymentRegistryVerification, error)
}

func validateRegistryVerification(value DeploymentRegistryVerification) error {
	in, hash, err := prepareRegistryVerification(value.Input)
	if err != nil {
		return err
	}
	if value.ID != in.ID || value.InputHash != hash || value.VerifiedAt.IsZero() || !value.ExpiresAt.After(value.VerifiedAt) || value.ExpiresAt.Sub(value.VerifiedAt) > api.ImageSignatureVerificationTTL || "sha256:"+standardReviewBytesDigest(in.Proof.Evidence.Payload) != in.Proof.PayloadDigest || "sha256:"+standardReviewBytesDigest(in.Proof.Evidence.Signature) != in.Proof.SignatureDigest {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func prepareRegistryVerification(in DeploymentRegistryVerificationInput) (DeploymentRegistryVerificationInput, string, error) {
	if !validStandardResourceRead(in.ID, in.ID) || !validStandardResourceRead(in.AccountID, in.AppID) ||
		!validStandardResourceRead(in.DeploymentID, in.DeploymentID) || in.OrgID != "" && !validStandardResourceRead(in.OrgID, in.OrgID) {
		return in, "", ErrInvalidArgument
	}
	if in.WorkloadName != "" && !api.ValidSidecarName(in.WorkloadName) {
		return in, "", ErrInvalidArgument
	}
	original, err := ociref.ParseReference(in.ImageReference)
	if err != nil {
		return in, "", ErrInvalidArgument
	}
	for _, pair := range [][2]string{{in.SourceReference, in.Proof.SubjectDigest}, {in.SelectedReference, in.SelectedDigest}} {
		pinned, err := ociref.ParseReference(pair[0])
		if err != nil || pinned.Tag != "" || pinned.Digest == "" || pinned.Digest != pair[1] ||
			pinned.Registry != original.Registry || pinned.Repository != original.Repository || pair[0] != pinned.String() {
			return in, "", ErrInvalidArgument
		}
	}
	if original.Digest != "" && original.Digest != in.Proof.SubjectDigest || in.Proof.Evidence == nil ||
		len(in.Proof.Evidence.Payload) == 0 || int64(len(in.Proof.Evidence.Payload)) > api.ImageSignatureMaxPayloadBytes ||
		len(in.Proof.Evidence.Signature) == 0 || len(in.Proof.Evidence.Signature) > api.ImageSignatureMaxDERBytes {
		return in, "", ErrInvalidArgument
	}
	in.ID, in.AccountID, in.AppID, in.DeploymentID = canonicalStandardUUID(in.ID), canonicalStandardUUID(in.AccountID), canonicalStandardUUID(in.AppID), canonicalStandardUUID(in.DeploymentID)
	if in.OrgID != "" {
		in.OrgID = canonicalStandardUUID(in.OrgID)
	}
	in = cloneRegistryVerificationInput(in)
	if in.ImageChain != nil {
		if _, err := imagechain.Validate(in.ImageChain, in.Proof.SubjectDigest, in.SelectedDigest); err != nil {
			return in, "", ErrInvalidArgument
		}
	}
	hash, err := standardReviewDigest(in)
	return in, hash, err
}

func cloneRegistryVerificationInput(in DeploymentRegistryVerificationInput) DeploymentRegistryVerificationInput {
	in.ImageChain = in.ImageChain.Clone()
	if in.Proof.Evidence != nil {
		in.Proof.Evidence = &imagepublisher.ImageSignatureEvidence{Payload: append([]byte(nil), in.Proof.Evidence.Payload...), Signature: append([]byte(nil), in.Proof.Evidence.Signature...)}
	}
	return in
}

func registryWorkloadReference(dep Deployment, workload string) (string, error) {
	if workload == "" {
		if dep.Kind != DeploymentKindImage {
			return "", ErrInvalidArgument
		}
		return dep.ImageDigest, nil
	}
	var sidecars api.Sidecars
	if err := json.Unmarshal(dep.Sidecars, &sidecars); err != nil {
		return "", ErrInvalidArgument
	}
	ref := ""
	for _, sc := range sidecars {
		if sc.Name == workload {
			if ref != "" {
				return "", ErrInvalidArgument
			}
			ref = sc.Image
		}
	}
	if ref == "" {
		return "", ErrInvalidArgument
	}
	return ref, nil
}

func checkRegistryVerificationOwner(in DeploymentRegistryVerificationInput, app App, dep Deployment) error {
	if !sameStandardUUID(app.ID, in.AppID) || !sameStandardUUID(app.AccountID, in.AccountID) ||
		registryCanonicalOrg(app.OrgID) != in.OrgID || !sameStandardUUID(dep.AppID, in.AppID) || !sameStandardUUID(dep.ID, in.DeploymentID) || app.Status == AppDeleted {
		return ErrApplicationStandardRuntimeStale
	}
	switch dep.Status {
	case DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting, DeployLive, DeploySuperseded:
	default:
		return ErrApplicationStandardRuntimeStale
	}
	ref, err := registryWorkloadReference(dep, in.WorkloadName)
	if err != nil || ref != in.ImageReference {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func verifyRegistryCurrentKey(in DeploymentRegistryVerificationInput, key []byte) error {
	if err := imagepublisher.ReverifyImageSignatureProof(in.Proof, key); err != nil {
		return fmt.Errorf("current registry publisher verification: %w", err)
	}
	return nil
}

func registryCanonicalOrg(id string) string {
	if id == "" {
		return ""
	}
	return canonicalStandardUUID(id)
}
