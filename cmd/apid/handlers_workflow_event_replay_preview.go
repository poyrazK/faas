package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewWorkflowEventReplay(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), api.EventReplayPreviewReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	options, problem := parseWorkflowEventReplayPreview(r.URL.Query())
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.WorkflowEventReplayPreviewStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("workflow event replay preview store"))
		return
	}
	result, err := store.PreviewWorkflowEventReplay(ctx, acct.ID, state.WorkflowEventReplayPreviewQuery{
		AppID: app.ID, WorkflowName: r.URL.Query().Get("workflow_name"),
		WorkflowEventReplayPreviewOptions: options,
	})
	if err != nil {
		problem := workflowEventReplayPreviewProblem(err, ctx.Err())
		if problem.Status >= http.StatusInternalServerError {
			s.log.ErrorContext(ctx, "read workflow event replay preview", "err", err)
		}
		api.WriteProblem(w, problem)
		return
	}
	for i := range result.Matches {
		result.Matches[i].ReceiptURL = eventReceiptURL(result.Matches[i].EventSource, result.Matches[i].EventID)
	}
	writeJSON(w, http.StatusOK, result)
}

func parseWorkflowEventReplayPreview(values url.Values) (api.WorkflowEventReplayPreviewOptions, *api.Problem) {
	parsed, problem := parseEventReplayPreview(values)
	if problem != nil {
		return api.WorkflowEventReplayPreviewOptions{}, problem
	}
	options := api.WorkflowEventReplayPreviewOptions{From: parsed.From, Until: parsed.Until, After: parsed.After, Limit: parsed.Limit}
	if strings.TrimSpace(values.Get("workflow_name")) == "" || len(values.Get("workflow_name")) > 256 {
		return options, api.ErrValidation("workflow_name must contain 1 to 256 non-whitespace bytes")
	}
	if err := options.Validate(); err != nil {
		return options, api.ErrValidation(err.Error())
	}
	return options, nil
}

func workflowEventReplayPreviewProblem(err, contextErr error) *api.Problem {
	switch {
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "App not found", "No current app is available for this account.")
	case errors.Is(err, state.ErrEventReplayPreviewQuery):
		return api.ErrValidation(err.Error())
	case contextErr != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		return api.NewProblem(http.StatusServiceUnavailable, "workflow_event_replay_preview_read_timeout", "Workflow replay preview read timed out", "Narrow the acceptance range or reduce the page limit and retry.")
	default:
		return api.ErrCapacity("failed to read retained workflow event replay preview")
	}
}
