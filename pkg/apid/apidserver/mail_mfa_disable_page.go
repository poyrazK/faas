package apidserver

import (
	"encoding/base64"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// Gate before authentication or token parsing. Neither opening the page nor
// email prefetch consumes a token.
func (s *server) mfaDisableEmailPageRoute() http.HandlerFunc {
	handler := s.dashboardChain(s.authLimited(s.requireScope(api.ScopesAdminOnly...)(s.mfaDisableEmailPage)))
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !s.mfaDisableEmailPageEnabled() {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		handler.ServeHTTP(w, r)
	}
}

// mfaDisableEmailPageEnabled serves the page whenever a disable link can be
// mailed. The legacy inline path mailed the POST-only confirmation endpoint,
// which a mail client opens with GET and gets 405, so email recovery never
// worked while the outbox was off (bug hunt 2026-10-28 B38); it now mails
// this page too. The page is off only while the durable outbox is requested
// but unavailable, when no link is mailed.
func (s *server) mfaDisableEmailPageEnabled() bool {
	if s.mfaDisableMailOutboxRequested == nil || !s.mfaDisableMailOutboxRequested() {
		return true
	}
	return s.deletionMailOutbox.Enabled()
}

func (s *server) mfaDisableEmailPage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.mfaDisableEmailPageEnabled() {
		http.NotFound(w, r)
		return
	}
	token := r.URL.Query().Get("token")
	raw, err := base64.RawURLEncoding.DecodeString(token)
	defer clear(raw)
	// A durable link carries the raw token; a legacy one the longer
	// age-sealed token the confirmation endpoint opens with host keys.
	if err != nil || len(raw) < api.MFADisableTokenBytes || len(raw) > api.MFADisableLegacyTokenMaxBytes {
		s.writeMFADisableEmailInvalid(w, r, acct, "token_malformed")
		return
	}
	page := dashboard.Page{Title: "Confirm MFA disable", Body: "mfa_disable", Account: acctViewFrom(acct), Data: token}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		s.log.Error("mfa.disable_email.page_render_failed")
	}
}
