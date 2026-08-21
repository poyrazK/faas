// handlers_dashboard_project_preview.go — ADR-124 dashboard surface.
//
// Three handlers behind three routes (server.go registrations):
//
//	GET  /dashboard/projects/{slug}/preview
//	POST /dashboard/projects/{slug}/preview         (multipart, plan only)
//	POST /dashboard/projects/{slug}/preview/apply   (multipart, apply)
//
// Why three routes instead of one with method switching inside the
// dashboardHandler: the multipart body is only present on POST,
// and a redirect on the apply path means the apply handler does
// not render the preview template at all. Splitting each method
// into its own exported (s *server) func keeps each handler ≤50
// lines (spec §Conventions) and avoids a Method-branching switch
// that grows with every future preview variant.
//
// Auth: the existing dashboardChain → sessionAuth wrap in server.go
// is the only auth gate. The session already carries the account;
// CSRF envelopes are minted at GET time and verified on POST
// (renderAppNew's pattern, handlers_dashboard_apps_new.go:78-94).
//
// The scan service is the v1 endpoint's core; we reuse it via a
// discard http.ResponseWriter so its one direct write (the
// plan_token_stale branch in scan_service.go:516) does not leak
// into the HTML response. The discardRW struct is a no-op
// ResponseWriter — see its doc for the methods it satisfies.
package main

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/dashboard/views"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// discardRW is a no-op http.ResponseWriter used to capture the
// one direct write scanService makes (plan_token_stale at
// scan_service.go:516) without leaking it into the dashboard
// HTML response. The struct satisfies http.ResponseWriter
// minimally: Header() returns a never-written header map,
// Write() returns success without buffering, WriteHeader() is a
// no-op. Hijacker/Flusher/Pusher are intentionally NOT
// implemented — scanService never calls them.
type discardRW struct {
	header http.Header
}

func (d *discardRW) Header() http.Header {
	if d.header == nil {
		d.header = make(http.Header)
	}
	return d.header
}
func (d *discardRW) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardRW) WriteHeader(int)             {}

// projectPreviewAction is the CSRF action binding for the preview
// form. Same shape as dashboardFireCronAction
// (dashboard_cron_fire.go:45). Distinct from the apply-action
// below because a leaked form token must not authorise the
// higher-privilege POST.
const projectPreviewAction = "project_preview"

// projectPreviewApplyAction is the CSRF binding for the apply POST
// on /dashboard/projects/{slug}/preview/apply. Carries the
// plan_token in the form so a replayed form against a stale token
// is rejected by scanService's plan_token_stale check.
const projectPreviewApplyAction = "project_preview_apply"

// setDashboardCSRFCookie writes the authenticated CSRF envelope
// cookie. Centralised so every preview-handler entrypoint mints
// the cookie with the same Secure/SameSite/MaxAge policy; the
// inline SetCookie copies previously scattered across the four
// branches (GET, submit success, submit problem, apply) drifted
// and made the apply form's cookie binding inconsistent with the
// apply form's `csrf_token` field.
//
// Cookie policy mirrors renderAppNew (handlers_dashboard_apps_new.go:78-94):
// HttpOnly + SameSite=Lax + Secure iff behind a real domain.
// MaxAge == DefaultCSRFTTL so the envelope expires on the same
// schedule middleware.VerifyAuthenticated enforces server-side.
func setDashboardCSRFCookie(w http.ResponseWriter, s *server, tok string) {
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.CookieNameAuthenticated,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.domain != "",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(middleware.DefaultCSRFTTL.Seconds()),
	})
}

// renderProjectPreview is the GET handler. Renders the empty
// form (Preview=false). The plan_token is empty on first GET;
// the operator uploads a tarball + (optionally) an exclude list
// and the POST handler re-runs scanService.
//
// If the project slug does not exist on the account, the page
// renders an inline "Project not found" hint instead of a 404
// so the operator gets the CTA to create it from the CLI without
// a 404-driven page reload.
func (s *server) renderProjectPreview(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	view := views.ProjectPreviewView{
		ProjectSlug: slug,
		Preview:     false,
	}
	if _, err := s.store.ProjectBySlug(r.Context(), acct.ID, slug); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			view.ProjectNotFound = true
		} else {
			log.Error("dashboard project_preview: ProjectBySlug",
				"account_id", acct.ID, "slug", slug, "err", err)
			renderProblem(w, log, err)
			return
		}
	}
	tok, err := middleware.IssueForAuthenticated(s.sessions, projectPreviewAction, acct.ID)
	if err != nil {
		log.Error("dashboard project_preview: csrf issue",
			"account_id", acct.ID, "slug", slug, "err", err)
		renderProblem(w, log, err)
		return
	}
	// GET only ever posts back to the preview endpoint (multipart
	// upload), so the cookie binds to the preview action. Once the
	// populate step runs the cookie is rotated to the apply
	// envelope (see submitProjectPreview success branch) so the
	// apply form's csrf_token matches.
	setDashboardCSRFCookie(w, s, tok)
	view.PreviewFormToken = tok
	s.renderProjectPreviewPage(w, log, r, view, acct)
}

// submitProjectPreview is the POST handler for the multipart
// preview form. Verifies CSRF, runs scanService via a discard
// http.ResponseWriter, and renders the populated preview
// template. Never applies — the apply handler is the separate
// POST /preview/apply route.
//
// scanService is invoked with planToken="" (no cached plan from
// a prior GET) and apply=false. The PreScanProblem field carries
// any RFC 7807 detail string so the operator sees the rejection
// inline above the form on a re-render.
func (s *server) submitProjectPreview(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	if err := middleware.VerifyAuthenticated(s.sessions, r, projectPreviewAction, acct.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	view := views.ProjectPreviewView{
		ProjectSlug: slug,
		Preview:     true,
	}
	resp, _, _, _, _, _, prob := s.scanService(&discardRW{}, r, acct, "", false)
	if prob != nil {
		// Pre-scan rejection (e.g. secret scan, source invalid,
		// exclude_unknown_slug, exclude_only_overlap). Render the
		// populated layout with the problem detail so the operator
		// can fix the input and re-submit without leaving the page.
		view.PreScanProblem = prob.Detail
		// Mint a fresh CSRF token + cookie so the re-submit can
		// pass VerifyAuthenticated. The old envelope is consumed.
		// Cookie binds to the preview action — the operator is
		// still in the upload-fix loop, not yet at apply.
		tok, err := middleware.IssueForAuthenticated(s.sessions, projectPreviewAction, acct.ID)
		if err != nil {
			log.Error("dashboard project_preview submit: csrf re-issue",
				"account_id", acct.ID, "slug", slug, "err", err)
			renderProblem(w, log, err)
			return
		}
		setDashboardCSRFCookie(w, s, tok)
		view.PreviewFormToken = tok
		s.renderProjectPreviewPage(w, log, r, view, acct)
		return
	}
	// Map scanPlanResponse → view.
	view.WillDeploy = toProjectPreviewAffected(resp.WillDeploy, false)
	view.Skipped = toProjectPreviewAffected(resp.Skipped, true)
	view.Unaffected = toProjectPreviewAffected(resp.Unaffected, false)
	view.Removed = resp.Removed
	view.PlanToken = resp.PlanToken
	view.CanApply = resp.CanApply
	view.NotAllowed = resp.NotAllowed
	view.ObservedApps = resp.ObservedApps
	view.LimitApps = resp.LimitApps
	// Two CSRF envelopes for the populated page: the apply form's
	// token is bound to projectPreviewApplyAction so a leaked
	// preview-form token cannot authorise apply. The cookie binds
	// to the MORE sensitive envelope (apply) because the populate
	// step leaves the operator one click away from the apply
	// button — the preview-only cookie would 400 the apply
	// submission with "Invalid CSRF token". IssueForAuthenticated
	// gives the apply token the same TTL as the preview token, so
	// the envelope is alive through both endpoints. (Spec §11:
	// never auto-issue a higher-privilege envelope on the lower-
	// privilege endpoint — we explicitly cross that boundary here
	// because the populated page is the operator's decision point
	// and the apply form's csrf_token field MUST match the cookie.)
	previewTok, err := middleware.IssueForAuthenticated(s.sessions, projectPreviewAction, acct.ID)
	if err != nil {
		log.Error("dashboard project_preview submit: csrf preview re-issue",
			"account_id", acct.ID, "slug", slug, "err", err)
		renderProblem(w, log, err)
		return
	}
	applyTok, err := middleware.IssueForAuthenticated(s.sessions, projectPreviewApplyAction, acct.ID)
	if err != nil {
		log.Error("dashboard project_preview submit: csrf apply issue",
			"account_id", acct.ID, "slug", slug, "err", err)
		renderProblem(w, log, err)
		return
	}
	setDashboardCSRFCookie(w, s, applyTok)
	view.PreviewFormToken = previewTok
	view.PreviewApplyToken = applyTok
	s.renderProjectPreviewPage(w, log, r, view, acct)
}

// applyProjectPreview is the POST handler at
// /dashboard/projects/{slug}/preview/apply. The apply form
// does NOT include a `source` file part (browsers strip file
// inputs from non-multipart submissions; the operator's UX
// is the prime directive — they do not re-attach the tarball).
// The source is replayed from the server-side plan cache
// (`scan_plan_cache.go`); the SHA-256 in the plan_token keys
// the lookup and re-validates against the cached entry's
// account_id.
//
// The form is urlencoded (no file input), so r.ParseForm
// populates r.Form with `csrf_token`, `plan_token`, and one
// `exclude` value per checked checkbox. We collect every
// `exclude` value, look up the cache, build a synthetic
// multipart request, and forward it to scanService with
// apply=true.
//
// On success the handler re-renders the same preview template
// with view.ApplyResult populated (a confirmation banner
// listing Added / Changed / Removed). No redirect — the
// operator's partition was their decision surface and the
// apply result is part of that surface. The apply form is
// hidden when ApplyResult is set so a second click is a no-op.
// On a problem (quota, plan, source-invalid,
// exclude_unknown_slug, cache-miss) the handler re-renders
// the populated preview with PreScanProblem populated so the
// operator can fix and re-submit without losing state.
func (s *server) applyProjectPreview(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	if err := middleware.VerifyAuthenticated(s.sessions, r, projectPreviewApplyAction, acct.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad form", err.Error()))
		return
	}
	planToken := r.FormValue("plan_token")
	pt, decErr := decodePlanToken(planToken)
	if decErr != nil {
		log.Warn("dashboard project_preview apply: bad plan_token",
			"account_id", acct.ID, "slug", slug, "err", decErr)
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid plan_token", "re-upload the tarball on /dashboard/projects/{slug}/preview"))
		return
	}
	cachedPath, cacheErr := lookupPlanCache(pt.Hash, acct.ID)
	if cacheErr != nil {
		if errors.Is(cacheErr, os.ErrNotExist) {
			log.Info("dashboard project_preview apply: cache miss",
				"account_id", acct.ID, "slug", slug, "sha256_prefix", pt.Hash[:min(8, len(pt.Hash))])
			view := views.ProjectPreviewView{
				ProjectSlug:    slug,
				Preview:        true,
				PreScanProblem: errPlanCacheMiss.Detail,
			}
			if tok, err := s.mintAndBindCSRF(w, log, r, slug, acct, projectPreviewAction); err == nil {
				view.PreviewFormToken = tok
			}
			if tok, err := s.mintAndBindCSRF(w, log, r, slug, acct, projectPreviewApplyAction); err == nil {
				view.PreviewApplyToken = tok
			}
			s.renderProjectPreviewPage(w, log, r, view, acct)
			return
		}
		log.Error("dashboard project_preview apply: cache lookup",
			"account_id", acct.ID, "slug", slug, "err", cacheErr)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Cache lookup failed", cacheErr.Error()))
		return
	}
	// Build synthetic multipart request from cached source +
	// checked exclude slugs. Each exclude slug becomes its own
	// multipart part so parseScanMultipart's comma-split /
	// lowercase / trim normalises identically to the live form.
	exclude := r.Form["exclude"]
	synthReq, buildErr := buildCachedSourceRequest(cachedPath, slug, exclude)
	if buildErr != nil {
		log.Error("dashboard project_preview apply: build synth req",
			"account_id", acct.ID, "slug", slug, "err", buildErr)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal,
			"Apply rebuild failed", buildErr.Error()))
		return
	}
	view := views.ProjectPreviewView{
		ProjectSlug: slug,
		Preview:     true,
	}
	resp, _, added, changed, removedSlugs, _, prob := s.scanService(&discardRW{}, synthReq, acct, planToken, true)
	if prob != nil {
		log.Warn("dashboard project_preview apply: scan problem",
			"account_id", acct.ID, "slug", slug, "code", prob.Code, "detail", prob.Detail)
		view.PreScanProblem = prob.Detail
		// Re-issue CSRF envelopes so the operator can fix + retry.
		// The apply envelope binds the cookie so the form's
		// `csrf_token` field matches after the problem-driven
		// re-render. The preview envelope is re-minted (its
		// cookie is overwritten by the apply mint, but the form
		// field is still needed for the multipart re-upload).
		if tok, err := s.mintAndBindCSRF(w, log, r, slug, acct, projectPreviewAction); err == nil {
			view.PreviewFormToken = tok
		}
		if tok, err := s.mintAndBindCSRF(w, log, r, slug, acct, projectPreviewApplyAction); err == nil {
			view.PreviewApplyToken = tok
		}
		s.renderProjectPreviewPage(w, log, r, view, acct)
		return
	}
	// Success: render the partition with an apply-result banner.
	// Removed slugs come from the scanService return value (the
	// destructive subset the operator should see explicitly per
	// ADR-124 §3). The apply form is hidden by the template when
	// ApplyResult is non-nil — no second apply possible.
	addedSlugs := make([]string, 0, len(added))
	for _, a := range added {
		if a.Slug != "" {
			addedSlugs = append(addedSlugs, a.Slug)
		}
	}
	changedSlugs := make([]string, 0, len(changed))
	for _, a := range changed {
		if a.Slug != "" {
			changedSlugs = append(changedSlugs, a.Slug)
		}
	}
	view.ApplyResult = &views.ProjectPreviewApplyResult{
		AddedSlugs:   addedSlugs,
		ChangedSlugs: changedSlugs,
		RemovedSlugs: removedSlugs,
		AppliedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	// Map scanPlanResponse → view (mirror submitProjectPreview
	// success branch). The partition is the operator's record of
	// what just changed; rendering it again is the audit trail.
	view.WillDeploy = toProjectPreviewAffected(resp.WillDeploy, false)
	view.Skipped = toProjectPreviewAffected(resp.Skipped, true)
	view.Unaffected = toProjectPreviewAffected(resp.Unaffected, false)
	view.Removed = resp.Removed
	view.PlanToken = resp.PlanToken
	view.CanApply = resp.CanApply
	view.NotAllowed = resp.NotAllowed
	view.ObservedApps = resp.ObservedApps
	view.LimitApps = resp.LimitApps
	log.Info("dashboard project_preview apply: success",
		"account_id", acct.ID, "slug", slug,
		"added", len(added), "changed", len(changed),
		"removed", len(removedSlugs),
		"will_deploy", len(resp.WillDeploy),
		"unaffected", len(resp.Unaffected),
		"skipped", len(resp.Skipped))
	s.renderProjectPreviewPage(w, log, r, view, acct)
}

// mintAndBindCSRF mints a CSRF envelope for action and writes
// the matching cookie. The cookie binds to whichever envelope
// the caller passes — preview-only renders pass
// projectPreviewAction; populated renders mint the apply
// envelope so the apply form's `csrf_token` field matches the
// cookie after a problem-driven re-render. Returns the token
// so the caller can populate the matching form field.
func (s *server) mintAndBindCSRF(w http.ResponseWriter, log *slog.Logger, r *http.Request, slug string, acct state.Account, action string) (string, error) {
	tok, err := middleware.IssueForAuthenticated(s.sessions, action, acct.ID)
	if err != nil {
		log.Error("dashboard project_preview: csrf issue",
			"account_id", acct.ID, "slug", slug, "action", action, "err", err)
		return "", err
	}
	setDashboardCSRFCookie(w, s, tok)
	return tok, nil
}

// renderProjectPreviewPage is the thin page-assembler shared by
// the GET + POST handlers. Mirrors renderAppsNewPage
// (handlers_dashboard_apps_new.go:171).
func (s *server) renderProjectPreviewPage(w http.ResponseWriter, log *slog.Logger, r *http.Request, view views.ProjectPreviewView, acct state.Account) {
	dview, _ := AccountFrom(r.Context())
	page := dashboard.Page{
		Title:   "Affected workloads preview",
		Body:    "project_preview",
		Account: dashboardAccountView(dview, len(view.WillDeploy)+len(view.Unaffected)),
		Data:    view,
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// toProjectPreviewAffected translates api.PlanAffectedApp into
// the dashboard-only ProjectPreviewAffected shape. The Excluded
// flag flips on the Skipped rows so the template can render the
// line-through style without a second switch. The glyph + label
// per row come from actionAffordance (kept in this file because
// the vocabulary is dashboard-local; the wire DTO does not
// encode it).
func toProjectPreviewAffected(in []api.PlanAffectedApp, excluded bool) []views.ProjectPreviewAffected {
	if len(in) == 0 {
		return nil
	}
	out := make([]views.ProjectPreviewAffected, 0, len(in))
	for _, a := range in {
		glyph, label := actionAffordance(a.Action, excluded)
		out = append(out, views.ProjectPreviewAffected{
			Slug:         a.Slug,
			Action:       a.Action,
			ActionGlyph:  glyph,
			ActionLabel:  label,
			ID:           a.ID,
			ExistingRoot: a.ExistingRootDir,
			Excluded:     excluded,
		})
	}
	return out
}

// actionAffordance picks the customer-facing glyph + label for
// one PlanAffectedApp row. Kept in this file (not in views) so
// the views package stays pure-data and the i18n / accessibility
// strings live next to the handler that calls them. The glyphs
// are intentionally ASCII so they survive any rendering target
// without a font-fallback hop (the terminal previews in
// `gregale scan --show-affected` use the same vocabulary):
//
//	create  →  "+ will create"
//	update  →  "~ will update"
//	remove  →  "x will remove"
//	noop    →  "· unchanged"            (Unaffected)
//	noop    →  "— excluded"             (Skipped — Excluded=true)
func actionAffordance(action string, excluded bool) (glyph, label string) {
	switch action {
	case "create":
		return "+", "will create"
	case "update":
		return "~", "will update"
	case "remove":
		return "x", "will remove"
	default: // "noop"
		if excluded {
			return "—", "excluded"
		}
		return "·", "unchanged"
	}
}

// idor-safe slug helper used by the mux path parser below. The
// check is intentionally narrow: only printable ASCII, no slashes,
// no URL-special chars. Mirrors validSlug (handlers_diff.go) so
// the redirect targets above cannot compose into an open-redirect
// (G710 gosec gate).
func previewSlugOK(slug string) bool {
	if slug == "" || len(slug) > 64 {
		return false
	}
	for _, r := range slug {
		// De Morgan'd from the literal "if not lowercase and not
		// digit and not dash" so QF1001 (staticcheck) stays clean.
		// Equivalent: reject any rune outside [a-z0-9-].
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		isDash := r == '-'
		if !isLower && !isDigit && !isDash {
			return false
		}
	}
	return !strings.Contains(slug, "--")
}

// submitProjectPreviewDispatch is the mux.HandleFunc entrypoint
// for POST /dashboard/projects/{slug}/preview. Decodes the path
// slug, validates it (previewSlugOK), and re-loads the account
// before delegating to submitProjectPreview. Mirrors
// dashboardFireCron's pattern (dashboard_cron_fire.go:140).
func (s *server) submitProjectPreviewDispatch(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !previewSlugOK(slug) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	acct, ok := AccountFrom(r.Context())
	if !ok {
		// sessionAuth would have redirected; defensive 401.
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.submitProjectPreview(w, r, s.log, acct, slug)
}

// applyProjectPreviewDispatch is the mux.HandleFunc entrypoint
// for POST /dashboard/projects/{slug}/preview/apply. Same
// dispatch shape as submitProjectPreviewDispatch.
func (s *server) applyProjectPreviewDispatch(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !previewSlugOK(slug) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.applyProjectPreview(w, r, s.log, acct, slug)
}
