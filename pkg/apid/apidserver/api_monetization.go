package apidserver

import (
	"net/http"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/apid/monetization"
	"github.com/onebox-faas/faas/pkg/apid/realtimepreview"
)

// apiMonetizationEnabled is the API monetization release gate
// (ADR-20261018090210), read on every call so an operator change applies
// without a restart. Off, its routes answer 404 before authentication, its
// workers pause and its webhook events cannot be chosen.
func (s *server) apiMonetizationEnabled() bool {
	return s.runtimeBool(runtimeConfigAPIMonetization, false)
}

// hideDisabledMonetization runs before routing, authentication and CORS, so
// disabled monetization cannot be discovered through auth errors, method
// probes or redirects.
func (s *server) hideDisabledMonetization(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if monetization.Path(r.URL.Path) && !s.apiMonetizationEnabled() {
			w.Header().Set("Cache-Control", "no-store")
			httpkit.NoSuchRoute(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// appWebhookEvents is the app webhook vocabulary an account may choose:
// without API monetization's events while it is off, or the realtime
// preview's events unless the account sees the preview (ADR-20261019102657).
func (s *server) appWebhookEvents(accountID string) []string {
	return realtimepreview.VisibleWebhookEvents(
		monetization.VisibleWebhookEvents(api.AllowedAppWebhookEvents, s.apiMonetizationEnabled()),
		s.realtime.PreviewVisible(accountID))
}

// hiddenTenantWebhookEvent reports whether filter names a monetization event
// while monetization is off.
func (s *server) hiddenTenantWebhookEvent(filter []string) bool {
	return !s.apiMonetizationEnabled() && slices.ContainsFunc(filter, monetization.WebhookEvent)
}
