package state

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PRPreviewSetPeerReader lists the apps that share a PR preview set with an
// app. A PR preview of an app bound without a project calls only these
// (ADR-20261105090226): it has no project to scope its service names.
type PRPreviewSetPeerReader interface {
	PRPreviewSetPeerIDs(ctx context.Context, appID string) ([]string, error)
}

var (
	_ PRPreviewSetPeerReader = (*PgStore)(nil)
	_ PRPreviewSetPeerReader = (*MemStore)(nil)
	_ PRPreviewSetPeerReader = servicePolicyReader{}
)

// PRPreviewSetPeerIDs returns the members of every set that holds appID,
// appID included, in ID order; none when no set holds it.
func (s *PgStore) PRPreviewSetPeerIDs(ctx context.Context, appID string) ([]string, error) {
	if _, err := uuid.Parse(appID); err != nil {
		return nil, nil
	}
	ids, err := sqlc.New().ServicePolicyPRPreviewSetMemberIDs(ctx, s.pool, appID)
	if err != nil {
		return nil, fmt.Errorf("state: PR preview set peers: %w", mapErr(err))
	}
	return ids, nil
}

// PRPreviewSetPeerIDs reads the peers in the service policy snapshot.
func (s servicePolicyReader) PRPreviewSetPeerIDs(ctx context.Context, appID string) ([]string, error) {
	if _, err := uuid.Parse(appID); err != nil {
		return nil, nil
	}
	ids, err := sqlc.New().ServicePolicyPRPreviewSetMemberIDs(ctx, s.tx, appID)
	if err != nil {
		return nil, fmt.Errorf("state: PR preview set peers: %w", mapErr(err))
	}
	return ids, nil
}

// PRPreviewSetPeerIDs mirrors PgStore.
func (m *MemStore) PRPreviewSetPeerIDs(_ context.Context, appID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for _, set := range m.previewSets {
		if !slices.Contains(set.MemberAppIDs, appID) {
			continue
		}
		for _, id := range set.MemberAppIDs {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	slices.Sort(ids)
	return ids, nil
}
