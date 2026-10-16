package state

// adr: 435. Private source-build publisher evidence; no runtime authority.

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/buildpublisher"
	"time"
)

type BuildExportPublicationInput struct {
	ID     string                `json:"-"`
	Claims buildpublisher.Claims `json:"claims"`
	Proof  buildpublisher.Proof  `json:"proof"`
}

type BuildExportPublication struct {
	ID, InputHash         string
	Input                 BuildExportPublicationInput
	VerifiedAt, ExpiresAt time.Time
}

type BuildExportPublicationStore interface {
	RecordBuildExportPublication(context.Context, BuildExportPublicationInput) (BuildExportPublication, error)
	GetFreshBuildExportPublication(context.Context, string, string, string, string) (BuildExportPublication, error)
	// Historical presence only denies an unsigned fallback; it grants no trust.
	HasBuildExportPublication(context.Context, string, string, string) (bool, error)
}

// Retained proof bytes can be submitted for new cryptographic verification.
// This historical read grants no current signature, conversion or scan authority.
type BuildExportPublicationHistoryStore interface {
	GetLatestBuildExportPublication(context.Context, string, string, string, string) (BuildExportPublication, error)
}

func prepareBuildExportPublication(in BuildExportPublicationInput) (BuildExportPublicationInput, string, error) {
	if !validStandardResourceRead(in.ID, in.ID) {
		return in, "", ErrInvalidArgument
	}
	if _, err := buildpublisher.Payload(in.Claims); err != nil {
		return in, "", ErrInvalidArgument
	}
	if err := buildpublisher.CheckProof(in.Claims, in.Proof); err != nil {
		return in, "", ErrInvalidArgument
	}
	if len(in.Proof.Payload) == 0 || len(in.Proof.Payload) > api.BuildExportMaxPublicationPayloadBytes || len(in.Proof.Signature) == 0 || len(in.Proof.Signature) > api.BuildExportMaxPublicationSignatureBytes {
		return in, "", ErrInvalidArgument
	}
	in.ID = canonicalStandardUUID(in.ID)
	in.Proof = in.Proof.Clone()
	hash, err := standardReviewDigest(in)
	return in, hash, err
}

func validateBuildExportPublication(value BuildExportPublication) error {
	in, hash, err := prepareBuildExportPublication(value.Input)
	if err != nil {
		return err
	}
	if value.ID != in.ID || value.InputHash != hash || value.VerifiedAt.IsZero() || !value.ExpiresAt.After(value.VerifiedAt) || value.ExpiresAt.Sub(value.VerifiedAt) > api.BuildExportPublicationVerificationTTL {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func checkBuildExportOwner(c buildpublisher.Claims, app App, dep Deployment, build Build) error {
	if c.AccountID != canonicalStandardUUID(app.AccountID) || c.OrgID != registryCanonicalOrg(app.OrgID) || c.AppID != canonicalStandardUUID(app.ID) || c.DeploymentID != canonicalStandardUUID(dep.ID) || c.BuildID != canonicalStandardUUID(build.ID) || !sameStandardUUID(dep.AppID, app.ID) || !sameStandardUUID(build.DeploymentID, dep.ID) || app.Status == AppDeleted || c.Runtime != app.Runtime || c.ClaimStartedAt != build.StartedAt.UTC().Format(time.RFC3339Nano) {
		return ErrApplicationStandardRuntimeStale
	}
	switch dep.Kind {
	case DeploymentKindTarball, DeploymentKindDockerfile, DeploymentKindGitHub, DeploymentKindPreview:
	default:
		return ErrApplicationStandardRuntimeStale
	}
	switch dep.Status {
	case DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting, DeployLive, DeploySuperseded:
	default:
		return ErrApplicationStandardRuntimeStale
	}
	if dep.SourceSHA256 != "" && dep.SourceSHA256 != c.SourceSHA256 {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}
