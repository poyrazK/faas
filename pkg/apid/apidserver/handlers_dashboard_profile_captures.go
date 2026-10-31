package apidserver

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/profilecapture"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

// Dashboard surface for on-demand profile captures (ADR-1155). The JSON API
// in pkg/apid/profilecapture owns validation and storage.

const profileCaptureAction = "profile_capture"
const profileCaptureCookie = "faas_csrf_profile_capture"

// profileCapturesView builds the profiles-page capture panel; nil hides it
// when on-demand profiling is unavailable to this account.
func (s *server) profileCapturesView(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) *dashboard.ProfileCapturesView {
	store, ok := s.profileCaptures.CaptureStore()
	if !ok || !api.MustLimitsFor(acct.Plan).Profiling.Enabled {
		return nil
	}
	return s.buildProfileCapturesView(w, r, acct, app, store)
}

// buildProfileCapturesView lists the app's captures and arms the capture
// form; nil when the session store is unavailable.
func (s *server) buildProfileCapturesView(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, store state.ProfileCaptureStore) *dashboard.ProfileCapturesView {
	if s.sessions == nil {
		return nil
	}
	view := &dashboard.ProfileCapturesView{AppSlug: app.Slug, MaxDuration: int(api.ProfileCaptureMaxDuration / time.Second)}
	captures, err := store.ListProfileCaptures(r.Context(), app.AccountID, app.ID)
	if err != nil {
		view.Error = "Recent captures could not be listed."
	}
	view.Captures = captures
	if view.CSRF = s.issueProfileCaptureCSRF(w, acct); view.CSRF == "" {
		view.Error = "Captures are temporarily unavailable."
	}
	return view
}

// issueProfileCaptureCSRF arms the capture forms of one page; "" when no
// token could be issued, which hides or disables them.
func (s *server) issueProfileCaptureCSRF(w http.ResponseWriter, acct state.Account) string {
	if s.sessions == nil {
		return ""
	}
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, profileCaptureAction, acct.ID, profileCaptureCookie)
	if err != nil {
		return ""
	}
	// #nosec G124 -- configured production domains use Secure; empty domain supports local HTTP development.
	http.SetCookie(w, &http.Cookie{Name: profileCaptureCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	return token
}

// verifyProfileCaptureForm parses a capture form and checks its CSRF token;
// on failure it has already written the response.
func (s *server) verifyProfileCaptureForm(w http.ResponseWriter, r *http.Request, acct state.Account, verb string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, api.ProfileControlMaxBytes)
	if err := r.ParseForm(); err != nil {
		api.WriteProblemForRequest(w, r, api.ErrValidation("invalid profile capture form"))
		return false
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, profileCaptureAction, acct.ID, profileCaptureCookie); err != nil {
		api.WriteProblemForRequest(w, r, api.ErrValidation("invalid CSRF token; reload before "+verb+" a capture"))
		return false
	}
	return true
}

// dashboardProfileCaptures is the app's captures page: the capture form and
// recent captures on a page of their own, so accounts released
// profile-captures reach them without the traffic-profiles page.
func (s *server) dashboardProfileCaptures(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	app, store, ok := s.profileCaptures.Target(w, r, acct)
	if !ok {
		return
	}
	view := s.buildProfileCapturesView(w, r, acct, app, store)
	if view == nil {
		api.WriteProblemForRequest(w, r, api.ErrCapacity("profile captures are temporarily unavailable"))
		return
	}
	count, _ := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: app.Slug + " profile captures", Body: "app_profile_captures", Account: dashboardAccountView(acct, count), Data: view}); err != nil {
		renderProblem(w, s.log, err)
	}
}

// dashboardDeleteProfileCapture deletes a capture from its page and returns
// to the app's captures.
func (s *server) dashboardDeleteProfileCapture(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if !s.verifyProfileCaptureForm(w, r, acct, "deleting") {
		return
	}
	app, store, ok := s.profileCaptures.Target(w, r, acct)
	if !ok {
		return
	}
	if problem := s.profileCaptures.Remove(r.Context(), acct, app, store, r.PathValue("id")); problem != nil {
		api.WriteProblemForRequest(w, r, problem)
		return
	}
	http.Redirect(w, r, dashboard.ProfileCapturesURL(app.Slug), http.StatusSeeOther)
}

func (s *server) dashboardCreateProfileCapture(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if !s.verifyProfileCaptureForm(w, r, acct, "starting") {
		return
	}
	app, store, ok := s.profileCaptures.Target(w, r, acct)
	if !ok {
		return
	}
	duration, err := strconv.Atoi(r.PostForm.Get("duration_seconds"))
	if err != nil {
		api.WriteProblemForRequest(w, r, api.ErrValidation("duration_seconds must be a whole number of seconds"))
		return
	}
	req := api.CreateProfileCaptureRequest{Kinds: r.PostForm["kind"], DurationSeconds: duration, InstanceID: r.PostForm.Get("instance_id")}
	capture, problem := s.profileCaptures.Queue(r.Context(), app, store, req)
	if problem != nil {
		api.WriteProblemForRequest(w, r, problem)
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
	app, store, ok := s.profileCaptures.Target(w, r, acct)
	if !ok {
		return
	}
	capture, ok := s.profileCaptures.Owned(w, r, acct, app, store)
	if !ok {
		return
	}
	page := dashboard.ProfileCapturePage{AppSlug: app.Slug, Capture: capture, Refresh: !capture.Done(), CSRF: s.issueProfileCaptureCSRF(w, acct)}
	if capture.Status == api.ProfileCaptureReady {
		page.Kinds = s.profileCaptureKinds(r, acct, app, store, capture)
		if all, err := store.ListProfileCaptures(r.Context(), app.AccountID, app.ID); err == nil {
			page.Compare = dashboard.ProfileCaptureCompareOptions(profilecapture.Comparable(capture, all))
		}
	}
	count, _ := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: app.Slug + " profile capture", Body: "app_profile_capture", Account: dashboardAccountView(acct, count), Data: page}); err != nil {
		renderProblem(w, s.log, err)
	}
}

func (s *server) profileCaptureKinds(r *http.Request, acct state.Account, app state.App, store state.ProfileCaptureStore, capture api.ProfileCapture) []dashboard.ProfileCaptureKindView {
	blobs, err := store.ProfileCaptureBlobs(r.Context(), app.AccountID, app.ID, capture.ID)
	out := make([]dashboard.ProfileCaptureKindView, 0, len(capture.Kinds))
	for _, kind := range capture.Kinds {
		if err != nil {
			out = append(out, dashboard.ProfileCaptureKindView{Kind: kind, Error: "Profile data is unavailable."})
			continue
		}
		merged, _, merr := profiling.MergeCapture(profilecapture.KindProfiles(blobs, kind), kind)
		view, verr := profiling.CaptureView(merged, kind)
		if merr != nil || verr != nil {
			out = append(out, dashboard.ProfileCaptureKindView{Kind: kind, Error: "The profile exceeds what the dashboard can display; download the pprof file instead."})
			continue
		}
		s.profileCaptures.LinkSources(r.Context(), app, capture, &view)
		out = append(out, dashboard.BuildProfileCaptureKind(app.Slug, capture.ID, view))
	}
	return out
}

// dashboardProfileCaptureDiff compares a capture with an earlier one of the
// same app; validation problems render on the page rather than as JSON.
func (s *server) dashboardProfileCaptureDiff(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	app, store, ok := s.profileCaptures.Target(w, r, acct)
	if !ok {
		return
	}
	capture, ok := s.profileCaptures.Owned(w, r, acct, app, store)
	if !ok {
		return
	}
	page := dashboard.ProfileCaptureDiffPage{AppSlug: app.Slug, CandidateURL: dashboard.ProfileCaptureURL(app.Slug, capture.ID)}
	diff, problem := s.profileCaptures.Compare(r.Context(), acct, app, store, capture, r.URL.Query().Get("base"), r.URL.Query().Get("kind"))
	if problem != nil {
		page.Error = problem.Detail
	} else {
		page = dashboard.BuildProfileCaptureDiff(app.Slug, diff)
	}
	count, _ := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: app.Slug + " capture comparison", Body: "app_profile_capture_diff", Account: dashboardAccountView(acct, count), Data: page}); err != nil {
		renderProblem(w, s.log, err)
	}
}

// requestProfileCaptureID names the stack snapshot taken while the request
// with this trace id was in flight (ADR-1155), or "".
func (s *server) requestProfileCaptureID(ctx context.Context, acct state.Account, app state.App, traceID string) string {
	store, ok := s.profileCaptures.CaptureStore()
	if !ok || traceID == "" {
		return ""
	}
	c, err := store.ProfileCaptureForRequest(ctx, app.AccountID, app.ID, traceID)
	if err != nil {
		return ""
	}
	return c.ID
}

// announcedProfileCaptures builds the app page banner for captures the
// platform took on its own or that an OOM kill followed (ADR-1155). A read
// failure hides the banner rather than the page.
func (s *server) announcedProfileCaptures(ctx context.Context, log *slog.Logger, acct state.Account, app state.App) *dashboard.AnnouncedProfileCapturesView {
	store, ok := s.profileCaptures.CaptureStore()
	if !ok || !api.MustLimitsFor(acct.Plan).Profiling.Enabled {
		return nil
	}
	captures, err := store.ListProfileCaptures(ctx, app.AccountID, app.ID)
	if err != nil {
		log.Warn("dashboard renderAppDetail: list profile captures", "account_id", acct.ID, "app_id", app.ID, "err", err)
		return nil
	}
	return dashboard.BuildAnnouncedProfileCaptures(app.Slug, captures)
}

// oomProfileCaptureURL links an OOM-failed deployment to the heap capture
// taken before the kill (ADR-1155), or "" when there is none.
func (s *server) oomProfileCaptureURL(ctx context.Context, acct state.Account, app state.App, dep state.Deployment) string {
	store, ok := s.profileCaptures.CaptureStore()
	if !ok {
		return ""
	}
	c, err := store.LatestOOMProfileCapture(ctx, app.AccountID, app.ID, dep.ID)
	if err != nil {
		return ""
	}
	return dashboard.ProfileCaptureURL(app.Slug, c.ID)
}

func (s *server) dashboardDownloadProfileCapture(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	s.profileCaptures.Download(w, r, acct)
}
