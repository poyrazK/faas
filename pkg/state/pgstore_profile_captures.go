package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) CreateProfileCapture(ctx context.Context, accountID, appID string, c api.ProfileCapture, storageLimit int64) error {
	return s.insertProfileCapture(ctx, accountID, appID, c, 0, storageLimit)
}

func (s *PgStore) QueueAutomaticProfileCapture(ctx context.Context, accountID, appID string, c api.ProfileCapture, minInterval time.Duration, storageLimit int64) error {
	if c.Trigger == "" || c.Trigger == api.ProfileCaptureTriggerManual || minInterval <= 0 {
		return fmt.Errorf("automatic profile capture needs a trigger and minimum interval")
	}
	return s.insertProfileCapture(ctx, accountID, appID, c, minInterval, storageLimit)
}

// insertProfileCapture enforces, under the account and app locks, the
// account's hourly quota for the capture's kind (manual or automatic), one
// active capture per app, for automatic captures the per-trigger minimum
// interval, and the account's capture storage limit, rotating out the
// oldest automatic captures when an automatic one needs their room.
func (s *PgStore) insertProfileCapture(ctx context.Context, accountID, appID string, c api.ProfileCapture, automaticInterval time.Duration, storageLimit int64) error {
	doc, err := profileCaptureRequestDoc(c)
	if err != nil {
		return fmt.Errorf("encode profile capture: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin profile capture: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	// The account lock serializes the account-wide quota and storage checks
	// across its apps; the app row lock, the single-active check.
	if err := q.LockProfileCaptureAccount(ctx, tx, accountID); err != nil {
		return routePolicyReadError(err)
	}
	if _, err := q.LockProfileCaptureApp(ctx, tx, sqlc.LockProfileCaptureAppParams{AppID: appID, AccountID: accountID}); err != nil {
		return routePolicyReadError(err)
	}
	automatic := c.Automatic()
	recent, err := q.CountRecentProfileCaptures(ctx, tx, sqlc.CountRecentProfileCapturesParams{AccountID: accountID, Since: pgtypeFromTime(c.CreatedAt.Add(-time.Hour)), Automatic: automatic})
	if err != nil {
		return fmt.Errorf("count profile captures: %w", err)
	}
	if recent >= profileCaptureHourlyLimit(automatic) {
		return ErrProfileCaptureQuota
	}
	active, err := q.CountActiveProfileCaptures(ctx, tx, appID)
	if err != nil {
		return fmt.Errorf("count active profile captures: %w", err)
	}
	if active > 0 {
		return ErrProfileCaptureActive
	}
	if automaticInterval > 0 {
		n, err := q.CountRecentAutomaticProfileCaptures(ctx, tx, sqlc.CountRecentAutomaticProfileCapturesParams{AppID: appID, Trigger: c.Trigger, Since: pgtypeFromTime(c.CreatedAt.Add(-automaticInterval))})
		if err != nil {
			return fmt.Errorf("count automatic profile captures: %w", err)
		}
		if n > 0 {
			return ErrProfileCaptureRecent
		}
	}
	row, err := q.ProfileCaptureAccountStorage(ctx, tx, sqlc.ProfileCaptureAccountStorageParams{AccountID: accountID, Now: pgtypeFromTime(c.CreatedAt)})
	if err != nil {
		return fmt.Errorf("profile capture storage: %w", err)
	}
	usage := profileCaptureUsage{StoredBytes: row.StoredBytes, Active: row.Active, AutomaticStoredBytes: row.AutomaticStoredBytes, AutomaticActive: row.AutomaticActive}
	var candidates []rotatableProfileCapture
	if automatic && usage.automaticShareFull(storageLimit) {
		rows, err := q.ListEvictableAutomaticProfileCaptures(ctx, tx, sqlc.ListEvictableAutomaticProfileCapturesParams{AccountID: accountID, Now: pgtypeFromTime(c.CreatedAt), MaxRows: api.ProfileCaptureMaxRotated})
		if err != nil {
			return fmt.Errorf("list rotatable profile captures: %w", err)
		}
		for _, r := range rows {
			candidates = append(candidates, rotatableProfileCapture{ID: r.ID, Bytes: r.Bytes})
		}
	}
	rotate, err := planProfileCaptureStorage(usage, automatic, storageLimit, candidates)
	if err != nil {
		return err
	}
	if len(rotate) > 0 {
		if _, err := q.DeleteAccountProfileCaptures(ctx, tx, sqlc.DeleteAccountProfileCapturesParams{AccountID: accountID, Ids: rotate}); err != nil {
			return fmt.Errorf("rotate out automatic profile captures: %w", err)
		}
	}
	if err := q.InsertProfileCapture(ctx, tx, sqlc.InsertProfileCaptureParams{ID: c.ID, AppID: appID, AccountID: accountID, Capture: doc,
		CreatedAt: pgtypeFromTime(c.CreatedAt), ExpiresAt: pgtypeFromTime(c.ExpiresAt)}); err != nil {
		return fmt.Errorf("insert profile capture: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit profile capture: %w", err)
	}
	return nil
}

func decodeProfileCapture(body []byte) (api.ProfileCapture, error) {
	var out api.ProfileCapture
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("decode profile capture: %w", err)
	}
	if out.Profiles == nil {
		out.Profiles = []api.ProfileCaptureProfile{}
	}
	return out, nil
}

func (s *PgStore) GetProfileCapture(ctx context.Context, accountID, appID, id string) (api.ProfileCapture, error) {
	body, err := sqlc.New().ReadProfileCapture(ctx, s.pool, sqlc.ReadProfileCaptureParams{ID: id, AppID: appID, AccountID: accountID})
	if err != nil {
		return api.ProfileCapture{}, routePolicyReadError(err)
	}
	return decodeProfileCapture(body)
}

func (s *PgStore) DeleteProfileCapture(ctx context.Context, accountID, appID, id string) error {
	n, err := sqlc.New().DeleteProfileCapture(ctx, s.pool, sqlc.DeleteProfileCaptureParams{ID: id, AppID: appID, AccountID: accountID})
	if err != nil {
		return fmt.Errorf("delete profile capture: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ListProfileCaptures(ctx context.Context, accountID, appID string) ([]api.ProfileCapture, error) {
	rows, err := sqlc.New().ListProfileCaptures(ctx, s.pool, sqlc.ListProfileCapturesParams{AppID: appID, AccountID: accountID, MaxRows: api.ProfileCaptureMaxListed})
	if err != nil {
		return nil, fmt.Errorf("list profile captures: %w", err)
	}
	out := make([]api.ProfileCapture, 0, len(rows))
	for _, body := range rows {
		c, err := decodeProfileCapture(body)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *PgStore) ProfileCaptureBlobs(ctx context.Context, accountID, appID, id string) ([]ProfileCaptureBlob, error) {
	rows, err := sqlc.New().ListProfileCaptureData(ctx, s.pool, sqlc.ListProfileCaptureDataParams{ID: id, AppID: appID, AccountID: accountID})
	if err != nil {
		return nil, fmt.Errorf("read profile capture data: %w", err)
	}
	out := make([]ProfileCaptureBlob, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProfileCaptureBlob{Kind: r.Kind, ProcessID: r.ProcessID, Profile: r.Profile})
	}
	return out, nil
}

func (s *PgStore) ClaimProfileCapture(ctx context.Context, now time.Time) (ClaimedProfileCapture, error) {
	row, err := sqlc.New().ClaimQueuedProfileCapture(ctx, s.pool, pgtypeFromTime(now))
	if err != nil {
		return ClaimedProfileCapture{}, routePolicyReadError(err)
	}
	c, err := decodeProfileCapture(row.Capture)
	if err != nil {
		return ClaimedProfileCapture{}, err
	}
	c.ID, c.AppID, c.Status = row.ID, row.AppID, api.ProfileCaptureCapturing
	if row.CreatedAt.Valid {
		c.CreatedAt = row.CreatedAt.Time.UTC()
	}
	return ClaimedProfileCapture{ID: row.ID, AppID: row.AppID, AccountID: row.AccountID, Capture: c}, nil
}

func (s *PgStore) FinishProfileCapture(ctx context.Context, id string, result api.ProfileCapture, blobs []ProfileCaptureBlob, now time.Time) error {
	if err := validProfileCaptureFinish(result, blobs); err != nil {
		return err
	}
	doc, err := profileCaptureResultDoc(result)
	if err != nil {
		return fmt.Errorf("encode profile capture result: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin profile capture finish: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	n, err := q.FinishProfileCapture(ctx, tx, sqlc.FinishProfileCaptureParams{Status: result.Status, Result: doc, Now: pgtypeFromTime(now), ID: id})
	if err != nil {
		return fmt.Errorf("finish profile capture: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	for i, b := range blobs {
		if err := q.InsertProfileCaptureData(ctx, tx, sqlc.InsertProfileCaptureDataParams{CaptureID: id, Seq: int16(i), Kind: b.Kind, ProcessID: b.ProcessID, Profile: b.Profile}); err != nil {
			return fmt.Errorf("store profile capture data: %w", err)
		}
	}
	if result.Status == api.ProfileCaptureReady {
		if err := pgEnqueueProfileCaptureNotice(ctx, tx, id, AppWebhookEventProfileCaptureReady); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit profile capture finish: %w", err)
	}
	return nil
}

// pgEnqueueProfileCaptureNotice enqueues the capture's webhook, if it has
// one, in the transaction that changed it, so the event is enqueued once
// with the change (ADR-1155).
func pgEnqueueProfileCaptureNotice(ctx context.Context, tx pgx.Tx, id string, event AppWebhookEvent) error {
	q := sqlc.New()
	row, err := q.ReadProfileCaptureNotice(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // the app was deleted meanwhile
	}
	if err != nil {
		return fmt.Errorf("read profile capture notice: %w", err)
	}
	c, err := decodeProfileCapture(row.Capture)
	if err != nil {
		return err
	}
	payload, ok, err := profileCaptureNotice(event, c, row.Slug)
	if err != nil || !ok {
		return err
	}
	if err := q.EnqueueProfileAlertNotification(ctx, tx, sqlc.EnqueueProfileAlertNotificationParams{AccountID: row.AccountID, AppID: row.AppID,
		Event: string(event), SourceID: id, Payload: payload}); err != nil {
		return fmt.Errorf("enqueue profile capture notice: %w", err)
	}
	return nil
}

func (s *PgStore) StartProfileCapture(ctx context.Context, id, instanceID, deploymentID string) error {
	n, err := sqlc.New().StartProfileCapture(ctx, s.pool, sqlc.StartProfileCaptureParams{InstanceID: instanceID, DeploymentID: deploymentID, ID: id})
	if err != nil {
		return fmt.Errorf("start profile capture: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) StampProfileCaptureOOM(ctx context.Context, appID, instanceID string, oom ProfileCaptureOOM, since time.Time) (string, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("begin profile capture OOM link: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := sqlc.New().StampProfileCaptureOOM(ctx, tx, sqlc.StampProfileCaptureOOMParams{KilledAt: pgtypeFromTime(oom.KilledAt),
		PeakMb: int32(oom.PeakMB), PlanMb: int32(oom.PlanMB), AppID: appID, InstanceID: instanceID, Since: pgtypeFromTime(since)})
	if err != nil {
		return "", routePolicyReadError(err)
	}
	// A capture still running announces the kill when it finishes.
	if row.FirstLink && row.Status == api.ProfileCaptureReady {
		if err := pgEnqueueProfileCaptureNotice(ctx, tx, row.ID, AppWebhookEventProfileCaptureOOMLinked); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit profile capture OOM link: %w", err)
	}
	return row.ID, nil
}

func (s *PgStore) LatestOOMProfileCapture(ctx context.Context, accountID, appID, deploymentID string) (api.ProfileCapture, error) {
	body, err := sqlc.New().LatestOOMProfileCapture(ctx, s.pool, sqlc.LatestOOMProfileCaptureParams{AppID: appID, AccountID: accountID, DeploymentID: deploymentID})
	if err != nil {
		return api.ProfileCapture{}, routePolicyReadError(err)
	}
	return decodeProfileCapture(body)
}

func (s *PgStore) ProfileCaptureForRequest(ctx context.Context, accountID, appID, traceID string) (api.ProfileCapture, error) {
	if traceID == "" {
		return api.ProfileCapture{}, ErrNotFound
	}
	body, err := sqlc.New().ProfileCaptureForRequest(ctx, s.pool, sqlc.ProfileCaptureForRequestParams{AppID: appID, AccountID: accountID, TraceID: traceID})
	if err != nil {
		return api.ProfileCapture{}, routePolicyReadError(err)
	}
	return decodeProfileCapture(body)
}

func (s *PgStore) ExpireProfileCaptures(ctx context.Context, now time.Time) (int64, error) {
	q := sqlc.New()
	failed, err := q.FailStaleProfileCaptures(ctx, s.pool, sqlc.FailStaleProfileCapturesParams{Now: pgtypeFromTime(now), Reason: profileCaptureInterruptedReason,
		ClaimedBefore: pgtypeFromTime(now.Add(-ProfileCaptureInterruptedAfter)), QueuedBefore: pgtypeFromTime(now.Add(-ProfileCaptureQueuedTimeout))})
	if err != nil {
		return 0, fmt.Errorf("fail stale profile captures: %w", err)
	}
	deleted, err := q.DeleteExpiredProfileCaptures(ctx, s.pool, pgtypeFromTime(now))
	if err != nil {
		return failed, fmt.Errorf("delete expired profile captures: %w", err)
	}
	return failed + deleted, nil
}
