package state

// adr: 435. Source conversion identity is stable across exact-claim renewals.

import "time"

func runtimeArtifactFromSourceRootfs(value SourceBuildRootfs) DeploymentRuntimeArtifact {
	in := value.Input
	return DeploymentRuntimeArtifact{Kind: in.Kind, ProducerID: value.ID, ProducerHash: value.InputHash,
		StorageKey: in.StorageKey, Digest: in.ArtifactDigest, Bytes: in.ArtifactBytes,
		BaseProducerID: in.BaseProducerID, BaseInputHash: in.BaseInputHash}
}

func sourceRuntimeProducerSelection(root SourceBuildRootfs, proof BuildExportPublication) runtimeProducerSelection {
	in := root.Input
	identity := deploymentRuntimeArtifactIdentity{Format: "gregale.runtime-artifact-input.v1", AccountID: in.AccountID,
		OrgID: in.OrgID, AppID: in.AppID, DeploymentID: in.DeploymentID, Scope: in.Scope}
	return runtimeProducerSelection{identity, runtimeArtifactFromSourceRootfs(root),
		runtimeProducerLease{root.PublishedAt, proof.VerifiedAt, proof.ExpiresAt}}
}

func checkSourceRuntimeIntent(in SourceBuildRootfsInput, intent sourceBuildRootfsIntent) error {
	kind, runtime := SourceBuildRootfsKind(App{Type: intent.Type, Runtime: intent.Runtime}, Deployment{Handler: intent.Handler})
	hash, err := hashSourceBuildRootfsIntent(intent)
	if err != nil {
		return err
	}
	if in.Kind != kind || in.Runtime != runtime || in.IntentHash != hash || in.Scope != intent.Scope {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func checkSourceRuntimeApproval(root SourceBuildRootfs, origin, proof BuildExportPublication, now time.Time) error {
	if validateSourceBuildRootfs(root) != nil || validateBuildExportPublication(origin) != nil || validateBuildExportPublication(proof) != nil ||
		root.Input.PublicationID != origin.ID || root.Input.PublicationHash != origin.InputHash || proof.Input.Claims != origin.Input.Claims ||
		root.PublishedAt.Before(origin.VerifiedAt) || root.ExpiresAt.After(origin.ExpiresAt) || root.PublishedAt.After(now) ||
		proof.VerifiedAt.After(now) || !proof.ExpiresAt.After(now) {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}
