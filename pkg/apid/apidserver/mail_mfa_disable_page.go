package apidserver

import (
	"encoding/base64"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// Gate before authentication or token parsing. This page adds no default-on
// browser surface, and neither opening it nor email prefetch consumes a token.
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

func (s *server) mfaDisableEmailPageEnabled() bool {
	return s.mfaDisableMailOutboxRequested != nil && s.mfaDisableMailOutboxRequested() && s.deletionMailOutbox.Enabled()
}

func (s *server) mfaDisableEmailPage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.mfaDisableEmailPageEnabled() {
		http.NotFound(w, r)
		return
	}
	token := r.URL.Query().Get("token")
	raw, err := base64.RawURLEncoding.DecodeString(token)
	defer clear(raw)
	if err != nil || len(raw) != api.MFADisableTokenBytes {
		s.writeMFADisableEmailInvalid(w, r, acct, "token_malformed")
		return
	}
	page := dashboard.Page{Title: "Confirm MFA disable", Body: "mfa_disable", Account: acctViewFrom(acct), Data: token}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		s.log.Error("mfa.disable_email.page_render_failed")
	}
}
