package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	_ ConfigRevisionStore       = (*PgStore)(nil)
	_ ConfigGuardTelemetryStore = (*PgStore)(nil)
)

func pgTimestamptzUTC(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func optionalTimestamptz(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

func configRevisionFromRow(r sqlc.AppConfigRevision) (ConfigRevision, error) {
	out := ConfigRevision{ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID),
		Scope: r.Scope, Revision: r.Revision, Source: r.Source, Key: r.ConfigKey, Actor: r.Actor,
		GuardID: pgUUIDStringNullable(r.GuardID), CreatedAt: r.CreatedAt.Time.UTC()}
	if r.RestoredRevision.Valid {
		out.RestoredRevision = r.RestoredRevision.Int64
	}
	if err := json.Unmarshal(r.Variables, &out.Variables); err != nil {
		return ConfigRevision{}, fmt.Errorf("state: decode config revision variables: %w", err)
	}
	if err := json.Unmarshal(r.Secrets, &out.Secrets); err != nil {
		return ConfigRevision{}, fmt.Errorf("state: decode config revision secrets: %w", err)
	}
	if out.Variables == nil {
		out.Variables = map[string]string{}
	}
	if out.Secrets == nil {
		out.Secrets = map[string]ConfigSecretFingerprint{}
	}
	return out, nil
}

func configGuardFromRow(g sqlc.AppConfigGuard) ConfigGuard {
	return ConfigGuard{ID: pgUUIDString(g.ID), AccountID: pgUUIDString(g.AccountID), AppID: pgUUIDString(g.AppID),
		Scope: g.Scope, BaseRevision: g.BaseRevision, HeadRevision: g.HeadRevision, Status: g.Status,
		Restorable: g.Restorable, OpenedAt: g.OpenedAt.Time.UTC(), LastChangeAt: g.LastChangeAt.Time.UTC(),
		WindowEndsAt: optionalTimestamptz(g.WindowEndsAt), ClosedAt: optionalTimestamptz(g.ClosedAt),
		Decision: append(json.RawMessage(nil), g.Decision...)}
}

func (s *PgStore) configStateTx(ctx context.Context, tx pgx.Tx, accountID, appID, scope string) (map[string]string, map[string]ConfigSecretFingerprint, error) {
	q := sqlc.New()
	rows, err := q.ReadAppConfigVariables(ctx, tx, sqlc.ReadAppConfigVariablesParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: scope})
	if err != nil {
		return nil, nil, fmt.Errorf("state: read config variables: %w", err)
	}
	variables := make(map[string]string, len(rows))
	for _, row := range rows {
		variables[row.Key] = row.Value
	}
	secretRows, err := q.ReadAppConfigSecretFingerprints(ctx, tx, sqlc.ReadAppConfigSecretFingerprintsParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Scope: scope})
	if err != nil {
		return nil, nil, fmt.Errorf("state: read config secrets: %w", err)
	}
	secrets := make(map[string]ConfigSecretFingerprint, len(secretRows))
	for _, row := range secretRows {
		secrets[row.Key] = ConfigSecretFingerprint{Version: row.SecretVersion, ValueHash: row.ValueHash}
	}
	return variables, secrets, nil
}

func (s *PgStore) ConfigState(ctx context.Context, accountID, appID, scope string) (map[string]string, map[string]ConfigSecretFingerprint, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return s.configStateTx(ctx, tx, accountID, appID, scope)
}

func (s *PgStore) latestConfigRevisionTx(ctx context.Context, tx pgx.Tx, appID, scope string) (ConfigRevision, bool, error) {
	row, err := sqlc.New().LatestAppConfigRevision(ctx, tx, sqlc.LatestAppConfigRevisionParams{AppID: mustPgUUID(appID), Scope: scope})
	if errors.Is(err, pgx.ErrNoRows) {
		return ConfigRevision{}, false, nil
	}
	if err != nil {
		return ConfigRevision{}, false, fmt.Errorf("state: read latest config revision: %w", err)
	}
	r, err := configRevisionFromRow(row)
	return r, err == nil, err
}

func (s *PgStore) insertConfigRevisionTx(ctx context.Context, tx pgx.Tx, r ConfigRevision) (ConfigRevision, error) {
	variables, err := json.Marshal(r.Variables)
	if err != nil {
		return ConfigRevision{}, err
	}
	secrets, err := json.Marshal(r.Secrets)
	if err != nil {
		return ConfigRevision{}, err
	}
	params := sqlc.InsertAppConfigRevisionParams{ID: mustPgUUID(uuid.NewString()), AccountID: mustPgUUID(r.AccountID),
		AppID: mustPgUUID(r.AppID), Scope: r.Scope, Revision: r.Revision, Source: r.Source, ConfigKey: r.Key,
		Actor: r.Actor, Variables: variables, Secrets: secrets, CreatedAt: pgTimestamptzUTC(r.CreatedAt)}
	if r.GuardID != "" {
		params.GuardID = mustPgUUID(r.GuardID)
	}
	if r.RestoredRevision > 0 {
		params.RestoredRevision = pgtype.Int8{Int64: r.RestoredRevision, Valid: true}
	}
	row, err := sqlc.New().InsertAppConfigRevision(ctx, tx, params)
	if err != nil {
		return ConfigRevision{}, fmt.Errorf("state: insert config revision: %w", mapErr(err))
	}
	return configRevisionFromRow(row)
}

func closeConfigGuardTx(ctx context.Context, tx pgx.Tx, id, status string, decision json.RawMessage, expectHead int64, now time.Time) (bool, error) {
	q := sqlc.New()
	n, err := q.CloseAppConfigGuard(ctx, tx, sqlc.CloseAppConfigGuardParams{Status: status, Decision: decision,
		ClosedAt: pgTimestamptzUTC(now), ID: mustPgUUID(id), ExpectHead: expectHead})
	if err != nil {
		return false, fmt.Errorf("state: close config guard: %w", mapErr(err))
	}
	if n == 0 {
		return false, nil
	}
	if err := q.DeleteAppConfigGuardSecrets(ctx, tx, mustPgUUID(id)); err != nil {
		return false, fmt.Errorf("state: erase config guard secrets: %w", err)
	}
	return true, nil
}

func (s *PgStore) BeginConfigChange(ctx context.Context, start ConfigChangeStart) (ConfigChangeTicket, error) {
	if !validConfigChangeStart(start) {
		return ConfigChangeTicket{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ConfigChangeTicket{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	now := start.Now.UTC()
	if err := q.LockAppConfigScope(ctx, tx, sqlc.LockAppConfigScopeParams{AppID: mustPgUUID(start.AppID), Scope: start.Scope}); err != nil {
		return ConfigChangeTicket{}, fmt.Errorf("state: lock config scope: %w", err)
	}
	variables, secrets, err := s.configStateTx(ctx, tx, start.AccountID, start.AppID, start.Scope)
	if err != nil {
		return ConfigChangeTicket{}, err
	}
	latest, ok, err := s.latestConfigRevisionTx(ctx, tx, start.AppID, start.Scope)
	if err != nil {
		return ConfigChangeTicket{}, err
	}
	guardRow, err := q.LockWatchingAppConfigGuard(ctx, tx, sqlc.LockWatchingAppConfigGuardParams{AppID: mustPgUUID(start.AppID), Scope: start.Scope})
	watching := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ConfigChangeTicket{}, fmt.Errorf("state: lock config guard: %w", err)
	}
	guard := configGuardFromRow(guardRow)
	if !ok || !configStateEqual(latest, variables, secrets) {
		if watching {
			if _, err := closeConfigGuardTx(ctx, tx, guard.ID, ConfigGuardSuperseded, supersededConfigGuardDecision(configGuardSupersededOutOfBand), 0, now); err != nil {
				return ConfigChangeTicket{}, err
			}
			watching = false
		}
		latest, err = s.insertConfigRevisionTx(ctx, tx, ConfigRevision{AccountID: start.AccountID, AppID: start.AppID,
			Scope: start.Scope, Revision: latest.Revision + 1, Source: ConfigSourceBaseline, Actor: start.Actor,
			Variables: variables, Secrets: secrets, CreatedAt: now})
		if err != nil {
			return ConfigChangeTicket{}, err
		}
	}
	ticket := ConfigChangeTicket{BaseRevision: latest.Revision}
	if !start.Guard {
		if watching {
			if _, err := closeConfigGuardTx(ctx, tx, guard.ID, ConfigGuardSuperseded, supersededConfigGuardDecision(configGuardSupersededByUnguarded), 0, now); err != nil {
				return ConfigChangeTicket{}, err
			}
		}
		return ticket, tx.Commit(ctx)
	}
	if !watching {
		row, err := q.InsertAppConfigGuard(ctx, tx, sqlc.InsertAppConfigGuardParams{ID: mustPgUUID(uuid.NewString()),
			AccountID: mustPgUUID(start.AccountID), AppID: mustPgUUID(start.AppID), Scope: start.Scope,
			BaseRevision: latest.Revision, OpenedAt: pgTimestamptzUTC(now)})
		if err != nil {
			return ConfigChangeTicket{}, fmt.Errorf("state: open config guard: %w", mapErr(err))
		}
		guard = configGuardFromRow(row)
	}
	if start.SecretKey != "" {
		if err := q.RetainAppConfigGuardSecret(ctx, tx, sqlc.RetainAppConfigGuardSecretParams{GuardID: mustPgUUID(guard.ID),
			ConfigKey: start.SecretKey, AccountID: mustPgUUID(start.AccountID), AppID: mustPgUUID(start.AppID),
			Scope: start.Scope}); err != nil {
			return ConfigChangeTicket{}, fmt.Errorf("state: retain guarded secret: %w", mapErr(err))
		}
	}
	ticket.GuardID, ticket.BaseRevision = guard.ID, guard.BaseRevision
	return ticket, tx.Commit(ctx)
}

func (s *PgStore) FinishConfigChange(ctx context.Context, f ConfigChangeFinish) (ConfigRevision, error) {
	if !validConfigChangeFinish(f) {
		return ConfigRevision{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ConfigRevision{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	now := f.Now.UTC()
	if err := q.LockAppConfigScope(ctx, tx, sqlc.LockAppConfigScopeParams{AppID: mustPgUUID(f.AppID), Scope: f.Scope}); err != nil {
		return ConfigRevision{}, fmt.Errorf("state: lock config scope: %w", err)
	}
	variables, secrets, err := s.configStateTx(ctx, tx, f.AccountID, f.AppID, f.Scope)
	if err != nil {
		return ConfigRevision{}, err
	}
	latest, _, err := s.latestConfigRevisionTx(ctx, tx, f.AppID, f.Scope)
	if err != nil {
		return ConfigRevision{}, err
	}
	revision, err := s.insertConfigRevisionTx(ctx, tx, ConfigRevision{AccountID: f.AccountID, AppID: f.AppID, Scope: f.Scope,
		Revision: latest.Revision + 1, Source: f.Source, Key: f.Key, Actor: f.Actor, Variables: variables, Secrets: secrets,
		GuardID: f.GuardID, RestoredRevision: f.RestoredRevision, CreatedAt: now})
	if err != nil {
		return ConfigRevision{}, err
	}
	if f.GuardID != "" {
		if _, err := q.RecordAppConfigGuardChange(ctx, tx, sqlc.RecordAppConfigGuardChangeParams{HeadRevision: revision.Revision,
			ChangedAt: pgTimestamptzUTC(now), Unrestorable: f.Unrestorable, ID: mustPgUUID(f.GuardID)}); err != nil {
			return ConfigRevision{}, fmt.Errorf("state: record guarded change: %w", err)
		}
	}
	if err := q.PruneAppConfigRevisions(ctx, tx, sqlc.PruneAppConfigRevisionsParams{AppID: mustPgUUID(f.AppID), Scope: f.Scope,
		KeepAfter: revision.Revision - api.ConfigRevisionsRetained}); err != nil {
		return ConfigRevision{}, fmt.Errorf("state: prune config revisions: %w", err)
	}
	return revision, tx.Commit(ctx)
}

func (s *PgStore) AbandonConfigChange(ctx context.Context, guardID string, now time.Time) error {
	if _, err := uuid.Parse(guardID); err != nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := sqlc.New().GetAppConfigGuard(ctx, tx, mustPgUUID(guardID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("state: read config guard: %w", err)
	}
	if row.Status != ConfigGuardWatching || row.HeadRevision != row.BaseRevision {
		return nil
	}
	if _, err := closeConfigGuardTx(ctx, tx, guardID, ConfigGuardSuperseded, supersededConfigGuardDecision(configGuardAbandoned), row.HeadRevision, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) ListConfigRevisions(ctx context.Context, appID, scope string, limit int) ([]ConfigRevision, error) {
	if limit <= 0 || limit > api.ConfigRevisionsListMax {
		limit = api.ConfigRevisionsListMax
	}
	rows, err := sqlc.New().ListAppConfigRevisions(ctx, s.pool, sqlc.ListAppConfigRevisionsParams{
		AppID: mustPgUUID(appID), Scope: scope, RowLimit: int32(limit)}) //nolint:gosec // bounded by ConfigRevisionsListMax
	if err != nil {
		return nil, fmt.Errorf("state: list config revisions: %w", err)
	}
	out := make([]ConfigRevision, 0, len(rows))
	for _, row := range rows {
		r, err := configRevisionFromRow(row.AppConfigRevision)
		if err != nil {
			return nil, err
		}
		r.GuardStatus = row.GuardStatus
		out = append(out, r)
	}
	return out, nil
}

func (s *PgStore) GetConfigRevision(ctx context.Context, appID, scope string, revision int64) (ConfigRevision, error) {
	row, err := sqlc.New().GetAppConfigRevision(ctx, s.pool, sqlc.GetAppConfigRevisionParams{
		AppID: mustPgUUID(appID), Scope: scope, Revision: revision})
	if err != nil {
		return ConfigRevision{}, mapErr(err)
	}
	r, err := configRevisionFromRow(row.AppConfigRevision)
	r.GuardStatus = row.GuardStatus
	return r, err
}

func (s *PgStore) ListWatchingConfigGuards(ctx context.Context, limit int) ([]ConfigGuard, error) {
	if limit <= 0 || limit > api.ConfigGuardBatchSize {
		limit = api.ConfigGuardBatchSize
	}
	rows, err := sqlc.New().ListWatchingAppConfigGuards(ctx, s.pool, int32(limit)) //nolint:gosec // bounded by ConfigGuardBatchSize
	if err != nil {
		return nil, fmt.Errorf("state: list watching config guards: %w", err)
	}
	out := make([]ConfigGuard, 0, len(rows))
	for _, row := range rows {
		out = append(out, configGuardFromRow(row))
	}
	return out, nil
}

func (s *PgStore) LatestConfigGuard(ctx context.Context, appID, scope string) (ConfigGuard, error) {
	row, err := sqlc.New().LatestAppConfigGuard(ctx, s.pool, sqlc.LatestAppConfigGuardParams{AppID: mustPgUUID(appID), Scope: scope})
	if err != nil {
		return ConfigGuard{}, mapErr(err)
	}
	return configGuardFromRow(row), nil
}

func (s *PgStore) OpenConfigGuardWindow(ctx context.Context, guardID string, lastChangeAt, endsAt time.Time) (bool, error) {
	n, err := sqlc.New().OpenAppConfigGuardWindow(ctx, s.pool, sqlc.OpenAppConfigGuardWindowParams{
		WindowEndsAt: pgTimestamptzUTC(endsAt), ID: mustPgUUID(guardID), LastChangeAt: pgTimestamptzUTC(lastChangeAt)})
	if err != nil {
		return false, fmt.Errorf("state: open config guard window: %w", err)
	}
	return n == 1, nil
}

func (s *PgStore) CloseConfigGuard(ctx context.Context, guardID, status string, decision json.RawMessage, expectHead int64, now time.Time) (bool, error) {
	if !configGuardClosedStatuses[status] || !json.Valid(decision) {
		return false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	closed, err := closeConfigGuardTx(ctx, tx, guardID, status, decision, expectHead, now)
	if err != nil || !closed {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *PgStore) ConfigGuardSecrets(ctx context.Context, guardID string) ([]ConfigGuardSecret, error) {
	rows, err := sqlc.New().ListAppConfigGuardSecrets(ctx, s.pool, mustPgUUID(guardID))
	if err != nil {
		return nil, fmt.Errorf("state: list config guard secrets: %w", err)
	}
	out := make([]ConfigGuardSecret, 0, len(rows))
	for _, row := range rows {
		out = append(out, ConfigGuardSecret{Key: row.ConfigKey, Existed: row.Existed,
			Ciphertext: append([]byte(nil), row.Ciphertext...), Kid: row.Kid.String, ValueHash: row.ValueHash.String,
			SecretClass: row.SecretClass.String})
	}
	return out, nil
}

func (s *PgStore) ConfigGuardTelemetry(ctx context.Context, cq ConfigGuardTelemetryQuery) (ConfigGuardSample, error) {
	row, err := sqlc.New().AppConfigGuardTelemetry(ctx, s.pool, sqlc.AppConfigGuardTelemetryParams{
		AppID: mustPgUUID(cq.AppID), Scope: cq.Scope, ChangedAt: pgTimestamptzUTC(cq.ChangedAt),
		BaselineFrom: pgTimestamptzUTC(cq.BaselineFrom), OpenedAt: pgTimestamptzUTC(cq.OpenedAt), Until: pgTimestamptzUTC(cq.Until)})
	if err != nil {
		return ConfigGuardSample{}, fmt.Errorf("state: read config guard telemetry: %w", err)
	}
	return ConfigGuardSample{NewRequests: row.NewRequests, NewServerErrors: row.NewServerErrors,
		OldRequests: row.OldRequests, OldServerErrors: row.OldServerErrors}, nil
}
