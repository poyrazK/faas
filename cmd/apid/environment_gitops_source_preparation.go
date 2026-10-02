package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) prepareEnvironmentGitArchive(ctx context.Context, lease state.EnvironmentGitOpsLease, request state.EnvironmentWorkloadSourceRequest) (state.EnvironmentWorkloadSourceArtifact, error) {
	var artifact state.EnvironmentWorkloadSourceArtifact
	if !isCanonicalCommitSHA(lease.Revision.CommitSHA) || request.RevisionID != lease.Revision.ID || request.CommitSHA != lease.Revision.CommitSHA || request.DefinitionDigest != lease.Revision.Digest {
		return artifact, state.ErrInvalidArgument
	}
	if _, err := s.verifyEnvironmentGitRepository(ctx, lease.Source.AccountID, lease.Source.Spec.InstallationID, lease.Source.Spec.Repository, lease.Source.Spec.RepositoryID); err != nil {
		return artifact, fmt.Errorf("verify Git build repository: %w", err)
	}
	account, err := s.store.AccountByID(ctx, lease.Source.AccountID)
	if err != nil {
		return artifact, err
	}
	limits, ok := api.LimitsFor(account.Plan)
	if !ok {
		return artifact, state.ErrInvalidArgument
	}
	maxBytes := int64(limits.SourceTarballMaxMB) << 20
	stream, err := s.githubd.StreamSourceRef(ctx, account.ID, lease.Source.Spec.InstallationID, lease.Source.Spec.Repository, lease.Revision.CommitSHA, maxBytes)
	if err != nil || stream == nil || stream.Body == nil {
		return artifact, fmt.Errorf("fetch reviewed Git build archive")
	}
	archivePath, bytes, problem := validateAndSpool(io.LimitReader(stream.Body, maxBytes+1), limits)
	closeErr := stream.Body.Close()
	if archivePath != "" {
		// Keep accepted files on ambiguous publication outcomes. Removing an accepted file on a publication error
		// could break a queued build whose successful commit reply was lost.
		defer func() {
			if artifact.Path == "" {
				_ = os.Remove(archivePath)
			}
		}()
	}
	if problem != nil || closeErr != nil || stream.Stats == nil || stream.Stats.Err != nil || stream.Stats.Truncated || stream.Stats.ResolvedCommitSHA != lease.Revision.CommitSHA || stream.Stats.BytesStreamed != bytes {
		return artifact, fmt.Errorf("verify reviewed Git build stream")
	}
	if err := verifyEnvironmentGitBuildArchive(archivePath, maxBytes, lease, request.Source); err != nil {
		return artifact, err
	}
	if problem := scanSourceTarballSecrets(archivePath, limits); problem != nil {
		return artifact, fmt.Errorf("Git build source validation rejected the archive")
	}
	if problem := scanForStatefulShapeWithDockerfileAtRoot(archivePath, request.Source.Kind == "dockerfile" || request.Source.Dockerfile != "", request.Source.Directory, request.Source.Dockerfile); problem != nil {
		return artifact, fmt.Errorf("Git build source violates supported workload shape")
	}
	file, err := openSpoolFile(archivePath)
	if err != nil {
		return artifact, err
	}
	hash := sha256.New()
	_, hashErr := io.Copy(hash, file)
	fileCloseErr := file.Close()
	if hashErr != nil || fileCloseErr != nil {
		return artifact, fmt.Errorf("hash reviewed Git build archive")
	}
	checksum := hex.EncodeToString(hash.Sum(nil))
	if err := apidsource.PublishReviewedSource(ctx, request.BuildID, archivePath, checksum); err != nil {
		return artifact, err
	}
	artifact = state.EnvironmentWorkloadSourceArtifact{RevisionID: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, DefinitionDigest: lease.Revision.Digest, BuildID: request.BuildID, Path: archivePath, SHA256: checksum, Bytes: bytes,
		LogPath: filepath.Join(spoolRoot(), request.BuildID, "build.log")}
	return artifact, nil
}

func verifyEnvironmentGitBuildArchive(archivePath string, maxBytes int64, lease state.EnvironmentGitOpsLease, source api.EnvironmentWorkloadSource) error {
	file, err := openSpoolFile(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	desired, err := environmentgitops.ReadGitBuildDefinition(file, lease.Source.Spec.ManifestPath, maxBytes, []api.EnvironmentWorkloadSource{source})
	if err != nil || desired.Digest != lease.Revision.Digest {
		return fmt.Errorf("Git build archive does not match the approved environment definition")
	}
	return nil
}
