package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

func operationWorkflowAuthorityHeaders(h http.Header, claims workloadidentity.Claims) (state.OperationWorkflowAuthority, error) {
	allowed := map[string]bool{}
	for _, name := range []string{api.OperationExecutionKindHeader, api.OperationWorkflowRunHeader, api.OperationWorkflowStepHeader, api.OperationGenerationHeader, api.OperationAttemptHeader, api.OperationWorkflowCapabilityHeader} {
		allowed[strings.ToLower(name)] = true
		if len(h.Values(name)) != 1 {
			return state.OperationWorkflowAuthority{}, state.ErrInvalidArgument
		}
	}
	for name := range h {
		if strings.HasPrefix(strings.ToLower(name), "x-gregale-operation-") && !allowed[strings.ToLower(name)] {
			return state.OperationWorkflowAuthority{}, state.ErrInvalidArgument
		}
	}
	if h.Get(api.OperationExecutionKindHeader) != "workflow" || h.Get(api.InvocationIDHeader) != "" {
		return state.OperationWorkflowAuthority{}, state.ErrInvalidArgument
	}
	generation, err := strconv.Atoi(h.Get(api.OperationGenerationHeader))
	if err != nil || generation < 1 {
		return state.OperationWorkflowAuthority{}, state.ErrInvalidArgument
	}
	attempt, err := strconv.Atoi(h.Get(api.OperationAttemptHeader))
	if err != nil || attempt < 1 {
		return state.OperationWorkflowAuthority{}, state.ErrInvalidArgument
	}
	return state.OperationWorkflowAuthority{AccountID: claims.AccountID, AppID: claims.AppID, InstanceID: claims.InstanceID, RunID: h.Get(api.OperationWorkflowRunHeader), StepName: h.Get(api.OperationWorkflowStepHeader), Generation: generation, Attempt: attempt, Capability: h.Get(api.OperationWorkflowCapabilityHeader)}, nil
}

func (s *server) workflowOperationRuntimeAuthority(w http.ResponseWriter, r *http.Request) (state.OperationWorkflowAuthority, bool) {
	if len(r.Header.Values("Authorization")) != 1 {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required", "provide a current workflow workload assertion"))
		return state.OperationWorkflowAuthority{}, false
	}
	claims, err := s.operationsWorkloadVerifier.Verify(operationBearer(r), workloadidentity.OperationsAudience, time.Now())
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required", "provide a current workflow workload assertion"))
		return state.OperationWorkflowAuthority{}, false
	}
	authority, err := operationWorkflowAuthorityHeaders(r.Header, claims)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workflow proof required", "provide a current workflow operation proof"))
		return authority, false
	}
	if _, ok := s.operationStore(w); !ok {
		return authority, false
	}
	return authority, true
}

func (s *server) workflowArtifactRuntime(w http.ResponseWriter, r *http.Request) (state.OperationWorkflowArtifactStore, state.OperationWorkflowAuthority, bool) {
	authority, ok := s.workflowOperationRuntimeAuthority(w, r)
	if !ok {
		return nil, authority, false
	}

	store, ok := s.store.(state.OperationWorkflowArtifactStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow operation artifacts unavailable"))
		return nil, authority, false
	}
	return store, authority, true
}

func (s *server) reuseWorkflowOperationArtifact(w http.ResponseWriter, r *http.Request) {
	store, authority, ok := s.workflowArtifactRuntime(w, r)
	if !ok {
		return
	}
	var req api.OperationArtifactRequest
	if !decodeOperationBody(w, r, &req, api.OperationReportBodyMaxBytes) {
		return
	}
	response, err := store.ReuseWorkflowOperationArtifact(r.Context(), r.PathValue("id"), authority, req)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func (s *server) prepareWorkflowOperationArtifact(w http.ResponseWriter, r *http.Request) {
	store, authority, ok := s.workflowArtifactRuntime(w, r)
	if !ok {
		return
	}
	var req api.OperationArtifactRequest
	if !decodeOperationBody(w, r, &req, api.OperationReportBodyMaxBytes) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.OperationArtifactTransferTimeout)
	defer cancel()
	response, err := s.retainWorkflowOperationArtifact(ctx, store, r.PathValue("id"), authority, req)
	if err != nil {
		writeOperationArtifactError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

func (s *server) retainWorkflowOperationArtifact(ctx context.Context, store state.OperationWorkflowArtifactStore, id string, a state.OperationWorkflowAuthority, req api.OperationArtifactRequest) (api.OperationWorkflowArtifactResponse, error) {
	response, err := store.ReuseWorkflowOperationArtifact(ctx, id, a, req)
	if err != nil || response.Available {
		return response, err
	}
	if s.operationArtifactStorage == nil {
		return response, objectstorage.ErrUnavailable
	}
	blob, op, err := store.ReserveWorkflowOperationArtifact(ctx, id, a, req)
	if err != nil {
		return response, err
	}
	if blob.State != "retained" {
		f, err := s.verifyOperationArtifact(ctx, op, req)
		if err != nil {
			return response, err
		}
		defer func() { _ = f.Close() }()
		if !blob.ExpiresAt.After(time.Now()) {
			return response, state.ErrOperationStaleAttempt
		}
		if err := s.operationArtifactStorage.Put(ctx, blob.StorageKey, f); err != nil {
			return response, err
		}
	}
	return store.PrepareVerifiedWorkflowOperationArtifact(ctx, id, a, req, blob.ID)
}
