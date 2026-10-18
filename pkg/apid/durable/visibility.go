package durable

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Route keeps the durable entity preview (ADR-712) off the customer surface.
// While FAAS_DURABLE_ENTITIES_ENABLED is off, every /v1/apps/{slug}/entities
// route answers exactly like an unregistered API route, before authentication,
// so neither the route nor the preview is discoverable. The check runs per
// request because apid tests build the handler before configuring entities.
func (s *Service) Route(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Engine == nil {
			httpkit.NoSuchRoute(w)
			return
		}
		next(w, r)
	}
}

// App extends Route to apps outside the FAAS_DURABLE_ENTITY_APPS allowlist
// while the preview is on: they also see an unregistered route instead of a
// preview-specific refusal.
func (s *Service) App(next httpkit.AccountHandler) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
		if !ok {
			return
		}
		if !s.Apps[app.ID] {
			httpkit.NoSuchRoute(w)
			return
		}
		next(w, r, acct)
	}
}

// Hidden reports whether r targets a durable entity route while the preview
// is off, so apid's API contract fallback answers a method mismatch with 404
// instead of a 405 whose Allow header would reveal the route.
func (s *Service) Hidden(r *http.Request) bool {
	return s.Engine == nil && IsEntityPath(r.URL.Path)
}

// IsEntityPath matches /v1/apps/{slug}/entities and everything below it.
func IsEntityPath(path string) bool {
	rest, ok := strings.CutPrefix(path, "/v1/apps/")
	if !ok {
		return false
	}
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return false
	}
	rest = rest[slash+1:]
	return rest == "entities" || strings.HasPrefix(rest, "entities/")
}
