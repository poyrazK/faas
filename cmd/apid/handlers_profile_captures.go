package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// On-demand profile captures (ADR-967). apid records the request in
// profile_captures and notifies schedd, which runs the capture through vmmd.
// apid never calls schedd or vmmd.

func (s *server) profileCaptureTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.ProfileCaptureStore, bool) {
	if !s.profileCapturesEnabled {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "On-demand profiling unavailable", "on-demand profiling is not enabled on this installation"))
		return state.App{}, nil, false
	}
	if !api.MustLimitsFor(acct.Plan).Profiling.Enabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("profiling", acct.Plan))
		return state.App{}, nil, false
	}
	store, ok := s.store.(state.ProfileCaptureStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("profile capture storage is unavailable"))
		return state.App{}, nil, false
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	return app, store, ok
}

func (s *server) createProfileCapture(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.profileCaptureTarget(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateProfileCaptureRequest
	if err := decodeJSONSized(r, &req, api.ProfileControlMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid profile capture request"))
		return
	}
	capture, problem := s.queueProfileCapture(r.Context(), acct, app, store, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusAccepted, capture)
}

// queueProfileCapture validates, stores and announces one capture. The
// queued row is the source of truth; schedd's safety tick claims it if the
// notification is lost.
func (s *server) queueProfileCapture(ctx context.Context, acct state.Account, app state.App, store state.ProfileCaptureStore, req api.CreateProfileCaptureRequest) (api.ProfileCapture, *api.Problem) {
	if err := req.Normalize(); err != nil {
		return api.ProfileCapture{}, api.ErrValidation(err.Error())
	}
	now := time.Now().UTC()
	capture := api.ProfileCapture{ID: uuid.NewString(), AppID: app.ID, Status: api.ProfileCaptureQueued, Kinds: req.Kinds,
		DurationSeconds: req.DurationSeconds, RequestedInstanceID: req.InstanceID, Profiles: []api.ProfileCaptureProfile{},
		CreatedAt: now, ExpiresAt: now.Add(api.ProfileCaptureRetention)}
	if err := store.CreateProfileCapture(ctx, acct.ID, app.ID, capture); err != nil {
		return capture, profileCaptureCreateProblem(err)
	}
	payload, _ := json.Marshal(map[string]string{"capture_id": capture.ID})
	if err := s.notif.Notify(ctx, db.NotifyProfileCapture, string(payload)); err != nil {
		s.log.Warn("apid: profile capture notify failed", "capture_id", capture.ID, "err", err)
	}
	return capture, nil
}

func profileCaptureCreateProblem(err error) *api.Problem {
	switch {
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "App not found", "the app does not exist")
	case errors.Is(err, state.ErrProfileCaptureActive):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Capture already running", "wait for the app's current profile capture to finish")
	case errors.Is(err, state.ErrProfileCaptureQuota):
		return api.NewProblem(http.StatusTooManyRequests, api.CodeQuotaExhausted, "Profile capture limit reached",
			fmt.Sprintf("an account can start %d profile captures per hour", api.ProfileCaptureMaxPerAccountHour)).
			WithLimit(api.ProfileCaptureMaxPerAccountHour, api.ProfileCaptureMaxPerAccountHour)
	}
	return api.ErrCapacity("profile capture could not be queued")
}

func (s *server) listProfileCaptures(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.profileCaptureTarget(w, r, acct)
	if !ok {
		return
	}
	captures, err := store.ListProfileCaptures(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("profile captures are unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, api.ProfileCaptureList{Captures: captures})
}

func (s *server) getProfileCapture(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.profileCaptureTarget(w, r, acct)
	if !ok {
		return
	}
	capture, ok := s.ownedProfileCapture(w, r, acct, app, store)
	if ok {
		writeJSON(w, http.StatusOK, capture)
	}
}

func (s *server) ownedProfileCapture(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, store state.ProfileCaptureStore) (api.ProfileCapture, bool) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Capture not found", "profile capture does not exist"))
		return api.ProfileCapture{}, false
	}
	capture, err := store.GetProfileCapture(r.Context(), acct.ID, app.ID, id)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Capture not found", "profile capture does not exist"))
		return capture, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("profile capture is unavailable"))
		return capture, false
	}
	return capture, true
}
