package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// A workload assertion identifies the running instance; a separate ephemeral
// capability identifies its current invocation attempt. Neither is sufficient
// alone, and customer bearer credentials cannot substitute for either proof.
func (s *server) runtimeOperationAuthority(r *http.Request) (state.OperationExecutionAuthority, error) {
	for _, name := range []string{"Authorization", api.InvocationIDHeader, api.OperationAttemptHeader, api.OperationCapabilityHeader} {
		if len(r.Header.Values(name)) != 1 {
			return state.OperationExecutionAuthority{}, state.ErrNotFound
		}
	}
	claims, err := s.operationsWorkloadVerifier.Verify(operationBearer(r), workloadidentity.OperationsAudience, time.Now())
	if err != nil {
		return state.OperationExecutionAuthority{}, state.ErrNotFound
	}
	authority, err := operationAuthorityHeaders(r.Header, claims)
	if err != nil {
		return authority, err
	}
	app, err := s.store.AppByID(r.Context(), claims.AppID)
	if err != nil || app.AccountID != claims.AccountID || app.Status == state.AppDeleted {
		return authority, state.ErrNotFound
	}
	acct, err := s.store.AccountByID(r.Context(), claims.AccountID)
	if err != nil || !acct.Active() {
		return authority, state.ErrNotFound
	}
	instance, err := s.store.InstanceByID(r.Context(), claims.InstanceID)
	if err != nil || instance.AppID != app.ID || instance.State != string(state.StateRunning) {
		return authority, state.ErrNotFound
	}
	return authority, nil
}

func operationAuthorityHeaders(headers http.Header, claims workloadidentity.Claims) (state.OperationExecutionAuthority, error) {
	// Native execution adapters need their own contracts. Reject their context
	// instead of accidentally interpreting it as an ordinary HTTP invocation.
	for name := range headers {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-gregale-operation-") && lower != strings.ToLower(api.OperationAttemptHeader) && lower != strings.ToLower(api.OperationCapabilityHeader) {
			return state.OperationExecutionAuthority{}, state.ErrInvalidArgument
		}
	}
	attempt, err := strconv.Atoi(headers.Get(api.OperationAttemptHeader))
	if err != nil || attempt < 1 {
		return state.OperationExecutionAuthority{}, state.ErrInvalidArgument
	}
	return state.OperationExecutionAuthority{AccountID: claims.AccountID, AppID: claims.AppID, InstanceID: claims.InstanceID,
		InvocationID: headers.Get(api.InvocationIDHeader), Attempt: attempt, Capability: headers.Get(api.OperationCapabilityHeader)}, nil
}

func (s *server) reportOperationProgress(w http.ResponseWriter, r *http.Request) {
	op, authority, ok := s.runtimeOperation(w, r)
	if !ok {
		return
	}
	store, _ := s.store.(state.OperationStore)
	var report api.OperationReportRequest
	if !decodeOperationBody(w, r, &report, api.OperationReportBodyMaxBytes) {
		return
	}
	got, err := store.ReportOperationProgress(r.Context(), op.ID, authority, report)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, got.OperationResponse)
}

func (s *server) recoverOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, store, ok := s.ownedOperation(w, r, acct)
	if !ok {
		return
	}
	var req api.OperationRecoveryRequest
	if !decodeOperationBody(w, r, &req, op.ValueMaxBytes+api.OperationRecoveryBodyOverheadBytes) {
		return
	}
	got, err := store.RecoverOperation(r.Context(), acct.ID, "", op.ID, req)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, got.OperationResponse)
}

func (s *server) getOperationExecutionControl(w http.ResponseWriter, r *http.Request) {
	authority, err := s.runtimeOperationAuthority(r)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required", "provide a current operation workload assertion"))
		return
	}
	if _, ok := s.operationStore(w); !ok {
		return
	}
	store, ok := s.store.(state.OperationExecutionControlStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation execution control is unavailable"))
		return
	}
	control, err := store.OperationExecutionControl(r.Context(), r.PathValue("id"), authority)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, control)
}
