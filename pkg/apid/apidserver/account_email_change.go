package apidserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	mailpkg "github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Account email change (ADR 20261107103000). Requested from a browser
// session with the password-change proof, confirmed from the new address.

const (
	emailChangeRequestPath    = "/dashboard/account/email"
	emailChangeConfirmPath    = "/auth/email-change"
	emailChangeTTL            = time.Hour
	emailChangeTokenBytes     = 32
	dashboardEmailChangeAct   = "account_email_change"
	dashboardEmailChangeCSRFC = "faas_csrf_account_email_change"
)

// emailChangeInput is one request to move the account to a new address.
type emailChangeInput struct {
	Email           string `json:"email"`
	CurrentPassword string `json:"current_password"`
	// CSRFToken is the browser's double-submit copy, verified before decode.
	CSRFToken string `json:"csrf_token,omitempty"`
}

// dashboardRequestEmailChange is the account page form.
func (s *server) dashboardRequestEmailChange(w http.ResponseWriter, r *http.Request) {
	acct, ok := s.accountFormAccount(w, r, dashboardEmailChangeAct, dashboardEmailChangeCSRFC)
	if !ok {
		return
	}
	in := emailChangeInput{Email: r.FormValue("email"), CurrentPassword: r.FormValue("current_password")}
	runAccountForm(w, r, "email_change=sent", func(cw http.ResponseWriter) bool {
		return s.requestEmailChange(cw, r, acct, in)
	})
}

// postAccountEmailChange is POST /v1/account/email for the web console. It
// takes a browser session only: an API key must not be able to move the
// account to an address its holder controls.
func (s *server) postAccountEmailChange(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := sessionFrom(r); !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
			"Browser session required", "change your sign-in email from the dashboard: gregale account email"))
		return
	}
	if err := middleware.VerifyAuthenticated(s.sessions, r, dashboardEmailChangeAct, acct.ID); err != nil {
		api.WriteProblem(w, csrfMismatchProblem)
		return
	}
	var in emailChangeInput
	if err := decodeJSON(r, &in); err != nil {
		api.WriteProblem(w, api.ErrValidation("body must be {\"email\": \"...\", \"current_password\": \"...\"}"))
		return
	}
	if !s.requestEmailChange(w, r, acct, in) {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "confirmation_sent", "email": strings.ToLower(strings.TrimSpace(in.Email))})
}

// requestEmailChange validates, proves presence and mails the new address.
// It writes the problem and returns false on refusal.
func (s *server) requestEmailChange(w http.ResponseWriter, r *http.Request, acct state.Account, in emailChangeInput) bool {
	newEmail := strings.ToLower(strings.TrimSpace(in.Email))
	if !looksLikeEmail(newEmail) || len(newEmail) > 254 || strings.ContainsAny(newEmail, "\r\n") {
		api.WriteProblem(w, api.ErrValidation("email must be a valid address"))
		return false
	}
	if strings.EqualFold(newEmail, acct.Email) {
		api.WriteProblem(w, api.ErrValidation("that is already your sign-in email"))
		return false
	}
	if _, _, ok := s.accountChangeProof(w, r, acct, emailChangeRequestPath, in.CurrentPassword); !ok {
		return false
	}
	flow, ok := s.store.(state.AccountFlowStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("email change is unavailable"))
		return false
	}
	s.audit.Emit(r.Context(), "account.email_change_requested", &acct.ID, nil)
	// The taken-address check, the token and the mail run in the
	// background, so neither the answer nor its timing tells which
	// addresses have accounts (as forgot-password, H86). A taken address
	// gets no mail.
	base := s.passwordResetLinkBase(r)
	//nolint:contextcheck // The work outlives the request; it keeps the request values and has its own timeout.
	ctx := context.WithoutCancel(r.Context())
	if !s.resetMail.try(func() {
		ctx, cancel := context.WithTimeout(ctx, api.PasswordResetMailTimeout)
		defer cancel()
		s.startEmailChange(ctx, flow, base, acct, newEmail)
	}) {
		s.log.Warn("email_change.busy", "account_id", acct.ID)
	}
	return true
}

// startEmailChange issues the change and mails the new address unless it
// already belongs to an account. Failures stay server-side.
func (s *server) startEmailChange(ctx context.Context, flow state.AccountFlowStore, base string, acct state.Account, newEmail string) {
	if _, err := s.store.AccountByEmail(ctx, newEmail); err == nil {
		return
	}
	if !s.allowSecurityMail(ctx, acct, "email_change") {
		return
	}
	raw := make([]byte, emailChangeTokenBytes)
	defer clear(raw)
	if _, err := rand.Read(raw); err != nil {
		s.log.Error("email_change.token", "err", err, "account_id", acct.ID)
		return
	}
	expiresAt := time.Now().Add(emailChangeTTL)
	if err := flow.IssueEmailChange(ctx, api.HashToken(raw), acct.ID, newEmail, expiresAt); err != nil {
		s.log.Error("email_change.issue", "err", err, "account_id", acct.ID)
		return
	}
	link := fmt.Sprintf("%s%s?token=%s", base, emailChangeConfirmPath, base64.RawURLEncoding.EncodeToString(raw))
	subject, body := mailpkg.EmailChangeConfirmBody(newEmail, link, expiresAt)
	if err := s.mailer.Send(ctx, Message{To: []string{newEmail}, Subject: subject, TextBody: body}); err != nil {
		s.log.Error("email_change.mailer", "err", err, "account_id", acct.ID)
	}
	// The current address hears about it while it can still stop it
	// (ADR 20261110100000). Its own mail kind, so the confirm link above
	// does not use up the notice's slot.
	subject, body = mailpkg.EmailChangeRequestedBody(acct.Email, newEmail, time.Now(), expiresAt)
	if s.allowSecurityMail(ctx, acct, "email_change_requested") {
		if err := s.mailer.Send(ctx, Message{To: []string{acct.Email}, Subject: subject, TextBody: body}); err != nil {
			s.log.Error("email_change.notice_mailer", "err", err, "account_id", acct.ID)
		}
	}
}

var emailChangeConfirmPage = template.Must(template.New("email-change-confirm").Parse(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Confirm your new email</title></head>
<body>
<main>
<h1>Confirm your new sign-in email</h1>
<form method="post" action="/auth/email-change">
<input type="hidden" name="token" value="{{.}}">
<button type="submit">Use this email</button>
</form>
</main>
</body>
</html>
`))

// emailChangeConfirmForm is GET /auth/email-change. It renders a button and
// consumes nothing, so mail scanners that prefetch the link cannot use it.
func (s *server) emailChangeConfirmForm(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if raw, err := base64.RawURLEncoding.DecodeString(token); err != nil || len(raw) != emailChangeTokenBytes {
		api.WriteProblemForRequest(w, r, api.ErrVerificationLinkInvalid())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_ = emailChangeConfirmPage.Execute(w, token)
}

// confirmEmailChange is POST /auth/email-change.
func (s *server) confirmEmailChange(w http.ResponseWriter, r *http.Request) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(r.PostFormValue("token")))
	if err != nil || len(raw) != emailChangeTokenBytes {
		api.WriteProblemForRequest(w, r, api.ErrVerificationLinkInvalid())
		return
	}
	defer clear(raw)
	flow, ok := s.store.(state.AccountFlowStore)
	if !ok {
		api.WriteProblemForRequest(w, r, api.ErrCapacity("email change is unavailable"))
		return
	}
	change, err := flow.ConsumeEmailChange(r.Context(), api.HashToken(raw))
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblemForRequest(w, r, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Email in use", "another Gregale account already uses that address"))
		return
	case err != nil:
		api.WriteProblemForRequest(w, r, api.ErrVerificationLinkInvalid())
		return
	}
	s.finishEmailChange(r, change)
	http.Redirect(w, r, loginPath+"?email_changed=1", http.StatusSeeOther)
}

// finishEmailChange signs out every session (the link may be opened on any
// device, so there is no session to keep) and notifies the old address.
func (s *server) finishEmailChange(r *http.Request, change state.EmailChange) {
	s.revokeSessionsAfterPasswordChange(r.Context(), change.AccountID, "")
	s.audit.Emit(r.Context(), "account.email_changed", &change.AccountID, nil)
	s.moveBillingEmailsInBackground(r.Context(), change)
	subject, body := mailpkg.EmailChangedNoticeBody(change.OldEmail, change.NewEmail, time.Now())
	s.sendNoticeInBackground(r.Context(), "email_changed", change.AccountID,
		Message{To: []string{change.OldEmail}, Subject: subject, TextBody: body})
}
