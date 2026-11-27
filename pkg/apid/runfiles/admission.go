package runfiles

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runfile"
	"github.com/onebox-faas/faas/pkg/runfilestore"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s Service) PrepareAdmission(ctx context.Context, reader state.ExecutionAdmissionReader, access Access, request api.CreateExecutionRequest, resolved *api.ResolvedExecutionRequest) ([]state.ExecutionArtifactGrant, error) {
	acct := access.Account
	if len(request.InputFiles) == 0 && request.ArtifactStorage != api.ExecutionArtifactStored {
		return nil, nil
	}
	if s.Storage == nil {
		return nil, api.ErrCapacity("Run file storage is unavailable")
	}
	files, ok := reader.(state.ExecutionFileAdmissionReader)
	if !ok {
		return nil, api.ErrCapacity("Run file metadata is unavailable")
	}
	var grants []state.ExecutionArtifactGrant
	var inputBytes int64
	for _, input := range request.InputFiles {
		id := input.FileID
		grantAccess := false
		if input.GrantToken != "" {
			if s.GrantTokenHash == nil {
				return nil, grantUnavailable()
			}
			hash, ok := s.GrantTokenHash(input.GrantToken)
			if !ok {
				return nil, grantUnavailable()
			}
			g, err := reader.ExecutionArtifactGrantByToken(ctx, acct.ID, hash, time.Now().UTC())
			if err != nil {
				return nil, grantUnavailable()
			}
			source, err := reader.ExecutionByID(ctx, acct.ID, g.SourceExecutionID)
			if err != nil || source.Status != api.ExecutionStatusSucceeded || source.ResultValidation != nil && source.ResultValidation.Status == "invalid" {
				return nil, grantUnavailable()
			}
			for _, a := range source.Artifacts {
				if a.Name == g.ArtifactName {
					id = a.FileID
				}
			}
			if id == "" {
				return nil, grantUnavailable()
			}
			grants = append(grants, g)
			grantAccess = true
		}
		f, err := files.ExecutionFileByID(ctx, acct.ID, id)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil, state.ErrExecutionFileNotReady
			}
			return nil, err
		}
		owner := access.Wide || f.PrincipalID == nil && access.Principal == nil || f.PrincipalID != nil && access.Principal != nil && *f.PrincipalID == *access.Principal
		if !owner && !grantAccess {
			return nil, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Run file not found", "no such Run file")
		}
		if f.Status != "ready" || !f.ExpiresAt.After(time.Now().UTC()) {
			return nil, state.ErrExecutionFileNotReady
		}
		if f.ExecutionID != nil {
			source, err := reader.ExecutionByID(ctx, acct.ID, *f.ExecutionID)
			if err != nil || source.ResultValidation != nil && source.ResultValidation.Status == "invalid" {
				return nil, api.ErrValidation("a stored input cannot reference a Run with an invalid result contract")
			}
		}
		private, err := runfilestore.OpenKey(acct.ID, id, f.StorageKey, f.SealedKey, s.Identities)
		if err != nil {
			return nil, api.ErrCapacity("Run file key is unavailable")
		}
		inputBytes += f.SizeBytes
		resolved.StoredInputs = append(resolved.StoredInputs, runfile.Ref{Descriptor: runfile.Descriptor{ID: id, Path: input.Path, SizeBytes: f.SizeBytes, SHA256: f.SHA256}, StorageKey: f.StorageKey, PrivateKey: private})
	}
	limits := acct.Plan.ExecutionFileLimits()
	if inputBytes > limits.MaxInputBytes {
		return nil, api.NewProblem(413, "execution_file_input_limit", "Run file input limit exceeded", "combined input file bytes exceed the plan limit").WithByteLimit(limits.MaxInputBytes, inputBytes).WithDocs(api.ExecutionFilesDocsURL)
	}
	available := int64(resolved.Limits.EphemeralDiskMB)*(1<<20) - api.ExecutionFileScratchReserveBytes - inputBytes - int64(resolved.SourceBytes()+len(resolved.Input))
	if available < 0 {
		return nil, api.ErrValidation("input files do not fit the selected ephemeral disk; increase ephemeral_disk_mb")
	}
	if resolved.Limits.MaxArtifactBytes > available {
		if request.Limits != nil && request.Limits.MaxArtifactBytes != 0 {
			return nil, api.ErrValidation("max_artifact_bytes does not fit the selected ephemeral disk")
		}
		resolved.Limits.MaxArtifactBytes = available
	}
	if len(request.OutputFiles) > 0 && request.ArtifactStorage == api.ExecutionArtifactStored {
		if resolved.Limits.MaxArtifactBytes <= 0 {
			return nil, api.ErrValidation("no ephemeral disk space remains for output files")
		}
		recipients := s.Recipients
		if len(recipients) == 0 {
			return nil, api.ErrCapacity("Run file sealing is unavailable")
		}
		for _, name := range request.OutputFiles {
			id := uuid.NewString()
			key := runfile.Key(acct.ID, id, uuid.NewString())
			private, sealed, err := runfilestore.NewKey(acct.ID, id, key, recipients)
			if err != nil {
				return nil, api.ErrCapacity("Run file sealing is unavailable")
			}
			resolved.StoredOutputs = append(resolved.StoredOutputs, runfile.Ref{Descriptor: runfile.Descriptor{ID: id, Path: name}, StorageKey: key, PrivateKey: private, SealedKey: sealed})
		}
	}
	return grants, nil
}

func grantUnavailable() *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Artifact grant unavailable", "the grant is unknown, expired, revoked, or already redeemed")
}
