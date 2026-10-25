package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// LogArchiveErasureStore tracks the archived runtime logs an account
// deletion must remove. Archive objects are keyed by instance alone, so the
// grace sweep first snapshots the account's instances, then deletes their
// objects a batch at a time and marks each one done.
type LogArchiveErasureStore interface {
	// BeginLogArchiveErasure snapshots the instances whose logs go with the
	// account, once per deletion request. A snapshot taken for an earlier
	// request is replaced.
	BeginLogArchiveErasure(ctx context.Context, accountID string, deletionRequestedAt time.Time) error
	// PendingLogArchiveErasure returns up to limit instances whose archived
	// logs are not yet deleted, in instance order.
	PendingLogArchiveErasure(ctx context.Context, accountID string, limit int) ([]string, error)
	// MarkLogArchiveErased records that an instance's archived logs are gone.
	MarkLogArchiveErased(ctx context.Context, accountID, instanceID string) error
}

// BeginLogArchiveErasure implements LogArchiveErasureStore.
func (s *PgStore) BeginLogArchiveErasure(ctx context.Context, accountID string, deletionRequestedAt time.Time) error {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return fmt.Errorf("state: log archive erasure account: %w", err)
	}
	// PostgreSQL keeps microseconds; compare at that precision.
	requested := pgtype.Timestamptz{Time: deletionRequestedAt.UTC().Truncate(time.Microsecond), Valid: true}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		current, err := q.LogArchiveErasureRequestedAt(ctx, tx, account)
		switch {
		case err == nil && current.Time.Equal(requested.Time):
			return nil
		case err == nil:
			if err := q.DeleteLogArchiveErasure(ctx, tx, account); err != nil {
				return fmt.Errorf("state: replace log archive erasure: %w", err)
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("state: read log archive erasure: %w", err)
		}
		inserted, err := q.InsertLogArchiveErasure(ctx, tx, sqlc.InsertLogArchiveErasureParams{AccountID: account, DeletionRequestedAt: requested})
		if err != nil {
			return fmt.Errorf("state: begin log archive erasure: %w", err)
		}
		if inserted == 0 {
			// Another sweep took the snapshot first.
			return nil
		}
		if _, err := q.SnapshotLogArchiveErasureInstances(ctx, tx, account); err != nil {
			return fmt.Errorf("state: snapshot log archive erasure: %w", err)
		}
		return nil
	})
}

// PendingLogArchiveErasure implements LogArchiveErasureStore.
func (s *PgStore) PendingLogArchiveErasure(ctx context.Context, accountID string, limit int) ([]string, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return nil, fmt.Errorf("state: log archive erasure account: %w", err)
	}
	rows, err := sqlc.New().PendingLogArchiveErasureInstances(ctx, s.pool, sqlc.PendingLogArchiveErasureInstancesParams{AccountID: account, RowLimit: int32(max(limit, 0))})
	if err != nil {
		return nil, fmt.Errorf("state: pending log archive erasure: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, pgUUIDString(row))
	}
	return out, nil
}

// MarkLogArchiveErased implements LogArchiveErasureStore.
func (s *PgStore) MarkLogArchiveErased(ctx context.Context, accountID, instanceID string) error {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return fmt.Errorf("state: log archive erasure account: %w", err)
	}
	instance, err := parsePgUUID(instanceID)
	if err != nil {
		return fmt.Errorf("state: log archive erasure instance: %w", err)
	}
	if err := sqlc.New().MarkLogArchiveInstanceErased(ctx, s.pool, sqlc.MarkLogArchiveInstanceErasedParams{AccountID: account, InstanceID: instance}); err != nil {
		return fmt.Errorf("state: mark log archive erased: %w", err)
	}
	return nil
}

// memLogArchiveErasure mirrors log_archive_erasures and its instance rows;
// instances maps an instance ID to whether its logs are erased.
type memLogArchiveErasure struct {
	requestedAt time.Time
	instances   map[string]bool
}

// BeginLogArchiveErasure mirrors the PostgreSQL snapshot.
func (m *MemStore) BeginLogArchiveErasure(_ context.Context, accountID string, deletionRequestedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if current, ok := m.logArchiveErasures[accountID]; ok && current.requestedAt.Equal(deletionRequestedAt) {
		return nil
	}
	personal := func(appID string) bool {
		app, ok := m.apps[appID]
		if !ok {
			// A hard-deleted app's billing rows still name the account.
			return true
		}
		return app.AccountID == accountID && (app.OrgID == "" || m.orgs[app.OrgID].Personal)
	}
	erasure := &memLogArchiveErasure{requestedAt: deletionRequestedAt, instances: map[string]bool{}}
	for id, ins := range m.instances {
		if app, ok := m.apps[ins.AppID]; ok && app.AccountID == accountID && personal(app.ID) {
			erasure.instances[id] = false
		}
	}
	for _, row := range m.usage {
		if row.AccountID == accountID && row.InstanceID != "" && (row.AppID == "" || personal(row.AppID)) {
			erasure.instances[row.InstanceID] = false
		}
	}
	if m.logArchiveErasures == nil {
		m.logArchiveErasures = map[string]*memLogArchiveErasure{}
	}
	m.logArchiveErasures[accountID] = erasure
	return nil
}

// PendingLogArchiveErasure mirrors the PostgreSQL read.
func (m *MemStore) PendingLogArchiveErasure(_ context.Context, accountID string, limit int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	erasure, ok := m.logArchiveErasures[accountID]
	if !ok {
		return []string{}, nil
	}
	pending := []string{}
	for id, erased := range erasure.instances {
		if !erased {
			pending = append(pending, id)
		}
	}
	sort.Strings(pending)
	if limit < 0 {
		limit = 0
	}
	if len(pending) > limit {
		pending = pending[:limit]
	}
	return pending, nil
}

// MarkLogArchiveErased mirrors the PostgreSQL update.
func (m *MemStore) MarkLogArchiveErased(_ context.Context, accountID, instanceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if erasure, ok := m.logArchiveErasures[accountID]; ok {
		if _, tracked := erasure.instances[instanceID]; tracked {
			erasure.instances[instanceID] = true
		}
	}
	return nil
}
