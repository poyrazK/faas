// RealService — slice 8. Implements the full pkg/githubdgrpc.Service
// contract (8 methods) using the OAuth + token cache + Checks writer
// from this slice. Slice 7's Service skeleton is the inbound-webhook
// side; RealService is the dashboard/OAuth side. Both share the
// githubdgrpc.Service interface via embedding UnimplementedService.
//
// PR-B demoted the in-memory `bindings` map to a read-through cache
// for the durable BindingsStore (cmd-side adapter backed by
// pkg/state.PgStore).
//
// PR-C (audit-gap closure) does the same for the install state:
// the `installs` map is now a read-through cache for the durable
// StoreInstalls (Postgres-backed via the cmd-side adapter, table
// github_installations from migration 00059). Pre-PR-C, githubd's
// restart vaporized this map and the dashboard's
// /v1/install/repos/list + /v1/apps/{slug}/install/bind started
// 502'ing; PR-C keeps the cache hot in-process for the warm path
// while making cold-start rehydrate deterministic. The install
// token is also sealed at rest under the host age key so the
// plaintext "ghs_…" form never lands in the database.
package githubd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gitfetch"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// defaultProductionBranch is the branch fallback used when the
// dashboard's bind form omits one. GitHub's "main" is the post-2020
// default; older installs default to "master" via the install
// payload (slice 9's dashboard form captures that).
const defaultProductionBranch = "main"

// installTokenSealKey is the Envelope key pkg/secretbox.SealOne
// uses to label the install-token value. Upper-cased so it can't
// collide with any future lower-case envelope keys (e.g. "stripe").
// The unseal path in ensureInstallToken matches this key exactly.
const installTokenSealKey = "GITHUB_INSTALL_TOKEN"

// maxInstallTokenBytes bounds the provider-issued install-token length
// before sealing. GitHub installation tokens are opaque credentials and
// their length is not a stable API contract; the previous 256-byte cap
// rejected a valid token during the live OAuth callback. Keep a defensive
// bound, but leave enough room for provider format changes without allowing
// an unexpectedly large response to bloat the durable row.
const maxInstallTokenBytes = 4 << 10

// tokenRefreshSkew is the lead-time ensureInstallToken uses to
// decide "is the sealed token still good, or should I re-mint?".
// 30 s matches TokenCache's refresh window order-of-magnitude; we
// want to re-mint BEFORE GitHub rejects a request with a stale
// token, not after.
const tokenRefreshSkew = 30 * time.Second

// AuditEvent is the audit callback signature RealService uses to
// emit install-handshake events (PR-C adds auth.install.token_sealed
// to the existing auth.install.* taxonomy from PR-A + PR-B). nil
// means "no auditor wired" — RealService still works, it just
// doesn't fire the event. cmd/githubd/main.go wires the callback
// to the apid auditor.
type AuditEvent func(event string, accountID string, payload map[string]any)

// RealService is the slice-8 production implementation of
// githubdgrpc.Service. It composes:
//   - AppAuth (RS256 JWT minting + installation-token exchange
//   - user-to-server OAuth exchange, PR-C)
//   - TokenCache (singleflight, proactive refresh; Seed added PR-C)
//   - ChecksAPI (POST /repos/{o}/{r}/check-runs)
//   - BindingsStore (Postgres-backed via cmd-side adapter, PR-B)
//   - StoreInstalls (Postgres-backed via cmd-side adapter, PR-C)
//   - Recipient/Identity for age sealing the install token at
//     rest; nil disables sealing (tests that don't go through the
//     full rehydrate path use the 4-arg NewRealService wrapper).
type RealService struct {
	githubdgrpc.UnimplementedService

	Auth     *AppAuth
	Tokens   *TokenCache
	Checks   *ChecksAPI
	Store    BindingsStore
	Installs StoreInstalls
	// Streamer is the SourceRefStreamer implementation wired
	// for the DEPLOY-PROV-4 / ADR-092 (issue #739) source-ref
	// deploy path. Production wiring (cmd/githubd/main.go) passes
	// a *sourceRefStreamer that resolves the install row via
	// s.Installs, mints the install token via s.Tokens, and
	// proxies the codeload body. nil-safe: StreamSourceRef
	// returns an error when Streamer is unconfigured.
	Streamer  SourceRefStreamer
	Recipient *age.X25519Recipient
	// Identities is the multi-identity unseal slice for rotation
	// overlap (issue #316 / ADR-057). Pre-rotation: length 1
	// (just the current). During the 30-day overlap window:
	// length 2 ([current, previous]). Loaded by the githubd
	// boot path via secretbox.LoadHostKeys(dir). SetIdentity
	// (deprecated) keeps the 1-element-slice shape for backward
	// compat with existing callers/tests.
	Identities []*age.X25519Identity
	// Identity is the single-identity unseal accessor. Same
	// rotation-overlap caveat as Identities — call sites that
	// need the multi-recipient fallback should pass
	// service.Identities to secretbox.OpenMulti directly.
	Identity *age.X25519Identity
	Audit    AuditEvent

	// bindingsCache is keyed by accountID → appID → state.GitHubBinding.
	// Demoted from source-of-truth to read-through cache by PR-B.
	// On BindAppRepo, we write to Store first; on success we
	// populate the cache. On GetAppBinding, we hit the cache; on
	// miss we fall back to Store.GetForApp and rebuild.
	bindingsCacheMu sync.RWMutex
	bindingsCache   map[string]map[string]state.GitHubBinding

	// installs is keyed by accountID → install state. Demoted to
	// a read-through cache by PR-C. Same shape as pre-PR-C (the
	// gRPC InstallState enum + InstID + DefBranch) so the
	// Service interface stays additive.
	installsMu sync.RWMutex
	installs   map[string]installState
}

// installState mirrors the githubdgrpc.InstallState enum plus the
// installation_id (string for cross-language stability; GitHub's
// integer IDs fit comfortably).
type installState struct {
	State     githubdgrpc.InstallState
	InstID    string
	DefBranch string
}

// NewRealService builds a RealService with all the PR-C wiring:
// the user-to-server install store, the age sealing recipient
// (used at mint time) and identity (used at rehydrate time), and
// the audit callback. Pass nil for installs/recipient/identity/audit
// to disable that capability — the 4-arg wrapper NewRealServiceLegacy
// preserves the PR-B shape for tests that don't exercise the cold
// path.
func NewRealService(auth *AppAuth, tokens *TokenCache, checks *ChecksAPI, store BindingsStore, installs StoreInstalls, recipient *age.X25519Recipient, identity *age.X25519Identity, audit AuditEvent) *RealService {
	return &RealService{
		Auth:          auth,
		Tokens:        tokens,
		Checks:        checks,
		Store:         store,
		Installs:      installs,
		Recipient:     recipient,
		Identity:      identity,
		Audit:         audit,
		bindingsCache: map[string]map[string]state.GitHubBinding{},
		installs:      map[string]installState{},
	}
}

// NewRealServiceLegacy preserves the PR-B 4-arg constructor shape
// for tests that don't exercise the seal/rehydrate path (e.g.
// BindAndLookup). Wraps NewRealService with nil installs/recipient/
// identity/audit. Real code should always use NewRealService so the
// cold-start rehydrate path is wired.
func NewRealServiceLegacy(auth *AppAuth, tokens *TokenCache, checks *ChecksAPI, store BindingsStore) *RealService {
	return NewRealService(auth, tokens, checks, store, nil, nil, nil, nil)
}

// WithStreamer wires a SourceRefStreamer implementation for
// the DEPLOY-PROV-4 / ADR-092 (issue #739) headless source-ref
// deploy path. Returns the receiver so the wiring reads as a
// builder chain in cmd/githubd/main.go.
//
// Streamer may be nil to disable the path (slice 1 / test
// builds); StreamSourceRef returns an error when Streamer is
// unconfigured so the gRPC handler maps it to codes.Unavailable.
func (s *RealService) WithStreamer(streamer SourceRefStreamer) *RealService {
	s.Streamer = streamer
	return s
}

// GetInstallState returns the install state for the given account.
// Returns UNSPECIFIED for accounts that haven't connected.
//
// PR-C: cache-first, falls back to StoreInstalls on miss (and
// rebuilds the cache so the warm path stays hot). Pre-PR-C the
// in-memory map was the source of truth; a kill -TERM was enough
// to make this return UNSPECIFIED for every account that had
// completed the OAuth handshake before the restart.
func (s *RealService) GetInstallState(accountID string) (githubdgrpc.InstallState, string, string, error) {
	s.installsMu.RLock()
	st, ok := s.installs[accountID]
	s.installsMu.RUnlock()
	if ok {
		return st.State, st.InstID, st.DefBranch, nil
	}
	if s.Installs == nil {
		return githubdgrpc.InstallStateUnspecified, "", "", nil
	}
	inst, err := s.Installs.ForAccount(context.Background(), accountID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return githubdgrpc.InstallStateUnspecified, "", "", nil
		}
		return githubdgrpc.InstallStateUnspecified, "", "", err
	}
	st = installState{
		State:     githubdgrpc.InstallStateInstalled,
		InstID:    strconv.FormatInt(inst.InstallationID, 10),
		DefBranch: inst.DefaultBranch,
	}
	s.installsMu.Lock()
	s.installs[accountID] = st
	s.installsMu.Unlock()
	return st.State, st.InstID, st.DefBranch, nil
}

// ExchangeOAuthCode trades a one-shot OAuth code for an installation
// token, persists the install state to StoreInstalls, and emits the
// auth.install.token_sealed audit event. PR-C closure.
//
// Flow:
//
//  1. POST /login/oauth/access_token with client_id+secret+code
//     → user-to-server access token.
//  2. GET /user/installations with the user token → list of
//     installs visible to the user. We pick the first one
//     (PR-C's single-install-per-account assumption; widening to
//     multi-install is a future migration).
//  3. Re-verify the install via /app/installations/{id} as a
//     defense-in-depth §11 check, defensively in case the user's
//     access token has scopes that include an install they don't
//     actually own.
//  4. POST /app/installations/{id}/access_tokens with the App JWT
//     → installation token.
//  5. Seal the install token under the host age key, write to
//     StoreInstalls.Upsert, populate the in-memory cache, seed
//     TokenCache so the next ListInstallableRepos / BindAppRepo is
//     a cache hit.
//  6. Emit auth.install.token_sealed via s.Audit.
//
// Returns the (installation_id, default_branch) pair. The Service
// signature widens from (string, error) to (string, string, error)
// so the wire response can carry default_branch to the apid handler.
func (s *RealService) ExchangeOAuthCode(accountID, code, stateStr string) (string, string, error) {
	if accountID == "" {
		return "", "", fmt.Errorf("githubd: accountID required")
	}
	if code == "" {
		return "", "", fmt.Errorf("githubd: code required")
	}
	if stateStr == "" {
		// Defense-in-depth: the apid handler is the canonical
		// verifier (constant-time compare against the session
		// envelope's ConnectState), but githubd also rejects an
		// empty state so a future caller that bypasses apid can't
		// accidentally skip the CSRF check.
		return "", "", fmt.Errorf("githubd: state required")
	}
	if s.Auth == nil {
		return "", "", fmt.Errorf("githubd: OAuth not configured")
	}
	if s.Installs == nil {
		return "", "", fmt.Errorf("githubd: install store not configured (PR-C wiring missing)")
	}
	if s.Recipient == nil {
		return "", "", fmt.Errorf("githubd: age recipient not configured (PR-C wiring missing)")
	}

	userToken, err := s.Auth.ExchangeUserOAuthCode(context.Background(), code)
	if err != nil {
		return "", "", fmt.Errorf("githubd: exchange user oauth code: %w", err)
	}
	installs, err := s.Auth.ListInstallationsForUser(context.Background(), userToken)
	if err != nil {
		return "", "", fmt.Errorf("githubd: list user installations: %w", err)
	}
	if len(installs) == 0 {
		return "", "", fmt.Errorf("githubd: user has no app installations")
	}
	// Single-install assumption: pick the first. Multi-install is
	// a future migration.
	pick := installs[0]
	// Defense-in-depth §11 re-verify.
	instPayload, verified, err := s.Auth.VerifyInstallation(context.Background(), pick.ID, pick.Account.Login)
	if err != nil {
		return "", "", fmt.Errorf("githubd: re-verify installation: %w", err)
	}
	if !verified {
		return "", "", fmt.Errorf("githubd: installation %d failed §11 ownership re-verify", pick.ID)
	}

	rawToken, expiresAt, err := s.Auth.ExchangeInstallationToken(context.Background(), pick.ID)
	if err != nil {
		return "", "", fmt.Errorf("githubd: exchange installation token: %w", err)
	}

	// Seal before persist. Plaintext never reaches the database.
	sealed, err := secretbox.SealOne(s.Recipient, installTokenSealKey, rawToken, maxInstallTokenBytes)
	if err != nil {
		return "", "", fmt.Errorf("githubd: seal install token: %w", err)
	}

	// default_branch: the /app/installations/{id} verify response
	// doesn't carry the install's default branch (per GitHub's API
	// surface — that's a per-repo field, not a per-install field).
	// Capture-time options for PR-C:
	//
	//   1. (chosen) store the GitHub App convention "main" as a
	//      best-effort seed. The bind path will refresh this from
	//      ListInstallableRepos at /v1/apps/{slug}/install/bind
	//      time, where the per-repo default_branch is available.
	//   2. (deferred) do a 4th round-trip to
	//      /app/installations/{id}/repositories to capture the
	//      first repo's default_branch at handshake time. PR-D
	//      scope — adds latency to the OAuth handshake.
	//
	// PR-C review finding #1 was originally framed as "the verify
	// payload carries DefaultBranch" — that was wrong; the field
	// doesn't exist on the Installation struct. The fix is the
	// bind-path refresh, not a different field reference here.
	branch := defaultProductionBranch

	// Verified-side login for the §11 audit trail. The
	// /user/installations response is the user-claimed login; the
	// /app/installations/{id} verify response is the authoritative
	// one. PR-C review finding #13: audit paper trails must point
	// at the verified identity, not the user-claimed one, so a
	// forged login can't poison the paper trail.
	auditLogin := instPayload.AccountLogin
	if auditLogin == "" {
		auditLogin = pick.Account.Login
	}

	if err := s.Installs.Upsert(context.Background(), state.GitHubInstall{
		AccountID:        accountID,
		InstallationID:   pick.ID,
		DefaultBranch:    branch,
		SealedToken:      sealed,
		TokenExpiresAt:   expiresAt,
		AuditGithubLogin: auditLogin,
	}); err != nil {
		return "", "", fmt.Errorf("githubd: persist install state: %w", err)
	}

	// Populate the in-memory cache so the next read is hot.
	s.installsMu.Lock()
	s.installs[accountID] = installState{
		State:     githubdgrpc.InstallStateInstalled,
		InstID:    strconv.FormatInt(pick.ID, 10),
		DefBranch: branch,
	}
	s.installsMu.Unlock()

	// Seed the install-token cache with the freshly-minted token
	// so the first ListInstallableRepos / BindAppRepo doesn't pay
	// the re-mint cost.
	if s.Tokens != nil {
		if err := s.Tokens.Seed(context.Background(), pick.ID, rawToken, expiresAt); err != nil {
			// Seed errors aren't fatal — the next Token() call will
			// re-mint. Log via audit but don't fail the handshake.
			if s.Audit != nil {
				s.Audit("auth.install.token_seed_failed", accountID, map[string]any{
					"install_id": pick.ID,
					"err":        err.Error(),
				})
			}
		}
	}

	// Audit event: token sealed + persisted. Fires AFTER the store
	// write succeeds, so a failed upsert doesn't generate a
	// misleading "sealed" line. github_login is the verified-side
	// login (from /app/installations/{id}) so the audit trail
	// reflects the install, not the user-claimed login.
	if s.Audit != nil {
		s.Audit("auth.install.token_sealed", accountID, map[string]any{
			"install_id":    pick.ID,
			"github_login":  auditLogin,
			"token_expires": expiresAt.Format(time.RFC3339),
			"sealed_bytes":  len(sealed),
		})
	}

	return strconv.FormatInt(pick.ID, 10), branch, nil
}

// ListInstallableRepos returns the repos the installation can see.
// Requires a non-nil Auth + Tokens. PR-C: rehydrate path goes
// through ensureInstallToken so the cold-start case (TokenCache
// miss after a kill -TERM) is handled: the sealed token is
// unsealed from the durable row instead of returning the
// pre-PR-C "no installation for account" error.
func (s *RealService) ListInstallableRepos(accountID string) ([]githubdgrpc.Repo, error) {
	if s.Auth == nil || s.Tokens == nil {
		return nil, fmt.Errorf("githubd: OAuth not configured")
	}
	_, token, err := s.ensureInstallToken(context.Background(), accountID)
	if err != nil {
		return nil, err
	}
	repos, err := s.Auth.ListInstallableRepos(context.Background(), token, 0)
	if err != nil {
		return nil, err
	}
	out := make([]githubdgrpc.Repo, 0, len(repos))
	for _, r := range repos {
		out = append(out, githubdgrpc.Repo{
			FullName:      r.FullName,
			DefaultBranch: r.DefaultBranch,
			Private:       r.Private,
		})
	}
	return out, nil
}

// BindAppRepo associates an app with (repo, branch) for the given
// account. PR-B: writes the bind to the durable Store FIRST; on
// success populates the in-memory cache. On a Store failure the
// cache is untouched so the next read pulls fresh state.
//
// bindingID is the deterministic "bind-<appID>-<repo>" form the
// pre-PR-B in-memory map emitted; the (account_id, binding_id)
// unique partial index in migration 00050 makes the upsert
// idempotent on retry.
//
// PR-C: installID lookup goes through lookupInstall (cache →
// StoreInstalls) so a kill -TERM doesn't break the bind path.
func (s *RealService) BindAppRepo(appID, accountID, repoFullName, productionBranch string) (string, error) {
	if appID == "" || accountID == "" || repoFullName == "" {
		return "", fmt.Errorf("githubd: appID, accountID, repoFullName required")
	}
	if productionBranch == "" {
		productionBranch = defaultProductionBranch
	}
	bindingID := fmt.Sprintf("bind-%s-%s", appID, repoFullName)

	installID, err := s.lookupInstall(context.Background(), accountID)
	if err != nil {
		return "", err
	}

	if s.Store == nil {
		return "", fmt.Errorf("githubd: bindings store not configured")
	}

	bid, err := s.Store.Upsert(context.Background(), state.GitHubBinding{
		AppID:            appID,
		AccountID:        accountID,
		InstallID:        installID,
		RepoFullName:     repoFullName,
		ProductionBranch: productionBranch,
		BindingID:        bindingID,
	})
	if err != nil {
		return "", fmt.Errorf("githubd: upsert binding: %w", err)
	}

	// Cache the row we just persisted.
	s.bindingsCacheMu.Lock()
	if _, ok := s.bindingsCache[accountID]; !ok {
		s.bindingsCache[accountID] = map[string]state.GitHubBinding{}
	}
	s.bindingsCache[accountID][appID] = state.GitHubBinding{
		AppID:            appID,
		AccountID:        accountID,
		InstallID:        installID,
		RepoFullName:     repoFullName,
		ProductionBranch: productionBranch,
		BindingID:        bid,
	}
	s.bindingsCacheMu.Unlock()
	return bid, nil
}

// lookupInstall returns the GitHub installation_id for an account,
// hitting the in-memory cache first and falling back to the durable
// StoreInstalls on miss (PR-C). The cache is rebuilt on a cold hit
// so the warm path stays hot. Returns the same
// "no installation for account" error the pre-PR-B code emitted so
// callers that distinguish "never installed" from "DB down" via
// errors.Is(err, state.ErrNotFound) keep working.
func (s *RealService) lookupInstall(ctx context.Context, accountID string) (int64, error) {
	s.installsMu.RLock()
	st, ok := s.installs[accountID]
	s.installsMu.RUnlock()
	if ok {
		var id int64
		if _, err := fmt.Sscanf(st.InstID, "%d", &id); err == nil && id > 0 {
			return id, nil
		}
	}
	if s.Installs == nil {
		return 0, fmt.Errorf("githubd: no installation for account %s", accountID)
	}
	inst, err := s.Installs.ForAccount(ctx, accountID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return 0, fmt.Errorf("githubd: no installation for account %s", accountID)
		}
		return 0, err
	}
	if inst.InstallationID <= 0 {
		return 0, fmt.Errorf("githubd: no installation for account %s", accountID)
	}
	// Rebuild cache.
	s.installsMu.Lock()
	s.installs[accountID] = installState{
		State:     githubdgrpc.InstallStateInstalled,
		InstID:    strconv.FormatInt(inst.InstallationID, 10),
		DefBranch: inst.DefaultBranch,
	}
	s.installsMu.Unlock()
	return inst.InstallationID, nil
}

// ensureInstallToken is the unified warm + cold path that returns
// (installationID, installToken, err) for an account. Order:
//
//  1. lookupInstall resolves the install id (cache → durable).
//  2. Durable rehydrate FIRST: if the cached token is within
//     refreshSkew of its expires_at (or missing), read the sealed
//     row, unseal it, seed TokenCache, and return. This is what
//     a freshly-restarted process does (TokenCache is empty).
//  3. TokenCache.Token() otherwise — warm path: in-process hit
//     with singleflight refresh.
//
// This ordering matters: with the cache-first order (pre-PR-C),
// TokenCache.Token() minted a fresh install token on every cold
// start, ignoring the durable row entirely. PR-C promotes the
// sealed row to the source of truth on cache miss so the
// restart-survival smoke (PR-C acceptance #3) holds: a githubd
// restart returns the same install token api.github.com issued
// pre-restart, not a freshly-minted replacement.
func (s *RealService) ensureInstallToken(ctx context.Context, accountID string) (int64, string, error) {
	if s.Tokens == nil {
		return 0, "", fmt.Errorf("githubd: install token cache not configured")
	}
	installID, err := s.lookupInstall(ctx, accountID)
	if err != nil {
		return 0, "", err
	}
	// Step 1: try to rehydrate from durable. This is what
	// a freshly-restarted process does — TokenCache is empty,
	// so the durable row is the source of truth.
	//
	// Prefer s.Identities (multi-identity slice) over s.Identity
	// (single) so the rotation-overlap window unseals envelopes
	// sealed under either the current or previous host.age.
	// s.Identities is the canonical accessor; s.Identity is the
	// backward-compat seam for callers that haven't been migrated
	// yet (issue #316 / ADR-057).
	identities := s.Identities
	if identities == nil && s.Identity != nil {
		identities = []*age.X25519Identity{s.Identity}
	}
	if s.Installs != nil && len(identities) > 0 {
		inst, ierr := s.Installs.ForAccount(ctx, accountID)
		switch {
		case ierr == nil:
			now := time.Now()
			if inst.TokenExpiresAt.After(now.Add(tokenRefreshSkew)) {
				env, oerr := secretbox.OpenMulti(identities, inst.SealedToken)
				if oerr != nil {
					return 0, "", fmt.Errorf("githubd: unseal install token: %w", oerr)
				}
				raw, ok := env[installTokenSealKey]
				if !ok || raw == "" {
					return 0, "", fmt.Errorf("githubd: sealed install token missing key %q", installTokenSealKey)
				}
				_ = s.Tokens.Seed(ctx, installID, raw, inst.TokenExpiresAt)
				return installID, raw, nil
			}
			// Token is within refresh skew or already expired:
			// rotate it (mint + re-seal + persist + audit).
			return s.rotateInstallToken(ctx, accountID, installID, inst)
		case errors.Is(ierr, state.ErrNotFound):
			// No durable row — fall through to a fresh mint.
		default:
			return 0, "", fmt.Errorf("githubd: durable rehydrate: %w", ierr)
		}
	}
	// Step 2: warm path — TokenCache.Token handles in-process
	// refresh via singleflight. Empty cache → fresh mint (this
	// is the case for accounts that completed pre-PR-C OAuth
	// and have no durable row yet).
	tok, err := s.Tokens.Token(ctx, installID)
	if err == nil {
		return installID, tok, nil
	}
	return 0, "", fmt.Errorf("githubd: install token unavailable: %w", err)
}

// rotateInstallToken mints a fresh install token via the GitHub
// App JWT, re-seals it under the host age key, persists it to
// StoreInstalls, seeds TokenCache, and emits the
// auth.install.token_sealed audit with rotated=true.
func (s *RealService) rotateInstallToken(ctx context.Context, accountID string, installID int64, prev state.GitHubInstall) (int64, string, error) {
	if s.Auth == nil {
		return 0, "", fmt.Errorf("githubd: cannot rotate install token (Auth not configured)")
	}
	if s.Recipient == nil {
		return 0, "", fmt.Errorf("githubd: cannot rotate install token (Recipient not configured)")
	}
	rawToken, expiresAt, err := s.Auth.ExchangeInstallationToken(ctx, installID)
	if err != nil {
		return 0, "", fmt.Errorf("githubd: rotate install token: %w", err)
	}
	sealed, err := secretbox.SealOne(s.Recipient, installTokenSealKey, rawToken, maxInstallTokenBytes)
	if err != nil {
		return 0, "", fmt.Errorf("githubd: seal rotated install token: %w", err)
	}
	if err := s.Installs.Upsert(ctx, state.GitHubInstall{
		AccountID:        accountID,
		InstallationID:   installID,
		DefaultBranch:    prev.DefaultBranch,
		SealedToken:      sealed,
		TokenExpiresAt:   expiresAt,
		AuditGithubLogin: prev.AuditGithubLogin,
	}); err != nil {
		return 0, "", fmt.Errorf("githubd: persist rotated install token: %w", err)
	}
	if s.Tokens != nil {
		_ = s.Tokens.Seed(ctx, installID, rawToken, expiresAt)
	}
	if s.Audit != nil {
		s.Audit("auth.install.token_sealed", accountID, map[string]any{
			"install_id":    installID,
			"github_login":  prev.AuditGithubLogin,
			"token_expires": expiresAt.Format(time.RFC3339),
			"rotated":       true,
		})
	}
	return installID, rawToken, nil
}

// UnbindAppRepo removes the binding for an app. Idempotent: nil
// even if no binding existed. PR-B: deletes the durable row first,
// then clears the cache.
func (s *RealService) UnbindAppRepo(appID, accountID string) error {
	if s.Store == nil {
		return nil // no store → no persistent binding to clear
	}
	if err := s.Store.Delete(context.Background(), appID); err != nil {
		// ErrAppNotFound is fine (idempotent); bubble other errors.
		if !errors.Is(err, ErrAppNotFound) {
			return fmt.Errorf("githubd: delete binding: %w", err)
		}
	}
	s.bindingsCacheMu.Lock()
	if byApp, ok := s.bindingsCache[accountID]; ok {
		delete(byApp, appID)
	}
	s.bindingsCacheMu.Unlock()
	return nil
}

// GetAppBinding looks up the binding for an app. Cache-first;
// falls back to the durable Store on miss and rebuilds the cache.
// Returns the gRPC-facing AppBinding shape (BindingID empty = no
// binding).
func (s *RealService) GetAppBinding(appID, accountID string) (githubdgrpc.AppBinding, error) {
	s.bindingsCacheMu.RLock()
	if byApp, ok := s.bindingsCache[accountID]; ok {
		if b, ok := byApp[appID]; ok {
			s.bindingsCacheMu.RUnlock()
			return bindingToGRPC(b), nil
		}
	}
	s.bindingsCacheMu.RUnlock()

	// Miss → Store.
	if s.Store == nil {
		return githubdgrpc.AppBinding{}, nil
	}
	b, err := s.Store.GetForApp(context.Background(), appID, accountID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return githubdgrpc.AppBinding{}, nil
		}
		return githubdgrpc.AppBinding{}, err
	}
	// Rebuild cache.
	s.bindingsCacheMu.Lock()
	if _, ok := s.bindingsCache[accountID]; !ok {
		s.bindingsCache[accountID] = map[string]state.GitHubBinding{}
	}
	s.bindingsCache[accountID][appID] = b
	s.bindingsCacheMu.Unlock()
	return bindingToGRPC(b), nil
}

// CreateDeploymentFromPush is the inbound gRPC entry from apid.
// Today it returns Unimplemented-equivalent errors — the inbound
// webhook path uses HTTP, not gRPC. Kept for the gRPC contract
// round-trip test (slice 7 bufconn_test).
func (s *RealService) CreateDeploymentFromPush(_, _, _, _ string) (string, string, error) {
	return "", "", fmt.Errorf("githubd: CreateDeploymentFromPush is HTTP-driven (slice 7 webhook path)")
}

// WriteCheck pushes a check-run for (repo, sha, phase). Requires
// non-nil Checks.
func (s *RealService) WriteCheck(repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, logsURL, summary string) error {
	if s.Checks == nil {
		return fmt.Errorf("githubd: Checks writer not configured")
	}
	return s.Checks.WriteCheck(context.Background(), repoFullName, commitSHA, phase, logsURL, summary)
}

// VerifyInstallation confirms the installation_id is real for the
// configured GitHub App AND, when expectedLogin is non-empty, that
// the install's account.login matches (PR-B §11 ownership proof).
// A 404 means the callback was forged or stale — verified=false,
// err=nil so the dashboard renders a "connection failed" banner
// rather than a 500. An account.login mismatch returns
// verified=false too (the install is real, just not owned by this
// user); the caller distinguishes by inspecting the AccountLogin
// field on the Installation payload (only the apid-side §11
// path consumes that field).
func (s *RealService) VerifyInstallation(installationID int64, expectedLogin string) (bool, string, string, error) {
	if s.Auth == nil {
		return false, "", "", fmt.Errorf("githubd: OAuth not configured")
	}
	inst, verified, err := s.Auth.VerifyInstallation(context.Background(), installationID, expectedLogin)
	if err != nil {
		return false, "", "", err
	}
	if !verified {
		// Don't surface AccountLogin on a non-verified install —
		// the §11 check is the whole point of the call, and a
		// mismatched login should look identical to a 404 to the
		// forged caller.
		return false, "", "", nil
	}
	return true, inst.AccountLogin, defaultProductionBranch, nil
}

// MintInstallationToken returns a fresh installation token for
// (accountID, installationID) (DEPLOY-PROV-4 / ADR-092, issue #739).
// It resolves the durable install row via s.Installs, mints a
// token via s.Tokens.Token (which handles the singleflight +
// 5-min proactive refresh), and surfaces the GitHub-reported
// expiry so the apid-side install-token cache can stamp it
// without an extra round-trip.
//
// Errors:
//   - ErrNoBinding on s.Installs.ErrNotFound (the apid handler
//     turns this into 404 + code=github_install_not_found).
//   - any other error wraps the upstream cause with operation
//     context for the §12 dashboard's source-fetch error slice.
//
// Unlike the production TokenCache callers (Checks writer,
// source fetcher), this method forces a fresh Token + read
// of the cache entry's expiry. A CI runner that just got a
// 401 from codeload can retry the RPC without waiting for
// the proactive refresh window.
func (s *RealService) MintInstallationToken(accountID string, installationID int64) (string, time.Time, error) {
	if s.Tokens == nil {
		return "", time.Time{}, fmt.Errorf("githubd: token cache not configured")
	}
	if s.Installs == nil {
		return "", time.Time{}, fmt.Errorf("githubd: install store not configured")
	}
	if installationID <= 0 {
		return "", time.Time{}, fmt.Errorf("githubd: invalid installation id %d", installationID)
	}
	// Resolve the durable install row. ErrNoBinding on
	// state.ErrNotFound so the gRPC handler maps to NotFound.
	inst, err := s.Installs.ForAccount(context.Background(), accountID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return "", time.Time{}, ErrNoBinding
		}
		return "", time.Time{}, fmt.Errorf("githubd: mint install token: resolve install: %w", err)
	}
	if inst.InstallationID != installationID {
		// Same defensive guard as installationSourceFetcher
		// (cmd/githubd/source_fetcher.go:128): mismatch is a
		// config bug, not a security one (no cross-account
		// take-over is possible because accountID gates the
		// row lookup).
		return "", time.Time{}, ErrNoBinding
	}
	// Mint. TokenCache.Token handles singleflight + 5-min
	// proactive refresh — the first concurrent caller blocks
	// on api.github.com; the rest piggy-back.
	token, err := s.Tokens.Token(context.Background(), installationID)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("githubd: mint install token: cache: %w", err)
	}
	// Expiry: prefer the cached entry's expiresAt (zero +
	// ErrNotFound-equivalent false on a cache miss, which
	// shouldn't happen because Token just wrote one). The
	// apid-side cache stamps expiryAt even if it's zero —
	// the next apid-side cache miss will re-mint.
	expiresAt, _ := s.Tokens.ExpiresAt(installationID)
	return token, expiresAt, nil
}

// StreamSourceRef streams the raw tar.gz archive for a
// (repo, ref) pair over the durable install's installation
// token (DEPLOY-PROV-4 / ADR-092, issue #739). It is the
// gRPC bridge behind POST /v1/apps/{slug}/deployments/source-ref
// — apid's handleSourceRefDeploy dials it via StreamSourceRef,
// pipes the returned io.ReadCloser into validateAndSpool,
// then os.Renames the staged file to FAAS_SPOOL_ROOT/<id>.tar.gz.
//
// The streaming wire shape lets a Free-plan tarball that
// exceeds SourceTarballMaxMB be rejected mid-flight (the
// truncated=true chunk) rather than buffered entirely in
// memory. bytesStreamed is the post-cap cumulative count
// for the deployment row's source_bytes column.
//
// Errors:
//   - api.CodeGitHubInstallNotFound when the streamer cannot prove
//     the requested account/install binding.
//   - the remaining SourceRefStreamer errors are passed through
//     so the gRPC handler can map them via toStatusErr.
func (s *RealService) StreamSourceRef(ctx context.Context, accountID string, installationID int64, repoFullName, ref string, maxArchiveBytes int64) (io.ReadCloser, string, bool, int64, error) {
	if s.Streamer == nil {
		return nil, "", false, 0, fmt.Errorf("githubd: source-ref streamer not configured")
	}
	res, err := s.Streamer.Stream(ctx, accountID, installationID, repoFullName, ref, maxArchiveBytes)
	if err != nil {
		if errors.Is(err, ErrNoBinding) {
			return nil, "", false, 0, api.ErrGitHubInstallNotFound()
		}
		if errors.Is(err, gitfetch.ErrNotFound) {
			if isCanonicalCommitSHA(ref) {
				return nil, "", false, 0, api.ErrSourceRefUnavailable("GitHub could not fetch the requested commit")
			}
			return nil, "", false, 0, api.ErrInvalidRef(ref)
		}
		if errors.Is(err, gitfetch.ErrUnauthorized) {
			return nil, "", false, 0, api.ErrSourceRefUnavailable("GitHub rejected the installation token")
		}
		if errors.Is(err, gitfetch.ErrBadArchive) {
			return nil, "", false, 0, api.ErrSourceRefUnavailable("GitHub returned an invalid source archive")
		}
		return nil, "", false, 0, err
	}
	if res.Body == nil {
		return nil, "", false, 0, fmt.Errorf("githubd: source-ref streamer returned nil body")
	}
	// The streamer already wraps the body in
	// io.LimitReader(maxArchiveBytes + 1, …); the apid-side
	// handler reads past EOF to detect the cap. We don't
	// pre-compute bytesStreamed here because the limit
	// enforces a stream bound the gRPC chunk loop can't
	// peek at without consuming the body. The handler
	// records source_bytes via the final chunk's
	// bytes_streamed field, which is the cumulative count
	// at EOF — same posture as the tarball SHA on the
	// multipart path.
	return res.Body, res.ResolvedCommitSHA, false, 0, nil
}

func isCanonicalCommitSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// bindingToGRPC translates the durable state row into the gRPC
// AppBinding shape (which deliberately omits install_id and
// linked_at — those are githubd-internal).
func bindingToGRPC(b state.GitHubBinding) githubdgrpc.AppBinding {
	return githubdgrpc.AppBinding{
		RepoFullName:     b.RepoFullName,
		ProductionBranch: b.ProductionBranch,
		BindingID:        b.BindingID,
	}
}
