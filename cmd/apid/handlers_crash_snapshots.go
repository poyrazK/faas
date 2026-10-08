package main

// Crash snapshot handlers (ADR-733).
//
//   GET  /v1/apps/{slug}/crash-snapshots/settings
//   PUT  /v1/apps/{slug}/crash-snapshots/settings   opt in or out
//   GET  /v1/apps/{slug}/crash-snapshots            list, newest first
//   POST /v1/apps/{slug}/crash-snapshots            capture now
//   GET  /v1/apps/{slug}/crash-snapshots/{id}
//   POST /v1/apps/{slug}/crash-snapshots/{id}/fork  open as an ADR-732 fork
//
// Every route answers 501 crash_snapshots_not_enabled until the operator
// sets FAAS_CRASH_SNAPSHOTS=1. Captures are only consumable as forks, so the
// plan gate is the fork entitlement.

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// crashSnapshotApp runs the operator gate, the plan gate and loads the app.
func (s *server) crashSnapshotApp(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, bool) {
	if !s.crashSnapshotsEnabled {
		api.WriteProblem(w, api.ErrCrashSnapshotsNotEnabled())
		return state.App{}, false
	}
	if perApp, _ := acct.Plan.AppForkLimits(); perApp < 1 {
		api.WriteProblem(w, api.ErrPlanAppForksNotAllowed(acct.Plan))
		return state.App{}, false
	}
	return s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // shared per-app loader.
}

func (s *server) getCrashSnapshotSettings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.crashSnapshotApp(w, r, acct)
	if !ok {
		return
	}
	settings, err := s.store.CrashSnapshotSettingsFor(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load crash snapshot settings"))
		return
	}
	writeJSON(w, http.StatusOK, crashSettingsResponse(settings))
}

func (s *server) putCrashSnapshotSettings(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.crashSnapshotApp(w, r, acct)
	if !ok {
		return
	}
	var req api.CrashSnapshotSettingsRequest
	if err := decodeJSONSized(r, &req, 1024); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid crash snapshot settings body"))
		return
	}
	settings, err := s.store.SetCrashSnapshotSettings(r.Context(), acct.ID, app.ID, req.Enabled, time.Now().UTC())
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not save crash snapshot settings"))
		return
	}
	s.audit.Emit(r.Context(), "app.crash_snapshots_configured", &acct.ID, map[string]any{
		"app_id": app.ID, "enabled": req.Enabled, "requested_by": appForkActor(r, acct),
	})
	writeJSON(w, http.StatusOK, crashSettingsResponse(settings))
}

func (s *server) listCrashSnapshots(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.crashSnapshotApp(w, r, acct)
	if !ok {
		return
	}
	captures, err := s.store.ListCrashCaptures(r.Context(), acct.ID, app.ID, 100)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list crash snapshots"))
		return
	}
	out := api.CrashCaptureListResponse{Items: make([]api.CrashCaptureResponse, 0, len(captures))}
	for _, c := range captures {
		out.Items = append(out.Items, crashCaptureResponse(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createCrashSnapshot(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.crashSnapshotApp(w, r, acct)
	if !ok {
		return
	}
	capture, err := s.store.RequestManualCrashCapture(r.Context(), acct.ID, app.ID, api.CrashCaptureCooldown, time.Now().UTC())
	if errors.Is(err, state.ErrCrashCaptureRefused) {
		api.WriteProblem(w, api.ErrCrashCaptureRefused())
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not request a crash snapshot"))
		return
	}
	s.audit.Emit(r.Context(), "app.crash_snapshot_requested", &acct.ID, map[string]any{
		"app_id": app.ID, "capture_id": capture.ID, "requested_by": appForkActor(r, acct),
	})
	writeJSON(w, http.StatusAccepted, crashCaptureResponse(capture))
}

func (s *server) getCrashSnapshot(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.crashSnapshotApp(w, r, acct)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		s.notFound(w, "no such crash snapshot")
		return
	}
	capture, err := s.store.CrashCaptureByID(r.Context(), acct.ID, app.ID, id)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such crash snapshot")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the crash snapshot"))
		return
	}
	writeJSON(w, http.StatusOK, crashCaptureResponse(capture))
}

// forkCrashSnapshot opens a capture as an ADR-732 fork. The route carries
// the fork route's scopes (deploy:write AND secrets:read).
func (s *server) forkCrashSnapshot(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.crashSnapshotsEnabled {
		api.WriteProblem(w, api.ErrCrashSnapshotsNotEnabled())
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		s.notFound(w, "no such crash snapshot")
		return
	}
	s.createAppForkFrom(w, r, acct, id)
}

func crashSettingsResponse(settings state.CrashSnapshotSettings) api.CrashSnapshotSettingsResponse {
	resp := api.CrashSnapshotSettingsResponse{Enabled: settings.Enabled}
	if !settings.UpdatedAt.IsZero() {
		at := settings.UpdatedAt.UTC().Format(time.RFC3339Nano)
		resp.UpdatedAt = &at
	}
	return resp
}

func crashCaptureResponse(c state.CrashCapture) api.CrashCaptureResponse {
	resp := api.CrashCaptureResponse{
		ID: c.ID, AppID: c.AppID, DeploymentID: c.DeploymentID, Trigger: c.Trigger,
		StatusCode: c.StatusCode, Route: c.Route, Status: string(c.Status), MemBytes: c.MemBytes,
		RequestedAt: c.RequestedAt.UTC().Format(time.RFC3339Nano),
		CapturedAt:  appTaskTimeResponse(c.CapturedAt),
		ExpiresAt:   appTaskTimeResponse(c.ExpiresAt),
	}
	if c.FailureCode != nil && c.FailureMessage != nil {
		resp.Failure = &api.AppForkFailure{Code: *c.FailureCode, Message: *c.FailureMessage}
	}
	return resp
}
