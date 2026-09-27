package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// The dashboard TOTP challenge. An mfa_pending session (one factor
// proved: password, magic link, or OAuth) is confined to these two routes
// by sessionAuth until a code verifies; success re-issues the cookie
// without mfa_pending and with a fresh step-up, exactly as
// POST /v1/account/mfa/verify does.
const (
	dashboardMFAAction     = "mfa_verify"
	dashboardMFACSRFCookie = "faas_csrf_mfa"
)

func (s *server) dashboardMFA(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if pending, _ := authmw.MFAPendingFrom(r); !pending {
		http.Redirect(w, r, "/dashboard/", http.StatusFound)
		return
	}
	s.renderDashboardMFA(w, r, acct, http.StatusOK, false, dashboardMFANext(r.URL.Query().Get("next")))
}

func (s *server) dashboardMFAVerify(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if pending, _ := authmw.MFAPendingFrom(r); !pending {
		http.Redirect(w, r, "/dashboard/", http.StatusSeeOther)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardMFAAction, acct.ID, dashboardMFACSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	if !s.totpAttemptAllowed(w, acct) {
		return
	}
	secret := readSealedSecret(s, w, r, acct.ID)
	if secret == "" {
		return // readSealedSecret wrote the problem
	}
	if !auth.VerifyCode(secret, strings.TrimSpace(r.PostFormValue("code"))) {
		s.totp.fail(acct.ID, time.Now())
		s.audit.Emit(r.Context(), "account.mfa_verify_failed", &acct.ID, map[string]any{"reason": "code_mismatch", "via": "dashboard"})
		// 401 so the dashboard auth limiter counts the guess.
		s.renderDashboardMFA(w, r, acct, http.StatusUnauthorized, true, dashboardMFANext(r.PostFormValue("next")))
		return
	}
	s.totp.reset(acct.ID)
	if err := s.reissueSessionCookieWithStepUp(w, r, acct, false, time.Now()); err != nil {
		s.log.Error("dashboard.mfa.reissue_cookie", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not re-issue session cookie"))
		return
	}
	s.audit.Emit(r.Context(), "account.mfa_session_stepped_up", &acct.ID, map[string]any{"via": "dashboard"})
	s.audit.Emit(r.Context(), "auth.step_up_verified", &acct.ID, map[string]any{
		"path": r.URL.Path, "method": r.Method, "ttl_sec": 300,
	})
	http.Redirect(w, r, dashboardMFANext(r.PostFormValue("next")), http.StatusSeeOther)
}

func (s *server) renderDashboardMFA(w http.ResponseWriter, r *http.Request, acct state.Account, status int, failed bool, next string) {
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardMFAAction, acct.ID, dashboardMFACSRFCookie)
	if err != nil {
		s.log.Error("dashboard.mfa.csrf_issue", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not render the MFA challenge"))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     dashboardMFACSRFCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.domain != "",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(middleware.DefaultCSRFTTL.Seconds()),
	})
	page := dashboard.Page{
		Title:   "Two-factor authentication",
		Account: acctViewFrom(acct),
		Body:    "mfa",
		Data:    dashboard.MFAChallengeData{CSRFToken: token, Failed: failed, Enrolled: acct.MFAEnrolled(), Next: next},
	}
	// Render sets these too, but only before the first write; a non-200
	// status has to be written first.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		s.log.Error("dashboard: mfa render failed", "err", err)
	}
}
