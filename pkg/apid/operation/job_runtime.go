// adr: 664
package operation

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func jobOperationAuthorityHeaders(headers http.Header) (state.JobOperationAuthority, error) {
	allowed := map[string]bool{}
	for _, name := range []string{api.OperationJobRunHeader, api.OperationJobInstanceHeader, api.OperationJobCapabilityHeader, api.OperationGenerationHeader, api.OperationAttemptHeader} {
		allowed[strings.ToLower(name)] = true
		if len(headers.Values(name)) != 1 {
			return state.JobOperationAuthority{}, state.ErrNotFound
		}
	}
	for name := range headers {
		lower := strings.ToLower(name)
		if lower == "authorization" || lower == strings.ToLower(api.InvocationIDHeader) || strings.HasPrefix(lower, "x-gregale-operation-") && !allowed[lower] {
			return state.JobOperationAuthority{}, state.ErrNotFound
		}
	}
	a := state.JobOperationAuthority{RunID: headers.Get(api.OperationJobRunHeader), InstanceID: headers.Get(api.OperationJobInstanceHeader), Capability: headers.Get(api.OperationJobCapabilityHeader)}
	if _, err := uuid.Parse(a.RunID); err != nil {
		return a, state.ErrNotFound
	}
	if _, err := uuid.Parse(a.InstanceID); err != nil {
		return a, state.ErrNotFound
	}
	generation, err := strconv.Atoi(headers.Get(api.OperationGenerationHeader))
	if err != nil || generation < 1 {
		return a, state.ErrNotFound
	}
	a.Generation = generation
	attempt, err := strconv.Atoi(headers.Get(api.OperationAttemptHeader))
	if err != nil || attempt != 1 {
		return a, state.ErrNotFound
	}
	a.Attempt = attempt
	if raw, err := hex.DecodeString(a.Capability); err != nil || len(raw) != 32 {
		return a, state.ErrNotFound
	}
	return a, nil
}
func (s *Service) jobOperationRuntime(w http.ResponseWriter, r *http.Request) (state.JobOperationStore, state.JobOperationAuthority, bool) {
	a, err := jobOperationAuthorityHeaders(r.Header)
	if err != nil {
		WriteOperationError(w, err)
		return nil, a, false
	}
	adapter, ok := s.Host.Store().(state.JobOperationStore)
	if !ok {
		WriteOperationError(w, state.ErrNotFound)
		return nil, a, false
	}
	op, exists, err := adapter.OperationForJobRun(r.Context(), a.RunID)
	if err != nil || !exists || op.ID != r.PathValue("id") {
		WriteOperationError(w, state.ErrNotFound)
		return nil, a, false
	}
	// Use native task/instance evidence. The legacy InstanceByID read omits
	// Job metadata in PostgreSQL and cannot establish runtime authority.
	if _, err := adapter.OperationJobControl(r.Context(), op.ID, a); err != nil {
		WriteOperationError(w, err)
		return nil, a, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return adapter, a, true
}
func (s *Service) GetJobOperationControl(w http.ResponseWriter, r *http.Request) {
	store, a, ok := s.jobOperationRuntime(w, r)
	if !ok {
		return
	}
	response, err := store.OperationJobControl(r.Context(), r.PathValue("id"), a)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, response)
}
func (s *Service) reportJobOperation(w http.ResponseWriter, r *http.Request) {
	store, a, ok := s.jobOperationRuntime(w, r)
	if !ok {
		return
	}
	var report api.OperationJobReportRequest
	bound := api.OperationReportBodyMaxBytes
	if r.PathValue("kind") == "result" {
		bound = api.MustLimitsFor(api.PlanScale).MaxSourceBytesPerInvocation + api.OperationReportBodyMaxBytes
	}
	if !DecodeOperationBody(w, r, &report, bound) {
		return
	}
	response, err := store.ReportOperationJob(r.Context(), r.PathValue("id"), a, r.PathValue("kind"), report)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, response.OperationResponse)
}

func (s *Service) ReportJobOperationProgress(w http.ResponseWriter, r *http.Request) {
	r.SetPathValue("kind", "progress")
	s.reportJobOperation(w, r)
}
func (s *Service) ReportJobOperationResult(w http.ResponseWriter, r *http.Request) {
	r.SetPathValue("kind", "result")
	s.reportJobOperation(w, r)
}
