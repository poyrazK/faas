package apidserver

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/apid/runtasks"
	"github.com/onebox-faas/faas/pkg/runfile"
	"github.com/onebox-faas/faas/pkg/runtask"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) agentFileSigner() *runfiles.AgentFileSigner {
	if s.sessions == nil {
		return nil
	}
	signer, _ := runfiles.NewAgentFileSigner(s.sessions.BindingKey())
	return signer
}

func (s *server) createAgentExecutionFileGrant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.agentStepsAllowed(acct) {
		http.NotFound(w, r)
		return
	}
	tasks := s.executionTaskService(r)
	if !tasks.Enabled {
		api.WriteProblem(w, api.ErrCapacity("new Run task invocations are disabled"))
		return
	}
	if !requireExecutionEntitlement(w, acct) {
		return
	}
	access, p := executionAccessForRequest(r)
	if p != nil {
		writeExecutionAccessError(w, p)
		return
	}
	if !access.controlWide {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Account authority required", "delegating an uploaded file requires account-wide deploy authority"))
		return
	}

	var req api.CreateAgentExecutionFileGrantRequest
	if decodeExecutionTaskBody(w, r, &req, api.ExecutionContractSchemaMaxBytes) != nil ||
		!runfile.ValidID(req.EndpointID) ||
		!agents.ValidCallbackID(req.ThreadID, api.AgentCallbackThreadIDMaxBytes) ||
		!agents.ValidCallbackID(req.ToolCallID, api.AgentCallbackThreadIDMaxBytes) ||
		!runtask.ValidRef(req.TaskName, req.Version) ||
		len(req.StepLabel) > 96 || api.ValidateExecutionWorkflowMetadata("agent", req.StepLabel) != nil ||
		!runfile.ValidPath(req.Path, api.ExecutionBundleMaxPathSize) {
		api.WriteProblem(w, api.ErrValidation("provide a valid endpoint, thread, logical call ID, task version and relative input path"))
		return
	}
	if req.StepLabel != "" && !s.executionPipelinesEnabled {
		api.WriteProblem(w, api.ErrCapacity("new Run pipelines are disabled"))
		return
	}
	ctx := r.Context()
	inbox, ok := s.store.(agents.CallbackInbox)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Agent endpoints are unavailable"))
		return
	}
	endpoint, err := inbox.ManagedRealtimeEndpointByID(ctx, req.EndpointID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("Agent endpoint lookup failed"))
		return
	}
	if err != nil || endpoint.AccountID != acct.ID || !endpoint.Enabled {
		http.NotFound(w, r)
		return
	}
	if p := s.authorizeCallbackEndpoint(r, acct, endpoint); p != nil {
		api.WriteProblem(w, p)
		return
	}
	version, err := tasks.Get(ctx, acct.ID, req.TaskName, req.Version)
	if err != nil {
		api.WriteProblem(w, runtasks.Problem(err))
		return
	}
	if version.Status != "active" || !slices.Contains(version.AgentEndpointIDs, endpoint.ID) {
		api.WriteProblem(w, runtasks.Problem(state.ErrExecutionTaskUnavailable))
		return
	}
	file, err := s.executionFilesService().Get(ctx, runfiles.Access{Account: acct, Principal: access.principal, Wide: access.broad}, r.PathValue("id"))
	if err != nil {
		writeRunFileError(w, err)
		return
	}
	scope := runfiles.AgentFileScope{AccountID: acct.ID, EndpointID: endpoint.ID, ThreadID: req.ThreadID, ToolCallID: req.ToolCallID,
		TaskName: req.TaskName, Version: req.Version, StepLabel: req.StepLabel}
	grant, err := tasks.AgentFiles.Issue(file, scope, req.Path, time.Now())
	if err != nil {
		writeRunFileError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, grant)
}
