// adr: 531
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Only schedd writes desired circuits; vmmd reads this policy before boot.
func (s *PgStore) PutAppEgressCircuits(ctx context.Context, appID string, targets []netns.EgressCircuitTarget) (netns.EgressCircuitSnapshot, error) {
	id, err := uuid.Parse(appID)
	if err != nil {
		return netns.EgressCircuitSnapshot{}, fmt.Errorf("state: egress circuit app_id: %w", err)
	}
	canonical, err := netns.CanonicalEgressCircuitTargets(targets)
	if err != nil {
		return netns.EgressCircuitSnapshot{}, err
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return netns.EgressCircuitSnapshot{}, err
	}
	row, err := s.dataUpstreamsQueries().PutAppEgressCircuits(ctx, s.pool, sqlc.PutAppEgressCircuitsParams{
		AppID: pgtype.UUID{Bytes: id, Valid: true}, Targets: payload,
	})
	if err != nil {
		return netns.EgressCircuitSnapshot{}, fmt.Errorf("state: persist egress circuits: %w", err)
	}
	return decodeEgressCircuits(row.Revision, row.Targets)
}

func (s *PgStore) GetAppEgressCircuits(ctx context.Context, appID string) (netns.EgressCircuitSnapshot, error) {
	id, err := uuid.Parse(appID)
	if err != nil {
		return netns.EgressCircuitSnapshot{}, fmt.Errorf("state: egress circuit app_id: %w", err)
	}
	row, err := s.dataUpstreamsQueries().GetAppEgressCircuits(ctx, s.pool, pgtype.UUID{Bytes: id, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return netns.EgressCircuitSnapshot{}, nil // no declared circuit policy yet
	}
	if err != nil {
		return netns.EgressCircuitSnapshot{}, fmt.Errorf("state: read egress circuits: %w", err)
	}
	return decodeEgressCircuits(row.Revision, row.Targets)
}

// ListAppEgressCircuitAppIDs retains empty desired sets too: they must be
// re-pushed to repair a missed close after opt-out or daemon restart.
func (s *PgStore) ListAppEgressCircuitAppIDs(ctx context.Context) ([]string, error) {
	rows, err := s.dataUpstreamsQueries().ListAppEgressCircuitAppIDs(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("state: list egress circuit apps: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, id := range rows {
		out = append(out, uuidString(id))
	}
	return out, nil
}

func decodeEgressCircuits(revision int64, payload []byte) (netns.EgressCircuitSnapshot, error) {
	var targets []netns.EgressCircuitTarget
	if err := json.Unmarshal(payload, &targets); err != nil {
		return netns.EgressCircuitSnapshot{}, fmt.Errorf("state: decode egress circuits: %w", err)
	}
	targets, err := netns.CanonicalEgressCircuitTargets(targets)
	return netns.EgressCircuitSnapshot{Revision: revision, Targets: targets}, err
}

func (m *MemStore) PutAppEgressCircuits(context.Context, string, []netns.EgressCircuitTarget) (netns.EgressCircuitSnapshot, error) {
	return netns.EgressCircuitSnapshot{}, errMemStoreDataUpstreams
}
func (m *MemStore) GetAppEgressCircuits(context.Context, string) (netns.EgressCircuitSnapshot, error) {
	return netns.EgressCircuitSnapshot{}, errMemStoreDataUpstreams
}
func (m *MemStore) ListAppEgressCircuitAppIDs(context.Context) ([]string, error) {
	return nil, errMemStoreDataUpstreams
}
