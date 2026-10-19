package ciapi

import (
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h Handler) executionStore(w http.ResponseWriter, acct state.Account) (ci.DispatchStore, bool) {
	if !h.available(w, acct) {
		return nil, false
	}
	store, ok := h.Store.(ci.DispatchStore)
	if !ok {
		httpkit.NotFound(w, "CI executions are not available")
		return nil, false
	}
	return store, true
}

func (h Handler) SubmitExecution(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.available(w, acct) {
		return
	}
	store, ok := h.Store.(ci.JobsStore)
	if !ok || !h.ExecutionReady || !h.Config.WorkerEnabled || !h.Config.TransportEnabled || !h.Config.DispatchAllows(acct.ID) {
		httpkit.NotFound(w, "CI execution admission is not available")
		return
	}
	var req ci.JobsSubmitRequest
	if !httpkit.DecodeJSONLimit(w, r, &req, api.CIRequestMaxBytes) {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !validJobsRequest(req, key) {
		api.WriteProblem(w, api.ErrValidation("execution requires a profile UUID or revision/binding/template UUIDs, a full lowercase commit SHA and Idempotency-Key"))
		return
	}
	row, replay, err := ci.AdmitJobs(r.Context(), h.Config, store, h.VerifyGitHub, acct.ID, key, req)
	if err != nil {
		writeError(w, err)
		return
	}
	if replay {
		w.Header().Set("Idempotency-Replayed", "true")
	} else {
		h.audit(r, acct.ID, "ci.execution.admitted", map[string]any{"execution_id": row.ID, "revision_id": row.RevisionID})
	}
	httpkit.WriteJSON(w, http.StatusAccepted, executionResponse(row))
}

func validJobsRequest(req ci.JobsSubmitRequest, key string) bool {
	if !ci.ValidCommitSHA(req.CommitSHA) || !ci.ValidIdempotencyKey(key) {
		return false
	}
	if req.ProfileID != "" {
		return ci.ValidID(req.ProfileID) && req.RevisionID == "" && req.BindingID == "" && len(req.Templates) == 0
	}
	return ci.ValidExecutionProfileRequest(ci.ExecutionProfileRequest{RevisionID: req.RevisionID, BindingID: req.BindingID, Templates: req.Templates})
}

func (h Handler) ListExecutions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := h.executionStore(w, acct)
	if !ok {
		return
	}
	rows, err := store.ListJobsExecutions(r.Context(), acct.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{"executions": rows})
}

func (h Handler) ownedExecution(w http.ResponseWriter, r *http.Request, acct state.Account) (ci.DispatchStore, ci.Execution, bool) {
	store, ok := h.executionStore(w, acct)
	if !ok || !validID(w, r.PathValue("id")) {
		return nil, ci.Execution{}, false
	}
	row, err := store.Execution(r.Context(), acct.ID, r.PathValue("id"))
	if err == nil && row.Mode != ci.ExecutionJobs {
		err = ci.ErrNotFound
	}
	if err != nil {
		writeError(w, err)
		return nil, ci.Execution{}, false
	}
	return store, row, true
}

func (h Handler) Execution(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, row, ok := h.ownedExecution(w, r, acct)
	if !ok {
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, executionResponse(row))
}

func (h Handler) CancelExecution(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, row, ok := h.ownedExecution(w, r, acct)
	if !ok {
		return
	}
	row, err := store.CancelExecution(r.Context(), acct.ID, row.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	h.audit(r, acct.ID, "ci.execution.cancelled", map[string]any{"execution_id": row.ID})
	httpkit.WriteJSON(w, http.StatusOK, executionResponse(row))
}

func (h Handler) ExecutionLogs(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, row, ok := h.ownedExecution(w, r, acct)
	if !ok {
		return
	}
	offset, limit, err := logCursor(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if !validID(w, r.PathValue("attempt")) {
		return
	}
	page, err := ci.ExecutionLogs(r.Context(), store, acct.ID, row.ID, r.PathValue("attempt"), offset, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, page)
}

func logCursor(r *http.Request) (int, int, error) {
	query := r.URL.Query()
	offset, limit := 0, api.CILogPageMaxBytes
	for _, key := range []string{"offset", "limit"} {
		if values, ok := query[key]; ok {
			if len(values) != 1 {
				return 0, 0, ci.ErrLogCursor
			}
			value, err := strconv.Atoi(values[0])
			if err != nil {
				return 0, 0, ci.ErrLogCursor
			}
			if key == "offset" {
				offset = value
			} else {
				limit = value
			}
		}
	}
	if offset < 0 || limit < 1 || limit > api.CILogPageMaxBytes {
		return 0, 0, ci.ErrLogCursor
	}
	return offset, limit, nil
}

// Logs have their own bounded byte cursor. Physical image storage keys and
// internal repository authority are never part of the HTTP response.
func executionResponse(row ci.Execution) ci.Execution {
	row.Compute = nil
	row.Jobs = append([]ci.JobExecution(nil), row.Jobs...)
	for i, job := range row.Jobs {
		if job.Attempt != nil {
			attempt := *job.Attempt
			attempt.Logs = nil
			row.Jobs[i].Attempt = &attempt
		}
	}
	return row
}
