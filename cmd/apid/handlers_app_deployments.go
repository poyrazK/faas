package main

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// appDeploymentCursorLister is implemented by both production stores. It is
// deliberately kept outside state.Store so adding this public read does not
// widen the large internal store interface for unrelated fakes.
type appDeploymentCursorLister interface {
	ListDeploymentsForAppBefore(ctx context.Context, appID string, before time.Time, limit int) ([]state.Deployment, error)
}

// listAppDeployments serves GET /v1/apps/{slug}/deployments. The app is
// resolved before touching deployment rows, preserving the IDOR-safe 404
// contract. The cursor is the created_at timestamp of the last row, matching
// the account-wide deployment list shape.
func (s *server) listAppDeployments(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > 200 {
				n = 200
			}
			limit = n
		}
	}

	var before time.Time
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			parsed, err = time.Parse(time.RFC3339, raw)
		}
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Bad cursor", "expected RFC3339 timestamp"))
			return
		}
		before = parsed
	}

	rows, err := s.listDeploymentsForAppBefore(r.Context(), app.ID, before, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list app deployments"))
		return
	}
	resp := api.DeploymentListResponse{Items: make([]api.DeploymentResponse, 0, len(rows))}
	for _, d := range rows {
		resp.Items = append(resp.Items, s.deploymentResponse(d, app))
	}
	if len(rows) == limit && len(rows) > 0 {
		resp.NextBefore = rows[len(rows)-1].CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) listDeploymentsForAppBefore(ctx context.Context, appID string, before time.Time, limit int) ([]state.Deployment, error) {
	if lister, ok := s.store.(appDeploymentCursorLister); ok {
		return lister.ListDeploymentsForAppBefore(ctx, appID, before, limit)
	}

	// Compatibility fallback for narrow test stores that predate this
	// optional method. Production PgStore and MemStore both take the indexed
	// path above; this branch keeps older embedders source-compatible.
	rows, err := s.store.ListDeploymentsForApp(ctx, appID, 0, 0)
	if err != nil {
		return nil, err
	}
	filtered := rows[:0]
	for _, d := range rows {
		if before.IsZero() || d.CreatedAt.Before(before) {
			filtered = append(filtered, d)
		}
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}
