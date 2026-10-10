package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

// Dashboard surface for on-demand profile captures (ADR-967). The JSON API
// handlers in handlers_profile_captures.go own validation and storage.

const profileCaptureAction = "profile_capture"
const profileCaptureCookie = "faas_csrf_profile_capture"

// profileCapturesView builds the profiles-page capture panel; nil hides it
// when on-demand profiling is unavailable to this account.
func (s *server) profileCapturesView(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) *dashboard.ProfileCapturesView {
	store, ok := s.store.(state.ProfileCaptureStore)
	if !s.profileCapturesEnabled || !ok || !api.MustLimitsFor(acct.Plan).Profiling.Enabled || s.sessions == nil {
		return nil
	}
	view := &dashboard.ProfileCapturesView{AppSlug: app.Slug, MaxDuration: int(api.ProfileCaptureMaxDuration / time.Second)}
	captures, err := store.ListProfileCaptures(r.Context(), acct.ID, app.ID)
	if err != nil {
		view.Error = "Recent captures could not be listed."
	}
	view.Captures = captures
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, profileCaptureAction, acct.ID, profileCaptureCookie)
	if err != nil {
		view.Error = "Captures are temporarily unavailable."
		return view
	}
	view.CSRF = token
	// #nosec G124 -- configured production domains use Secure; empty domain supports local HTTP development.
	http.SetCookie(w, &http.Cookie{Name: profileCaptureCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	return view
}

func (s *server) dashboardCreateProfileCapture(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.ProfileControlMaxBytes)
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid profile capture form"))
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, profileCaptureAction, acct.ID, profileCaptureCookie); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid CSRF token; reload before starting a capture"))
		return
	}
	app, store, ok := s.profileCaptureTarget(w, r, acct)
	if !ok {
		return
	}
	duration, err := strconv.Atoi(r.PostForm.Get("duration_seconds"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("duration_seconds must be a whole number of seconds"))
		return
	}
	req := api.CreateProfileCaptureRequest{Kinds: r.PostForm["kind"], DurationSeconds: duration, InstanceID: r.PostForm.Get("instance_id")}
	capture, problem := s.queueProfileCapture(r.Context(), acct, app, store, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	http.Redirect(w, r, dashboard.ProfileCaptureURL(app.Slug, capture.ID), http.StatusSeeOther)
}

func (s *server) dashboardProfileCapture(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	app, store, ok := s.profileCaptureTarget(w, r, acct)
	if !ok {
		return
	}
	capture, ok := s.ownedProfileCapture(w, r, acct, app, store)
	if !ok {
		return
	}
	page := dashboard.ProfileCapturePage{AppSlug: app.Slug, Capture: capture, Refresh: !capture.Done()}
	if capture.Status == api.ProfileCaptureReady {
		page.Kinds = s.profileCaptureKinds(r, acct, app, store, capture)
	}
	view, _ := AccountFrom(r.Context())
	count, _ := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: app.Slug + " profile capture", Body: "app_profile_capture", Account: dashboardAccountView(view, count), Data: page}); err != nil {
		renderProblem(w, s.log, err)
	}
}

func (s *server) profileCaptureKinds(r *http.Request, acct state.Account, app state.App, store state.ProfileCaptureStore, capture api.ProfileCapture) []dashboard.ProfileCaptureKindView {
	blobs, err := store.ProfileCaptureBlobs(r.Context(), acct.ID, app.ID, capture.ID)
	out := make([]dashboard.ProfileCaptureKindView, 0, len(capture.Kinds))
	for _, kind := range capture.Kinds {
		if err != nil {
			out = append(out, dashboard.ProfileCaptureKindView{Kind: kind, Error: "Profile data is unavailable."})
			continue
		}
		var raws [][]byte
		for _, b := range blobs {
			if b.Kind == kind {
				raws = append(raws, b.Profile)
			}
		}
		merged, _, merr := profiling.MergeCapture(raws, kind)
		view, verr := profiling.CaptureView(merged, kind)
		if merr != nil || verr != nil {
			out = append(out, dashboard.ProfileCaptureKindView{Kind: kind, Error: "The profile exceeds what the dashboard can display; download the pprof file instead."})
			continue
		}
		out = append(out, dashboard.BuildProfileCaptureKind(app.Slug, capture.ID, view))
	}
	return out
}

func (s *server) dashboardDownloadProfileCapture(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	s.downloadProfileCapture(w, r, acct)
}
