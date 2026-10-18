package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// LogArchiveScopeStore answers whether an instance's archived runtime logs
// belong to an app. Archive objects are keyed only by instance ID, so the
// read-back route must prove ownership before fetching one.
//
// Instance rows are deleted by retention sweeps long before archived logs
// expire, so ownership also accepts the app's billing rows for that instance
// in [from, to). The account, app and instance must all match.
type LogArchiveScopeStore interface {
	InstanceLogsOwnedByApp(ctx context.Context, accountID, appID, instanceID string, from, to time.Time) (bool, error)
}

// archiveScopeIDs parses the three IDs; any malformed ID means "not owned".
func archiveScopeIDs(accountID, appID, instanceID string) (account, app, instance uuid.UUID, ok bool) {
	var err error
	if account, err = uuid.Parse(accountID); err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	if app, err = uuid.Parse(appID); err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	if instance, err = uuid.Parse(instanceID); err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	return account, app, instance, true
}

// InstanceLogsOwnedByApp implements LogArchiveScopeStore.
func (s *PgStore) InstanceLogsOwnedByApp(ctx context.Context, accountID, appID, instanceID string, from, to time.Time) (bool, error) {
	account, app, instance, ok := archiveScopeIDs(accountID, appID, instanceID)
	if !ok {
		return false, nil
	}
	return sqlc.New().InstanceLogsOwnedByApp(ctx, s.pool, sqlc.InstanceLogsOwnedByAppParams{
		InstanceID: pgtype.UUID{Bytes: instance, Valid: true},
		AppID:      pgtype.UUID{Bytes: app, Valid: true},
		AccountID:  pgtype.UUID{Bytes: account, Valid: true},
		FromMinute: pgtype.Timestamptz{Time: from, Valid: true},
		ToMinute:   pgtype.Timestamptz{Time: to, Valid: true},
	})
}

// InstanceLogsOwnedByApp mirrors the PostgreSQL predicate.
func (m *MemStore) InstanceLogsOwnedByApp(_ context.Context, accountID, appID, instanceID string, from, to time.Time) (bool, error) {
	if _, _, _, ok := archiveScopeIDs(accountID, appID, instanceID); !ok {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ins, ok := m.instances[instanceID]; ok && ins.AppID == appID {
		if app, ok := m.apps[appID]; ok && app.AccountID == accountID {
			return true, nil
		}
	}
	for _, row := range m.usage {
		if row.AccountID == accountID && row.AppID == appID && row.InstanceID == instanceID &&
			!row.Minute.Before(from) && row.Minute.Before(to) {
			return true, nil
		}
	}
	return false, nil
}
