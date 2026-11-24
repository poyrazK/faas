package claim

import (
	"context"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	mailpkg "github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// Claim pages, served on the API host:
//
//	GET  /claim/{token}   what is waiting to be claimed, and an email form
//	POST /claim/{token}   mail a confirmation link to that address
//	GET  /claim/confirm   a button (mail scanners must not consume the link)
//	POST /claim/confirm   put the address on the account, close the claim,
//	                      and hand the browser to the magic-link sign-in
//
// The confirmation reuses the account email-change flow: the claim issues an
// email change from the placeholder to the claimer's address, and consuming
// it moves the address only if no other account holds it and the account is
// still active. The confirmation never outlives the claim window, and
// replacing the claim link or expiring the account drops it.

// confirmTokenBytes is the email-change token length.
const confirmTokenBytes = 32

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
<p>We sent a confirmation link to the address you entered. Open it before {{.Expires}} to finish claiming.</p>
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

type pageApp struct {
	Slug string
	URL  string
}

type pageData struct {
	Gone    bool
	Sent    bool
	Apps    []pageApp
	Expires string
	Action  string
}

func writeHTML(w http.ResponseWriter, status int, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// same-origin, not no-referrer: under no-referrer a browser sends
	// Origin: null on the form posts, which the origin check would refuse.
	// The claim token in the path still never leaves the API host.
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(status)
	_ = tmpl.Execute(w, data)
}

func formatExpiry(t time.Time) string { return t.UTC().Format("Mon 2 Jan 2006 15:04 MST") }

// validEmail is a conservative shape check; the confirmation proves it.
func validEmail(email string) bool {
	if len(email) < 3 || len(email) > 254 || strings.ContainsAny(email, " \t\r\n<>\"") {
		return false
	}
	at := strings.LastIndexByte(email, '@')
	return at > 0 && at < len(email)-1 && strings.Contains(email[at+1:], ".") && !api.IsUnclaimedPlaceholderEmail(email)
}

// row resolves a claim link's token to a live unclaimed account.
func (s *Service) row(ctx context.Context, token string) (state.UnclaimedAccount, state.Account, bool) {
	store, ok := s.unclaimedStore()
	if !ok {
		return state.UnclaimedAccount{}, state.Account{}, false
	}
	raw, ok := decodeToken(token, TokenBytes)
	if !ok {
		return state.UnclaimedAccount{}, state.Account{}, false
	}
	defer clear(raw)
	row, err := store.UnclaimedAccountByClaimToken(ctx, api.HashToken(raw))
	if err != nil || !row.Live(s.now()) {
		return state.UnclaimedAccount{}, state.Account{}, false
	}
	acct, err := s.Host.Store().AccountByID(ctx, row.AccountID)
	if err != nil || acct.Status != state.AccountActive || !IsUnclaimed(acct) {
		return state.UnclaimedAccount{}, state.Account{}, false
	}
	return row, acct, true
}

// Page is GET /claim/{token}.
func (s *Service) Page(w http.ResponseWriter, r *http.Request) {
	row, acct, ok := s.row(r.Context(), r.PathValue("token"))
	if !ok {
		writeHTML(w, http.StatusGone, claimPage, pageData{Gone: true})
		return
	}
	data := pageData{Expires: formatExpiry(row.ExpiresAt), Action: PagePath + "/" + r.PathValue("token")}
	if apps, err := s.Host.Store().ListApps(r.Context(), acct.ID); err == nil {
		for _, app := range apps {
			data.Apps = append(data.Apps, pageApp{Slug: app.Slug, URL: s.Host.AppURL(app.Slug)})
		}
	}
	writeHTML(w, http.StatusOK, claimPage, data)
}

// Request is POST /claim/{token}: mail a confirmation to the address. The
// answer is the same whether or not the address has an account. The
// confirmation is issued here, atomically with the claim link still being
// live (state.IssueClaimEmailChange), so a link rotation or expiry racing
// this request either runs first and refuses it or drops what it issued;
// only the mail goes to the background.
func (s *Service) Request(w http.ResponseWriter, r *http.Request) {
	row, acct, ok := s.row(r.Context(), r.PathValue("token"))
	if !ok {
		writeHTML(w, http.StatusGone, claimPage, pageData{Gone: true})
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.PostFormValue("email")))
	if !validEmail(email) {
		api.WriteProblemForRequest(w, r, api.ErrValidation("email must be a valid address"))
		return
	}
	// The confirmation never outlives the claim window.
	expiresAt := s.now().Add(api.UnclaimedClaimEmailTTL)
	if row.ExpiresAt.Before(expiresAt) {
		expiresAt = row.ExpiresAt
	}
	sent := pageData{Sent: true, Expires: formatExpiry(expiresAt)}
	if !s.Host.AllowSecurityMail(r.Context(), acct, "email_change") {
		writeHTML(w, http.StatusOK, claimPage, sent)
		return
	}
	mail, ok := s.prepareClaimMail(w, r, row, acct, email, expiresAt)
	if !ok {
		return
	}
	if !s.Host.Background(mail) {
		s.Host.Log().Warn("claim.busy", "account_id", acct.ID)
		api.WriteProblemForRequest(w, r, api.ErrCapacity("could not send the confirmation now; try again in a minute"))
		return
	}
	s.Host.Audit(r.Context(), "account.claim_requested", &acct.ID, nil)
	writeHTML(w, http.StatusOK, claimPage, sent)
}

// prepareClaimMail issues the confirmation (or, for an address that already
// has an account, nothing) and returns the mail to send. On false it has
// written the answer.
func (s *Service) prepareClaimMail(w http.ResponseWriter, r *http.Request, row state.UnclaimedAccount, acct state.Account, email string, expiresAt time.Time) (func(context.Context), bool) {
	log := s.Host.Log()
	if _, err := s.Host.Store().AccountByEmail(r.Context(), email); err == nil {
		subject, body := mailpkg.ClaimAddressTakenBody(email)
		return func(ctx context.Context) {
			if err := s.Host.SendMail(ctx, email, subject, body); err != nil {
				log.Error("claim.taken_mailer", "err", err, "account_id", acct.ID)
			}
		}, true
	}
	store, ok := s.unclaimedStore()
	if !ok {
		api.WriteProblemForRequest(w, r, api.ErrCapacity("claiming is unavailable"))
		return nil, false
	}
	raw, err := newToken(confirmTokenBytes)
	if err != nil {
		api.WriteProblemForRequest(w, r, api.ErrCapacity("could not start the claim; try again"))
		return nil, false
	}
	defer clear(raw)
	if _, err := store.IssueClaimEmailChange(r.Context(), row.ClaimTokenSHA256, api.HashToken(raw), email, expiresAt, s.now()); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeHTML(w, http.StatusGone, claimPage, pageData{Gone: true})
			return nil, false
		}
		log.Error("claim.issue", "err", err, "account_id", acct.ID)
		api.WriteProblemForRequest(w, r, api.ErrCapacity("could not start the claim; try again"))
		return nil, false
	}
	link := s.Host.ClaimBase(r) + ConfirmPath + "?token=" + base64.RawURLEncoding.EncodeToString(raw)
	subject, body := mailpkg.ClaimConfirmBody(link, expiresAt)
	return func(ctx context.Context) {
		if err := s.Host.SendMail(ctx, email, subject, body); err != nil {
			log.Error("claim.mailer", "err", err, "account_id", acct.ID)
		}
	}, true
}

// ConfirmPage is GET /claim/confirm. It consumes nothing.
func (s *Service) ConfirmPage(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if _, ok := decodeToken(token, confirmTokenBytes); !ok {
		api.WriteProblemForRequest(w, r, api.ErrVerificationLinkInvalid())
		return
	}
	writeHTML(w, http.StatusOK, claimConfirmPage, token)
}

// Confirm is POST /claim/confirm.
func (s *Service) Confirm(w http.ResponseWriter, r *http.Request) {
	raw, ok := decodeToken(r.PostFormValue("token"), confirmTokenBytes)
	if !ok {
		api.WriteProblemForRequest(w, r, api.ErrVerificationLinkInvalid())
		return
	}
	defer clear(raw)
	flow, ok := s.Host.Store().(state.AccountFlowStore)
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
	// FinishEmailChange signs out, notifies the old (placeholder, dropped)
	// address and finishes the claim for a placeholder account.
	s.Host.FinishEmailChange(r, change)
	if !api.IsUnclaimedPlaceholderEmail(change.OldEmail) {
		http.Redirect(w, r, s.Host.LoginURL(r, "email_changed=1"), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, s.signInURL(r, change.AccountID), http.StatusSeeOther)
}

// signInURL mints a one-time sign-in link for the claimer: the address was
// proven a moment ago, so the claim ends signed in on the dashboard.
func (s *Service) signInURL(r *http.Request, accountID string) string {
	fallback := s.Host.LoginURL(r, "claimed=1")
	tok, err := newToken(api.MagicLinkTokenBytes)
	if err != nil {
		return fallback
	}
	defer clear(tok)
	if err := s.Host.Store().IssueLoginToken(r.Context(), api.HashToken(tok), accountID, s.now().Add(api.MagicLinkTTL)); err != nil {
		s.Host.Log().Warn("claim.sign_in_token", "err", err, "account_id", accountID)
		return fallback
	}
	return s.Host.MagicLinkURL(r, base64.RawURLEncoding.EncodeToString(tok))
}
