// OIDC / keyless deploy auth handler (issue #270 / ADR-101).
//
// Wires POST /v1/auth/oidc/exchange — the path that mints a
// short-lived opaque bearer (fp_oidc_<48 hex>) in exchange for an
// IdP-issued JWT. The downstream auth chain (Authorization: Bearer
// … on every deploy route) is unchanged; this handler is the ONLY
// mint path.
//
// The body of the handler is pkg/oidc.Handler. The wrapper here
// adapts the (accountHandler, http.HandlerFunc) shape: the routes
// in cmd/apid/server.go use the (accountHandler) chain for the
// authenticated routes, but the OIDC route is anonymous — the
// caller has no session, no bearer. We mount via middleware.AuthLimit
// (the per-IP fail-counter from spec §11) directly, without the
// session-required wrappers. The limit is the sha-red
// apiAuthLimiter so an OIDC brute-force contributes to the same
// 10/min/IP envelope as the rest of the API surface.
//
// The handler is constructed once at boot in newServerWithDeps
// and stamped on the server.oidcHandler field. Unit tests can
// leave it nil and skip the route; the route mount is conditional
// on the field being non-nil.
package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/apid/oidcrepo"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/oidc"
	"github.com/onebox-faas/faas/pkg/state"
)

// oidcHandler is the wiring-side wrapper around pkg/oidc.Handler.
// The http.Handler implementation is the pkg/oidc handler's
// ServeHTTP (state.APIKey-projection), which is anonymous — the
// *server does not need a session or bearer to dispatch it. The
// type is local so the construction site in newServerWithDeps
// reads as a single-typed value.
type oidcHandler struct {
	// inner is the pkg/oidc.Handler. We embed the interface so
	// the wrapper satisfies http.Handler via a single-line
	// delegation. The pkg/oidc.Handler interface is sealed
	// (constructor only, no exported fields).
	inner *oidc.Handler
}

// ServeHTTP forwards to the inner handler. The http.ResponseWriter
// + *http.Request pair is identical to what pkg/oidc.Handler
// expects; no state.Account is stamped here because the OIDC
// exchange route is anonymous.
func (h *oidcHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.inner.ServeHTTP(w, r)
}

// buildOIDCHandler constructs the OIDC handler from the server's
// existing deps. Pulled out of newServerWithDeps so the constructor
// stays readable; the only knob is the JWKS fetch timeout (5s, the
// same handshake-ish number pkg/edgejwks.Cache uses for refresh).
//
// The handler deps are wired from the same store/audit/log the
// server already holds. The OIDC package's narrow interfaces
// (AccountLookup, AuditEmitter) are satisfied by the store + audit
// directly without an adapter — see the doc comments on the
// interface declarations in pkg/oidc.
//
// The OIDC verifier (pkg/oidc.edgeJWKSVerifier) is constructed
// against a fresh pkg/edgejwks.Cache. The cache is per-process
// and refreshed on every Verify call — the same posture as the
// edge-rules gateway (cmd/gatewayd-internal/edge_rules_jwks.go:42-58).
func (s *server) buildOIDCHandler() *oidcHandler {
	// 5s JWKS fetch timeout. Mirrors the edgejwks refresh posture
	// (pkg/edgejwks/cache.go); a CI runner deploy call must
	// resolve inside a few seconds, so a 5s JWKS fetch is the
	// upper bound we tolerate. Failed fetches fall back to the
	// cached key set; sustained failures surface as 503 from the
	// handler.
	h := oidc.NewHandler(oidc.HandlerDeps{
		Verifier: oidc.NewVerifier(s.log),
		Policies: storeAsOIDCTrustPolicyStore(s.store),
		Tokens:   storeAsTokenExchangeStore(s.store),
		Lookups:  s.oidcAccountLookup(),
		Audit:    auditorAsOIDCEmitter(s.audit),
		Log:      s.log,
		// nil Clock = time.Now. The handler stamps ExpiresAt
		// against the system clock; tests inject a swappable
		// clock via HandlerDeps.Clock.
	})
	return &oidcHandler{inner: h}
}

// The narrow interfaces below are pkg/oidc-side contracts. The
// adapters are project-local glue (mirrors the pattern in
// cmd/apid/auth_adapters.go for the Authenticator interface).
// CodeQL review may flag the indirection as unnecessary — the
// interfaces are pkg/oidc's wire-shape, not a defensive layer.
// Keeping the adapters here means the pkg/oidc package can be
// independently unit-tested by injecting fakes; the production
// code path is the same adapter.

// storeAsOIDCTrustPolicyStore wraps state.Store as
// oidc.OIDCTrustPolicyStore. The store's surface is exactly the
// four UpsertOIDCTrustPolicy / GetOIDCTrustPolicy /
// ListOIDCTrustPoliciesForAccount / (DeleteOIDCExchangedToken — no,
// that's the token store) methods.
func storeAsOIDCTrustPolicyStore(s state.Store) oidc.OIDCTrustPolicyStore {
	return oidcTrustPolicyStoreAdapter{store: s}
}

type oidcTrustPolicyStoreAdapter struct{ store state.Store }

func (a oidcTrustPolicyStoreAdapter) Upsert(ctx context.Context, p *oidc.OIDCTrustPolicy) (*oidc.OIDCTrustPolicy, error) {
	return a.store.UpsertOIDCTrustPolicy(ctx, p)
}

func (a oidcTrustPolicyStoreAdapter) Get(ctx context.Context, accountID, issuerURL string) (*oidc.OIDCTrustPolicy, error) {
	policy, err := a.store.GetOIDCTrustPolicy(ctx, accountID, issuerURL)
	if errors.Is(err, state.ErrNotFound) {
		// The pkg/state contract is "ErrNotFound" but the
		// pkg/oidc-side interface uses a distinct sentinel so the
		// exchange handler can tell "no policy" from "store
		// failure". Translate at the adapter boundary so the
		// handler's errors.Is(err, ErrTrustPolicyNotFound) check
		// works for both PgStore and MemStore.
		return nil, oidc.ErrTrustPolicyNotFound
	}
	return policy, err
}

func (a oidcTrustPolicyStoreAdapter) ListForAccount(ctx context.Context, accountID string) ([]*oidc.OIDCTrustPolicy, error) {
	return a.store.ListOIDCTrustPoliciesForAccount(ctx, accountID)
}

// storeAsTokenExchangeStore wraps state.Store as
// oidc.TokenExchangeStore. The store's surface is the three
// InsertOIDCExchangedToken / GetOIDCExchangedTokenByHash /
// DeleteOIDCExchangedToken methods.
func storeAsTokenExchangeStore(s state.Store) oidc.TokenExchangeStore {
	return tokenExchangeStoreAdapter{store: s}
}

type tokenExchangeStoreAdapter struct{ store state.Store }

func (a tokenExchangeStoreAdapter) Insert(ctx context.Context, t *oidc.ExchangedToken) (string, error) {
	id, err := a.store.InsertOIDCExchangedToken(ctx, t)
	if err != nil {
		return "", err
	}
	// Return the server-minted row id so the handler can echo it
	// in the response and use it as the audit correlation key.
	t.ID = id
	return id, nil
}

func (a tokenExchangeStoreAdapter) GetByHash(ctx context.Context, hash []byte) (*oidc.ExchangedToken, error) {
	tok, err := a.store.GetOIDCExchangedTokenByHash(ctx, hash)
	if errors.Is(err, state.ErrNotFound) {
		// Same sentinel translation as
		// oidcTrustPolicyStoreAdapter.Get — the pkg/state contract
		// is "ErrNotFound" but pkg/oidc wants ErrTokenNotFound so
		// the auth middleware can distinguish "key never existed"
		// from "long-lived key was revoked".
		return nil, oidc.ErrTokenNotFound
	}
	return tok, err
}

func (a tokenExchangeStoreAdapter) Delete(ctx context.Context, id string) error {
	return a.store.DeleteOIDCExchangedToken(ctx, id)
}

// oidcAccountLookup wraps state.Store as the narrow pkg/oidc.AccountLookup
// interface. The pkg/oidc handler only needs AccountByOIDCSubject; the auth
// chain's other lookups (AccountByKeyHash, AccountByID) are not on the OIDC
// path. It adds the project repository bindings githubd confirms to the
// store's lookups (ADR-20261101160300, amendment 1).
func (s *server) oidcAccountLookup() oidc.AccountLookup {
	if _, stub := s.githubd.(stubGithubdClient); stub || s.githubd == nil {
		return newOIDCAccountLookup(s.store, nil)
	}
	return newOIDCAccountLookup(s.store, githubdRepoLookup{s.githubd})
}

// newOIDCAccountLookup confirms project repository bindings through repos;
// nil repos confirms none.
func newOIDCAccountLookup(store state.Store, repos oidcrepo.RepoLookup) accountLookupAdapter {
	adapter := accountLookupAdapter{store: store}
	if projects, ok := store.(state.OIDCProjectRepositoryResolver); ok && repos != nil {
		adapter.projects = oidcrepo.Resolver{Store: projects, Repos: repos}
	}
	return adapter
}

type accountLookupAdapter struct {
	store state.Store
	// projects confirms repositories bound only through a project; the zero
	// value confirms none.
	projects oidcrepo.Resolver
}

// AccountByOIDCSubject resolves a trust policy or app binding through the
// store, then a project binding githubd confirms.
func (a accountLookupAdapter) AccountByOIDCSubject(ctx context.Context, issuerURL, subject string) (state.Account, error) {
	acct, err := a.store.AccountByOIDCSubject(ctx, issuerURL, subject)
	if errors.Is(err, state.ErrNotFound) {
		return a.projectAccount(ctx, issuerURL, subject)
	}
	return acct, err
}

// AccountByOIDCRepositoryBinding satisfies oidc.RepositoryBindingLookup when
// the store can resolve GitHub repository bindings.
func (a accountLookupAdapter) AccountByOIDCRepositoryBinding(ctx context.Context, issuerURL, subject string) (state.Account, error) {
	resolver, ok := a.store.(state.OIDCRepositoryBindingResolver)
	if !ok {
		return a.projectAccount(ctx, issuerURL, subject)
	}
	acct, err := resolver.AccountByOIDCRepositoryBinding(ctx, issuerURL, subject)
	if errors.Is(err, state.ErrNotFound) {
		return a.projectAccount(ctx, issuerURL, subject)
	}
	return acct, err
}

func (a accountLookupAdapter) projectAccount(ctx context.Context, issuerURL, subject string) (state.Account, error) {
	accountID, err := a.projects.AccountID(ctx, issuerURL, subject)
	if err != nil {
		return state.Account{}, err
	}
	return a.store.AccountByID(ctx, accountID)
}

// GitHubRepositoryIDsForOIDCSubject satisfies oidc.RepositoryIDLookup when the
// store records the immutable IDs of a GitHub repository binding. A
// repository bound only through a project takes the ID githubd confirms, so
// the token's repository_id claim must name that repository.
func (a accountLookupAdapter) GitHubRepositoryIDsForOIDCSubject(ctx context.Context, accountID, issuerURL, subject string) (int64, int64, error) {
	var ownerID, repoID int64
	if resolver, ok := a.store.(state.OIDCRepositoryIDResolver); ok {
		var err error
		if ownerID, repoID, err = resolver.GitHubRepositoryIDsForOIDCSubject(ctx, accountID, issuerURL, subject); err != nil {
			return 0, 0, err
		}
	}
	if ownerID != 0 || repoID != 0 {
		return ownerID, repoID, nil
	}
	repoID, err := a.projects.RepositoryID(ctx, accountID, issuerURL, subject)
	return 0, repoID, err
}

// githubdRepositoryClient is the single-repository lookup a githubd from
// ADR-20261101160300 amendment 2 answers; it is kept off GithubdClient so
// existing fakes need not implement it.
type githubdRepositoryClient interface {
	GetInstallationRepository(ctx context.Context, accountID string, installationID int64, repoFullName string) (githubdgrpc.InstallationRepository, bool, error)
}

// githubdRepoLookup finds a repository an installation sees through githubd:
// one GitHub request, so the OIDC exchange stays inside apid's request
// budget. An older githubd lists the installation's repositories instead.
type githubdRepoLookup struct{ client GithubdClient }

func (l githubdRepoLookup) InstallationRepositoryID(ctx context.Context, accountID string, installationID int64, fullName string) (int64, error) {
	if lookup, ok := l.client.(githubdRepositoryClient); ok {
		repo, found, err := lookup.GetInstallationRepository(ctx, accountID, installationID, fullName)
		switch {
		case errors.Is(err, githubdgrpc.ErrInstallationRepositoryUnsupported):
		case err != nil:
			return 0, err
		case !found:
			return 0, nil
		default:
			return repo.ID, nil
		}
	}
	repos, err := l.client.ListInstallableRepos(ctx, accountID, installationID)
	if err != nil {
		return 0, err
	}
	for _, repo := range repos {
		if repo.ID > 0 && strings.EqualFold(repo.FullName, fullName) {
			return repo.ID, nil
		}
	}
	return 0, nil
}

// OIDCDeployTargetForSubject satisfies oidc.DeployTargetLookup. A store that
// cannot resolve deploy targets refuses them: the exchange then issues no
// deploy bearer under a trust policy Gregale created.
func (a accountLookupAdapter) OIDCDeployTargetForSubject(ctx context.Context, accountID, issuerURL, subject, appSlug string) (state.OIDCDeployTarget, error) {
	resolver, ok := a.store.(state.OIDCDeployTargetResolver)
	if !ok {
		return state.OIDCDeployTarget{}, state.ErrNotFound
	}
	return resolver.OIDCDeployTargetForSubject(ctx, accountID, issuerURL, subject, appSlug)
}

// auditorAsOIDCEmitter wraps *auditor as the narrow
// pkg/oidc.AuditEmitter interface. The auditor's Emit is the same
// signature (kind, accountID, data); the adapter is a single
// delegation.
func auditorAsOIDCEmitter(a *auditor) oidc.AuditEmitter {
	if a == nil {
		return nilAuditEmitter{}
	}
	return oidcEmitterAdapter{a: a}
}

type oidcEmitterAdapter struct{ a *auditor }

func (a oidcEmitterAdapter) Emit(ctx context.Context, kind string, accountID *string, data map[string]any) {
	a.a.Emit(ctx, kind, accountID, data)
}

// nilAuditEmitter is the no-op emitter when the server has no
// audit handle (older tests + dev mode that hasn't wired the
// auditor). Matches the same nil-pattern as the router-side
// helpers.
type nilAuditEmitter struct{}

func (nilAuditEmitter) Emit(_ context.Context, _ string, _ *string, _ map[string]any) {}
