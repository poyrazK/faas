// BindingsStore is the per-app, per-account GitHub install binding
// surface RealService reads and writes. Pre-PR-B the in-memory map
// was the source of truth; PR-B demotes that map to a read-through
// cache and pushes the durable state into Postgres (apps table,
// columns added in migration 00050).
//
// The interface lives in pkg/githubd so the daemon stays
// persistence-agnostic; the concrete adapter that bridges
// pkg/state.PgStore lives in cmd/githubd/main.go (where pgxpool.Pool
// is already in scope per the slice-8 design notes).
//
// The interface uses pkg/state types directly: pkg/state doesn't
// import pkg/githubd (no cycle), and re-defining parallel shapes
// here would be a permanent translation liability.
package githubd

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// ErrAppNotFound is returned by BindingsStore.Delete when the app
// row itself is missing (so callers can distinguish "not bound" from
// "app doesn't exist"). The cmd-side adapter maps state.ErrNotFound
// to this on the delete path.
var ErrAppNotFound = errors.New("githubd: app not found")

// BindingsStore is the persistence seam RealService binds to.
// Implementations MUST be safe for concurrent use.
type BindingsStore interface {
	// Upsert persists the bind edge and returns the binding_id
	// (the store mints the deterministic "bind-<appID>-<repo>" form
	// RealService has always emitted). The (account_id, binding_id)
	// unique partial index added in migration 00050 makes the
	// upsert idempotent on retry.
	Upsert(ctx context.Context, b state.GitHubBinding) (bindingID string, err error)
	// Delete clears the bind columns for an app. Idempotent: returns
	// nil for an app with no current binding. Returns ErrAppNotFound
	// when the app row is missing.
	Delete(ctx context.Context, appID string) error
	// GetForApp returns the (appID, accountID) → GitHubBinding
	// mapping or ErrNotFound when none. accountID scopes the
	// lookup so an attacker holding a forged cookie cannot read
	// another tenant's binding.
	GetForApp(ctx context.Context, appID, accountID string) (state.GitHubBinding, error)
	// ListForAccount returns the per-account binding map keyed by
	// appID. Empty (non-nil) map when the account has no bindings.
	// Used by the dashboard's hydrate path.
	ListForAccount(ctx context.Context, accountID string) (map[string]state.GitHubBinding, error)
	// FindForRepoBranch is the inbound-webhook dispatch lookup.
	// Returns the (repo, branch) → GitHubBinding mapping; ErrNotFound
	// when no app is bound.
	FindForRepoBranch(ctx context.Context, repoFullName, branch string) (state.GitHubBinding, error)
	// InstallationIDForRepo keeps the existing BindingsLookup
	// surface (used by ChecksAPI to mint per-repo access tokens).
	// Returns ErrNotFound when no app is bound to the repo.
	InstallationIDForRepo(ctx context.Context, repoFullName string) (int64, error)
}

// StoreInstalls is the durable GitHub App install-state surface
// (PR-C, audit-gap closure). Pre-PR-C the install state lived in
// RealService's in-memory `s.installs` map; PR-C demotes that map
// to a read-through cache and pushes the durable state into the
// github_installations Postgres table (migration 00059). The
// githubd daemon stays persistence-agnostic — the concrete adapter
// that bridges pkg/state.PgStore lives in cmd/githubd/main.go.
//
// Implementations MUST be safe for concurrent use.
type StoreInstalls interface {
	// Upsert persists the OAuth handshake state for one account's
	// install. Idempotent on (AccountID, InstallationID) via the table's PK +
	// ON CONFLICT DO UPDATE; the OAuth flow can retry without
	// crashing on the unique constraint. SealedToken is the
	// age-encrypted install token (the "ghs_…" form, sealed by
	// githubd via pkg/secretbox.SealOne before persisting).
	Upsert(ctx context.Context, inst state.GitHubInstall) error
	// ForAccount returns the durable install row, or ErrNotFound
	// when the account hasn't completed the OAuth handshake yet.
	// Used by RealService.ensureInstallToken's cold-start rehydrate
	// path: when TokenCache is empty (process restart), unseal the
	// SealedToken only if TokenExpiresAt > now()+30s; otherwise the
	// cold path mints a fresh install token and re-seals.
	ForAccount(ctx context.Context, accountID string) (state.GitHubInstall, error)
}

// ScopedStoreInstalls is implemented by multi-install stores. Paths that
// already know an installation ID use this contract to avoid substituting a
// different personal or organization installation owned by the same account.
type ScopedStoreInstalls interface {
	ForAccountInstallation(ctx context.Context, accountID string, installationID int64) (state.GitHubInstall, error)
}
