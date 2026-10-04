package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) operationStore(w http.ResponseWriter) (state.OperationStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(state.OperationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation storage is unavailable"))
	}
	return store, ok
}

func writeOperationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Operation not found", "the operation or definition is unavailable"))
	case errors.Is(err, state.ErrOperationExpired):
		api.WriteProblem(w, api.NewProblem(http.StatusGone, "operation_expired", "Operation expired", "the retained result expired; its identity remains reserved for the deduplication window"))
	case errors.Is(err, state.ErrOperationInputConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "operation_input_conflict", "Conflicting submission", "this identity was already used with a different payload"))
	case errors.Is(err, state.ErrConflict), errors.Is(err, state.ErrOperationStaleAttempt):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "operation_state_conflict", "Operation state changed", "refresh the operation before submitting this change"))
	case errors.Is(err, state.ErrOperationQuota):
		api.WriteProblem(w, api.OperationLimitProblem(err))
	case errors.Is(err, state.ErrPlatformTenantSuspended):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Customer suspended", "resume this customer before accepting new execution"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("operation input or contract is invalid"))
	default:
		api.WriteProblem(w, api.ErrInternal("operation request failed"))
	}
}

// Canonicalization rejects duplicate members before typed decoding discards
// their evidence. Every new control body uses a bounded read and closed DTO.
func decodeOperationBody(w http.ResponseWriter, r *http.Request, dst any, limit int) bool {
	if r.Body == nil {
		api.WriteProblem(w, api.ErrValidation("operation body is required"))
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(limit)))
	if err != nil {
		var maximum *http.MaxBytesError
		if errors.As(err, &maximum) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(int64(limit), int64(limit)+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("operation body could not be read"))
		}
		return false
	}
	canonical, err := operations.CanonicalJSON(body)
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(canonical))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(dst)
	}
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("operation body must match the request contract"))
		return false
	}
	return true
}

func (s *server) operationAdmissionOpen(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if !s.operationsAdmissionEnabled {
		api.WriteProblem(w, api.ErrCapacity("new operation admission is disabled"))
		return false
	}
	return true
}

func (s *server) operationDefinitionDeployment(w http.ResponseWriter, r *http.Request, acct state.Account) (state.Deployment, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.Deployment{}, false
	}
	dep, err := s.store.DeploymentByID(r.Context(), r.PathValue("deployment_id"))
	if err != nil || dep.AppID != app.ID {
		writeOperationError(w, state.ErrNotFound)
		return state.Deployment{}, false
	}
	return dep, true
}

func (s *server) putOperationDefinition(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.operationAdmissionOpen(w) {
		return
	}
	store, ok := s.operationStore(w)
	if !ok {
		return
	}
	dep, ok := s.operationDefinitionDeployment(w, r, acct)
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.Operations.Allowed {
		writeOperationError(w, state.NewOperationLimitError("plan_admission", 0, 1))
		return
	}
	var spec api.OperationDefinitionSpec
	if !decodeOperationBody(w, r, &spec, api.OperationDefinitionBodyMaxBytes) {
		return
	}
	if spec.Name == "" {
		spec.Name = r.PathValue("name")
	}
	if spec.Name != r.PathValue("name") {
		writeOperationError(w, state.ErrInvalidArgument)
		return
	}
	def, err := store.PutOperationDefinition(r.Context(), state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: dep.AppID, Scope: dep.Scope, DeploymentID: dep.ID, ReleaseID: r.Header.Get(api.ReleaseHeader), Spec: spec}})
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, def.OperationDefinitionResponse)
}

func (s *server) ownedOperation(w http.ResponseWriter, r *http.Request, acct state.Account) (state.Operation, state.OperationStore, bool) {
	store, ok := s.operationStore(w)
	if !ok {
		return state.Operation{}, nil, false
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.Operation{}, nil, false
	}
	op, err := store.OperationByID(r.Context(), acct.ID, "", r.PathValue("id"))
	if err != nil {
		writeOperationError(w, err)
		return state.Operation{}, nil, false
	}
	if op.AppID != app.ID {
		writeOperationError(w, state.ErrNotFound)
		return state.Operation{}, nil, false
	}
	return op, store, true
}

func (s *server) getOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, _, ok := s.ownedOperation(w, r, acct)
	if ok {
		writeJSON(w, http.StatusOK, op.OperationResponse)
	}
}

func (s *server) platformTenantSelfOperation(w http.ResponseWriter, r *http.Request, acct state.Account) (state.Operation, state.OperationStore, bool) {
	store, ok := s.operationStore(w)
	if !ok {
		return state.Operation{}, nil, false
	}
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return state.Operation{}, nil, false
	}
	op, err := store.OperationByID(r.Context(), acct.ID, tenant, r.PathValue("id"))
	if err != nil {
		writeOperationError(w, err)
		return state.Operation{}, nil, false
	}
	return op, store, true
}

func (s *server) getPlatformTenantSelfOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, _, ok := s.platformTenantSelfOperation(w, r, acct)
	if ok {
		writeJSON(w, http.StatusOK, op.OperationResponse)
	}
}

func operationEventsCursor(r *http.Request) (int64, error) {
	query := r.URL.Query()
	raw := r.Header.Get("Last-Event-ID")
	if query.Has("after") {
		raw = query.Get("after")
		if raw == "" {
			return 0, state.ErrInvalidArgument
		}
	} else if raw == "" {
		return 0, nil
	}
	after, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || after < 0 {
		return 0, state.ErrInvalidArgument
	}
	return after, nil
}

func (s *server) getPlatformTenantSelfOperationEvents(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, store, ok := s.platformTenantSelfOperation(w, r, acct)
	if !ok {
		return
	}
	after, err := operationEventsCursor(r)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		s.streamOperationEvents(w, r, op, store, after)
		return
	}
	page, err := store.OperationEvents(r.Context(), acct.ID, op.PlatformTenantID, op.ID, after, api.OperationEventsPageMax)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) startPlatformTenantSelfOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.operationAdmissionOpen(w) {
		return
	}
	store, ok := s.operationStore(w)
	if !ok {
		return
	}
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	var req api.OperationStartRequest
	if !decodeOperationBody(w, r, &req, api.OperationSubmissionMaxBytes+api.OperationStartBodyOverheadBytes) {
		return
	}
	op, _, err := store.AdmitOperation(r.Context(), state.OperationAdmission{AccountID: acct.ID, PlatformTenantID: tenant, DefinitionID: req.DefinitionID, IdempotencyKey: r.Header.Get("Idempotency-Key"), Input: req.Input})
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, api.OperationAcceptedResponse{ID: op.ID, StatusURL: "/v1/platform-tenant-self/customer-operations/" + op.ID, EventsURL: "/v1/platform-tenant-self/customer-operations/" + op.ID + "/events"})
}

func (s *server) cancelOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, store, ok := s.ownedOperation(w, r, acct)
	if ok {
		s.cancelScopedOperation(w, r, acct, op, store, "")
	}
}

func (s *server) cancelPlatformTenantSelfOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, store, ok := s.platformTenantSelfOperation(w, r, acct)
	if ok {
		s.cancelScopedOperation(w, r, acct, op, store, op.PlatformTenantID)
	}
}

func (s *server) cancelScopedOperation(w http.ResponseWriter, r *http.Request, acct state.Account, op state.Operation, store state.OperationStore, tenant string) {
	var req api.OperationCancellationRequest
	if !decodeOperationBody(w, r, &req, api.OperationReportBodyMaxBytes) {
		return
	}
	got, err := store.CancelOperation(r.Context(), acct.ID, tenant, op.ID, req.ExpectedGeneration)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, got.OperationResponse)
}

func operationBearer(r *http.Request) string {
	raw := r.Header.Get("Authorization")
	if len(raw) < 7 || !strings.EqualFold(raw[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(raw[7:])
}
