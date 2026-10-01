package state

// adr: 387

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ociref"
)

// DeploymentRegistryRootfs binds a conversion's complete ext4 byte identity
// to one exact storage-issued publisher verification. It is a producer record;
// it does not establish a scan, descriptor/DiffID chain or native observation.
type DeploymentRegistryRootfs struct {
	ID, InputHash          string
	Input                  DeploymentRegistryRootfsInput
	PublishedAt, ExpiresAt time.Time
}
type DeploymentRegistryRootfsInput struct {
	ID                     string `json:"-"`
	RegistryVerificationID string `json:"registry_verification_id"`
	RegistryInputHash      string `json:"registry_input_hash"`
	AccountID              string `json:"account_id"`
	OrgID                  string `json:"org_id"`
	AppID                  string `json:"app_id"`
	DeploymentID           string `json:"deployment_id"`
	WorkloadName           string `json:"workload_name"`
	Scope                  string `json:"scope"`
	Kind                   string `json:"kind"`
	StorageKey             string `json:"storage_key"`
	RootfsPath             string `json:"rootfs_path"`
	ContentBytes           int64  `json:"content_bytes"`
	ArtifactDigest         string `json:"artifact_digest"`
	ArtifactBytes          int64  `json:"artifact_bytes"`
}
type DeploymentRegistryRootfsStore interface {
	PublishDeploymentRegistryRootfs(context.Context, DeploymentRegistryRootfsInput) (DeploymentRegistryRootfs, error)
	// Current selection is historical producer evidence. Expiry, publisher
	// revocation, scans and native consumption require separate validation.
	GetCurrentDeploymentRegistryRootfs(context.Context, string, string, string, string) (DeploymentRegistryRootfs, error)
}

func prepareRegistryRootfs(in DeploymentRegistryRootfsInput) (DeploymentRegistryRootfsInput, string, error) {
	if !validStandardResourceRead(in.ID, in.RegistryVerificationID) || !validStandardResourceRead(in.AccountID, in.AppID) ||
		!validStandardResourceRead(in.DeploymentID, in.DeploymentID) || in.OrgID != "" && !validStandardResourceRead(in.OrgID, in.OrgID) ||
		len(in.RegistryInputHash) != 64 || in.ArtifactBytes <= 0 || in.ContentBytes < 0 || in.StorageKey == "" || api.ValidateScope(in.Scope) != nil ||
		in.WorkloadName != "" && !api.ValidSidecarName(in.WorkloadName) {
		return in, "", ErrInvalidArgument
	}
	if err := ociref.ValidateDigest(in.ArtifactDigest); err != nil {
		return in, "", ErrInvalidArgument
	}
	switch in.Kind {
	case "app-layer", "full-rootfs":
		if in.WorkloadName != "" || in.RootfsPath == "" {
			return in, "", ErrInvalidArgument
		}
	case "sidecar-layer":
		if in.WorkloadName == "" || in.RootfsPath != "" {
			return in, "", ErrInvalidArgument
		}
	default:
		return in, "", ErrInvalidArgument
	}
	in.ID, in.RegistryVerificationID, in.AccountID, in.AppID, in.DeploymentID = canonicalStandardUUID(in.ID), canonicalStandardUUID(in.RegistryVerificationID), canonicalStandardUUID(in.AccountID), canonicalStandardUUID(in.AppID), canonicalStandardUUID(in.DeploymentID)
	in.OrgID = registryCanonicalOrg(in.OrgID)
	hash, err := standardReviewDigest(in)
	return in, hash, err
}
func checkRegistryRootfsParent(in DeploymentRegistryRootfsInput, parent DeploymentRegistryVerification, dep Deployment, now time.Time) error {
	p := parent.Input
	if in.RegistryVerificationID != parent.ID || in.RegistryInputHash != parent.InputHash || in.AccountID != p.AccountID || in.OrgID != p.OrgID ||
		in.AppID != p.AppID || in.DeploymentID != p.DeploymentID || in.WorkloadName != p.WorkloadName || dep.Scope != in.Scope || !parent.ExpiresAt.After(now) {
		return ErrApplicationStandardRuntimeStale
	}
	switch dep.Status {
	case DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting:
		return nil
	default:
		return ErrApplicationStandardRuntimeStale
	}
}
func registryRootfsMatchesMetadata(value DeploymentRegistryRootfs, dep Deployment, sidecar DeploymentSidecarLayer, parent DeploymentRegistryVerification) bool {
	in := value.Input
	if in.Scope != dep.Scope || in.RegistryInputHash != parent.InputHash {
		return false
	}
	if in.WorkloadName == "" {
		return dep.RootfsKey == in.StorageKey && dep.RootfsPath == in.RootfsPath && dep.RootfsBytes == in.ContentBytes
	}
	return sidecar.StorageKey == in.StorageKey && sidecar.Bytes == in.ContentBytes && sidecar.ContentDigest == parent.Input.SelectedReference
}
func validateRegistryRootfsStored(value DeploymentRegistryRootfs) error {
	in, hash, err := prepareRegistryRootfs(value.Input)
	if err != nil {
		return err
	}
	if in.ID != value.ID || hash != value.InputHash || value.PublishedAt.IsZero() || !value.ExpiresAt.After(value.PublishedAt) || value.ExpiresAt.Sub(value.PublishedAt) > api.ImageSignatureVerificationTTL {
		return fmt.Errorf("registry rootfs stored binding mismatch")
	}
	return nil
}
