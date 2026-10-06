package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// getDeploymentRoutePolicySnapshot serves the owner-scoped immutable gateway
// policy captured for one deployment. The snapshot API intentionally returns
// 404 for legacy deployments that have no historical policy evidence.
func (s *server) getDeploymentRoutePolicySnapshot(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	depID := r.PathValue("deployment")
	dep, err := s.store.DeploymentByID(r.Context(), depID)
	if err != nil || app.AccountID != acct.ID || dep.AppID != app.ID {
		s.notFound(w, "no such deployment")
		return
	}
	store, ok := s.store.(state.DeploymentRoutePolicySnapshotStore)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"deployment route policy snapshots are unavailable", "store does not support route policy snapshots"))
		return
	}
	snap, err := store.DeploymentRoutePolicySnapshotByDeployment(r.Context(), depID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "deployment_route_policy_snapshot_not_found",
				"no route policy snapshot was captured for this deployment", ""))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error",
			"failed to read deployment route policy snapshot", err.Error()))
		return
	}
	if snap.AppID != app.ID {
		s.notFound(w, "no such deployment")
		return
	}
	rules, err := state.UnmarshalDeploymentRoutePolicySnapshot(snap)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "invalid_deployment_route_policy_snapshot",
			"deployment route policy snapshot is invalid", err.Error()))
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	writeJSON(w, http.StatusOK, api.DeploymentRoutePolicySnapshotResponse{
		DeploymentID:  snap.DeploymentID,
		AppID:         snap.AppID,
		Scope:         snap.Scope,
		SHA256:        snap.SHA256,
		SchemaVersion: snap.SchemaVersion,
		CapturedAt:    snap.CapturedAt,
		Rules:         rules,
	})
}
