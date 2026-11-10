// adr: 1305
package storedworkflowconsole

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func (s *Service) retireBaseline(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.StoredWorkflowConsoleData) {
	ctx, cancel := context.WithTimeout(r.Context(), api.StoredWorkflowHealthReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	form := &dashboard.StoredWorkflowBaselineForm{Name: r.PostFormValue("baseline_name"), RetirementID: r.PostFormValue("retirement_id")}
	data.Baseline = form
	revision, parseErr := strconv.ParseInt(r.PostFormValue("baseline_revision"), 10, 64)
	id, idErr := uuid.Parse(form.RetirementID)
	if failure := s.selectStoredWorkflow(r, scope, &data, data.Name); failure != nil {
		data.Error = "The saved stored workflow is unavailable. Retirement was not attempted."
		s.render(w, r, scope, data, failure.Status)
		return
	}
	if !api.ValidStoredWorkflowLiveBaselineName(form.Name) || parseErr != nil || revision <= 0 || idErr != nil || id == uuid.Nil || r.PostFormValue("confirm_retirement") != "yes" {
		s.invalid(w, r, scope, data, "Reload the baseline and explicitly confirm permanent retirement.")
		return
	}
	form.RetirementRequest = &api.RetireStoredWorkflowLiveBaselineRequest{ExpectedRevision: revision}
	response := s.Host.Request(r, scope, Operation{Kind: "baseline-retire", Name: data.Name, BaselineName: form.Name, RetirementID: id.String(), Body: api.RetireStoredWorkflowLiveBaselineRequest{ExpectedRevision: revision}})
	var receipt api.StoredWorkflowLiveBaselineRetirement
	if !read(response, &receipt) || receipt.AppID != scope.AppID || receipt.Name != data.Name || receipt.BaselineName != form.Name || receipt.RetirementID != id.String() || receipt.Revision != revision+1 || receipt.RetiredAt.IsZero() {
		data.Error = "Retirement was not confirmed. Reload after a revision conflict. Retry an uncertain request only with its original retirement ID and revision."
		status := response.Status
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		s.render(w, r, scope, data, status)
		return
	}
	form.RetirementRequest = nil
	data.Message = fmt.Sprintf("Baseline %s retired at revision %d. Its name is reserved and its history is preserved.", form.Name, receipt.Revision)
	s.render(w, r, scope, data, http.StatusOK)
}
