package state

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectSubmissionRetentionStore prunes project apply receipts that no
// release state needs any more (ADR-20261021094500).
type ProjectSubmissionRetentionStore interface {
	PruneProjectSubmissions(ctx context.Context, before time.Time, limit int) (int64, error)
}

// projectSubmissionAdvancing reports a receipt whose safe release may still
// change: its reconciler reads the receipt until the release settles.
func projectSubmissionAdvancing(payload []byte) bool {
	var r api.ProjectSubmissionReceipt
	if json.Unmarshal(payload, &r) != nil {
		return true
	}
	safe := r.Apply.SafeRelease
	if safe == nil {
		return false
	}
	switch safe.Status {
	case "building", "verifying", "rolling_back":
		return true
	}
	return api.ProjectReleaseMonitoring(safe)
}

func (m *MemStore) PruneProjectSubmissions(_ context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Release state keyed by attempt keeps its receipt: canaries, migration
	// contracts and their recoveries, configuration intents, worker
	// candidates and service addition reservations read it later.
	referenced := map[string]bool{}
	attempts := map[string]bool{}
	for key := range m.projectCanaries {
		referenced[key] = true
	}
	for key, contract := range m.migrationContracts {
		referenced[key] = true
		if contract.Recovery != nil && contract.Recovery.Receipt.ReplacementAttemptID != "" {
			account, _, _ := strings.Cut(key, "\x00")
			referenced[account+"\x00"+contract.Recovery.Receipt.ReplacementAttemptID] = true
		}
	}
	for key := range m.releaseConfigurationIntents {
		referenced[key] = true
	}
	for _, attempt := range m.releaseWorkerCandidates {
		attempts[attempt] = true
	}
	for _, record := range m.projectReleaseAdditions {
		attempts[record.AttemptID] = true
	}
	var expired []string
	for key, entry := range m.projectSubmissions {
		_, attempt, _ := strings.Cut(key, "\x00")
		if entry.UpdatedAt.Before(before) && !referenced[key] && !attempts[attempt] && !projectSubmissionAdvancing(entry.Payload) {
			expired = append(expired, key)
		}
	}
	sort.Slice(expired, func(i, j int) bool {
		return m.projectSubmissions[expired[i]].UpdatedAt.Before(m.projectSubmissions[expired[j]].UpdatedAt)
	})
	if len(expired) > limit {
		expired = expired[:limit]
	}
	for _, key := range expired {
		delete(m.projectSubmissions, key)
	}
	return int64(len(expired)), nil
}

func (s *PgStore) PruneProjectSubmissions(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	return sqlc.New().PruneProjectSubmissions(ctx, s.pool, sqlc.PruneProjectSubmissionsParams{Before: pgTimestamptzUTC(before), BatchLimit: int32(min(limit, 10000))})
}
