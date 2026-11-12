package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
)

// selfVerified puts a per-IP failure budget in front of an unauthenticated
// route that checks its own credential with a database lookup, such as
// consumer billing, whose keys pick their own rate-limit prefix. Each refused
// request (401, 403, 404) costs one; past the budget the client gets 429
// before any lookup, while callers with valid credentials are never limited
// (bug hunt H15). Routes a shared sender calls for every tenant (Slack
// interactions) stay out: one tenant's bad signing secret would block the
// sender's address for all.
func selfVerified(h http.HandlerFunc) http.Handler {
	return middleware.AuthLimit(middleware.AuthLimitConfig{
		Window:        time.Minute,
		MaxFailures:   api.SelfVerifiedRouteFailuresPerIPPerMinute,
		CountStatuses: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
	})(h)
}
