package state

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func validateEnvironmentSourceArtifact(artifact EnvironmentWorkloadSourceArtifact) error {
	id, err := uuid.Parse(artifact.BuildID)
	checksum, checksumErr := hex.DecodeString(artifact.SHA256)
	digest, digestErr := hex.DecodeString(artifact.DefinitionDigest)
	if err != nil || id.Version() != 7 || checksumErr != nil || len(checksum) != 32 || strings.ToLower(artifact.SHA256) != artifact.SHA256 || artifact.Bytes < 1 ||
		digestErr != nil || len(digest) != 32 || strings.ToLower(artifact.DefinitionDigest) != artifact.DefinitionDigest || artifact.RevisionID == "" || !environmentCommitRE.MatchString(artifact.CommitSHA) ||
		!filepath.IsAbs(artifact.Path) || filepath.Clean(artifact.Path) != artifact.Path ||
		!filepath.IsAbs(artifact.LogPath) || filepath.Clean(artifact.LogPath) != artifact.LogPath {
		return fmt.Errorf("%w: invalid environment source artifact", ErrInvalidArgument)
	}
	return nil
}

func candidateFrozenInputs(input Deployment) EnvironmentWorkloadRuntime {
	var frozen EnvironmentWorkloadRuntime
	_ = json.Unmarshal([]byte(input.EnvironmentWorkloadRuntime), &frozen)
	return frozen
}

func sourceRequest(input Deployment) (EnvironmentWorkloadSourceRequest, error) {
	frozen := candidateFrozenInputs(input)
	id, err := uuid.NewV7()
	if err != nil {
		return EnvironmentWorkloadSourceRequest{}, err
	}
	return EnvironmentWorkloadSourceRequest{Resource: frozen.Resource, BuildID: id.String(), Source: *frozen.Source,
		RevisionID: frozen.RevisionID, CommitSHA: input.CommitSHA, DefinitionDigest: frozen.DefinitionDigest}, nil
}

func attachCandidateSource(input Deployment, artifacts map[string]EnvironmentWorkloadSourceArtifact, plan api.Plan) (Deployment, error) {
	if input.Kind == DeploymentKindImage {
		return input, nil
	}
	frozen := candidateFrozenInputs(input)
	artifact, exists := artifacts[frozen.Resource]
	if !exists {
		return input, fmt.Errorf("%w: reviewed Git archive must be materialized before publication", ErrEnvironmentWorkloadPreparationUnavailable)
	}
	if err := validateEnvironmentSourceArtifact(artifact); err != nil {
		return input, err
	}
	if artifact.RevisionID != frozen.RevisionID || artifact.CommitSHA != input.CommitSHA || artifact.DefinitionDigest != frozen.DefinitionDigest {
		return input, ErrConflict
	}
	limits, ok := api.LimitsFor(plan)
	if !ok || artifact.Bytes > int64(limits.SourceTarballMaxMB)<<20 {
		return input, ErrInvalidArgument
	}
	frozen.SourceArchive = &artifact
	raw, err := json.Marshal(frozen)
	if err != nil {
		return input, err
	}
	input.EnvironmentWorkloadRuntime = string(raw)
	input.SourcePath, input.SourceSHA256, input.SourceBytes, input.BuildID, input.LogPath = artifact.Path, artifact.SHA256, artifact.Bytes, artifact.BuildID, artifact.LogPath
	return input, nil
}
