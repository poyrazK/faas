package apidserver

import (
	"net/http"
	"time"

	mailpkg "github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// Credential notices (ADR-20261108150000). Browser sign-ins already mail a
// notice from an unfamiliar device; a new API key, CLI sign-in or agent
// sign-in mints a credential that outlives any session, so each one mails
// the account owner. The send runs off the request path and shares the
// per-account security-mail throttle.

const (
	credentialAPIKey       = "An API key"
	credentialRotatedKey   = "A rotated API key"
	credentialCLISignIn    = "A CLI sign-in"
	credentialAgentSignIn  = "An agent (MCP) sign-in"
	credentialAPISignIn    = "A password sign-in through the API"
	credentialNoticeKind   = "credential_created"
	credentialNoticeAction = "auth.credential_created_notice"
	// credentialNoticeSignupQuiet skips the notice for the key a signup
	// mints for itself.
	credentialNoticeSignupQuiet = 10 * time.Minute
)

// noticeCredentialCreated mails acct that what (one of the credential*
// phrases) was created with label from the request's client.
func (s *server) noticeCredentialCreated(r *http.Request, acct state.Account, what, label string) {
	ip, ua := clientIPFromRequest(r), r.UserAgent()
	s.audit.Emit(r.Context(), credentialNoticeAction, &acct.ID, map[string]any{"credential": what, "ip": ip})
	subject, body := mailpkg.CredentialCreatedBody(acct.Email, what, label, ip, ua, time.Now())
	s.sendSecurityNotice(r.Context(), acct, credentialNoticeKind, subject, body)
}
