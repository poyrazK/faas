package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

// getAppOpenAPIContractDiff is the read-only half of ADR-121. It uses the
// same authoritative-document projection and differ as the imaged promotion
// gate, so CI can inspect the exact result that would block a production
// deployment.
func (s *server) getAppOpenAPIContractDiff(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "prod"
	}
	if problem := api.ValidateScope(scope); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if !api.ApiContractDiffEnabled() {
		api.WriteProblem(w, api.ErrAPIContractDiffDisabled())
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}

	check, err := openapidiff.CheckPromotion(r.Context(), s.store, app.ID, "contract-preview", scope)
	if err != nil && !errors.Is(err, openapidiff.ErrSnapshotBaselineMissing) {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"API contract diff unavailable", err.Error()))
		return
	}
	resp := api.OpenAPIContractDiffResponse{
		AppID: app.ID, Scope: scope, Source: check.ProposedSource, ProposedSHA256: check.Diff.ProposedSHA256,
		Blocking: check.Diff.Blocking(),
		Breaks:   contractBreaks(check.Diff), Unknowns: contractUnknowns(check.Diff), Additions: contractAdditions(check.Diff),
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

func contractUnknowns(diff openapidiff.SnapshotDiff) []api.OpenAPIContractUnknown {
	out := make([]api.OpenAPIContractUnknown, 0, len(diff.Unknowns))
	for _, unknown := range diff.Unknowns {
		out = append(out, api.OpenAPIContractUnknown{
			Path: unknown.Path, Method: unknown.Method, Status: unknown.Status,
			PathInSchema: unknown.PathInSchema, Code: string(unknown.Code),
		})
	}
	return out
}

func contractGateProblem(diff openapidiff.SnapshotDiff) *api.Problem {
	detail := (&openapidiff.GateError{Diff: diff}).Error()
	if len(diff.Breaks) == 0 && len(diff.Unknowns) > 0 {
		return api.ErrAPIContractComparisonIncomplete(detail)
	}
	return api.ErrAPIContractBreakingChange(detail)
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

func (s *server) contractTrafficContext(ctx context.Context, app state.App, d state.Deployment) (context.Context, *api.Problem) {
	if !api.ApiContractDiffEnabled() || !strings.EqualFold(strings.TrimSpace(d.Scope), "prod") {
		return ctx, nil
	}
	check, err := openapidiff.CheckLiveContract(ctx, s.store, app.ID, d.ID, "prod", true)
	if errors.Is(err, openapidiff.ErrSnapshotBaselineMissing) {
		return ctx, nil
	}
	if err != nil {
		return ctx, api.ErrCapacity("could not evaluate serving API contract")
	}
	check, fence, err := openapidiff.ApplyRemovalException(ctx, s.store, check, false)
	if err != nil {
		return ctx, api.ErrCapacity("could not evaluate route removal approval")
	}
	if check.Diff.Blocking() {
		return ctx, contractGateProblem(check.Diff)
	}
	if fence != nil {
		ctx = state.WithRouteRemovalFence(ctx, *fence)
	}
	return ctx, nil
}
