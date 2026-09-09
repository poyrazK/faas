package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

// getAppOpenAPIContractDiff is the read-only half of ADR-121. It uses the
// same projection and differ as the imaged promotion gate, so CI can inspect
// the exact result that would block a production deployment.
func (s *server) getAppOpenAPIContractDiff(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !api.ApiContractDiffEnabled() {
		api.WriteProblem(w, api.ErrAPIContractDiffDisabled())
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "prod"
	}
	if problem := api.ValidateScope(scope); problem != nil {
		api.WriteProblem(w, problem)
		return
	}

	check, err := openapidiff.CheckPromotion(r.Context(), s.store, app.ID, "contract-preview", scope)
	if err != nil && !errors.Is(err, openapidiff.ErrSnapshotBaselineMissing) {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"API contract diff unavailable", err.Error()))
		return
	}
	resp := api.OpenAPIContractDiffResponse{
		AppID: app.ID, Scope: scope, ProposedSHA256: check.Diff.ProposedSHA256,
		Blocking: len(check.Diff.Breaks) > 0,
		Breaks:   contractBreaks(check.Diff), Additions: contractAdditions(check.Diff),
	}
	if check.HasBaseline {
		resp.BaselineDeploymentID = check.Baseline.DeploymentID
		resp.BaselineSHA256 = check.Diff.BaselineSHA256
		captured := check.Baseline.CapturedAt
		if !captured.IsZero() {
			resp.BaselineCapturedAt = &captured
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}

func contractBreaks(diff openapidiff.SnapshotDiff) []api.OpenAPIContractBreak {
	out := make([]api.OpenAPIContractBreak, 0, len(diff.Breaks))
	for _, b := range diff.Breaks {
		out = append(out, api.OpenAPIContractBreak{
			Path: b.Path, Method: b.Method, Status: b.Status, Kind: string(b.Kind),
			PathInSchema: b.PathInSchema, Before: b.Before, After: b.After,
		})
	}
	return out
}

func contractAdditions(diff openapidiff.SnapshotDiff) []api.OpenAPIContractAddition {
	out := make([]api.OpenAPIContractAddition, 0, len(diff.Additions))
	for _, a := range diff.Additions {
		out = append(out, api.OpenAPIContractAddition{
			Kind: string(a.Kind), Path: a.Path, Method: a.Method, Status: a.Status,
			PathInSchema: a.PathInSchema, Field: a.Field,
		})
	}
	return out
}
