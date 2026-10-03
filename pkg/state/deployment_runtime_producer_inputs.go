package state

// adr: 435. Scanner bootstrap is distinct from renewable scan approval.

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// DeploymentRuntimeProducerInputs binds the complete, currently selected
// producer set to current publisher signatures, without requiring any scan.
// It does not approve findings, read storage bytes or confer native authority.
type DeploymentRuntimeProducerInputs struct {
	deploymentRuntimeArtifactIdentity
	InputHash            string
	CheckedAt, ExpiresAt time.Time
}

type DeploymentRuntimeProducerInputStore interface {
	GetFreshDeploymentRuntimeProducerInputs(context.Context, string, string, string) (DeploymentRuntimeProducerInputs, error)
}

// Only parent fields are used by the shared producer/signature fence. No scan
// record is synthesized or published, and no report/scan lease is consulted.
func runtimeProducerParentInput(root DeploymentRegistryRootfs, proof DeploymentRegistryVerification) DeploymentArtifactScanInput {
	in := root.Input
	return DeploymentArtifactScanInput{RootfsProducerID: root.ID, RootfsInputHash: root.InputHash,
		RegistryVerificationID: proof.ID, RegistryInputHash: proof.InputHash,
		AccountID: in.AccountID, OrgID: in.OrgID, AppID: in.AppID, DeploymentID: in.DeploymentID,
		WorkloadName: in.WorkloadName, Scope: in.Scope, ImageReference: proof.Input.ImageReference,
		ArtifactDigest: in.ArtifactDigest, ArtifactBytes: in.ArtifactBytes}
}

func runtimeProducerIdentity(root DeploymentRegistryRootfs) deploymentRuntimeArtifactIdentity {
	in := root.Input
	return deploymentRuntimeArtifactIdentity{Format: "gregale.runtime-artifact-input.v1", AccountID: in.AccountID,
		OrgID: in.OrgID, AppID: in.AppID, DeploymentID: in.DeploymentID, Scope: in.Scope}
}

func finishRuntimeProducerInputs(identity deploymentRuntimeArtifactIdentity, parents []artifactScanParents, now time.Time) (DeploymentRuntimeProducerInputs, error) {
	identity, hash, err := prepareRuntimeArtifactIdentity(identity)
	if err != nil {
		return DeploymentRuntimeProducerInputs{}, err
	}
	if now.IsZero() || len(parents) == 0 {
		return DeploymentRuntimeProducerInputs{}, ErrApplicationStandardRuntimeStale
	}
	var expires time.Time
	sources := make([]runtimeadmission.ArtifactSource, 0, len(identity.Artifacts))
	for _, artifact := range identity.Artifacts {
		sources = append(sources, runtimeadmission.ArtifactSource{Kind: artifact.Kind, WorkloadName: artifact.WorkloadName,
			StorageKey: artifact.StorageKey, Digest: artifact.Digest, Bytes: artifact.Bytes})
	}
	if _, err := runtimeadmission.HashArtifactSources(sources); err != nil {
		return DeploymentRuntimeProducerInputs{}, ErrApplicationStandardRuntimeStale
	}
	for _, parent := range parents {
		proof := parent.Approval
		if proof.VerifiedAt.After(now) || parent.Rootfs.PublishedAt.After(now) || !proof.ExpiresAt.After(now) {
			return DeploymentRuntimeProducerInputs{}, ErrApplicationStandardRuntimeStale
		}
		if expires.IsZero() || proof.ExpiresAt.Before(expires) {
			expires = proof.ExpiresAt
		}
	}
	return DeploymentRuntimeProducerInputs{deploymentRuntimeArtifactIdentity: identity, InputHash: hash, CheckedAt: now, ExpiresAt: expires}, nil
}
