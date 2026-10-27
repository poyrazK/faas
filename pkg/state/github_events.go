package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// GitHubEventStore publishes gregale.github events (ADR-20261027090010 §9). apid finds
// the installation's account, then accepts the event like any published
// event: the events trigger captures the matching subscriptions and workflow
// event triggers. Platform sources are never charged to the account's event
// storage.
type GitHubEventStore interface {
	PublishedEventAcceptanceStore
	AccountIDByGitHubInstallation(ctx context.Context, installationID int64) (string, error)
}

// AccountIDByGitHubInstallation returns the account that installed the
// GitHub App installation, or ErrNotFound.
func (s *PgStore) AccountIDByGitHubInstallation(ctx context.Context, installationID int64) (string, error) {
	if installationID <= 0 {
		return "", ErrNotFound
	}
	id, err := sqlc.New().AccountIDByGitHubInstallation(ctx, s.pool, installationID)
	if err != nil {
		return "", mapErr(err)
	}
	return uuidFromPgtype(id).String(), nil
}

func (m *MemStore) AccountIDByGitHubInstallation(_ context.Context, installationID int64) (string, error) {
	if installationID <= 0 {
		return "", ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range m.githubInstalls {
		if inst.InstallationID == installationID {
			return inst.AccountID, nil
		}
	}
	return "", ErrNotFound
}
