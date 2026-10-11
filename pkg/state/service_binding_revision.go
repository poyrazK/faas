// adr: 596 — service evidence includes target resolution and authorization facts.
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ServiceBindingRevisionStore returns a private token independent of probe results.
// It conservatively covers account-wide service namespaces and access policies.
// The same dependencies also invalidate BindingPromotionStore's traffic fence.
type ServiceBindingRevisionStore interface {
	ReadServiceBindingRevision(context.Context, string, string) (string, error)
}

func (m *MemStore) ReadServiceBindingRevision(_ context.Context, accountID, appID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return "", ErrNotFound
	}
	return m.serviceBindingRevisionLocked(accountID)
}

func (m *MemStore) serviceBindingRevisionLocked(accountID string) (string, error) {
	facts := map[string]any{}
	for id, app := range m.apps {
		if app.AccountID == accountID {
			facts["app/"+id] = []any{app.ID, app.AccountID, app.Slug, app.Status, app.Manifest, app.ProjectID, app.PreviewOfSlug, app.PreviewPrNumber, app.PreviewPrState, app.PreviewExpiresAt, app.AppProtocol, app.WebSocketEnabled}
		}
	}
	for id, policy := range m.githubDeployPolicies {
		if policy.AccountID == accountID {
			facts["policy/"+id] = []any{policy.ProjectID, policy.AccountID, policy.PreviewServicePolicy}
		}
	}
	for id, member := range m.scenarioTestMembers {
		if member.AccountID == accountID {
			facts["test/"+id] = member
		}
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		return "", fmt.Errorf("state: snapshot service binding revision: %w", err)
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func (s *PgStore) ReadServiceBindingRevision(ctx context.Context, accountID, appID string) (string, error) {
	revision, err := sqlc.New().ReadServiceBindingRevision(ctx, s.pool, sqlc.ReadServiceBindingRevisionParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("state: read service binding revision: %w", err)
	}
	return revision, nil
}
