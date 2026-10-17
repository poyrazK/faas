// adr: 1302
package automationlive

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type BaselineService struct {
	Store state.AutomationLiveBaselineStore
}

func (s BaselineService) Get(w http.ResponseWriter, r *http.Request, appID string) {
	w.Header().Set("Cache-Control", "no-store")
	view, err := s.Store.GetAutomationLiveBaseline(r.Context(), appID, r.PathValue("name"), r.PathValue("baseline"))
	if err != nil {
		writeBaselineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
func (s BaselineService) History(w http.ResponseWriter, r *http.Request, appID string) {
	w.Header().Set("Cache-Control", "no-store")
	view, err := s.Store.GetAutomationLiveBaselineHistory(r.Context(), appID, r.PathValue("name"), r.PathValue("baseline"))
	if err != nil {
		writeBaselineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
func (s BaselineService) Promote(w http.ResponseWriter, r *http.Request, appID, actor, key string) {
	w.Header().Set("Cache-Control", "no-store")
	request, ok := readBaselineRequest(w, r)
	if !ok {
		return
	}
	view, err := s.Store.PromoteAutomationLiveBaseline(r.Context(), appID, r.PathValue("name"), r.PathValue("baseline"), r.PathValue("promotion_id"), actor, key, request)
	if err != nil {
		writeBaselineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
func readBaselineRequest(w http.ResponseWriter, r *http.Request) (api.PromoteAutomationLiveBaselineRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, api.AutomationLiveBaselineRequestMaxBytes)
	var wire struct {
		api.PromoteAutomationLiveBaselineRequest
		ExpectedRevision *int64 `json:"expected_revision"`
	}
	var body api.PromoteAutomationLiveBaselineRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.AutomationLiveBaselineRequestMaxBytes, api.AutomationLiveBaselineRequestMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("Provide one bounded promotion request; actor, verdict and receipt fields are not accepted."))
		}
		return body, false
	}
	if decoder.Decode(new(any)) != io.EOF {
		api.WriteProblem(w, api.ErrValidation("Provide exactly one promotion request."))
		return body, false
	}
	if wire.ExpectedRevision == nil {
		api.WriteProblem(w, api.ErrValidation("Provide expected_revision explicitly; 0 creates a new named baseline."))
		return body, false
	}
	body = wire.PromoteAutomationLiveBaselineRequest
	body.ExpectedRevision = *wire.ExpectedRevision
	return body, true
}
func writeBaselineError(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrAutomationLiveBaselineConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Baseline changed", "Reload the current baseline revision before a new promotion. Retry an uncertain request only with its original promotion ID, revision, target and policy."))
		return
	}
	writeSuiteError(w, err)
}

// List reads heads and retained evidence in one store snapshot.
func (s BaselineService) List(w http.ResponseWriter, r *http.Request, appID string) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.Store.(state.AutomationLiveBaselineListingStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Baseline discovery is unavailable."))
		return
	}
	view, err := store.ListAutomationLiveBaselines(r.Context(), appID, r.PathValue("name"))
	if err != nil {
		writeBaselineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s BaselineService) Retire(w http.ResponseWriter, r *http.Request, appID, actor, key string) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.Store.(state.AutomationLiveBaselineRetirementStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Baseline retirement is unavailable."))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.AutomationLiveBaselineRequestMaxBytes)
	var wire struct {
		ExpectedRevision *int64 `json:"expected_revision"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.AutomationLiveBaselineRequestMaxBytes, api.AutomationLiveBaselineRequestMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("Provide one retirement request with expected_revision only."))
		}
		return
	}
	if wire.ExpectedRevision == nil || decoder.Decode(new(any)) != io.EOF {
		api.WriteProblem(w, api.ErrValidation("Provide exactly one retirement request with an explicit expected_revision."))
		return
	}
	receipt, err := store.RetireAutomationLiveBaseline(r.Context(), appID, r.PathValue("name"), r.PathValue("baseline"), r.PathValue("retirement_id"), actor, key, api.RetireAutomationLiveBaselineRequest{ExpectedRevision: *wire.ExpectedRevision})
	if errors.Is(err, state.ErrAutomationLiveBaselineConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Baseline changed", "Reload the current baseline revision before a new retirement. Retry an uncertain request only with its original retirement ID and expected revision."))
		return
	}
	if err != nil {
		writeBaselineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}
