package apidserver

import (
	"net/http"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/monetization"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// apiMonetizationEnabled reports whether API monetization is released to
// every account: the api_monetization_enabled runtime switch
// (ADR-20261018090210), or the api_monetization platform flag on for every
// account (ADR-20261020170649). Surfaces that know no account, such as the
// served OpenAPI document, follow it. It is read on every call, so an
// operator change applies without a restart.
func (s *server) apiMonetizationEnabled() bool {
	return s.runtimeBool(runtimeConfigAPIMonetization, false) ||
		s.platformFlags().Snapshot().EveryAccount(platformflags.APIMonetization)
}

// apiMonetizationOff reports whether no account has API monetization: its
// routes then answer 404 before authentication and its workers pause.
func (s *server) apiMonetizationOff() bool {
	return !s.runtimeBool(runtimeConfigAPIMonetization, false) &&
		!s.platformFlags().Snapshot().GloballyEnabled(platformflags.APIMonetization)
}

// apiMonetizationFor reports whether API monetization is released to an
// account. The flag is decided by account only, as in the gateway, so plan
// rules never match.
func (s *server) apiMonetizationFor(accountID string) bool {
	return s.runtimeBool(runtimeConfigAPIMonetization, false) ||
		s.platformFlags().Enabled(platformflags.APIMonetization, platformflags.Subject{AccountID: accountID})
}

// hideDisabledMonetization runs before routing, authentication and CORS, so
// monetization no account has cannot be discovered through auth errors,
// method probes or redirects. Once some accounts have it, the routes reach
// authentication and monetizationAccount answers the others, as for live
// queries (ADR-1700).
func (s *server) hideDisabledMonetization(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if monetization.Path(r.URL.Path) && s.apiMonetizationOff() {
			w.Header().Set("Cache-Control", "no-store")
			httpkit.NoSuchRoute(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// monetizationAccount answers a monetization route like an unregistered one,
// right after authentication, to an account the release does not include
// (ADR-20261020170649).
func (s *server) monetizationAccount(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if monetization.Path(r.URL.Path) && !s.apiMonetizationFor(acct.ID) {
			w.Header().Set("Cache-Control", "no-store")
			httpkit.NoSuchRoute(w)
			return
		}
		next(w, r, acct)
	}
}

// appWebhookEvents is the app webhook vocabulary an account may choose.
func (s *server) appWebhookEvents(accountID string) []string {
	return monetization.VisibleWebhookEvents(api.AllowedAppWebhookEvents, s.apiMonetizationFor(accountID))
}

// hiddenTenantWebhookEvent reports whether filter names a monetization event
// the account does not have.
func (s *server) hiddenTenantWebhookEvent(accountID string, filter []string) bool {
	return !s.apiMonetizationFor(accountID) && slices.ContainsFunc(filter, monetization.WebhookEvent)
}
