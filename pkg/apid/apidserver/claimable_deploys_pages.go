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
	"github.com/onebox-faas/faas/pkg/state"
)

// Claim pages (ADR-20261124080000), served on the API host:
//
//	GET  /claim/{token}   what is waiting to be claimed, and an email form
//	POST /claim/{token}   mail a confirmation link to that address
//	GET  /claim/confirm   a button (mail scanners must not consume the link)
//	POST /claim/confirm   put the address on the account, close the claim,
//	                      and hand the browser to the magic-link sign-in
//
// The confirmation reuses the account email-change flow: the claim issues an
// email change from the placeholder to the claimer's address, and consuming
// it moves the address only if no other account holds it.

const (
	claimPagePath    = "/claim"
	claimConfirmPath = "/claim/confirm"
)

var claimPage = template.Must(template.New("claim").Parse(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="robots" content="noindex"><title>Claim your Gregale app</title></head>
<body>
<main>
{{if .Gone}}
<h1>This claim link is no longer valid</h1>
<p>The app was already claimed, the link was replaced by a newer one, or the claim window ended. Ask the agent that deployed it to run <code>gregale claim</code> for a fresh link, or deploy again after <code>gregale login</code>.</p>
{{else if .Sent}}
<h1>Check your email</h1>
<p>We sent a confirmation link to the address you entered. Open it within an hour to finish claiming.</p>
<p>If that address already has a Gregale account, the email explains what to do instead.</p>
{{else}}
<h1>Claim your Gregale app</h1>
<p>An agent deployed {{if .Apps}}these apps{{else}}to Gregale{{end}} without an account. Claim it to keep it: it becomes yours on the Free plan, and you can then add custom domains, databases and more apps.</p>
{{if .Apps}}<ul>{{range .Apps}}<li><a href="{{.URL}}" rel="noopener">{{.Slug}}</a></li>{{end}}</ul>{{end}}
<p>Unclaimed apps are deleted on {{.Expires}}.</p>
<form method="post" action="{{.Action}}">
<label for="email">Your email</label>
<input id="email" name="email" type="email" autocomplete="email" required maxlength="254">
<button type="submit">Claim</button>
</form>
<p>By claiming you accept the <a href="/terms">Terms of Service</a>.</p>
{{end}}
</main>
</body>
</html>
`))

var claimConfirmPage = template.Must(template.New("claim-confirm").Parse(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="robots" content="noindex"><title>Finish claiming your app</title></head>
<body>
<main>
<h1>Finish claiming your Gregale app</h1>
<form method="post" action="/claim/confirm">
<input type="hidden" name="token" value="{{.}}">
<button type="submit">Claim and sign in</button>
</form>
</main>
</body>
</html>
`))

type claimPageApp struct {
	Slug string
	URL  string
}

type claimPageData struct {
	Gone    bool
	Sent    bool
	Apps    []claimPageApp
	Expires string
	Action  string
}

func writeClaimHTML(w http.ResponseWriter, status int, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(status)
	_ = tmpl.Execute(w, data)
}

// claimRow resolves a claim link's token to a live unclaimed account.
func (s *server) claimRow(ctx context.Context, token string) (state.UnclaimedAccount, state.Account, bool) {
	store, ok := s.store.(state.UnclaimedAccountStore)
	if !ok {
		return state.UnclaimedAccount{}, state.Account{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil || len(raw) != claimTokenBytes {
		return state.UnclaimedAccount{}, state.Account{}, false
	}
	defer clear(raw)
	row, err := store.UnclaimedAccountByClaimToken(ctx, api.HashToken(raw))
	if err != nil || !row.Live(timeNow()) {
		return state.UnclaimedAccount{}, state.Account{}, false
	}
	acct, err := s.store.AccountByID(ctx, row.AccountID)
	if err != nil || acct.Status != state.AccountActive || !isUnclaimedAccount(acct) {
		return state.UnclaimedAccount{}, state.Account{}, false
	}
	return row, acct, true
}

// getClaimPage is GET /claim/{token}.
func (s *server) getClaimPage(w http.ResponseWriter, r *http.Request) {
	row, acct, ok := s.claimRow(r.Context(), r.PathValue("token"))
	if !ok {
		writeClaimHTML(w, http.StatusGone, claimPage, claimPageData{Gone: true})
		return
	}
	data := claimPageData{
		Expires: row.ExpiresAt.UTC().Format("Mon 2 Jan 2006 15:04 MST"),
		Action:  claimPagePath + "/" + r.PathValue("token"),
	}
	if apps, err := s.store.ListApps(r.Context(), acct.ID); err == nil {
		for _, app := range apps {
			data.Apps = append(data.Apps, claimPageApp{Slug: app.Slug, URL: "https://" + app.Slug + "." + s.domain})
		}
	}
	writeClaimHTML(w, http.StatusOK, claimPage, data)
}

// postClaimPage is POST /claim/{token}: mail a confirmation to the address.
// The answer is the same whether or not the address has an account.
func (s *server) postClaimPage(w http.ResponseWriter, r *http.Request) {
	_, acct, ok := s.claimRow(r.Context(), r.PathValue("token"))
	if !ok {
		writeClaimHTML(w, http.StatusGone, claimPage, claimPageData{Gone: true})
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
	if !looksLikeEmail(email) || len(email) > 254 || strings.ContainsAny(email, "\r\n") || api.IsUnclaimedPlaceholderEmail(email) {
		api.WriteProblemForRequest(w, r, api.ErrValidation("email must be a valid address"))
		return
	}
	flow, ok := s.store.(state.AccountFlowStore)
	if !ok {
		api.WriteProblemForRequest(w, r, api.ErrCapacity("claiming is unavailable"))
		return
	}
	base := s.claimURLBase(r)
	//nolint:contextcheck // The mail outlives the request; it keeps the request values and has its own timeout.
	ctx := context.WithoutCancel(r.Context())
	if !s.resetMail.try(func() {
		ctx, cancel := context.WithTimeout(ctx, api.PasswordResetMailTimeout)
		defer cancel()
		s.startClaim(ctx, flow, base, acct, email)
	}) {
		s.log.Warn("claim.busy", "account_id", acct.ID)
	}
	s.audit.Emit(r.Context(), "account.claim_requested", &acct.ID, nil)
	writeClaimHTML(w, http.StatusOK, claimPage, claimPageData{Sent: true})
}

// startClaim mails the confirmation link, or, when the address already has
// an account, explains that claiming moves the app into a new account only.
func (s *server) startClaim(ctx context.Context, flow state.AccountFlowStore, base string, acct state.Account, email string) {
	if !s.allowSecurityMail(ctx, acct, "email_change") {
		return
	}
	if _, err := s.store.AccountByEmail(ctx, email); err == nil {
		subject, body := mailpkg.ClaimAddressTakenBody(email)
		if err := s.mailer.Send(ctx, Message{To: []string{email}, Subject: subject, TextBody: body}); err != nil {
			s.log.Error("claim.taken_mailer", "err", err, "account_id", acct.ID)
		}
		return
	}
	raw := make([]byte, emailChangeTokenBytes)
	defer clear(raw)
	if _, err := rand.Read(raw); err != nil {
		s.log.Error("claim.token", "err", err, "account_id", acct.ID)
		return
	}
	expiresAt := timeNow().Add(api.UnclaimedClaimEmailTTL)
	if err := flow.IssueEmailChange(ctx, api.HashToken(raw), acct.ID, email, expiresAt); err != nil {
		s.log.Error("claim.issue", "err", err, "account_id", acct.ID)
		return
	}
	link := fmt.Sprintf("%s%s?token=%s", base, claimConfirmPath, base64.RawURLEncoding.EncodeToString(raw))
	subject, body := mailpkg.ClaimConfirmBody(link, expiresAt)
	if err := s.mailer.Send(ctx, Message{To: []string{email}, Subject: subject, TextBody: body}); err != nil {
		s.log.Error("claim.mailer", "err", err, "account_id", acct.ID)
	}
}

// getClaimConfirm is GET /claim/confirm. It consumes nothing.
func (s *server) getClaimConfirm(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if raw, err := base64.RawURLEncoding.DecodeString(token); err != nil || len(raw) != emailChangeTokenBytes {
		api.WriteProblemForRequest(w, r, api.ErrVerificationLinkInvalid())
		return
	}
	writeClaimHTML(w, http.StatusOK, claimConfirmPage, token)
}

// postClaimConfirm is POST /claim/confirm.
func (s *server) postClaimConfirm(w http.ResponseWriter, r *http.Request) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(r.PostFormValue("token")))
	if err != nil || len(raw) != emailChangeTokenBytes {
		api.WriteProblemForRequest(w, r, api.ErrVerificationLinkInvalid())
		return
	}
	defer clear(raw)
	flow, ok := s.store.(state.AccountFlowStore)
	if !ok {
		api.WriteProblemForRequest(w, r, api.ErrCapacity("claiming is unavailable"))
		return
	}
	change, err := flow.ConsumeEmailChange(r.Context(), api.HashToken(raw))
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblemForRequest(w, r, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Email in use", "another Gregale account already uses that address; claim with a different address"))
		return
	case err != nil:
		api.WriteProblemForRequest(w, r, api.ErrVerificationLinkInvalid())
		return
	}
	// finishEmailChange signs out, notifies the old (placeholder, dropped)
	// address and calls completeClaim for a placeholder account.
	s.finishEmailChange(r, change)
	if !api.IsUnclaimedPlaceholderEmail(change.OldEmail) {
		http.Redirect(w, r, s.passwordResetLinkBase(r)+loginPath+"?email_changed=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, s.claimSignInURL(r, change.AccountID), http.StatusSeeOther)
}

// claimSignInURL mints a one-time sign-in link for the claimer: the address
// was proven a moment ago, so the claim ends signed in on the dashboard.
func (s *server) claimSignInURL(r *http.Request, accountID string) string {
	base := s.passwordResetLinkBase(r)
	fallback := base + loginPath + "?claimed=1"
	tok := make([]byte, api.MagicLinkTokenBytes)
	defer clear(tok)
	if _, err := rand.Read(tok); err != nil {
		return fallback
	}
	if err := s.store.IssueLoginToken(r.Context(), api.HashToken(tok), accountID, time.Now().Add(api.MagicLinkTTL)); err != nil {
		s.log.Warn("claim.sign_in_token", "err", err, "account_id", accountID)
		return fallback
	}
	return base + magicLinkPath + "?token=" + base64.RawURLEncoding.EncodeToString(tok)
}
