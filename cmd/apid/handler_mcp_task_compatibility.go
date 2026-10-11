package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) putMCPTaskCompatibilityReport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !acct.Plan.CustomMetricsAllowed() {
		api.WriteProblem(w, api.ErrPlanCustomMetricsNotAllowed(acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.MCPTaskCompatibilityMaxRequestBytes))
	decoder.DisallowUnknownFields()
	var req api.MCPTaskCompatibilityReportRequest
	if err := decoder.Decode(&req); err != nil {
		writeMCPTaskCompatibilityDecodeProblem(w, err)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid MCP task compatibility report", "body must contain exactly one JSON object."))
		return
	}
	if problem := api.ValidateMCPTaskCompatibilityReport(req); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	sort.Slice(req.Handlers, func(i, j int) bool {
		if req.Handlers[i].WaitingTasks != req.Handlers[j].WaitingTasks {
			return req.Handlers[i].WaitingTasks > req.Handlers[j].WaitingTasks
		}
		if req.Handlers[i].ToolName != req.Handlers[j].ToolName {
			return req.Handlers[i].ToolName < req.Handlers[j].ToolName
		}
		return req.Handlers[i].HandlerVersion < req.Handlers[j].HandlerVersion
	})
	stored := state.MCPTaskCompatibilityReport{
		Handlers:   make([]state.MCPTaskHandlerCompatibility, 0, len(req.Handlers)),
		Truncated:  req.Truncated,
		ObservedAt: time.Now().UTC(),
	}
	for _, handler := range req.Handlers {
		item := state.MCPTaskHandlerCompatibility{
			ToolName: handler.ToolName, HandlerVersion: handler.HandlerVersion,
			WaitingTasks: handler.WaitingTasks, LiveCompatibleWorkers: handler.LiveCompatibleWorkers,
			UnknownDeploymentWorkers: handler.UnknownDeploymentWorkers,
			Deployments: make([]state.MCPTaskHandlerDeploymentWorkers, 0, len(handler.Deployments)),
		}
		var attributedWorkers int64
		for _, deployment := range handler.Deployments {
			attributedWorkers += deployment.Workers
			item.Deployments = append(item.Deployments, state.MCPTaskHandlerDeploymentWorkers{
				DeploymentID: deployment.DeploymentID, Workers: deployment.Workers,
			})
		}
		item.UnknownDeploymentWorkers += handler.LiveCompatibleWorkers - attributedWorkers - item.UnknownDeploymentWorkers
		stored.Handlers = append(stored.Handlers, item)
	}
	if err := s.store.PutMCPTaskCompatibilityReport(r.Context(), app.ID, stored); err != nil {
		writeCustomerInternalProblem(w, r, s.log, "save MCP task compatibility report",
			"Gregale could not save this MCP task compatibility report.",
			"Retry the request in a moment; if it continues, contact support.", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) getMCPTaskCompatibilityReport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	response := api.MCPTaskCompatibilityReportResponse{
		Handlers:         []api.MCPTaskHandlerCompatibility{},
		FreshnessSeconds: api.MCPTaskCompatibilityFreshnessSeconds,
		Stale:            true,
	}
	report, err := s.store.GetMCPTaskCompatibilityReport(r.Context(), app.ID)
	if errors.Is(err, state.ErrNotFound) {
		writeJSON(w, http.StatusOK, response)
		return
	}
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "load MCP task compatibility report",
			"Gregale could not load the MCP task compatibility report.",
			"Refresh the page or retry the request in a moment.", err)
		return
	}
	response.Published = true
	response.Truncated = report.Truncated
	observedAt := report.ObservedAt.UTC()
	response.ObservedAt = &observedAt
	age := time.Since(report.ObservedAt)
	response.Stale = age < 0 || age > time.Duration(response.FreshnessSeconds)*time.Second
	response.Handlers = make([]api.MCPTaskHandlerCompatibility, 0, len(report.Handlers))
	for _, handler := range report.Handlers {
		item := api.MCPTaskHandlerCompatibility{
			ToolName: handler.ToolName, HandlerVersion: handler.HandlerVersion,
			WaitingTasks: handler.WaitingTasks, LiveCompatibleWorkers: handler.LiveCompatibleWorkers,
			UnknownDeploymentWorkers: handler.UnknownDeploymentWorkers,
			Deployments: make([]api.MCPTaskHandlerDeploymentWorkers, 0, len(handler.Deployments)),
		}
		for _, deployment := range handler.Deployments {
			item.Deployments = append(item.Deployments, api.MCPTaskHandlerDeploymentWorkers{
				DeploymentID: deployment.DeploymentID, Workers: deployment.Workers,
			})
		}
		response.Handlers = append(response.Handlers, item)
	}
	writeJSON(w, http.StatusOK, response)
}

func writeMCPTaskCompatibilityDecodeProblem(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, api.CodeValidation,
			"MCP task compatibility report is too large", "body must not exceed 128 KiB."))
		return
	}
	api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
		"Invalid MCP task compatibility report", "body must be a JSON object with a handlers array and optional truncated boolean."))
}
