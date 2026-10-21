// ADR-715: public business facts share existing owner and execution boundaries.
package operation

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) operationMilestoneStore(w http.ResponseWriter) (state.OperationMilestoneStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.Host.Store().(state.OperationMilestoneStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("operation milestones unavailable"))
	}
	return store, ok
}

func (s *Service) ReportOperationMilestone(w http.ResponseWriter, r *http.Request) {
	op, authority, ok := s.runtimeOperation(w, r)
	if !ok {
		return
	}
	store, ok := s.operationMilestoneStore(w)
	if !ok {
		return
	}
	var report api.OperationMilestoneRequest
	if !DecodeOperationBody(w, r, &report, api.OperationMilestonePayloadMaxBytes+api.OperationStartBodyOverheadBytes) {
		return
	}
	milestone, err := store.ReportOperationMilestone(r.Context(), op.ID, authority, report)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, milestone)
}

func (s *Service) ValidateOperationMilestones(w http.ResponseWriter, r *http.Request) {
	op, authority, ok := s.runtimeOperation(w, r)
	if !ok {
		return
	}
	store, ok := s.operationMilestoneStore(w)
	if !ok {
		return
	}
	var batch api.OperationMilestoneValidationRequest
	if !DecodeOperationBody(w, r, &batch, api.OperationMilestoneBatchMaxBytes) {
		return
	}
	if err := store.ValidateOperationMilestones(r.Context(), op.ID, authority, batch.Milestones); err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, api.OperationMilestoneValidationResponse{Valid: true})
}

func (s *Service) ReportOperationWorkflowState(w http.ResponseWriter, r *http.Request) {
	op, authority, ok := s.runtimeOperation(w, r)
	if !ok {
		return
	}
	store, ok := s.operationMilestoneStore(w)
	if !ok {
		return
	}
	var report api.OperationWorkflowStateReport
	if !DecodeOperationBody(w, r, &report, api.OperationWorkflowStateBatchMaxBytes) {
		return
	}
	response, err := store.ReportOperationWorkflowState(r.Context(), op.ID, authority, report)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, response)
}

func (s *Service) ValidateOperationWorkflowStates(w http.ResponseWriter, r *http.Request) {
	op, authority, ok := s.runtimeOperation(w, r)
	if !ok {
		return
	}
	store, ok := s.operationMilestoneStore(w)
	if !ok {
		return
	}
	var batch api.OperationWorkflowStateValidationRequest
	if !DecodeOperationBody(w, r, &batch, api.OperationWorkflowStateBatchMaxBytes) {
		return
	}
	if err := store.ValidateOperationWorkflowStates(r.Context(), op.ID, authority, batch.WorkflowStates, batch.Milestones); err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, api.OperationWorkflowStateValidationResponse{Valid: true})
}

func OperationMilestoneHistoryOptions(r *http.Request, operator, bySubject bool, appID string) (api.OperationMilestoneListOptions, error) {
	opts := api.OperationMilestoneListOptions{AppID: appID}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return opts, state.ErrInvalidArgument
	}
	for key, values := range query {
		if len(values) != 1 || values[0] == "" {
			return opts, state.ErrInvalidArgument
		}
		switch key {
		case "limit", "cursor":
		case "scope", "subject_type", "subject_id", "workflow", "workflow_instance_id", "workflow_state_cursor", "stale_only":
			if !bySubject {
				return opts, state.ErrInvalidArgument
			}
		case "app_id":
			if operator || !bySubject {
				return opts, state.ErrInvalidArgument
			}
			opts.AppID = values[0]
		case "tenant_id":
			if !operator || !bySubject {
				return opts, state.ErrInvalidArgument
			}
			opts.TenantID = values[0]
		default:
			return opts, state.ErrInvalidArgument
		}
	}
	if bySubject {
		opts.Scope, opts.SubjectType, opts.SubjectID = query.Get("scope"), query.Get("subject_type"), query.Get("subject_id")
		if opts.SubjectType == "" || opts.SubjectID == "" {
			return opts, state.ErrInvalidArgument
		}
		opts.Workflow, opts.WorkflowInstanceID = query.Get("workflow"), query.Get("workflow_instance_id")
		opts.WorkflowStateCursor = query.Get("workflow_state_cursor")
		if query.Get("stale_only") != "" {
			if query.Get("stale_only") != "true" && query.Get("stale_only") != "false" {
				return opts, state.ErrInvalidArgument
			}
			opts.WorkflowStaleOnly = query.Get("stale_only") == "true"
		}
		if (opts.Workflow == "") != (opts.WorkflowInstanceID == "") || opts.Workflow != "" &&
			(api.ValidateOperationWorkflowName(opts.Workflow) != nil || api.ValidateOperationWorkflowInstanceID(opts.WorkflowInstanceID) != nil) {
			return opts, state.ErrInvalidArgument
		}
	}
	opts.Cursor = query.Get("cursor")
	if query.Has("limit") {
		opts.Limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || opts.Limit < 1 {
			return opts, state.ErrInvalidArgument
		}
	}
	return opts, nil
}

func (s *Service) GetAccountOperationMilestones(w http.ResponseWriter, r *http.Request, acct state.Account) {
	op, _, ok := s.ownedOperation(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.operationMilestoneStore(w)
	if !ok {
		return
	}
	opts, err := OperationMilestoneHistoryOptions(r, true, false, op.AppID)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	opts.OperationID, opts.Scope = op.ID, op.Scope
	page, err := store.ListAccountOperationMilestones(r.Context(), acct.ID, opts)
	writeOperationMilestonePage(w, page, err)
}

func (s *Service) GetPlatformTenantSelfOperationMilestones(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, ok := s.activePlatformTenantSelfID(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.operationMilestoneStore(w)
	if !ok {
		return
	}
	operations, ok := s.OperationStore(w)
	if !ok {
		return
	}
	op, err := operations.OperationByID(r.Context(), acct.ID, tenant, r.PathValue("id"))
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	opts, err := OperationMilestoneHistoryOptions(r, false, false, op.AppID)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	opts.OperationID, opts.Scope = op.ID, op.Scope
	page, err := store.ListPlatformTenantOperationMilestones(r.Context(), acct.ID, tenant, opts)
	writeOperationMilestonePage(w, page, err)
}

func (s *Service) ListAccountBusinessMilestones(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.operationMilestoneStore(w)
	if !ok {
		return
	}
	opts, err := OperationMilestoneHistoryOptions(r, true, true, app.ID)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	page, err := store.ListAccountOperationMilestones(r.Context(), acct.ID, opts)
	writeOperationMilestonePage(w, page, err)
}

func (s *Service) ListPlatformTenantSelfBusinessMilestones(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, ok := s.activePlatformTenantSelfID(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.operationMilestoneStore(w)
	if !ok {
		return
	}
	opts, err := OperationMilestoneHistoryOptions(r, false, true, "")
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	page, err := store.ListPlatformTenantOperationMilestones(r.Context(), acct.ID, tenant, opts)
	writeOperationMilestonePage(w, page, err)
}

func writeOperationMilestonePage(w http.ResponseWriter, page api.OperationMilestonesResponse, err error) {
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, page)
}
