package consumersignup

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// The owner's view of OAuth identities (ADR-20261124094745): which sign-in
// bills to which consumer, and unlinking one so it can link again.

func (s *Service) identityLinkStore(w http.ResponseWriter) (state.APIConsumerIdentityLinkStore, bool) {
	store, ok := s.Host.Store().(state.APIConsumerIdentityLinkStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("consumer identities are unavailable"))
	}
	return store, ok
}

// ListIdentities returns the OAuth identities of an app's consumers,
// newest first.
func (s *Service) ListIdentities(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.identityLinkStore(w)
	if !ok {
		return
	}
	app, ok := s.Host.AppRoute(w, r, acct)
	if !ok {
		return
	}
	ids, err := store.ListAPIConsumerIdentities(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list consumer identities"))
		return
	}
	out := api.ConsumerIdentityListResponse{Identities: make([]api.ConsumerIdentity, 0, len(ids))}
	for _, id := range ids {
		out.Identities = append(out.Identities, api.ConsumerIdentity{ID: id.ID, ConsumerID: id.ConsumerID, Issuer: id.Issuer,
			Subject: id.Subject, Email: id.Email, CreatedAt: id.CreatedAt, UpdatedAt: id.UpdatedAt})
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// DeleteIdentity unlinks an OAuth identity from its consumer. The person's
// next sign-in enrolls a new consumer on the free plan, or links again.
func (s *Service) DeleteIdentity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.identityLinkStore(w)
	if !ok {
		return
	}
	app, ok := s.Host.AppRoute(w, r, acct)
	if !ok {
		return
	}
	id := r.PathValue("id")
	err := store.DeleteAPIConsumerIdentity(r.Context(), acct.ID, app.ID, id)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no such consumer identity on this app")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not unlink the consumer identity"))
		return
	}
	s.Host.Audit(r.Context(), "api_consumer.identity_unlinked", acct.ID, map[string]any{"app_id": app.ID, "identity_id": id})
	w.WriteHeader(http.StatusNoContent)
}
