package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// resolveExecutionArtifactInputs expands account-scoped receipt references
// and one-time capability grants into the new run's bounded bundle before it
// is validated and sealed. Returned grants are consumed atomically with run
// admission by the state store.
func (s *server) resolveExecutionArtifactInputs(ctx context.Context, acct state.Account, access executionAccess, request *api.CreateExecutionRequest) ([]state.ExecutionArtifactGrant, *api.Problem) {
	refs := request.ArtifactInputs
	if len(refs) == 0 {
		return nil, nil
	}
	if len(refs) > api.ExecutionArtifactInputMaxFiles || len(request.Files)+len(refs) > api.ExecutionBundleMaxFiles {
		return nil, invalidArtifactInputs("artifact_inputs exceeds the file limit")
	}
	if strings.TrimSpace(request.Source) != "" || strings.TrimSpace(request.Entrypoint) == "" {
		return nil, invalidArtifactInputs("artifact_inputs requires an entrypoint files bundle")
	}
	planLimits, ok := acct.Plan.ExecutionLimits()
	if !ok || !planLimits.Allowed {
		return nil, api.ErrExecutionsNotAllowed(acct.Plan)
	}
	observed := 0
	for _, file := range request.Files {
		observed += len(file.Content)
	}
	if observed > planLimits.MaxSourceBytes {
		return nil, artifactBundleTooLarge(planLimits.MaxSourceBytes, observed)
	}

	var grants []state.ExecutionArtifactGrant
	for _, ref := range refs {
		if api.ValidateExecutionOutputFiles([]string{ref.Path}) != nil {
			return nil, invalidArtifactInputs("each artifact input requires a normalized destination path")
		}
		var row state.Execution
		var grant *state.ExecutionArtifactGrant
		if ref.GrantToken != "" {
			if ref.ExecutionID != "" || ref.Name != "" {
				return nil, invalidArtifactInputs("grant_token cannot be combined with execution_id or name")
			}
			tokenHash, valid := hashExecutionArtifactGrantToken(ref.GrantToken)
			if !valid {
				return nil, artifactGrantUnavailableProblem()
			}
			grantStore, ok := s.store.(state.ExecutionArtifactGrantStore)
			if !ok {
				return nil, api.ErrCapacity("execution artifact grants are unavailable in this store")
			}
			loaded, err := grantStore.ExecutionArtifactGrantByToken(ctx, acct.ID, tokenHash, time.Now().UTC())
			if errors.Is(err, state.ErrNotFound) {
				return nil, artifactGrantUnavailableProblem()
			}
			if err != nil {
				if s.log != nil {
					s.log.Error("execution artifact grant lookup failed", "account_id", acct.ID, "err", err)
				}
				return nil, api.ErrInternal("could not load artifact grant")
			}
			row, err = s.store.ExecutionByID(ctx, acct.ID, loaded.SourceExecutionID)
			if errors.Is(err, state.ErrNotFound) {
				return nil, artifactGrantUnavailableProblem()
			}
			if err != nil {
				if s.log != nil {
					s.log.Error("execution artifact grant source lookup failed", "account_id", acct.ID, "err", err)
				}
				return nil, api.ErrInternal("could not load artifact grant source")
			}
			grant = &loaded
			ref.Name = loaded.ArtifactName
		} else {
			if _, err := uuid.Parse(ref.ExecutionID); err != nil || strings.TrimSpace(ref.Name) == "" || api.ValidateExecutionOutputFiles([]string{ref.Name}) != nil {
				return nil, invalidArtifactInputs("each account-scoped artifact input requires a valid execution_id, artifact name, and normalized destination path")
			}
			var err error
			row, err = s.store.ExecutionByID(ctx, acct.ID, ref.ExecutionID)
			if errors.Is(err, state.ErrNotFound) {
				return nil, artifactSourceUnavailableProblem()
			}
			if err != nil {
				if s.log != nil {
					s.log.Error("execution artifact source lookup failed", "account_id", acct.ID, "err", err)
				}
				return nil, api.ErrInternal("could not load artifact input")
			}
			if !access.owns(row) {
				return nil, artifactSourceUnavailableProblem()
			}
		}
		if row.Status != api.ExecutionStatusSucceeded {
			if grant != nil {
				return nil, artifactGrantUnavailableProblem()
			}
			return nil, api.NewProblem(http.StatusConflict, api.CodeConflict, "Artifact source is not ready", "artifact inputs can only reference successful executions")
		}
		var artifact *api.ExecutionArtifact
		for i := range row.Artifacts {
			if row.Artifacts[i].Name == ref.Name {
				artifact = &row.Artifacts[i]
				break
			}
		}
		if artifact == nil {
			if grant != nil {
				return nil, artifactGrantUnavailableProblem()
			}
			return nil, artifactSourceUnavailableProblem()
		}
		if err := api.ValidateExecutionArtifacts([]api.ExecutionArtifact{*artifact}); err != nil {
			if s.log != nil {
				s.log.Error("stored execution artifact failed integrity verification", "account_id", acct.ID, "err", err)
			}
			return nil, api.ErrInternal("stored artifact input failed integrity verification")
		}
		observed += len(artifact.Content)
		if observed > planLimits.MaxSourceBytes {
			return nil, artifactBundleTooLarge(planLimits.MaxSourceBytes, observed)
		}
		request.Files = append(request.Files, api.ExecutionFile{Path: ref.Path, Content: append([]byte(nil), artifact.Content...)})
		if grant != nil {
			grants = append(grants, *grant)
		}
	}
	request.ArtifactInputs = nil
	return grants, nil
}

func hashExecutionArtifactGrantToken(token string) ([]byte, bool) {
	if !strings.HasPrefix(token, "rag_") || len(token) > 64 {
		return nil, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "rag_"))
	if err != nil || len(decoded) != 32 {
		return nil, false
	}
	digest := sha256.Sum256([]byte(token))
	return digest[:], true
}

func artifactSourceUnavailableProblem() *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Artifact source not found", "no successful execution artifact matches this reference")
}

func artifactGrantUnavailableProblem() *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Artifact grant unavailable", "the grant is unknown, expired, revoked, or already redeemed")
}

func invalidArtifactInputs(detail string) *api.Problem {
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeExecutionPayloadInvalid, "Invalid artifact inputs", detail)
}

func artifactBundleTooLarge(limit, observed int) *api.Problem {
	return api.NewProblem(
		http.StatusRequestEntityTooLarge,
		api.CodeExecutionPayloadTooLarge,
		"Execution payload too large",
		fmt.Sprintf("artifact inputs expand the files bundle to %d bytes; the plan limit is %d bytes", observed, limit),
	).WithLimit(int64(limit), int64(observed)).WithDocs("https://gregale.dev/docs/executions#artifact-handoff")
}
