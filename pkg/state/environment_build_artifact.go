package state

// adr: 1544. A borrowed layer keeps its producer's identity and signature scope.

import (
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// EnvironmentWorkloadBuiltArtifact is a private materialization receipt. It
// carries code provenance, never another environment's runtime or snapshots.
type EnvironmentWorkloadBuiltArtifact struct {
	ID           string `json:"id"`
	InputHash    string `json:"input_hash"`
	DeploymentID string `json:"deployment_id"`
	BuildID      string `json:"build_id"`
	CommitSHA    string `json:"commit_sha"`
	Digest       string `json:"digest"`
	RootfsPath   string `json:"rootfs_path"`
	StorageKey   string `json:"storage_key"`
	Bytes        int64  `json:"bytes"`
}

func buildArtifactRecipe(source api.EnvironmentWorkloadSource) api.EnvironmentWorkloadSource {
	source.BuildArtifactID, source.BuildArtifactHash = "", ""
	return source
}

func attachBuiltArtifact(input Deployment, origin Deployment, root SourceBuildRootfs, plan api.Plan) (Deployment, error) {
	frozen := candidateFrozenInputs(input)
	var sidecars []json.RawMessage
	if len(input.Sidecars) != 0 && (json.Unmarshal(input.Sidecars, &sidecars) != nil || len(sidecars) != 0) {
		return input, fmt.Errorf("%w: build artifact reuse does not yet support sidecars", ErrEnvironmentWorkloadPreparationUnavailable)
	}
	original, err := origin.ScopedWorkloadRuntime()
	limits, ok := api.LimitsFor(plan)
	if err != nil || original == nil || original.Source == nil || original.SourceArchive == nil || frozen.Source == nil ||
		!ok || root.Input.ContentBytes <= 0 || root.Input.ContentBytes > int64(limits.AppLayerMaxMB)<<20 || root.Input.ArtifactBytes > int64(limits.AppLayerMaxMB)<<20 ||
		validateSourceBuildRootfs(root) != nil || root.ID != frozen.Source.BuildArtifactID || root.InputHash != frozen.Source.BuildArtifactHash ||
		!sameStandardUUID(root.Input.DeploymentID, origin.ID) || !sameStandardUUID(root.Input.AppID, input.AppID) || !sameStandardUUID(origin.AppID, input.AppID) ||
		origin.DeletedAt != nil || !sourceBuildRootfsMetadataMatches(root, origin) ||
		sameStandardUUID(origin.ID, input.ID) || original.SourceProvider != frozen.SourceProvider || original.SourceRepositoryID != frozen.SourceRepositoryID || original.SourceRepository != frozen.SourceRepository ||
		buildArtifactRecipe(*original.Source) != buildArtifactRecipe(*frozen.Source) || original.AppType != frozen.AppType || original.RuntimeBase != frozen.RuntimeBase {
		return input, fmt.Errorf("%w: build artifact is absent, incompatible or outside the app trust scope", ErrEnvironmentWorkloadPreparationUnavailable)
	}
	frozen.BuiltArtifact = &EnvironmentWorkloadBuiltArtifact{ID: root.ID, InputHash: root.InputHash, DeploymentID: canonicalStandardUUID(origin.ID), BuildID: canonicalStandardUUID(origin.BuildID),
		CommitSHA: origin.CommitSHA, Digest: root.Input.ArtifactDigest, RootfsPath: root.Input.RootfsPath, StorageKey: root.Input.StorageKey, Bytes: root.Input.ContentBytes}
	raw, err := json.Marshal(frozen)
	if err != nil {
		return input, err
	}
	input.EnvironmentWorkloadRuntime = string(raw)
	input.RootfsPath, input.RootfsKey, input.RootfsBytes = root.Input.RootfsPath, root.Input.StorageKey, root.Input.ContentBytes
	return input, nil
}

func builtArtifactMatchesDeployment(dep Deployment, frozen EnvironmentWorkloadRuntime) bool {
	b := frozen.BuiltArtifact
	return b != nil && frozen.Source != nil && frozen.Source.Kind != "image" && frozen.SourceArchive == nil &&
		api.ValidEnvironmentBuildArtifactReference(b.ID, b.InputHash) && frozen.Source.BuildArtifactID == b.ID && frozen.Source.BuildArtifactHash == b.InputHash &&
		canonicalStateUUID(b.DeploymentID) && canonicalStateUUID(b.BuildID) && environmentCommitRE.MatchString(b.CommitSHA) &&
		validSourceHashDigest(b.Digest) && b.Bytes > 0 && validLayerArtifactKey(b.StorageKey) &&
		dep.RootfsPath == b.RootfsPath && dep.RootfsKey == b.StorageKey && dep.RootfsBytes == b.Bytes &&
		dep.BuildID == "" && dep.SourcePath == "" && dep.SourceSHA256 == "" && dep.SourceBytes == 0 && dep.LogPath == "" && dep.ImageDigest == "" &&
		validEnvironmentGitCandidateProvenance(dep, frozen) && dep.SourceRoot == frozen.Source.Directory
}

func validSourceHashDigest(value string) bool {
	return len(value) == 71 && value[:7] == "sha256:" && validSourceHash(value[7:])
}

// Presence includes damaged materialization receipts so producer consumers
// cannot fall back to a legacy artifact path when a reference becomes stale.
func environmentHasBuildArtifactReference(dep Deployment) bool {
	frozen := candidateFrozenInputs(dep)
	return frozen.BuiltArtifact != nil || frozen.Source != nil && (frozen.Source.BuildArtifactID != "" || frozen.Source.BuildArtifactHash != "")
}
