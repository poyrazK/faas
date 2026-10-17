// adr: 20261017201005
package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const ScenarioNetworkContained = "contained"

// ScenarioNetworkPolicyStore reads retained policy, including after namespace
// pruning and soft deletion. An empty run means no containment enrollment.
// Readers must propagate errors rather than treating them as an empty policy.
type ScenarioNetworkPolicyStore interface {
	ScenarioRehearsalNetworkRun(context.Context, string) (string, error)
}

func (s *PgStore) ScenarioRehearsalNetworkRun(ctx context.Context, appID string) (string, error) {
	run, err := sqlc.New().ScenarioRehearsalNetworkRun(ctx, s.pool, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read rehearsal network policy: %w", err)
	}
	return run, nil
}

func (m *MemStore) ScenarioRehearsalNetworkRun(ctx context.Context, appID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	policy, ok := m.scenarioActionPolicyLocked(appID)
	if !ok || policy.Member.NetworkMode != ScenarioNetworkContained {
		return "", nil
	}
	return policy.Member.RunID, nil
}
