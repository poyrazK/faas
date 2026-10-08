package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func crashCaptureFromSQLC(row sqlc.CrashCapture) CrashCapture {
	var memBytes *int64
	if row.MemBytes.Valid {
		v := row.MemBytes.Int64
		memBytes = &v
	}
	return CrashCapture{
		ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID),
		DeploymentID: pgUUIDString(row.DeploymentID), InstanceID: pgUUIDString(row.InstanceID),
		Trigger: row.Trigger, StatusCode: executionIntPtr(row.StatusCode), Route: row.Route,
		Status: CrashCaptureStatus(row.Status), StorageKey: executionStringPtr(row.StorageKey),
		VMStateStorageKey: executionStringPtr(row.VmstateStorageKey), FCVersion: executionStringPtr(row.FcVersion),
		MemBytes: memBytes, FailureCode: executionStringPtr(row.FailureCode), FailureMessage: executionStringPtr(row.FailureMessage),
		RequestedAt: row.RequestedAt.Time.UTC(), CapturedAt: timestamptzToTimePtr(row.CapturedAt),
		FinishedAt: timestamptzToTimePtr(row.FinishedAt), ExpiresAt: timestamptzToTimePtr(row.ExpiresAt),
		UpdatedAt: row.UpdatedAt.Time.UTC(), PlaintextState: CrashCapturePlaintext(row.PlaintextState),
		SealedKey: row.SealedKey, EncryptedAt: timestamptzToTimePtr(row.EncryptedAt),
	}
}

func crashCaptureRow(row sqlc.CrashCapture, err error, noRow error) (CrashCapture, error) {
	if isNoRows(err) {
		return CrashCapture{}, noRow
	}
	if err != nil {
		return CrashCapture{}, mapErr(err)
	}
	return crashCaptureFromSQLC(row), nil
}

func crashCaptureRows(rows []sqlc.CrashCapture, err error) ([]CrashCapture, error) {
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]CrashCapture, 0, len(rows))
	for _, row := range rows {
		out = append(out, crashCaptureFromSQLC(row))
	}
	return out, nil
}

func cooldownSeconds(d time.Duration) int32 {
	return int32(d / time.Second) //nolint:gosec // bounded by limits.go
}

func (s *PgStore) SetCrashSnapshotSettings(ctx context.Context, accountID, appID string, enabled bool, now time.Time) (CrashSnapshotSettings, error) {
	row, err := sqlc.New().SetCrashSnapshotSettings(ctx, s.pool, sqlc.SetCrashSnapshotSettingsParams{
		Enabled: enabled, Now: pgTime(now), AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID),
	})
	if isNoRows(err) {
		return CrashSnapshotSettings{}, ErrNotFound
	}
	if err != nil {
		return CrashSnapshotSettings{}, mapErr(err)
	}
	return crashSettingsFromSQLC(row), nil
}

func (s *PgStore) CrashSnapshotSettingsFor(ctx context.Context, accountID, appID string) (CrashSnapshotSettings, error) {
	row, err := sqlc.New().GetCrashSnapshotSettings(ctx, s.pool, sqlc.GetCrashSnapshotSettingsParams{
		AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID),
	})
	if isNoRows(err) {
		return CrashSnapshotSettings{AppID: appID, AccountID: accountID}, nil
	}
	if err != nil {
		return CrashSnapshotSettings{}, mapErr(err)
	}
	return crashSettingsFromSQLC(row), nil
}

func crashSettingsFromSQLC(row sqlc.CrashSnapshotSetting) CrashSnapshotSettings {
	return CrashSnapshotSettings{
		AppID: pgUUIDString(row.AppID), AccountID: pgUUIDString(row.AccountID),
		Enabled: row.Enabled, UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
}

func (s *PgStore) RequestHTTPCrashCapture(ctx context.Context, appID, instanceID string, statusCode int, route string, cooldown time.Duration, now time.Time) (CrashCapture, error) {
	if statusCode < 500 || statusCode > 599 {
		return CrashCapture{}, ErrCrashCaptureRefused
	}
	row, err := sqlc.New().RequestHTTPCrashCapture(ctx, s.pool, sqlc.RequestHTTPCrashCaptureParams{
		StatusCode: int32(statusCode), Route: route, Now: pgTime(now), //nolint:gosec // 500..599
		InstanceID: mustPgUUID(instanceID), AppID: mustPgUUID(appID), CooldownSeconds: cooldownSeconds(cooldown),
	})
	return crashCaptureRow(row, err, ErrCrashCaptureRefused)
}

func (s *PgStore) RequestManualCrashCapture(ctx context.Context, accountID, appID string, cooldown time.Duration, now time.Time) (CrashCapture, error) {
	row, err := sqlc.New().RequestManualCrashCapture(ctx, s.pool, sqlc.RequestManualCrashCaptureParams{
		Now: pgTime(now), AppID: mustPgUUID(appID), AccountID: mustPgUUID(accountID), CooldownSeconds: cooldownSeconds(cooldown),
	})
	return crashCaptureRow(row, err, ErrCrashCaptureRefused)
}

func (s *PgStore) ClaimNextCrashCapture(ctx context.Context, now time.Time) (CrashCapture, error) {
	row, err := sqlc.New().ClaimNextCrashCapture(ctx, s.pool, pgTime(now))
	return crashCaptureRow(row, err, ErrNotFound)
}

func (s *PgStore) CompleteCrashCapture(ctx context.Context, p CompleteCrashCaptureParams) (CrashCapture, error) {
	row, err := sqlc.New().CompleteCrashCapture(ctx, s.pool, sqlc.CompleteCrashCaptureParams{
		StorageKey: p.StorageKey, VmstateStorageKey: p.VMStateStorageKey, FcVersion: p.FCVersion, MemBytes: p.MemBytes,
		Now: pgTime(p.CapturedAt), ExpiresAt: pgTime(p.ExpiresAt), CaptureID: mustPgUUID(p.ID),
	})
	return crashCaptureRow(row, err, ErrNotFound)
}

func (s *PgStore) FailCrashCapture(ctx context.Context, id, code, message string, now time.Time) (CrashCapture, error) {
	row, err := sqlc.New().FailCrashCapture(ctx, s.pool, sqlc.FailCrashCaptureParams{
		FailureCode: code, FailureMessage: message, Now: pgTime(now), CaptureID: mustPgUUID(id),
	})
	return crashCaptureRow(row, err, ErrNotFound)
}

func (s *PgStore) FailStaleCrashCaptures(ctx context.Context, cutoff, now time.Time) ([]CrashCapture, error) {
	return crashCaptureRows(sqlc.New().FailStaleCrashCaptures(ctx, s.pool, sqlc.FailStaleCrashCapturesParams{
		Now: pgTime(now), Cutoff: pgTime(cutoff),
	}))
}

func (s *PgStore) ExpiredCrashCaptures(ctx context.Context, now time.Time, limit int) ([]CrashCapture, error) {
	return crashCaptureRows(sqlc.New().ListExpiredCrashCaptures(ctx, s.pool, sqlc.ListExpiredCrashCapturesParams{
		Now: pgTime(now), RowLimit: int32(clampAppForkListLimit(limit)), //nolint:gosec // clamped
	}))
}

func (s *PgStore) ExpireCrashCapture(ctx context.Context, id string, now time.Time) (CrashCapture, error) {
	row, err := sqlc.New().ExpireCrashCapture(ctx, s.pool, sqlc.ExpireCrashCaptureParams{Now: pgTime(now), CaptureID: mustPgUUID(id)})
	return crashCaptureRow(row, err, ErrNotFound)
}

func (s *PgStore) ListCrashCaptures(ctx context.Context, accountID, appID string, limit int) ([]CrashCapture, error) {
	return crashCaptureRows(sqlc.New().ListCrashCaptures(ctx, s.pool, sqlc.ListCrashCapturesParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), RowLimit: int32(clampAppForkListLimit(limit)), //nolint:gosec // clamped
	}))
}

func (s *PgStore) CrashCaptureByID(ctx context.Context, accountID, appID, id string) (CrashCapture, error) {
	row, err := sqlc.New().GetCrashCapture(ctx, s.pool, sqlc.GetCrashCaptureParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), CaptureID: mustPgUUID(id),
	})
	return crashCaptureRow(row, err, ErrNotFound)
}

func (s *PgStore) CrashCaptureForRestore(ctx context.Context, id string) (CrashCapture, error) {
	row, err := sqlc.New().GetCrashCaptureByID(ctx, s.pool, mustPgUUID(id))
	return crashCaptureRow(row, err, ErrNotFound)
}

func crashRowLimit(limit int) int32 {
	return int32(clampAppForkListLimit(limit)) //nolint:gosec // clamped
}

func (s *PgStore) CrashCapturesToEncrypt(ctx context.Context, now time.Time, limit int) ([]CrashCapture, error) {
	return crashCaptureRows(sqlc.New().ListCrashCapturesToEncrypt(ctx, s.pool, sqlc.ListCrashCapturesToEncryptParams{
		Now: pgTime(now), RowLimit: crashRowLimit(limit),
	}))
}

func (s *PgStore) MarkCrashCaptureEncrypted(ctx context.Context, id string, sealedKey []byte, now time.Time) (CrashCapture, error) {
	if len(sealedKey) == 0 {
		return CrashCapture{}, ErrNotFound
	}
	row, err := sqlc.New().MarkCrashCaptureEncrypted(ctx, s.pool, sqlc.MarkCrashCaptureEncryptedParams{
		SealedKey: sealedKey, Now: pgTime(now), CaptureID: mustPgUUID(id),
	})
	return crashCaptureRow(row, err, ErrNotFound)
}

func (s *PgStore) CrashCapturesToPurge(ctx context.Context, limit int) ([]CrashCapture, error) {
	return crashCaptureRows(sqlc.New().ListCrashCapturesToPurge(ctx, s.pool, crashRowLimit(limit)))
}

func (s *PgStore) BeginCrashCapturePurge(ctx context.Context, id string, now time.Time) (CrashCapture, error) {
	row, err := sqlc.New().BeginCrashCapturePurge(ctx, s.pool, sqlc.BeginCrashCapturePurgeParams{Now: pgTime(now), CaptureID: mustPgUUID(id)})
	return crashCaptureRow(row, err, ErrNotFound)
}

func (s *PgStore) FinishCrashCapturePurge(ctx context.Context, id string, now time.Time) (CrashCapture, error) {
	row, err := sqlc.New().FinishCrashCapturePurge(ctx, s.pool, sqlc.FinishCrashCapturePurgeParams{Now: pgTime(now), CaptureID: mustPgUUID(id)})
	return crashCaptureRow(row, err, ErrNotFound)
}

func (s *PgStore) CrashCapturesToStage(ctx context.Context, now time.Time, limit int) ([]CrashCapture, error) {
	return crashCaptureRows(sqlc.New().ListCrashCapturesToStage(ctx, s.pool, sqlc.ListCrashCapturesToStageParams{
		Now: pgTime(now), RowLimit: crashRowLimit(limit),
	}))
}

func (s *PgStore) BeginCrashCaptureStage(ctx context.Context, id string, now time.Time) (CrashCapture, error) {
	row, err := sqlc.New().BeginCrashCaptureStage(ctx, s.pool, sqlc.BeginCrashCaptureStageParams{Now: pgTime(now), CaptureID: mustPgUUID(id)})
	return crashCaptureRow(row, err, ErrNotFound)
}

func (s *PgStore) FinishCrashCaptureStage(ctx context.Context, id string, now time.Time) (CrashCapture, error) {
	row, err := sqlc.New().FinishCrashCaptureStage(ctx, s.pool, sqlc.FinishCrashCaptureStageParams{Now: pgTime(now), CaptureID: mustPgUUID(id)})
	return crashCaptureRow(row, err, ErrNotFound)
}

func (s *PgStore) LiveCrashCaptureDeploymentIDs(ctx context.Context) ([]string, error) {
	rows, err := sqlc.New().ListLiveCrashCaptureDeploymentIDs(ctx, s.pool)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]string, 0, len(rows))
	for _, id := range rows {
		out = append(out, pgUUIDString(id))
	}
	return out, nil
}
