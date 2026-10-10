package main

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

// getAppHeapProfile serves continuous heap profiles (ADR-967). Merged heap
// snapshots add up, so the query is narrowed to the last collection window
// before end: roughly one snapshot per instrumented process, a point-in-time
// view of the live heap across the deployment's instances.
func (s *server) getAppHeapProfile(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	q, err := parseProfileQuery(r.URL.Query())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("start and end must be RFC3339 timestamps"))
		return
	}
	dep, problem := s.profileQueryDeployment(r.Context(), acct, app, q)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	backend, ok := s.profileBackend.(profiling.HeapBackend)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("heap profiling is unavailable on this installation"))
		return
	}
	window := time.Duration(app.Manifest.Profiling.EffectiveWindowSeconds()) * time.Second
	if q.End.Sub(q.Start) > window {
		q.Start = q.End.Add(-window)
	}
	select {
	case s.profileQuerySlots <- struct{}{}:
		defer func() { <-s.profileQuerySlots }()
	default:
		api.WriteProblem(w, api.ErrCapacity("profile query capacity is busy"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ProfileQueryTimeout)
	defer cancel()
	scope := dep.Scope
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	p, err := backend.QueryHeap(ctx, app.AccountID, app.ID, scope, q)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("profile backend unavailable"))
		return
	}
	view, err := profiling.CaptureView(p, api.ProfileKindHeap)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("profile exceeds supported query bounds"))
		return
	}
	writeJSON(w, http.StatusOK, api.ProfileHeapResponse{Query: q, ProfileCaptureView: view})
}
