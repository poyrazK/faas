package state

// adr: 393. Producer identity is immutable; approval remains a fresh check.

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func prepareRuntimeArtifactIdentity(in deploymentRuntimeArtifactIdentity) (deploymentRuntimeArtifactIdentity, string, error) {
	in.Artifacts = slices.Clone(in.Artifacts)
	if in.Format != "gregale.runtime-artifact-input.v1" || len(in.Artifacts) == 0 || len(in.Artifacts) > api.SidecarCapMax+2 {
		return in, "", ErrApplicationStandardRuntimeStale
	}
	if err := checkRuntimeArtifactIdentity(in); err != nil {
		return in, "", err
	}
	if err := checkRuntimeArtifactProducerSet(in.Artifacts); err != nil {
		return in, "", err
	}
	slices.SortFunc(in.Artifacts, func(a, b DeploymentRuntimeArtifact) int {
		if a.Kind == "base-image" && b.Kind != "base-image" {
			return -1
		}
		if b.Kind == "base-image" && a.Kind != "base-image" {
			return 1
		}
		return strings.Compare(a.WorkloadName, b.WorkloadName)
	})
	hash, err := standardReviewDigest(in)
	return in, hash, err
}

func checkRuntimeArtifactProducerSet(artifacts []DeploymentRuntimeArtifact) error {
	roots, bases := map[string]DeploymentRuntimeArtifact{}, map[string]DeploymentRuntimeArtifact{}
	for _, artifact := range artifacts {
		set, key := roots, artifact.WorkloadName
		if artifact.Kind == "base-image" {
			set, key = bases, artifact.ProducerID
		}
		if _, duplicate := set[key]; duplicate {
			return ErrApplicationStandardRuntimeStale
		}
		set[key] = artifact
	}
	if _, main := roots[""]; !main {
		return ErrApplicationStandardRuntimeStale
	}
	used := map[string]bool{}
	for _, artifact := range roots {
		if artifact.BaseProducerID == "" {
			continue
		}
		base, ok := bases[artifact.BaseProducerID]
		if !ok || base.ProducerHash != artifact.BaseInputHash || base.StorageKey == artifact.StorageKey {
			return ErrApplicationStandardRuntimeStale
		}
		used[base.ProducerID] = true
	}
	if len(used) != len(bases) {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func runtimeArtifactFromBaseProducer(value BaseImageProducer) DeploymentRuntimeArtifact {
	in := value.Input.Artifact
	return DeploymentRuntimeArtifact{Kind: "base-image", ProducerID: value.ID, ProducerHash: value.InputHash, StorageKey: in.StorageKey, Digest: in.Digest, Bytes: in.Bytes}
}

func decodeRuntimeArtifactCapture(raw json.RawMessage, accountID, orgID, appID, depID, scope string) ([]DeploymentRuntimeArtifact, string, error) {
	if len(raw) == 0 {
		return nil, "", nil // Preserve captures without private producer lineage.
	}
	var in deploymentRuntimeArtifactIdentity
	if json.Unmarshal(raw, &in) != nil {
		return nil, "", ErrApplicationStandardRuntimeStale
	}
	in, hash, err := prepareRuntimeArtifactIdentity(in)
	if err != nil {
		return nil, "", err
	}
	if !sameStandardUUID(in.AccountID, accountID) || registryCanonicalOrg(in.OrgID) != registryCanonicalOrg(orgID) || !sameStandardUUID(in.AppID, appID) || !sameStandardUUID(in.DeploymentID, depID) || in.Scope != scope {
		return nil, "", ErrApplicationStandardRuntimeStale
	}
	return in.Artifacts, hash, nil
}
