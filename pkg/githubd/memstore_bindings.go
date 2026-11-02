// memBindingsStore is an in-memory BindingsStore used by tests
// and as the dev-mode fallback when pkg/state.PgStore is not wired.
// Production binaries use the cmd/githubd adapter that bridges to
// pkg/state.PgStore.
package githubd

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/state"
)

// memBindingsStore satisfies BindingsStore. Safe for concurrent use.
// Kept in pkg/githubd (not pkg/state) because pkg/githubd is the
// consumer and the test seam — pkg/state's role is the durable
// adapter, not the in-memory one.
type memBindingsStore struct {
	mu    sync.Mutex
	byApp map[string]state.GitHubBinding
}

func newMemBindingsStore() *memBindingsStore {
	return &memBindingsStore{byApp: map[string]state.GitHubBinding{}}
}

func (m *memBindingsStore) Upsert(_ context.Context, b state.GitHubBinding) (string, error) {
	if b.AppID == "" || b.AccountID == "" || b.RepoFullName == "" {
		return "", fmt.Errorf("memstore: appID, accountID, repoFullName required")
	}
	if b.BindingID == "" {
		return "", fmt.Errorf("memstore: BindingID required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.byApp[b.AppID]; ok {
		// Existing bind must match (account, binding). A different
		// account rebinding the same app is an error (mirrors the
		// §11 cross-tenant guard).
		if existing.AccountID != b.AccountID {
			return "", fmt.Errorf("memstore: app %s already bound to account %s", b.AppID, existing.AccountID)
		}
	}
	m.byApp[b.AppID] = b
	return b.BindingID, nil
}

func (m *memBindingsStore) Delete(_ context.Context, appID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byApp[appID]; !ok {
		return ErrAppNotFound
	}
	delete(m.byApp, appID)
	return nil
}

func (m *memBindingsStore) GetForApp(_ context.Context, appID, accountID string) (state.GitHubBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.byApp[appID]
	if !ok {
		return state.GitHubBinding{}, state.ErrNotFound
	}
	if b.AccountID != accountID {
		// Treat as "not found" so an attacker can't probe for
		// bindings that exist but belong to another tenant.
		return state.GitHubBinding{}, state.ErrNotFound
	}
	return b, nil
}

func (m *memBindingsStore) ListForAccount(_ context.Context, accountID string) (map[string]state.GitHubBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]state.GitHubBinding{}
	for appID, b := range m.byApp {
		if b.AccountID == accountID {
			out[appID] = b
		}
	}
	return out, nil
}

func (m *memBindingsStore) ListForInstallationRepo(_ context.Context, installationID int64, repoFullName string) ([]state.GitHubBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []state.GitHubBinding
	for appID, b := range m.byApp {
		if b.InstallID == installationID && strings.EqualFold(b.RepoFullName, repoFullName) {
			b.AppID = appID
			out = append(out, b)
		}
	}
	return out, nil
}

func (m *memBindingsStore) FindForRepoBranch(_ context.Context, repoFullName, branch string) (state.GitHubBinding, error) {
	if repoFullName == "" {
		return state.GitHubBinding{}, state.ErrNotFound
	}
	if branch == "" {
		branch = defaultProductionBranch
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.byApp {
		if b.RepoFullName == repoFullName && b.ProductionBranch == branch {
			return b, nil
		}
	}
	return state.GitHubBinding{}, state.ErrNotFound
}

func (m *memBindingsStore) InstallationIDForRepo(_ context.Context, repoFullName string) (int64, error) {
	if repoFullName == "" {
		return 0, state.ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.byApp {
		if b.RepoFullName == repoFullName && b.InstallID != 0 {
			return b.InstallID, nil
		}
	}
	return 0, state.ErrNotFound
}
