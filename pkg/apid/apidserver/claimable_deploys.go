package apidserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Claimable deploys (ADR-20261124080000).
//
// An agent deploys with no account. POST /v1/anonymous/sessions creates a
// Free account with a placeholder address in the reserved .invalid domain,
// records it in unclaimed_accounts with an expiry, and returns a deploy key
// and a claim link. Until it is claimed the account may only deploy and read
// its apps (unclaimedAllowed). A human claims it by opening the link and
// proving an email address; the account then becomes theirs in place, with
// its apps, and the anonymous key stops working. An unclaimed account past
// its expiry is scheduled for deletion like any other.

const (
	// claimableDeploysCapability owns the anonymous session and claim-link
	// routes in the product catalog. It ships as a preview: anonymous
	// sessions are offered only once its flag is on by default.
	claimableDeploysCapability = "claimable-deploys"

	runtimeConfigUnclaimedTTLHours   = "unclaimed_deploy_ttl_hours"
	runtimeConfigUnclaimedLiveMax    = "unclaimed_accounts_live_max"
	runtimeConfigUnclaimedPerAddress = "unclaimed_accounts_per_address_per_day"
	runtimeConfigUnclaimedPerNetwork = "unclaimed_accounts_per_network_per_day"

	// anonymousKeyLabel names the deploy key an anonymous session mints.
	anonymousKeyLabel = "anonymous-deploy"
	// claimTokenBytes is the claim link's random token length.
	claimTokenBytes = 32

	unclaimedEventCreated         = "created"
	unclaimedEventClaimed         = "claimed"
	unclaimedEventExpired         = "expired"
	unclaimedEventRefusedNetwork  = "refused_network"
	unclaimedEventRefusedLiveCap  = "refused_live_cap"
	unclaimedEventRefusedHeldLink = "refused_held_link"
)

var unclaimedEvents = []string{
	unclaimedEventCreated, unclaimedEventClaimed, unclaimedEventExpired,
	unclaimedEventRefusedNetwork, unclaimedEventRefusedLiveCap, unclaimedEventRefusedHeldLink,
}

// claimableDeploysOpen reports whether anonymous sessions are offered: the
// capability flag is on for callers no rule names. A server without a gate
// (unit tests) offers them.
func (s *server) claimableDeploysOpen() bool {
	return s.capGate == nil || s.capGate.ReleasedByDefault(claimableDeploysCapability)
}

func (s *server) unclaimedTTL() time.Duration {
	return time.Duration(s.runtimeInt(runtimeConfigUnclaimedTTLHours, api.UnclaimedDeployTTLHoursDefault)) * time.Hour
}

// isUnclaimedAccount reports whether acct is an unclaimed anonymous-deploy
// account. The placeholder address is the marker: only an anonymous session
// creates one, and the claim replaces it, so no store read is needed.
func isUnclaimedAccount(acct state.Account) bool {
	return api.IsUnclaimedPlaceholderEmail(acct.Email)
}

// postAnonymousSession is POST /v1/anonymous/sessions (unauthenticated).
func (s *server) postAnonymousSession(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(state.UnclaimedAccountStore)
	if !ok || !s.claimableDeploysOpen() {
		writeMissingRoute(w, r)
		return
	}
	var req api.AnonymousSessionRequest
	if err := decodeJSON(r, &req); err != nil || !req.AcceptTerms {
		api.WriteProblem(w, api.ErrValidation("body must be {\"accept_terms\": true}: deploying without an account accepts the Terms of Service"))
		return
	}
	ip := clientIPFromRequest(r)
	if prob := s.anonymousSessionRefusal(r.Context(), store, ip); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	acct, raw, expiresAt, err := s.createUnclaimedAccount(r, ip)
	if err != nil {
		s.log.Error("anonymous_session.create", "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not create the anonymous session; try again"))
		return
	}
	defer clear(raw)
	if links := s.recordSignupFacts(r.Context(), r, acct.ID, acct.Email, signupMethodAnonymous); links > 0 {
		s.expireUnclaimedAccount(r.Context(), store, acct, "unclaimed_held_link")
		s.signupMetrics.observeUnclaimed(unclaimedEventRefusedHeldLink)
		api.WriteProblem(w, api.ErrAnonymousDeploysUnavailable(0))
		return
	}
	key, ok := s.mintAnonymousKey(w, r, acct, expiresAt)
	if !ok {
		return
	}
	s.signupMetrics.observeUnclaimed(unclaimedEventCreated)
	s.audit.Emit(r.Context(), "account.unclaimed_created", &acct.ID, map[string]any{
		"expires_at": expiresAt.UTC().Format(time.RFC3339), "client": truncateClient(req.Client),
	})
	writeJSON(w, http.StatusCreated, api.AnonymousSessionResponse{
		AccountID: acct.ID,
		APIKey:    key,
		ClaimURL:  s.claimURL(r, raw),
		ExpiresAt: expiresAt.UTC(),
		Limits:    unclaimedLimits(),
	})
}

// anonymousSessionRefusal applies the per-network and platform caps.
func (s *server) anonymousSessionRefusal(ctx context.Context, store state.UnclaimedAccountStore, ip string) *api.Problem {
	perAddress := s.runtimeInt(runtimeConfigUnclaimedPerAddress, api.UnclaimedAccountsPerAddressPerDayDefault)
	perNetwork := s.runtimeInt(runtimeConfigUnclaimedPerNetwork, api.UnclaimedAccountsPerNetworkPerDayDefault)
	if !s.anonSessions.allowWithin(ip, perAddress, perNetwork) {
		s.signupMetrics.observeUnclaimed(unclaimedEventRefusedNetwork)
		s.audit.Emit(ctx, "account.unclaimed_refused", nil, map[string]any{"reason": "network", "ip": ip})
		return api.ErrAnonymousDeploysUnavailable(int((24 * time.Hour).Seconds()))
	}
	limit := s.runtimeInt(runtimeConfigUnclaimedLiveMax, api.UnclaimedAccountsLiveMaxDefault)
	live, err := store.CountLiveUnclaimedAccounts(ctx, timeNow())
	if err != nil {
		// Unlike sign-up, a failed count refuses: anonymous deploys are
		// optional and the live cap is their main platform-wide bound.
		s.log.Warn("anonymous_session.count", "err", err)
		return api.ErrAnonymousDeploysUnavailable(60)
	}
	if live >= limit {
		s.signupMetrics.observeUnclaimed(unclaimedEventRefusedLiveCap)
		s.audit.Emit(ctx, "account.unclaimed_refused", nil, map[string]any{"reason": "live_cap", "live": live, "limit": limit})
		return api.ErrAnonymousDeploysUnavailable(int(time.Hour.Seconds()))
	}
	return nil
}

// createUnclaimedAccount creates the Free account and its unclaimed row in
// one transaction. It returns the claim token's plaintext.
func (s *server) createUnclaimedAccount(r *http.Request, ip string) (state.Account, []byte, time.Time, error) {
	raw := make([]byte, claimTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return state.Account{}, nil, time.Time{}, err
	}
	local := make([]byte, 10)
	if _, err := rand.Read(local); err != nil {
		return state.Account{}, nil, time.Time{}, err
	}
	expiresAt := timeNow().Add(s.unclaimedTTL())
	res, err := s.store.CreateAccountWithPersonalOrg(r.Context(), state.CreateAccountWithPersonalOrgParams{
		Email: "anon-" + hex.EncodeToString(local) + "@" + api.UnclaimedEmailDomain,
		Plan:  api.PlanFree,
		// The placeholder is never mailed; the account is trusted to deploy
		// within the unclaimed limits, and a claim proves a real address.
		RequireEmailVerification: false,
		TermsAcceptance:          signupTermsAcceptance(signupMethodAnonymous),
		Unclaimed: &state.UnclaimedAccountParams{
			ClaimTokenSHA256: api.HashToken(raw),
			ExpiresAt:        expiresAt,
			CreatedIP:        ip,
			UserAgent:        r.UserAgent(),
		},
	})
	if err != nil {
		clear(raw)
		return state.Account{}, nil, time.Time{}, err
	}
	s.anonSessions.record(ip)
	s.recordSignup(r.Context(), res.Account.ID, signupMethodAnonymous, signupSourceAgent)
	return res.Account, raw, expiresAt, nil
}

// mintAnonymousKey creates the session's deploy key, expiring with the
// account. It writes the problem and returns false on failure.
func (s *server) mintAnonymousKey(w http.ResponseWriter, r *http.Request, acct state.Account, expiresAt time.Time) (string, bool) {
	plaintext, hash, err := api.GenerateAPIKey()
	if err == nil {
		var org state.Org
		org, err = s.store.OrgByPersonalAccount(r.Context(), acct.ID)
		if err == nil {
			var k state.APIKey
			k, err = s.store.CreateOrgAPIKeyWithProvenance(r.Context(), org.ID, acct.ID, hash, anonymousKeyLabel,
				api.ScopesAdminOnly, &expiresAt, clientIPFromRequest(r), r.UserAgent(), nil)
			if err == nil {
				s.recordKeyDisplayPrefix(r.Context(), k.ID, plaintext)
				return plaintext, true
			}
		}
	}
	s.log.Error("anonymous_session.key", "err", err, "account_id", acct.ID)
	api.WriteProblem(w, api.ErrCapacity("could not create the anonymous session; try again"))
	return "", false
}

func truncateClient(client string) string {
	client = strings.TrimSpace(client)
	if len(client) > 80 {
		client = client[:80]
	}
	return client
}

func unclaimedLimits() api.UnclaimedLimits {
	limits, _ := api.LimitsFor(api.PlanFree)
	return api.UnclaimedLimits{
		Plan:    string(api.PlanFree),
		MaxApps: limits.DeployedApps,
		MaxRAM:  limits.RAMMB,
		Blocked: append([]string(nil), api.UnclaimedBlockedFeatures...),
	}
}

// claimURLBase is where claim pages are served: the API host, which always
// routes /claim to apid (the apex is the web dashboard's).
func (s *server) claimURLBase(r *http.Request) string {
	if domain := strings.TrimPrefix(strings.TrimSpace(s.platformHost()), "."); domain != "" && domain != domainUnset {
		return "https://api." + domain
	}
	return s.passwordResetLinkBase(r)
}

func (s *server) claimURL(r *http.Request, raw []byte) string {
	return s.claimURLBase(r) + claimPagePath + "/" + base64.RawURLEncoding.EncodeToString(raw)
}

// postClaimLink is POST /v1/account/claim-link: an unclaimed account's key
// gets a fresh claim link; the previous link stops working.
func (s *server) postClaimLink(w http.ResponseWriter, r *http.Request, acct state.Account) {
	actor := actorAccount(r, acct)
	store, ok := s.store.(state.UnclaimedAccountStore)
	if !ok || !isUnclaimedAccount(actor) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Account already claimed", "only an account created by an anonymous deploy has a claim link"))
		return
	}
	row, err := store.UnclaimedAccountByID(r.Context(), actor.ID)
	if err != nil || !row.Live(timeNow()) {
		api.WriteProblem(w, api.NewProblem(http.StatusGone, api.CodeConflict,
			"Claim window ended", "this anonymous deploy expired; run gregale login and deploy again"))
		return
	}
	raw := make([]byte, claimTokenBytes)
	defer clear(raw)
	if _, err := rand.Read(raw); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create a claim link"))
		return
	}
	if err := store.RotateUnclaimedClaimToken(r.Context(), actor.ID, api.HashToken(raw)); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create a claim link"))
		return
	}
	writeJSON(w, http.StatusOK, api.ClaimLinkResponse{ClaimURL: s.claimURL(r, raw), ExpiresAt: row.ExpiresAt.UTC()})
}

// unclaimedAllowed holds an unclaimed account to deploying and reading its
// apps. Reads are allowed under the deploy surfaces; writes are allowed
// there unless the route touches something only a claimed account may use.
// Everything else answers claim_required.
func (s *server) unclaimedAllowed(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if isUnclaimedAccount(actorAccount(r, acct)) && !unclaimedRouteAllowed(r.Method, r.Pattern, r.URL.Path) {
			api.WriteProblem(w, api.ErrClaimRequired())
			return
		}
		next(w, r, acct)
	}
}

// unclaimedRoots are the API roots an unclaimed account may use.
var unclaimedRoots = []string{
	"/v1/apps", "/v1/deployments", "/v1/uploads", "/v1/projects", "/v1/builds",
	"/v1/account", "/v1/me", "/v1/whoami", "/v1/usage", "/v1/runtimes",
}

// unclaimedDeniedSegments are path segments whose writes need a claim.
var unclaimedDeniedSegments = map[string]bool{
	"domains": true, "domain": true, "certificates": true, "tenant-surfaces": true,
	"postgres": true, "databases": true, "database": true, "redis": true, "valkey": true, "caches": true,
	"buckets": true, "objects": true, "object-storage": true, "storage": true,
	"keys": true, "tokens": true, "deploy-tokens": true, "service-accounts": true, "oauth": true, "sso": true,
	"billing": true, "plan": true, "subscription": true, "invoices": true, "spend-cap": true, "payment-methods": true,
	"orgs": true, "members": true, "invitations": true, "transfer": true,
	"crons": true, "cron": true, "webhooks": true, "email": true, "smtp": true,
	"mirror": true, "mirrors": true, "egress": true, "static-egress-ip": true, "reserved-ips": true,
	"private-networks": true, "private-network": true, "tcp": true, "udp": true,
	"forks": true, "sandboxes": true, "consumers": true, "monetization": true, "mcp": true, "agents": true,
	"min-instances": true, "scaling": true,
}

// unclaimedAccountWrites are the /v1/account writes an unclaimed account may
// make: only fetching a fresh claim link.
var unclaimedAccountWrites = map[string]bool{
	"/v1/account/claim-link": true,
}

func unclaimedRouteAllowed(method, pattern, path string) bool {
	p := pattern
	if i := strings.IndexByte(p, ' '); i >= 0 {
		p = p[i+1:]
	}
	if p == "" {
		p = path
	}
	root := ""
	for _, candidate := range unclaimedRoots {
		if p == candidate || strings.HasPrefix(p, candidate+"/") || strings.HasPrefix(p, candidate+":") {
			root = candidate
			break
		}
	}
	if root == "" {
		return false
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if root == "/v1/account" {
		return unclaimedAccountWrites[p]
	}
	for _, seg := range strings.Split(p, "/") {
		seg = strings.ToLower(seg)
		if i := strings.IndexByte(seg, ':'); i >= 0 {
			seg = seg[:i]
		}
		if unclaimedDeniedSegments[seg] {
			return false
		}
	}
	return true
}

// expireUnclaimedAccount ends an unclaimed account: the row is marked expired
// and the account is scheduled for deletion, which stops its apps and keys.
func (s *server) expireUnclaimedAccount(ctx context.Context, store state.UnclaimedAccountStore, acct state.Account, via string) bool {
	marked, err := store.MarkUnclaimedAccountExpired(ctx, acct.ID, timeNow())
	if err != nil || !marked {
		return false
	}
	if _, prob := s.scheduleDeletion(ctx, acct, via, false); prob != nil {
		s.log.Warn("unclaimed_expiry.schedule", "account_id", acct.ID, "status", prob.Status, "code", prob.Code)
	}
	s.audit.Emit(ctx, "account.unclaimed_expired", &acct.ID, map[string]any{"via": via})
	return true
}

// completeClaim runs once the claimer's address is on the account: the
// unclaimed row is closed and every anonymous key is revoked, so the agent
// that deployed must sign in as the new owner to keep deploying.
func (s *server) completeClaim(ctx context.Context, accountID string) {
	store, ok := s.store.(state.UnclaimedAccountStore)
	if !ok {
		return
	}
	claimed, err := store.MarkUnclaimedAccountClaimed(ctx, accountID, timeNow())
	if err != nil {
		s.log.Error("claim.mark", "err", err, "account_id", accountID)
		return
	}
	if !claimed {
		return
	}
	if keys, err := s.store.ListAPIKeys(ctx, accountID); err == nil {
		for _, k := range keys {
			if err := s.store.DeleteAPIKey(ctx, accountID, k.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
				s.log.Warn("claim.revoke_key", "err", err, "account_id", accountID)
			}
		}
	}
	s.signupMetrics.observeUnclaimed(unclaimedEventClaimed)
	s.audit.Emit(ctx, "account.claimed", &accountID, nil)
}

// runUnclaimedAccountExpiry sweeps every few minutes until ctx ends.
func (s *server) runUnclaimedAccountExpiry(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		s.expireUnclaimedAccounts(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// expireUnclaimedAccounts ends one batch of unclaimed accounts past their
// expiry and returns how many it ended. Several apids may run it at once:
// marking the row expired is the claim on the work.
func (s *server) expireUnclaimedAccounts(ctx context.Context) int {
	store, ok := s.store.(state.UnclaimedAccountStore)
	if !ok {
		return 0
	}
	ids, err := store.ListExpiredUnclaimedAccounts(ctx, timeNow(), api.UnclaimedAccountExpiryBatch)
	if err != nil {
		s.log.Warn("unclaimed_expiry.list", "err", err)
		return 0
	}
	ended := 0
	for _, id := range ids {
		acct, err := s.store.AccountByID(ctx, id)
		if err != nil || !isUnclaimedAccount(acct) {
			continue
		}
		if s.expireUnclaimedAccount(ctx, store, acct, "unclaimed_expiry") {
			s.signupMetrics.observeUnclaimed(unclaimedEventExpired)
			ended++
		}
	}
	if ended > 0 {
		s.log.Info("unclaimed_expiry.ended", "count", ended)
	}
	return ended
}
