// Source-tree fetcher adapter for githubd's push-dispatch path
// (PR-H, repo decomposition Phase 5).
//
// This file is the bridge between pkg/githubd.Service (which
// invokes the typeless SourceFetcher interface) and the two
// production dependencies:
//
//   - stateInstallsAdapter — read the durable install row for the
//     account (returns the sealed install token).
//   - pkg/gitfetch.Fetcher — transport-only archive download
//     (HTTP codeload, token-agnostic, per PR-GH.3).
//
// The fetcher must NEVER fetch against an install whose token
// it cannot unseal. The defensive guard
// `inst.InstallationID == installID` covers the case where the
// binding lookup and the install lookup disagree on the install
// row — PR-A's audit pipeline rejects takeover attempts, but
// the daemon-side guard is load-bearing (the audit row is
// post-hoc; the upstream HTTP call has already happened).
//
// Token lifetime: the unsealed plaintext comes back from
// secretbox.Open as a string value inside an Envelope map. Go
// strings are immutable, so we can't overwrite the byte slice
// in place. The transport uses the token once for the
// Authorization header and never logs it. The fetcher never
// logs URLs (which would embed the token in codeload's query
// string), the response body, or any header.

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/gitfetch"
	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// installTokenSealKey is the Envelope key minted by
// pkg/secretbox.SealOne when EnsureInstallToken persists the
// token. PR-H reuses the same envelope shape so a pre-PR-H
// install row is readable by the push-dispatch path without
// migration.
const installTokenSealKey = "GITHUB_INSTALL_TOKEN"

// installsLookup is the slice of stateInstallsAdapter the
// fetcher needs. Exposed as an interface so the test rig can
// stub without a real pgxpool.
type installsLookup interface {
	ForAccount(ctx context.Context, accountID string) (state.GitHubInstall, error)
}

type scopedInstallsLookup interface {
	ForAccountInstallation(ctx context.Context, accountID string, installationID int64) (state.GitHubInstall, error)
}

// installTokenProvider is implemented by githubd.TokenCache. Production source
// fetches must obtain a fresh, installation-scoped token at request time; the
// durable sealed token is only a restart seed and expires after about an hour.
type installTokenProvider interface {
	Token(ctx context.Context, installationID int64) (string, error)
	Invalidate(installationID int64)
}

// installationSourceFetcher implements githubd.SourceFetcher
// against the durable install row + the package-level fetcher.
// One instance per daemon; it is safe for concurrent use
// (the underlying pkg/gitfetch.fetcher is stateless and the
// stateInstallsAdapter delegates to a pgxpool.Pool).
type installationSourceFetcher struct {
	installs installsLookup
	fetcher  gitfetch.Fetcher
	identity *age.X25519Identity
	tokens   installTokenProvider
	log      *slog.Logger
}

func (s *installationSourceFetcher) WithTokenProvider(tokens installTokenProvider) *installationSourceFetcher {
	s.tokens = tokens
	return s
}

// newInstallationSourceFetcher wires the four dependencies. The
// identity is the host age keypair loaded at boot via
// secretbox.LoadHostKey (cmd/githubd/main.go:120); failure to
// load it is fatal upstream, so the constructor takes a non-nil
// pointer without re-validating.
func newInstallationSourceFetcher(installs installsLookup, fetcher gitfetch.Fetcher, identity *age.X25519Identity, log *slog.Logger) *installationSourceFetcher {
	if log == nil {
		log = slog.Default()
	}
	return &installationSourceFetcher{
		installs: installs,
		fetcher:  fetcher,
		identity: identity,
		log:      log,
	}
}

// Fetch resolves the install row for the given account, unseals
// the token, calls the underlying Fetcher, and returns the
// Tree. The token is scoped to the single Fetcher call and
// never appears in log lines or URLs. The Tree's Close() is
// the caller's responsibility (githubd.Service.HandlePushRequest
// defers it).
//
// Returns:
//
//   - githubd.SourceTree on success. The caller MUST Close it.
//   - githubd.ErrNoBinding when the install row is missing
//     (state.ErrNotFound) or has a mismatched InstallationID.
//     The webhook handler turns this into 200 + {ignored: true,
//     reason: no_binding}.
//   - any other error is wrapped with operation context for
//     the §12 dashboard's source-fetch error slice.
func (s *installationSourceFetcher) Fetch(ctx context.Context, accountID string, installID int64, repoFullName, commitSHA string) (githubd.SourceTree, error) {
	// 1. Resolve the install row. state.ErrNotFound surfaces as
	// ErrNoBinding so the webhook handler renders the ignored
	// payload instead of a 500.
	var inst state.GitHubInstall
	var err error
	if scoped, ok := s.installs.(scopedInstallsLookup); ok {
		inst, err = scoped.ForAccountInstallation(ctx, accountID, installID)
	} else {
		inst, err = s.installs.ForAccount(ctx, accountID)
	}
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, githubd.ErrNoBinding
		}
		return nil, fmt.Errorf("githubd: source fetcher: resolve install: %w", err)
	}
	if inst.AccountID == "" || inst.AccountID != accountID || inst.InstallationID == 0 {
		// Defensive: the install row exists but is incomplete.
		// The OAuth handshake should never write a partial row,
		// but a manual SQL edit could.
		return nil, githubd.ErrNoBinding
	}

	// 2. Defensive guard: the install row's InstallationID must
	// match the installID the dispatcher passed in. Mismatch is
	// a half-open handover (binding was created under one
	// install, the push claims to be from another) — refuse
	// rather than silently re-using the token.
	if inst.InstallationID != installID {
		s.log.Warn("githubd: source fetcher install id mismatch",
			"account_id", accountID,
			"expected_install_id", installID,
			"stored_install_id", inst.InstallationID,
			"repo", repoFullName)
		return nil, githubd.ErrNoBinding
	}

	// 3. Resolve a current install token. Production wires TokenCache, which
	// refreshes expiring tokens and coalesces concurrent refreshes. The sealed
	// row fallback keeps store-only test rigs and recovery tooling compatible.
	var raw string
	if s.tokens != nil {
		raw, err = s.tokens.Token(ctx, installID)
		if err != nil {
			return nil, fmt.Errorf("githubd: source fetcher: refresh install token: %w", err)
		}
	} else {
		env, openErr := secretbox.Open(s.identity, inst.SealedToken)
		if openErr != nil {
			return nil, fmt.Errorf("githubd: source fetcher: unseal install token: %w", openErr)
		}
		var ok bool
		raw, ok = env[installTokenSealKey]
		if !ok || raw == "" {
			return nil, fmt.Errorf("githubd: source fetcher: sealed install token missing key %q", installTokenSealKey)
		}
	}

	// 4. Fetch. The token is scoped to this single call; the
	// underlying httpFetcher uses it for exactly one
	// Authorization header and never persists it.
	gitTree, err := s.fetcher.Fetch(ctx, repoFullName, commitSHA, raw)
	if err != nil && s.tokens != nil && errors.Is(err, gitfetch.ErrUnauthorized) {
		// A token may be revoked before its advertised expiry. Drop it and retry
		// exactly once with a newly exchanged installation token.
		s.tokens.Invalidate(installID)
		raw, err = s.tokens.Token(ctx, installID)
		if err == nil {
			gitTree, err = s.fetcher.Fetch(ctx, repoFullName, commitSHA, raw)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("githubd: source fetcher: archive fetch: %w", err)
	}
	// Wrap the gitfetch.Tree into a githubd.SourceTree so the
	// two interfaces stay de-coupled at the type-system level
	// (pkg/githubd does not import pkg/gitfetch).
	return &sourceTreeAdapter{inner: gitTree}, nil
}

// sourceTreeAdapter bridges gitfetch.Tree to githubd.SourceTree.
// The two interfaces have identical method shapes; the wrapper
// keeps pkg/githubd from importing pkg/gitfetch (a future
// migration off codeload — PR-H.2's self-hosted mirror — would
// just swap the inner type).
type sourceTreeAdapter struct {
	inner gitfetch.Tree
}

func (a *sourceTreeAdapter) FS() fs.FS    { return a.inner.FS() }
func (a *sourceTreeAdapter) Close() error { return a.inner.Close() }

// Compile-time guards: confirm the production adapter satisfies
// the install lookup seam and the package-level fetcher is on
// the import path. The gitfetch compile-time guard uses the
// interface type as a place-holder; the production adapter
// (*httpFetcher) is in the same package.
var (
	_ installsLookup        = (*stateInstallsAdapter)(nil)
	_ gitfetch.Fetcher      // satisfied by *httpFetcher at runtime
	_ githubd.SourceFetcher = (*installationSourceFetcher)(nil)
)
