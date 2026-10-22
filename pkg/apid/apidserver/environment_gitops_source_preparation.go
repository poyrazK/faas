package apidserver

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
	account, err := s.store.AccountByID(ctx, lease.Source.AccountID)
	if err != nil {
		return artifact, err
	}
	limits, ok := api.LimitsFor(account.Plan)
	if !ok {
		return artifact, state.ErrInvalidArgument
	}
	maxBytes := int64(limits.SourceTarballMaxMB) << 20
	body, verify, err := s.environmentGitBuildStream(ctx, lease, maxBytes)
	if err != nil {
		return artifact, err
	}
	archivePath, bytes, problem := validateAndSpool(io.LimitReader(body, maxBytes+1), limits)
	closeErr := body.Close()
	if archivePath != "" {
		// Keep accepted files on ambiguous publication outcomes. Removing an accepted file on a publication error
		// could break a queued build whose successful commit reply was lost.
		defer func() {
			if artifact.Path == "" {
				_ = os.Remove(archivePath)
			}
		}()
	}
	if problem != nil || closeErr != nil || !verify(bytes) {
		return artifact, fmt.Errorf("verify reviewed Git build stream")
	}
	if err := verifyEnvironmentGitBuildArchive(archivePath, maxBytes, lease, request.Source); err != nil {
		return artifact, err
	}
	contextFormat := ""
	if request.Source.ReuseUnchangedBuild {
		canonicalPath, canonicalBytes, err := canonicalEnvironmentBuildContext(archivePath, request.Source.BuildContext, maxBytes)
		if err != nil {
			return artifact, err
		}
		_ = os.Remove(archivePath)
		archivePath, bytes = canonicalPath, canonicalBytes
		contextFormat = environmentgitops.BuildContextFormat
	}
	if problem := scanSourceTarballSecrets(archivePath, limits); problem != nil {
		return artifact, fmt.Errorf("git build source validation rejected the archive")
	}
	if problem := scanForStatefulShapeWithDockerfileAtRoot(archivePath, request.Source.Kind == "dockerfile" || request.Source.Dockerfile != "", request.Source.Directory, request.Source.Dockerfile); problem != nil {
		return artifact, fmt.Errorf("git build source violates supported workload shape")
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
	artifact = state.EnvironmentWorkloadSourceArtifact{ContextFormat: contextFormat, RevisionID: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, DefinitionDigest: lease.Revision.Digest, BuildID: request.BuildID, Path: archivePath, SHA256: checksum, Bytes: bytes,
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
		return fmt.Errorf("git build archive does not match the approved environment definition")
	}
	return nil
}

// Both providers feed the same bounded spool, definition, source-layout and
// secret checks. GitLab archives are fetched only by the reverified exact SHA.
func (s *server) environmentGitBuildStream(ctx context.Context, lease state.EnvironmentGitOpsLease, maxBytes int64) (io.ReadCloser, func(int64) bool, error) {
	if lease.Source.Spec.Provider == "gitlab" {
		client, token, sha, err := s.resolveVerifiedGitLabCommit(ctx, lease.Source.AccountID, lease.Source, lease.Revision.CommitSHA)
		if err != nil || sha != lease.Revision.CommitSHA {
			return nil, nil, fmt.Errorf("verify reviewed GitLab build commit")
		}
		body, err := client.Archive(ctx, token, lease.Source.Spec.RepositoryID, sha, maxBytes)
		return body, func(int64) bool { return true }, err
	}
	if _, err := s.verifyEnvironmentGitRepository(ctx, lease.Source.AccountID, lease.Source.Spec.InstallationID, lease.Source.Spec.Repository, lease.Source.Spec.RepositoryID); err != nil {
		return nil, nil, fmt.Errorf("verify Git build repository: %w", err)
	}
	stream, err := s.githubd.StreamSourceRef(ctx, lease.Source.AccountID, lease.Source.Spec.InstallationID, lease.Source.Spec.Repository, lease.Revision.CommitSHA, maxBytes)
	if err != nil || stream == nil || stream.Body == nil {
		return nil, nil, fmt.Errorf("fetch reviewed Git build archive")
	}
	return stream.Body, func(bytes int64) bool {
		return stream.Stats != nil && stream.Stats.Err == nil && !stream.Stats.Truncated && stream.Stats.ResolvedCommitSHA == lease.Revision.CommitSHA && stream.Stats.BytesStreamed == bytes
	}, nil
}

// The approved provider archive was already checked above. Canonicalization
// changes the actual delivered context, never just its cache fingerprint.
func canonicalEnvironmentBuildContext(archivePath, buildContext string, maxBytes int64) (string, int64, error) {
	input, err := openSpoolFile(archivePath)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = input.Close() }()
	return apidsource.CanonicalGitBuildContext(input, spoolRoot(), buildContext, maxBytes)
}
