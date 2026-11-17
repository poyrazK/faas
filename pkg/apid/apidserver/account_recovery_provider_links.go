package apidserver

import (
	"context"
	"strings"
	"time"

	mailpkg "github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// removeForeignProviderLinks ends the provider sign-ins a recovery cannot
// vouch for. A linked Google or GitHub identity signs in by its own subject,
// so one a squatter or a thief linked under another address kept signing in
// after the owner reset the password, claimed the address or had an operator
// restore it (account-flow audit 2026-11-10, ADR 20261111090000). Recovery
// keeps a provider only when one of its links carries the account's own
// address; the rest are removed, recorded as unlinked (so they do not re-link
// by email) and mailed. Failures are logged: the recovery itself succeeded.
func (s *server) removeForeignProviderLinks(ctx context.Context, accountID, reason string) {
	flow, ok := s.store.(state.AccountFlowStore)
	if !ok {
		return
	}
	acct, err := s.store.AccountByID(ctx, accountID)
	if err != nil {
		s.log.Warn("recovery: read account for provider links", "account", accountID, "err", err)
		return
	}
	links, err := flow.ListOAuthLinks(ctx, accountID)
	if err != nil {
		s.log.Warn("recovery: list provider links", "account", accountID, "err", err)
		return
	}
	for _, provider := range foreignProviders(links, acct.Email) {
		if _, err := flow.DeleteOAuthLink(ctx, accountID, provider); err != nil {
			s.log.Warn("recovery: remove provider link", "account", accountID, "provider", provider, "err", err)
			continue
		}
		s.audit.Emit(ctx, "account.sign_in_method_unlinked", &acct.ID, map[string]any{"provider": provider, "reason": reason})
		subject, body := mailpkg.SignInMethodRemovedBody(acct.Email, provider, time.Now())
		s.sendSecurityNotice(ctx, acct, "sign_in_method_removed", subject, body)
	}
}

// foreignProviders lists, in link order, the providers none of whose links
// carry email.
func foreignProviders(links []state.OAuthLink, email string) []string {
	own := map[string]bool{}
	for _, l := range links {
		if strings.EqualFold(strings.TrimSpace(l.Email), strings.TrimSpace(email)) {
			own[l.Provider] = true
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, l := range links {
		if own[l.Provider] || seen[l.Provider] {
			continue
		}
		seen[l.Provider] = true
		out = append(out, l.Provider)
	}
	return out
}

// removeOldAddressProviderLinks removes the Google or GitHub sign-in links
// made under oldEmail once the account has moved to another address. The
// typical case is leaving a work address: the employer still controls that
// Google or GitHub identity and could keep signing in (F11-1,
// ADR-20261115100000). A provider that also has a link under the new
// address stays, as do links under any other address.
func (s *server) removeOldAddressProviderLinks(ctx context.Context, acct state.Account, oldEmail string) {
	flow, ok := s.store.(state.AccountFlowStore)
	if !ok {
		return
	}
	links, err := flow.ListOAuthLinks(ctx, acct.ID)
	if err != nil {
		s.log.Warn("email change: list provider links", "account", acct.ID, "err", err)
		return
	}
	for _, provider := range oldAddressProviders(links, oldEmail, acct.Email) {
		if _, err := flow.DeleteOAuthLink(ctx, acct.ID, provider); err != nil {
			s.log.Warn("email change: remove provider link", "account", acct.ID, "provider", provider, "err", err)
			continue
		}
		s.audit.Emit(ctx, "account.sign_in_method_unlinked", &acct.ID, map[string]any{"provider": provider, "reason": "email_changed"})
		subject, body := mailpkg.SignInMethodRemovedBody(acct.Email, provider, time.Now())
		s.sendSecurityNotice(ctx, acct, "sign_in_method_removed", subject, body)
	}
}

// oldAddressProviders lists, in link order, the providers with a link under
// oldEmail and none under newEmail.
func oldAddressProviders(links []state.OAuthLink, oldEmail, newEmail string) []string {
	same := func(a, b string) bool { return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) }
	underNew := map[string]bool{}
	for _, l := range links {
		if same(l.Email, newEmail) {
			underNew[l.Provider] = true
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, l := range links {
		if !same(l.Email, oldEmail) || underNew[l.Provider] || seen[l.Provider] {
			continue
		}
		seen[l.Provider] = true
		out = append(out, l.Provider)
	}
	return out
}
