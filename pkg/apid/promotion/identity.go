package promotion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func Change(app state.App, source state.Deployment, hasSource bool, target state.Deployment, hasTarget bool) api.ProjectEnvironmentPromotionChange {
	change := api.ProjectEnvironmentPromotionChange{
		WorkloadSlug: app.Slug, WorkloadName: app.WorkloadName,
	}
	if hasSource {
		change.SourceDeploymentID = source.ID
		change.SourceBuildID = source.BuildID
		change.SourceRevisionKind, change.SourceRevision = deploymentRevision(source)
	}
	if hasTarget {
		change.TargetDeploymentID = target.ID
		change.TargetBuildID = target.BuildID
		change.TargetRevisionKind, change.TargetRevision = deploymentRevision(target)
	}
	switch {
	case !hasSource:
		change.Kind = "source_missing"
	case !hasTarget:
		change.Kind = "create"
	case change.SourceRevisionKind == change.TargetRevisionKind && change.SourceRevision == change.TargetRevision:
		change.Kind = "unchanged"
	default:
		change.Kind = "update"
	}
	return change
}

func deploymentRevision(deployment state.Deployment) (kind, value string) {
	switch {
	case deployment.SourceSHA256 != "":
		return "source_sha256", deployment.SourceSHA256
	case deployment.ImageDigest != "":
		return "image_digest", deployment.ImageDigest
	case deployment.CommitSHA != "":
		return "commit_sha", deployment.CommitSHA
	default:
		return "deployment_id", deployment.ID
	}
}

func Hash(
	projectSlug, fromEnvironment, toEnvironment string,
	configDiff api.ProjectEnvironmentConfigDiffResponse,
	syncConfig bool,
	fromReleaseSetID, toReleaseSetID, sourceQualificationID string,
	changes []api.ProjectEnvironmentPromotionChange,
	featureFlagsHashes ...string,
) (string, error) {
	var sourceFlagsHash, targetFlagsHash string
	if len(featureFlagsHashes) != 0 {
		if len(featureFlagsHashes) != 2 {
			return "", state.ErrInvalidArgument
		}
		sourceFlagsHash, targetFlagsHash = featureFlagsHashes[0], featureFlagsHashes[1]
	}
	identity := struct {
		ProjectSlug            string                                  `json:"project_slug"`
		FromEnvironment        string                                  `json:"from_environment"`
		ToEnvironment          string                                  `json:"to_environment"`
		FromConfigHash         string                                  `json:"from_config_hash"`
		ToConfigHash           string                                  `json:"to_config_hash"`
		SyncConfig             bool                                    `json:"sync_config,omitempty"`
		FromReleaseSetID       string                                  `json:"from_release_set_id,omitempty"`
		ToReleaseSetID         string                                  `json:"to_release_set_id,omitempty"`
		SourceQualificationID  string                                  `json:"source_qualification_id,omitempty"`
		Changes                []api.ProjectEnvironmentPromotionChange `json:"changes"`
		SourceFeatureFlagsHash string                                  `json:"source_feature_flags_hash,omitempty"`
		TargetFeatureFlagsHash string                                  `json:"target_feature_flags_hash,omitempty"`
	}{
		ProjectSlug: projectSlug, FromEnvironment: fromEnvironment, ToEnvironment: toEnvironment,
		FromConfigHash: configDiff.FromHash, ToConfigHash: configDiff.ToHash, SyncConfig: syncConfig,
		FromReleaseSetID: fromReleaseSetID, ToReleaseSetID: toReleaseSetID,
		SourceQualificationID: sourceQualificationID, Changes: changes,
		SourceFeatureFlagsHash: sourceFlagsHash, TargetFeatureFlagsHash: targetFlagsHash,
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func ApplySnapshotChange(view *state.ProjectEnvironmentPromotionReleaseConfiguration, change api.ProjectEnvironmentPromotionChange) api.ProjectEnvironmentPromotionChange {
	if view == nil {
		return change
	}
	change.ReleaseConfigurationHash = view.Hash
	change.EnvironmentChanges = view.Changes[change.WorkloadSlug]
	name := change.WorkloadName
	if name == "" {
		name = change.WorkloadSlug
	}
	change.SecretChanges = SecretChanges(view.Secrets, name)
	if change.Kind == "unchanged" && (len(change.EnvironmentChanges) != 0 || len(change.SecretChanges) != 0) {
		change.Kind = "update"
	}
	return change
}
