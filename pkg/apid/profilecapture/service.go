// Package profilecapture is apid's on-demand profile capture surface
// (ADR-1155). apid records each request in profile_captures and notifies
// schedd, which runs the capture through vmmd. apid never calls schedd or
// vmmd.
//
// Everything it needs from apid goes through Host, which apidserver
// implements by reading its own fields at call time.
package profilecapture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

// Notifier is the pg_notify surface that announces a queued capture to schedd.
type Notifier interface {
	Notify(ctx context.Context, channel, payload string) error
}

// Host is what the profile capture surface needs from apid.
type Host interface {
	httpkit.AppLoader
	Store() state.Store
	Logger() *slog.Logger
	Notifier() Notifier
	// SourceProvenance is the deployment origin continuous profiles link
	// frames against; captures link the same way.
	SourceProvenance(app state.App, dep state.Deployment) profiling.SourceProvenance
}

// Service is the profile capture surface.
type Service struct {
	Host Host

	// Enabled mirrors FAAS_PROFILING_ON_DEMAND. When false every capture
	// route answers 404 and the dashboard hides the capture panel.
	Enabled bool
}

// CaptureStore returns the capture store when on-demand profiling is
// enabled and the configured store supports it.
func (s *Service) CaptureStore() (state.ProfileCaptureStore, bool) {
	if !s.Enabled {
		return nil, false
	}
	store, ok := s.Host.Store().(state.ProfileCaptureStore)
	return store, ok
}

// Target gates a capture request on installation, plan and store, then
// loads the app named by the {slug} path value. On failure it has already
// written the response.
func (s *Service) Target(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.ProfileCaptureStore, bool) {
	if !s.Enabled {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "On-demand profiling unavailable", "on-demand profiling is not enabled on this installation"))
		return state.App{}, nil, false
	}
	if !api.MustLimitsFor(acct.Plan).Profiling.Enabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("profiling", acct.Plan))
		return state.App{}, nil, false
	}
	store, ok := s.CaptureStore()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("profile capture storage is unavailable"))
		return state.App{}, nil, false
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	return app, store, ok
}

// Create handles POST /v1/apps/{slug}/profiles/captures.
func (s *Service) Create(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.Target(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateProfileCaptureRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.ProfileControlMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid profile capture request"))
		return
	}
	capture, problem := s.Queue(r.Context(), acct, app, store, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	httpkit.WriteJSON(w, http.StatusAccepted, capture)
}

// Queue validates, stores and announces one capture. The queued row is the
// source of truth; schedd's safety tick claims it if the notification is
// lost.
func (s *Service) Queue(ctx context.Context, acct state.Account, app state.App, store state.ProfileCaptureStore, req api.CreateProfileCaptureRequest) (api.ProfileCapture, *api.Problem) {
	if err := req.Normalize(); err != nil {
		return api.ProfileCapture{}, api.ErrValidation(err.Error())
	}
	now := time.Now().UTC()
	capture := api.ProfileCapture{ID: uuid.NewString(), AppID: app.ID, Status: api.ProfileCaptureQueued, Kinds: req.Kinds,
		DurationSeconds: req.DurationSeconds, RequestedInstanceID: req.InstanceID, Profiles: []api.ProfileCaptureProfile{},
		CreatedAt: now, ExpiresAt: now.Add(api.ProfileCaptureRetention)}
	if err := store.CreateProfileCapture(ctx, acct.ID, app.ID, capture, api.MustLimitsFor(acct.Plan).Profiling.CaptureStorageBytes); err != nil {
		return capture, createProblem(err)
	}
	payload, _ := json.Marshal(map[string]string{"capture_id": capture.ID})
	if err := s.Host.Notifier().Notify(ctx, db.NotifyProfileCapture, string(payload)); err != nil {
		s.Host.Logger().Warn("apid: profile capture notify failed", "capture_id", capture.ID, "err", err)
	}
	return capture, nil
}

func createProblem(err error) *api.Problem {
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
	var full *state.ProfileCaptureStorageError
	if errors.As(err, &full) {
		return api.NewProblem(http.StatusTooManyRequests, api.CodeQuotaExhausted, "Profile capture storage full",
			fmt.Sprintf("the account's profile captures use %d of %d bytes; captures expire %d days after they are taken",
				full.Used, full.Limit, int(api.ProfileCaptureRetention.Hours()/24))).
			WithByteLimit(full.Limit, full.Used).
			WithDocs("https://gregale.dev/docs/profiling#capture-a-running-instance-on-demand")
	}
	return api.ErrCapacity("profile capture could not be queued")
}

// List handles GET /v1/apps/{slug}/profiles/captures.
func (s *Service) List(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.Target(w, r, acct)
	if !ok {
		return
	}
	captures, err := store.ListProfileCaptures(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("profile captures are unavailable"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, api.ProfileCaptureList{Captures: captures})
}

// Get handles GET /v1/apps/{slug}/profiles/captures/{id}.
func (s *Service) Get(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.Target(w, r, acct)
	if !ok {
		return
	}
	capture, ok := s.Owned(w, r, acct, app, store)
	if ok {
		httpkit.WriteJSON(w, http.StatusOK, capture)
	}
}

// Owned loads the capture named by the {id} path value, collapsing
// malformed, missing and other apps' captures to the same 404.
func (s *Service) Owned(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, store state.ProfileCaptureStore) (api.ProfileCapture, bool) {
	capture, problem := load(r.Context(), acct, app, store, r.PathValue("id"))
	if problem != nil {
		api.WriteProblem(w, problem)
		return capture, false
	}
	return capture, true
}

func load(ctx context.Context, acct state.Account, app state.App, store state.ProfileCaptureStore, id string) (api.ProfileCapture, *api.Problem) {
	notFound := api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Capture not found", "profile capture does not exist")
	if _, err := uuid.Parse(id); err != nil {
		return api.ProfileCapture{}, notFound
	}
	capture, err := store.GetProfileCapture(ctx, acct.ID, app.ID, id)
	if errors.Is(err, state.ErrNotFound) {
		return capture, notFound
	}
	if err != nil {
		return capture, api.ErrCapacity("profile capture is unavailable")
	}
	return capture, nil
}
