package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const executionArtifactGrantTokenPrefix = "rag_"

func (s *server) createExecutionArtifactGrant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !requireExecutionEntitlement(w, acct) || !s.requireExecutionAPI(w) {
		return
	}
	access, problem := executionAccessForRequest(r)
	if problem != nil {
		writeExecutionAccessError(w, problem)
		return
	}
	grantStore, ok := s.store.(state.ExecutionArtifactGrantStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("execution artifact grants are unavailable in this store"))
		return
	}

	var request api.CreateExecutionArtifactGrantRequest
	if err := decodeJSONSized(r, &request, 4<<10); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid artifact grant request"))
		return
	}
	request.ArtifactName = strings.TrimSpace(request.ArtifactName)
	if request.ArtifactName == "" || api.ValidateExecutionOutputFiles([]string{request.ArtifactName}) != nil {
		api.WriteProblem(w, invalidArtifactGrantRequest("artifact_name must name one normalized output artifact"))
		return
	}
	ttlSeconds := request.ExpiresInSeconds
	if ttlSeconds == 0 {
		ttlSeconds = api.ExecutionArtifactGrantDefaultTTLSeconds
	}
	if ttlSeconds < api.ExecutionArtifactGrantMinTTLSeconds || ttlSeconds > api.ExecutionArtifactGrantMaxTTLSeconds {
		api.WriteProblem(w, invalidArtifactGrantRequest("expires_in_seconds must be between 30 and 3600"))
		return
	}

	executionID := r.PathValue("id")
	if _, err := uuid.Parse(executionID); err != nil {
		s.notFound(w, "no such execution")
		return
	}
	row, err := s.store.ExecutionByID(r.Context(), acct.ID, executionID)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such execution")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load execution artifact source"))
		return
	}
	if !requireExecutionOwnership(w, s, access, row) {
		return
	}
	if row.Status != api.ExecutionStatusSucceeded {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Artifact source is not ready", "grants can only be created for artifacts from successful executions"))
		return
	}
	found := false
	for _, artifact := range row.Artifacts {
		if artifact.Name == request.ArtifactName {
			if err := api.ValidateExecutionArtifacts([]api.ExecutionArtifact{artifact}); err != nil {
				api.WriteProblem(w, api.ErrInternal("stored artifact failed integrity verification"))
				return
			}
			found = true
			break
		}
	}
	if !found {
		s.notFound(w, "no such execution artifact")
		return
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create artifact grant"))
		return
	}
	token := executionArtifactGrantTokenPrefix + base64.RawURLEncoding.EncodeToString(secret)
	tokenHash := sha256.Sum256([]byte(token))
	now := time.Now().UTC()
	grantID := uuid.NewString()
	params := state.CreateExecutionArtifactGrantParams{
		ID: grantID, AccountID: acct.ID, SourceExecutionID: row.ID, ArtifactName: request.ArtifactName,
		CreatorPrincipalID: access.principal, TokenHash: tokenHash[:],
		ExpiresAt: now.Add(time.Duration(ttlSeconds) * time.Second), CreatedAt: now,
	}
	grant, err := grantStore.CreateExecutionArtifactGrant(r.Context(), params)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such execution artifact")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not create artifact grant"))
		return
	}
	s.auditExecutionArtifactGrant(r, acct.ID, "execution.artifact_grant_created", grant, "")
	writeJSON(w, http.StatusCreated, api.ExecutionArtifactGrantResponse{
		ID: grant.ID, SourceExecutionID: grant.SourceExecutionID, ArtifactName: grant.ArtifactName,
		Token: token, ExpiresAt: grant.ExpiresAt.UTC().Format(time.RFC3339Nano),
	})
}

func (s *server) revokeExecutionArtifactGrant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	access, problem := executionAccessForRequest(r)
	if problem != nil {
		writeExecutionAccessError(w, problem)
		return
	}
	grantStore, ok := s.store.(state.ExecutionArtifactGrantStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("execution artifact grants are unavailable in this store"))
		return
	}
	grantID := r.PathValue("id")
	if _, err := uuid.Parse(grantID); err != nil {
		s.notFound(w, "no such artifact grant")
		return
	}
	grant, err := grantStore.RevokeExecutionArtifactGrant(r.Context(), acct.ID, grantID, access.principal, access.broad, time.Now().UTC())
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such artifact grant")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not revoke artifact grant"))
		return
	}
	s.auditExecutionArtifactGrant(r, acct.ID, "execution.artifact_grant_revoked", grant, "")
	revokedAt := ""
	if grant.RevokedAt != nil {
		revokedAt = grant.RevokedAt.UTC().Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, api.RevokeExecutionArtifactGrantResponse{ID: grant.ID, RevokedAt: revokedAt})
}

func invalidArtifactGrantRequest(detail string) *api.Problem {
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeExecutionPayloadInvalid, "Invalid artifact grant request", detail)
}
