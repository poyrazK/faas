package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const EventRecoveryCoverage = "captured_application_recipients"

var _ EventRecoveryStore = (*PgStore)(nil)
var _ EventRecoveryStore = (*MemStore)(nil)

var (
	ErrEventRecoveryRetryParent     = errors.New("parent must be a completed or cancelled execution recovery")
	ErrEventRecoveryRequestConflict = errors.New("request_id is already used with a different recovery selection")
	ErrEventRecoveryState           = errors.New("event recovery job is completed, cancelled, or expired")
	ErrEventRecoveryQuery           = errors.New("invalid event recovery request")
	ErrEventRecoveryQuota           = errors.New("event recovery active-job limit reached")
	ErrEventRecoverySelection       = errors.New("event recovery selection exceeds recipient limit; narrow the filters")
)

type EventRecoveryStore interface {
	ListEventRecoveryHistory(context.Context, string, string, int64, int) (api.EventRecoveryHistory, error)
	ListEventRecoveries(context.Context, string, string, api.EventRecoveryListQuery) (api.EventRecoveryJobs, error)
	PauseEventRecovery(context.Context, string, string) (api.EventRecoveryJob, error)
	ResumeEventRecovery(context.Context, string, string) (api.EventRecoveryJob, error)
	SetEventRecoveryRate(context.Context, string, string, api.EventRecoveryRateRequest) (api.EventRecoveryJob, error)
	PreviewEventRecovery(context.Context, string, string, api.EventRecoveryRequest) (api.EventRecoveryPreview, error)
	CreateEventRecovery(context.Context, string, string, api.EventRecoveryRequest) (api.EventRecoveryJob, error)
	GetEventRecovery(context.Context, string, string) (api.EventRecoveryJob, error)
	CancelEventRecovery(context.Context, string, string) (api.EventRecoveryJob, error)
	ListEventRecoveryItems(context.Context, string, string, int64, int) (api.EventRecoveryItems, error)
	ProcessNextEventRecovery(context.Context, time.Time) (bool, error)
	PruneEventRecoveries(context.Context, time.Time, int) (int64, error)
}

func normalizeEventRecovery(accountID, appID string, req *api.EventRecoveryRequest) error {
	for _, id := range []string{accountID, appID} {
		if _, err := uuid.Parse(id); err != nil {
			return ErrEventRecoveryQuery
		}
	}
	if err := req.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	if req.ParentJobID != "" {
		req.ParentJobID = canonicalMemUUID(req.ParentJobID)
	}
	if req.RequestID != "" {
		req.RequestID = canonicalMemUUID(req.RequestID)
	}
	if req.RatePerSecond == 0 {
		req.RatePerSecond = api.EventRecoveryRateDefault
	}
	return nil
}
func eventRecoveryIDs(accountID, jobID string) error {
	for _, id := range []string{accountID, jobID} {
		if _, err := uuid.Parse(id); err != nil {
			return ErrEventRecoveryQuery
		}
	}
	return nil
}
func eventRecoveryCandidates(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, accountID, appID string, req api.EventRecoveryRequest, now time.Time) ([]sqlc.EventRecoveryCandidatesRow, error) {
	if req.ParentJobID != "" {
		return eventRecoveryRetryCandidates(ctx, q, db, accountID, appID, req, now)
	}
	if req.Mode == "execution" {
		return eventExecutionRecoveryCandidates(ctx, q, db, accountID, appID, req, now)
	}
	return q.EventRecoveryCandidates(ctx, db, sqlc.EventRecoveryCandidatesParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), SubscriptionID: req.SubscriptionID, EventSource: req.EventSource, EventType: req.EventType, FailureCode: req.FailureCode, IncludeNonRetryable: req.IncludeNonRetryable, FailedBefore: pgtypeFromTime(now.Add(-time.Duration(req.MinAgeSeconds) * time.Second)), PageLimit: api.EventRecoveryRecipientsMax + 1})
}
func recoveryCandidateItem(row sqlc.EventRecoveryCandidatesRow, position int64) api.EventRecoveryItem {
	out := api.EventRecoveryItem{InvocationID: executionRecoveryInvocationID(row.ExpectedProgress), Position: position, EventSource: row.EventSource, EventID: row.EventID, EventType: row.EventType, SubscriptionID: row.SubscriptionID, FailedAt: timeFromPgtype(row.FailedAt), FailureCode: eventHistoryText(row.FailureCode, api.EventRoutingHistoryCodeMaxBytes), Retryable: row.Retryable, State: "pending"}
	recoveryItemLineage(&out, row.ExpectedProgress)
	return out
}
func recoveryStoredItem(row sqlc.EventRecoveryItem) api.EventRecoveryItem {
	out := api.EventRecoveryItem{InvocationID: executionRecoveryInvocationID(row.ExpectedProgress), Position: row.Position, EventSource: row.EventSource, EventID: row.EventID, EventType: row.EventType, SubscriptionID: row.SubscriptionID, FailedAt: timeFromPgtype(row.FailedAt), FailureCode: eventHistoryText(row.FailureCode, api.EventRoutingHistoryCodeMaxBytes), Retryable: row.Retryable, State: row.State, Reason: row.Reason}
	recoveryItemLineage(&out, row.ExpectedProgress)
	if row.ReplayInvocationID.Valid {
		out.ReplayInvocationID = uuidString(row.ReplayInvocationID)
	}
	if row.ReplayGeneration.Valid {
		generation := row.ReplayGeneration.Int64
		out.ReplayGeneration = &generation
	}
	return out
}
func (s *PgStore) PreviewEventRecovery(ctx context.Context, accountID, appID string, req api.EventRecoveryRequest) (api.EventRecoveryPreview, error) {
	if err := normalizeEventRecovery(accountID, appID, &req); err != nil {
		return api.EventRecoveryPreview{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.EventRecoveryPreview{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = sqlc.New().EventRecoveryReadApp(ctx, tx, sqlc.EventRecoveryReadAppParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)}); errors.Is(err, pgx.ErrNoRows) {
		return api.EventRecoveryPreview{}, ErrNotFound
	} else if err != nil {
		return api.EventRecoveryPreview{}, err
	}
	now := time.Now().UTC()
	rows, err := eventRecoveryCandidates(ctx, sqlc.New(), tx, accountID, appID, req, now)
	if err != nil {
		return api.EventRecoveryPreview{}, fmt.Errorf("preview event recovery: %w", err)
	}
	out := api.EventRecoveryPreview{ObservedAt: now, Coverage: eventRecoveryCoverage(req), MatchedCount: int64(len(rows)), ExceedsJobLimit: len(rows) > api.EventRecoveryRecipientsMax, Sample: []api.EventRecoveryItem{}}
	for i, row := range rows[:min(len(rows), api.EventRecoveryPreviewLimit)] {
		out.Sample = append(out.Sample, recoveryCandidateItem(row, int64(i+1)))
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) CreateEventRecovery(ctx context.Context, accountID, appID string, req api.EventRecoveryRequest) (api.EventRecoveryJob, error) {
	if req.ParentJobID != "" && req.RequestID == "" {
		return api.EventRecoveryJob{}, fmt.Errorf("%w: request_id is required for a child recovery", ErrEventRecoveryQuery)
	}
	if err := normalizeEventRecovery(accountID, appID, &req); err != nil {
		return api.EventRecoveryJob{}, err
	}
	ctx, err := WithEventRecoveryReason(ctx, req.Reason)
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	req.Reason = ""
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	// Serialize job quota decisions and retained-receipt selection with pruning.
	if err = q.EventReplayBackfillLockAccountRange(ctx, tx, mustPgUUID(accountID)); err != nil {
		return api.EventRecoveryJob{}, err
	}
	if _, err = q.EventRecoveryApp(ctx, tx, sqlc.EventRecoveryAppParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)}); errors.Is(err, pgx.ErrNoRows) {
		return api.EventRecoveryJob{}, ErrNotFound
	} else if err != nil {
		return api.EventRecoveryJob{}, err
	}
	existing, err := prepareRecoveryRetry(ctx, q, tx, accountID, appID, req)
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	if existing != nil {
		return *existing, tx.Commit(ctx)
	}
	active, err := q.EventRecoveryActiveCount(ctx, tx, mustPgUUID(accountID))
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	if active >= api.EventRecoveryActiveJobsMax {
		return api.EventRecoveryJob{}, ErrEventRecoveryQuota
	}
	now := time.Now().UTC()
	rows, err := eventRecoveryCandidates(ctx, q, tx, accountID, appID, req, now)
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	if len(rows) > api.EventRecoveryRecipientsMax {
		return api.EventRecoveryJob{}, ErrEventRecoverySelection
	}
	selection, err := json.Marshal(req)
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	id, err := q.EventRecoveryCreate(ctx, tx, sqlc.EventRecoveryCreateParams{RequestID: recoveryRequestUUID(req.RequestID), AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Selection: selection, RatePerSecond: int32(req.RatePerSecond), NowAt: pgtypeFromTime(now), ExpiresAt: pgtypeFromTime(now.Add(api.EventRecoveryJobLifetime))})
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	for i, row := range rows {
		if err = q.EventRecoveryInsertItem(ctx, tx, sqlc.EventRecoveryInsertItemParams{JobID: id, Position: int64(i + 1), OutboxID: row.OutboxID, SubscriptionID: row.SubscriptionID, EventSource: row.EventSource, EventID: row.EventID, EventType: row.EventType, FailedAt: row.FailedAt, FailureCode: eventHistoryText(row.FailureCode, api.EventRoutingHistoryCodeMaxBytes), Retryable: row.Retryable, ExpectedProgress: row.ExpectedProgress}); err != nil {
			return api.EventRecoveryJob{}, err
		}
	}
	if len(rows) == 0 {
		if err = q.EventRecoverySchedule(ctx, tx, sqlc.EventRecoveryScheduleParams{JobID: id, NowAt: pgtypeFromTime(now), NextAt: pgtypeFromTime(now)}); err != nil {
			return api.EventRecoveryJob{}, err
		}
	}
	out, err := getEventRecovery(ctx, q, tx, accountID, uuidString(id))
	if err != nil {
		return out, err
	}
	if err = insertRecoveryHistory(ctx, q, tx, out.ID, "created", "", 0, now); err != nil {
		return out, err
	}
	if out.State == "completed" {
		if err = enqueueRecoveryNotification(ctx, q, tx, out.ID, "completed"); err != nil {
			return out, err
		}
	}
	return out, tx.Commit(ctx)
}
func getEventRecoveryMetadata(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, accountID, jobID string) (api.EventRecoveryJob, error) {
	if err := eventRecoveryIDs(accountID, jobID); err != nil {
		return api.EventRecoveryJob{}, err
	}
	row, err := q.EventRecoveryGet(ctx, db, sqlc.EventRecoveryGetParams{AccountID: mustPgUUID(accountID), JobID: mustPgUUID(jobID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.EventRecoveryJob{}, ErrNotFound
	}
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	return eventRecoveryMetadata(row)
}
func eventRecoveryMetadata(row sqlc.EventRecoveryGetRow) (api.EventRecoveryJob, error) {
	out := api.EventRecoveryJob{ExecutionFinishedAt: timestamptzToTimePtr(row.ExecutionFinishedAt), RatePerSecond: int(row.RatePerSecond), PausedAt: timestamptzToTimePtr(row.PausedAt), ID: uuidString(row.ID), AppID: uuidString(row.AppID), Coverage: EventRecoveryCoverage, State: row.State, SelectedCount: row.SelectedCount, PendingCount: row.PendingCount, QueuedCount: row.QueuedCount, SkippedCount: row.SkippedCount, CancelledCount: row.CancelledCount, CreatedAt: timeFromPgtype(row.CreatedAt), UpdatedAt: timeFromPgtype(row.UpdatedAt), ExpiresAt: timeFromPgtype(row.ExpiresAt), CompletedAt: timestamptzToTimePtr(row.CompletedAt)}
	if err := json.Unmarshal(row.Selection, &out.Selection); err != nil {
		return api.EventRecoveryJob{}, err
	}
	out.Coverage = eventRecoveryCoverage(out.Selection)
	return out, nil
}
func cancelEventRecoveryTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, id string, now time.Time, reason string) error {
	if err := insertRecoveryCancellation(ctx, q, tx, id, reason, now); err != nil {
		return err
	}
	if err := q.EventRecoveryCancelItems(ctx, tx, sqlc.EventRecoveryCancelItemsParams{JobID: mustPgUUID(id), Reason: reason}); err != nil {
		return err
	}
	if err := q.EventRecoveryCancelJob(ctx, tx, sqlc.EventRecoveryCancelJobParams{JobID: mustPgUUID(id), NowAt: pgtypeFromTime(now)}); err != nil {
		return err
	}
	outcome := "cancelled"
	if reason == "expired" {
		outcome = "expired"
	}
	return enqueueRecoveryNotification(ctx, q, tx, id, outcome)
}
func (s *PgStore) CancelEventRecovery(ctx context.Context, accountID, jobID string) (api.EventRecoveryJob, error) {
	if err := eventRecoveryIDs(accountID, jobID); err != nil {
		return api.EventRecoveryJob{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.EventRecoveryLock(ctx, tx, sqlc.EventRecoveryLockParams{AccountID: mustPgUUID(accountID), JobID: mustPgUUID(jobID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.EventRecoveryJob{}, ErrNotFound
	}
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	if eventRecoveryActive(row.State) {
		if err = cancelEventRecoveryTx(ctx, q, tx, jobID, time.Now().UTC(), "cancelled"); err != nil {
			return api.EventRecoveryJob{}, err
		}
	}
	out, err := getEventRecovery(ctx, q, tx, accountID, jobID)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) ListEventRecoveryItems(ctx context.Context, accountID, jobID string, after int64, limit int) (api.EventRecoveryItems, error) {
	if err := eventRecoveryIDs(accountID, jobID); err != nil {
		return api.EventRecoveryItems{}, err
	}
	if after < 0 || limit < 1 || limit > api.EventRecoveryItemsPageMax {
		return api.EventRecoveryItems{}, ErrEventRecoveryQuery
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.EventRecoveryItems{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	job, err := getEventRecoveryMetadata(ctx, q, tx, accountID, jobID)
	if err != nil {
		return api.EventRecoveryItems{}, err
	}
	rows, err := q.EventRecoveryItems(ctx, tx, sqlc.EventRecoveryItemsParams{JobID: mustPgUUID(jobID), AfterPosition: after, PageLimit: int32(limit + 1)})
	if err != nil {
		return api.EventRecoveryItems{}, err
	}
	out := api.EventRecoveryItems{JobID: jobID, Items: []api.EventRecoveryItem{}}
	if len(rows) > limit {
		out.NextAfter = rows[limit-1].Position
		rows = rows[:limit]
	}
	observations := map[int64]api.EventRecoveryExecution{}
	if job.Selection.Mode == "execution" && len(rows) > 0 {
		observations, err = observeRecoveryExecutions(ctx, q, tx, accountID, jobID, after, rows[len(rows)-1].Position, time.Now().UTC())
		if err != nil {
			return out, err
		}
	}
	for _, row := range rows {
		item := recoveryStoredItem(row)
		if execution, ok := observations[row.Position]; ok {
			item.Execution = &execution
		}
		out.Items = append(out.Items, item)
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) ProcessNextEventRecovery(ctx context.Context, now time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	job, err := q.EventRecoveryNextJob(ctx, tx, pgtypeFromTime(now))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !timeFromPgtype(job.ExpiresAt).After(now) {
		if err = cancelEventRecoveryTx(ctx, q, tx, uuidString(job.ID), now, "expired"); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	item, err := q.EventRecoveryNextItem(ctx, tx, job.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = scheduleRecoveryNotification(ctx, q, tx, sqlc.EventRecoveryScheduleParams{JobID: job.ID, NowAt: pgtypeFromTime(now), NextAt: pgtypeFromTime(now)})
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	state, reason := "queued", ""
	var selection api.EventRecoveryRequest
	if err = json.Unmarshal(job.Selection, &selection); err != nil {
		return false, err
	}
	if selection.Mode == "execution" {
		state, reason, err = processEventExecutionRecoveryTx(ctx, q, tx, job, item, now)
		if err != nil {
			return false, err
		}
	} else {
		target, err := q.EventRecoveryTarget(ctx, tx, sqlc.EventRecoveryTargetParams{AccountID: job.AccountID, AppID: job.AppID, OutboxID: item.OutboxID, SubscriptionID: item.SubscriptionID, ExpectedProgress: item.ExpectedProgress})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			state, reason = "skipped", "receipt_expired"
		case err != nil:
			return false, err
		case !target.TargetAvailable:
			state, reason = "skipped", "target_unavailable"
		case !target.Unchanged:
			state, reason = "skipped", "changed"
		case !target.RecipientClaims && target.ReceiptState == "processing":
			// A legacy receipt claim owns all routing. Defer without consuming this item.
			if err = q.EventRecoveryWait(ctx, tx, sqlc.EventRecoveryWaitParams{JobID: job.ID, WaitReason: "legacy_claim", NowAt: pgtypeFromTime(now)}); err != nil {
				return false, err
			}
			if err = scheduleRecoveryNotification(ctx, q, tx, sqlc.EventRecoveryScheduleParams{JobID: job.ID, NowAt: pgtypeFromTime(now), NextAt: pgtypeFromTime(now.Add(time.Second))}); err != nil {
				return false, err
			}
			return true, tx.Commit(ctx)
		default:
			if err = replayEventRecipientTx(ctx, q, tx, target.ID, uuidString(job.AppID), item.SubscriptionID, target.Progress, target.RecipientClaims, now); errors.Is(err, ErrEventDeliveryExpired) {
				state, reason = "skipped", "expired"
			} else if err != nil {
				return false, err
			}
		}

	}
	itemReason := reason
	if state == "pending" {
		itemReason = ""
	}
	if err = q.EventRecoverySetItem(ctx, tx, sqlc.EventRecoverySetItemParams{JobID: job.ID, Position: item.Position, State: state, Reason: itemReason}); err != nil {
		return false, err
	}
	next := now
	if err = scheduleRecoveryNotification(ctx, q, tx, sqlc.EventRecoveryScheduleParams{JobID: job.ID, NowAt: pgtypeFromTime(now), NextAt: pgtypeFromTime(next)}); err != nil {
		return false, err
	}
	if err = q.EventRecoveryUsePermit(ctx, tx, sqlc.EventRecoveryUsePermitParams{JobID: job.ID, NowAt: pgtypeFromTime(now)}); err != nil {
		return false, err
	}
	if state != "pending" {
		if err = q.EventRecoveryProgress(ctx, tx, sqlc.EventRecoveryProgressParams{JobID: job.ID, NowAt: pgtypeFromTime(now)}); err != nil {
			return false, err
		}
	}
	if state == "pending" {
		if err = q.EventRecoveryWait(ctx, tx, sqlc.EventRecoveryWaitParams{JobID: job.ID, WaitReason: "capacity", CapacityScope: reason, NowAt: pgtypeFromTime(now)}); err != nil {
			return false, err
		}
		if err = scheduleRecoveryNotification(ctx, q, tx, sqlc.EventRecoveryScheduleParams{JobID: job.ID, NowAt: pgtypeFromTime(now), NextAt: pgtypeFromTime(now.Add(EventDeliveryCapacityRetryDelay))}); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}
func (s *PgStore) PruneEventRecoveries(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit < 1 || limit > api.EventRecoveryItemsPageMax {
		return 0, ErrEventRecoveryQuery
	}
	return sqlc.New().EventRecoveryPrune(ctx, s.pool, sqlc.EventRecoveryPruneParams{BeforeAt: pgtypeFromTime(now.Add(-api.EventRecoveryJobRetention)), PageLimit: int32(limit)})
}
