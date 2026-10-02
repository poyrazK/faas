package state

// adr: 430. Stable private byte inputs are separate from renewable approval.

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/ociref"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// DeploymentRuntimeArtifact names a complete producer blob, including ext4
// metadata and capacity. Base and per-app inputs remain separate drives.
// Its identity survives scan/signature renewal for the same immutable producer.
type DeploymentRuntimeArtifact struct {
	Kind           string `json:"kind"`
	WorkloadName   string `json:"workload_name"`
	ProducerID     string `json:"producer_id"`
	ProducerHash   string `json:"producer_hash"`
	StorageKey     string `json:"storage_key"`
	Digest         string `json:"digest"`
	Bytes          int64  `json:"bytes"`
	BaseProducerID string `json:"base_producer_id,omitempty"`
	BaseInputHash  string `json:"base_input_hash,omitempty"`
}

type deploymentRuntimeArtifactIdentity struct {
	Format       string                      `json:"format"`
	AccountID    string                      `json:"account_id"`
	OrgID        string                      `json:"org_id"`
	AppID        string                      `json:"app_id"`
	DeploymentID string                      `json:"deployment_id"`
	Scope        string                      `json:"scope"`
	Artifacts    []DeploymentRuntimeArtifact `json:"artifacts"`
}

// DeploymentRuntimeArtifactInputs captures current producers and renewable
// component evidence in one storage cut. InputHash excludes approval/scan IDs
// and clocks. This does not approve an overlaid runtime, read storage bytes,
// issue a native grant, acknowledge consumed bytes or advance observed adoption.
type DeploymentRuntimeArtifactInputs struct {
	deploymentRuntimeArtifactIdentity
	InputHash            string
	CheckedAt, ExpiresAt time.Time
}

type DeploymentRuntimeArtifactInputStore interface {
	GetFreshDeploymentRuntimeArtifactInputs(context.Context, string, string, string) (DeploymentRuntimeArtifactInputs, error)
}

func runtimeArtifactFromRootfs(value DeploymentRegistryRootfs) DeploymentRuntimeArtifact {
	in := value.Input
	return DeploymentRuntimeArtifact{Kind: in.Kind, WorkloadName: in.WorkloadName, ProducerID: value.ID, ProducerHash: value.InputHash, StorageKey: in.StorageKey, Digest: in.ArtifactDigest, Bytes: in.ArtifactBytes, BaseProducerID: in.BaseProducerID, BaseInputHash: in.BaseInputHash}
}

func runtimeArtifactFromBaseScan(value BaseImageScan) DeploymentRuntimeArtifact {
	in := value.Input
	return DeploymentRuntimeArtifact{Kind: "base-image", ProducerID: in.BaseProducerID, ProducerHash: in.BaseInputHash, StorageKey: in.Artifact.StorageKey, Digest: in.Artifact.Digest, Bytes: in.Artifact.Bytes}
}

func deploymentRuntimeArtifactInputs(evidence DeploymentArtifactScanEvidence) (DeploymentRuntimeArtifactInputs, error) {
	if len(evidence.Components) == 0 || len(evidence.Components) > api.SidecarCapMax+1 || len(evidence.Artifacts) != len(evidence.Components)+len(evidence.Bases) || evidence.CheckedAt.IsZero() || !evidence.ExpiresAt.After(evidence.CheckedAt) {
		return DeploymentRuntimeArtifactInputs{}, ErrApplicationStandardRuntimeStale
	}
	in := evidence.Components[0].Input
	identity, hash, err := prepareRuntimeArtifactIdentity(deploymentRuntimeArtifactIdentity{Format: "gregale.runtime-artifact-input.v1", AccountID: in.AccountID, OrgID: in.OrgID, AppID: in.AppID, DeploymentID: in.DeploymentID, Scope: in.Scope, Artifacts: evidence.Artifacts})
	if err != nil {
		return DeploymentRuntimeArtifactInputs{}, err
	}
	if err := checkRuntimeArtifactMembership(identity, evidence); err != nil {
		return DeploymentRuntimeArtifactInputs{}, err
	}
	expires, err := runtimeArtifactInputDeadline(evidence)
	if err != nil {
		return DeploymentRuntimeArtifactInputs{}, err
	}
	return DeploymentRuntimeArtifactInputs{deploymentRuntimeArtifactIdentity: identity, InputHash: hash, CheckedAt: evidence.CheckedAt, ExpiresAt: expires}, nil
}

func runtimeArtifactInputDeadline(evidence DeploymentArtifactScanEvidence) (time.Time, error) {
	expires := evidence.ExpiresAt
	capReport := func(report *api.ScanResult) error {
		if report == nil {
			return ErrApplicationStandardRuntimeStale
		}
		built, err := time.Parse(time.RFC3339Nano, report.ScannerDBBuiltAt)
		if err != nil || built.After(evidence.CheckedAt) {
			return ErrApplicationStandardRuntimeStale
		}
		if deadline := built.Add(api.ApplicationStandardScannerDBMaxAge); deadline.Before(expires) {
			expires = deadline
		}
		return nil
	}
	for _, component := range evidence.Components {
		if err := capReport(component.Input.Report); err != nil {
			return time.Time{}, err
		}
	}
	for _, base := range evidence.Bases {
		if err := capReport(base.Input.Report); err != nil {
			return time.Time{}, err
		}
	}
	if !expires.After(evidence.CheckedAt) {
		return time.Time{}, ErrApplicationStandardRuntimeStale
	}
	return expires, nil
}

func checkRuntimeArtifactIdentity(in deploymentRuntimeArtifactIdentity) error {
	if !validStandardResourceRead(in.AccountID, in.AppID) || !validStandardResourceRead(in.DeploymentID, in.DeploymentID) || in.OrgID != "" && !validStandardResourceRead(in.OrgID, in.OrgID) || api.ValidateScope(in.Scope) != nil {
		return ErrApplicationStandardRuntimeStale
	}
	for _, a := range in.Artifacts {
		if !validStandardResourceRead(a.ProducerID, a.ProducerID) || !runtimeadmission.ValidHash(a.ProducerHash) || a.StorageKey == "" || a.Bytes <= 0 || ociref.ValidateDigest(a.Digest) != nil {
			return ErrApplicationStandardRuntimeStale
		}
		if a.Kind == "base-image" {
			if a.WorkloadName != "" || a.BaseProducerID != "" || a.BaseInputHash != "" || !(imagechain.BaseArtifact{StorageKey: a.StorageKey, Digest: a.Digest, Bytes: a.Bytes}).Valid() {
				return ErrApplicationStandardRuntimeStale
			}
		} else if a.WorkloadName == "" {
			if a.Kind != "app-layer" && a.Kind != "full-rootfs" {
				return ErrApplicationStandardRuntimeStale
			}
		} else if a.Kind != "sidecar-layer" || !api.ValidSidecarName(a.WorkloadName) || a.BaseProducerID != "" || a.BaseInputHash != "" {
			return ErrApplicationStandardRuntimeStale
		}
		if a.BaseProducerID != "" && (a.Kind != "app-layer" || !validStandardResourceRead(a.BaseProducerID, a.BaseProducerID) || !runtimeadmission.ValidHash(a.BaseInputHash)) || a.BaseProducerID == "" && a.BaseInputHash != "" {
			return ErrApplicationStandardRuntimeStale
		}
	}
	return nil
}

func checkRuntimeArtifactMembership(identity deploymentRuntimeArtifactIdentity, evidence DeploymentArtifactScanEvidence) error {
	roots, bases := map[string]DeploymentRuntimeArtifact{}, map[string]DeploymentRuntimeArtifact{}
	for _, a := range identity.Artifacts {
		if a.Kind == "base-image" {
			if _, duplicate := bases[a.ProducerID]; duplicate {
				return ErrApplicationStandardRuntimeStale
			}
			bases[a.ProducerID] = a
		} else {
			if _, duplicate := roots[a.WorkloadName]; duplicate {
				return ErrApplicationStandardRuntimeStale
			}
			roots[a.WorkloadName] = a
		}
	}
	if _, main := roots[""]; !main || len(roots) != len(evidence.Components) || len(bases) != len(evidence.Bases) {
		return ErrApplicationStandardRuntimeStale
	}
	used := map[string]bool{}
	for _, scan := range evidence.Components {
		a, ok := roots[scan.Input.WorkloadName]
		if !ok || !runtimeArtifactMatchesComponent(identity, a, scan.Input) {
			return ErrApplicationStandardRuntimeStale
		}
		delete(roots, scan.Input.WorkloadName)
		if a.BaseProducerID != "" {
			b, ok := bases[a.BaseProducerID]
			if !ok || b.ProducerHash != a.BaseInputHash || b.StorageKey == a.StorageKey {
				return ErrApplicationStandardRuntimeStale
			}
			used[b.ProducerID] = true
		}
	}
	for _, scan := range evidence.Bases {
		if a, ok := bases[scan.Input.BaseProducerID]; !ok || !used[a.ProducerID] || a != runtimeArtifactFromBaseScan(scan) {
			return ErrApplicationStandardRuntimeStale
		}
		delete(bases, scan.Input.BaseProducerID)
	}
	if len(roots) != 0 || len(bases) != 0 {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func runtimeArtifactMatchesComponent(owner deploymentRuntimeArtifactIdentity, artifact DeploymentRuntimeArtifact, scan DeploymentArtifactScanInput) bool {
	return scan.AccountID == owner.AccountID && scan.OrgID == owner.OrgID && scan.AppID == owner.AppID && scan.DeploymentID == owner.DeploymentID && scan.Scope == owner.Scope && scan.RootfsProducerID == artifact.ProducerID && scan.RootfsInputHash == artifact.ProducerHash && scan.ArtifactDigest == artifact.Digest && scan.ArtifactBytes == artifact.Bytes
}
