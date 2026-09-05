// Package main (cmd/githubd) holds the state-BindingsStore adapter.
// Lives in cmd/githubd (not pkg/githubd) because pgxpool.Pool is the
// one piece already in scope here per the slice-8 design — keeping
// the adapter next to the wiring makes the seam obvious.
//
// PR-B replaces the pre-PR-B pgBindingsLookup (which only
// implemented InstallationIDForRepo) with a full BindingsStore
// adapter. The previous direct-SQL surface is closed per ADR-017:
// all DB access now goes through pkg/state.
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/state"
)

// stateBindingsAdapter bridges pkg/state.PgStore to
// pkg/githubd.BindingsStore. Pre-PR-B the only requirement was the
// ChecksAPI's InstallationIDForRepo (so ChecksAPI could mint
// per-install tokens); PR-B widens the surface to the full binding
// CRUD the dashboard's bind flow needs.
//
// The adapter translates between pkg/state's
// state.GitHubBinding and pkg/githubd's view (which is just
// state.GitHubBinding today — the interface was deliberately
// written in state types so this adapter is a near no-op).
type stateBindingsAdapter struct {
	store *state.PgStore
}

func newStateBindingsAdapter(pool *pgxpool.Pool) *stateBindingsAdapter {
	return &stateBindingsAdapter{store: state.NewPgStore(pool)}
}

// Upsert writes the bind row. The deterministic bindingID is
// "bind-<appID>-<repo>" — the same form RealService.BindAppRepo
// has always emitted, so on-disk rows are forward-compatible with
// pre-PR-B log replays (issue #319's audit-log migration can refer
// to it without translation).
func (a *stateBindingsAdapter) Upsert(ctx context.Context, b state.GitHubBinding) (string, error) {
	if b.BindingID == "" {
		// Defensive: callers (RealService.BindAppRepo) mint this
		// upstream. Fail loudly so a refactor that drops the
		// bindingID mint surfaces in tests.
		return "", fmt.Errorf("state adapter: BindingID required")
	}
	// LinkedAt is left at zero value when the caller omits it;
	// PgStore fills it with time.Now via its own clock (clock skew
	// between cmd/githubd and the DB host is absorbed by the store
	// layer, not by this adapter).
	if err := a.store.UpsertGithubInstallBinding(ctx, b); err != nil {
		return "", err
	}
	return b.BindingID, nil
}

// Delete clears the bind columns. Maps state.ErrNotFound to
// githubd.ErrAppNotFound so the RealService can distinguish "app
// not bound" from "app doesn't exist".
func (a *stateBindingsAdapter) Delete(ctx context.Context, appID string) error {
	err := a.store.DeleteGithubInstallBinding(ctx, appID)
	if err == nil {
		return nil
	}
	if errors.Is(err, state.ErrNotFound) {
		return githubd.ErrAppNotFound
	}
	return err
}

// GetForApp returns the (appID, accountID) → GitHubBinding row.
// The store already enforces account scoping (it requires the
// caller to pass accountID and returns ErrNotFound for mismatched
// accounts).
func (a *stateBindingsAdapter) GetForApp(ctx context.Context, appID, accountID string) (state.GitHubBinding, error) {
	return a.store.GetGithubInstallBindingForApp(ctx, appID, accountID)
}

// ListForAccount returns the per-account bind map.
func (a *stateBindingsAdapter) ListForAccount(ctx context.Context, accountID string) (map[string]state.GitHubBinding, error) {
	return a.store.ListGithubInstallBindingsForAccount(ctx, accountID)
}

// FindForRepoBranch is the inbound-webhook dispatch lookup.
func (a *stateBindingsAdapter) FindForRepoBranch(ctx context.Context, repoFullName, branch string) (state.GitHubBinding, error) {
	return a.store.GithubInstallBindingForRepoBranch(ctx, repoFullName, branch)
}

// GetAppBinding is the pkg/githubd.AppBindingStore seam. The
// method name + signature match the interface so the
// production wiring can hand the adapter directly to
// webhookSvc.Bindings (= AppBindingStore). The implementation
// delegates to FindForRepoBranch; they're the same lookup
// today, but separating the names lets future slices evolve
// the BindingsStore surface independently of the OAuth-flow
// adapters (which use FindForRepoBranch).
func (a *stateBindingsAdapter) GetAppBinding(ctx context.Context, repoFullName, branch string) (state.GitHubBinding, error) {
	return a.FindForRepoBranch(ctx, repoFullName, branch)
}

func (a *stateBindingsAdapter) GetAppBindingForInstallation(ctx context.Context, repoFullName, branch string, installationID int64) (state.GitHubBinding, error) {
	return a.store.GithubInstallBindingForRepoBranchInstallation(ctx, repoFullName, branch, installationID)
}

// InstallationIDForRepo is the legacy ChecksAPI seam. Maps
// state.ErrNotFound to githubd.ErrNoBinding (the pre-PR-B
// sentinel) so the cmd-side swap is invisible to ChecksAPI.
func (a *stateBindingsAdapter) InstallationIDForRepo(ctx context.Context, repoFullName string) (int64, error) {
	id, err := a.store.InstallationIDForRepo(ctx, repoFullName)
	if err == nil {
		return id, nil
	}
	if errors.Is(err, state.ErrNotFound) {
		return 0, githubd.ErrNoBinding
	}
	return 0, err
}

// stateInstallsAdapter bridges pkg/state.PgStore to
// pkg/githubd.StoreInstalls (PR-C). The interface is deliberately
// written in state types (per the binding adapter's pattern), so
// this is a near no-op — the only translation is the ErrNotFound
// pass-through that RealService.lookupInstall reads as "no
// installation for account".
type stateInstallsAdapter struct {
	store *state.PgStore
}

func newStateInstallsAdapter(pool *pgxpool.Pool) *stateInstallsAdapter {
	return &stateInstallsAdapter{store: state.NewPgStore(pool)}
}

// Upsert persists the OAuth handshake state. SealedToken is
// passed through verbatim — pkg/secretbox.SealOne already armoured
// it before RealService handed it here, so the database never sees
// a plaintext "ghs_…" token.
func (a *stateInstallsAdapter) Upsert(ctx context.Context, inst state.GitHubInstall) error {
	return a.store.UpsertGitHubInstall(ctx, inst)
}

// ForAccount reads the durable install row. state.ErrNotFound
// surfaces as-is so RealService.lookupInstall can render the
// pre-PR-B "no installation for account" error.
func (a *stateInstallsAdapter) ForAccount(ctx context.Context, accountID string) (state.GitHubInstall, error) {
	return a.store.GitHubInstallForAccount(ctx, accountID)
}

func (a *stateInstallsAdapter) ForAccountInstallation(ctx context.Context, accountID string, installationID int64) (state.GitHubInstall, error) {
	return a.store.GitHubInstallForAccountInstallation(ctx, accountID, installationID)
}
