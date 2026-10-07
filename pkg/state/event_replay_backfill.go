package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventReplayBackfillStore interface {
	CreateEventReplayBackfill(context.Context, string, EventReplayBackfillQuery) (api.EventReplayBackfillJobResponse, error)
	GetEventReplayBackfill(context.Context, string, string) (api.EventReplayBackfillJobResponse, error)
	ListEventReplayBackfillItems(context.Context, string, string, api.EventReplayBackfillItemsQuery) (api.EventReplayBackfillItemsResponse, error)
	RetryFailedEventReplayBackfill(context.Context, string, string, int) (api.EventReplayBackfillRetryResponse, error)
	ProcessNextEventReplayBackfill(context.Context, time.Time) (bool, error)
	PruneEventReplayBackfills(context.Context, time.Time, int) (int64, error)
}

type EventReplayBackfillQuery struct {
	AppID          string
	SubscriptionID string
	api.EventReplayBackfillRequest
}

var (
	ErrEventReplayBackfillQuery       = errors.New("invalid event replay backfill request")
	ErrEventReplayBackfillDisabled    = errors.New("event replay backfill requires an enabled subscription")
	ErrEventReplayBackfillUnsupported = errors.New("event replay backfill does not support work-bound subscriptions")
	ErrEventReplayBackfillRange       = errors.New("event replay backfill range exceeds retained receipt retention")
	ErrEventReplayBackfillQuota       = errors.New("event replay backfill active-job limit reached")
	ErrEventReplayBackfillState       = errors.New("event replay backfill cannot retry in its current state")
)

type EventReplayBackfillClaim struct {
	JobID          string
	OutboxID       int64
	SubscriptionID string
}

const EventReplayBackfillRetryMax = api.EventReplayBackfillRetryMax

func validateEventReplayBackfillQuery(accountID string, query *EventReplayBackfillQuery, now time.Time) error {
	for _, id := range []string{accountID, query.AppID, query.SubscriptionID} {
		if _, err := uuid.Parse(id); err != nil {
			return fmt.Errorf("%w: account, app and subscription identifiers must be UUIDs", ErrEventReplayBackfillQuery)
		}
	}
	query.AppID, query.SubscriptionID = canonicalMemUUID(query.AppID), canonicalMemUUID(query.SubscriptionID)
	query.From, query.Until = query.From.UTC(), query.Until.UTC()
	if err := query.EventReplayBackfillRequest.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrEventReplayBackfillQuery, err)
	}
	cutoff := eventReplayBackfillCutoff(query.Until, now.UTC())
	fromBoundary := eventReplayPreviewPgBoundary(query.From).Time
	if !fromBoundary.Before(cutoff) {
		return fmt.Errorf("%w: from must precede the acceptance cutoff", ErrEventReplayBackfillQuery)
	}
	if cutoff.Sub(fromBoundary) > api.EventReplayBackfillMaxRange {
		return ErrEventReplayBackfillRange
	}
	return nil
}

func eventReplayBackfillCutoff(until, now time.Time) time.Time {
	if until.Before(now) {
		return eventReplayPreviewPgBoundary(until).Time
	}
	return now.UTC().Truncate(time.Microsecond)
}

func (s *PgStore) CreateEventReplayBackfill(ctx context.Context, accountID string, query EventReplayBackfillQuery) (api.EventReplayBackfillJobResponse, error) {
	now := time.Now().UTC()
	if err := validateEventReplayBackfillQuery(accountID, &query, now); err != nil {
		return api.EventReplayBackfillJobResponse{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("begin event replay backfill: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	q := sqlc.New()
	if err := q.EventReplayBackfillLockAccountRange(ctx, tx, mustPgUUID(accountID)); err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("lock event replay range: %w", err)
	}
	row, err := q.EventReplayPreviewTarget(ctx, tx, sqlc.EventReplayPreviewTargetParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(query.AppID), SubscriptionID: mustPgUUID(query.SubscriptionID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.EventReplayBackfillJobResponse{}, ErrNotFound
	}
	if err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("read event replay target: %w", err)
	}
	sub := EventSubscription{ID: uuidFromPgtype(row.ID).String(), AccountID: uuidFromPgtype(row.AccountID).String(), AppID: uuidFromPgtype(row.AppID).String(), Source: row.Source, Type: row.Type, Filter: row.Filter, Enabled: row.Enabled, CreatedAt: timeFromPgtype(row.CreatedAt), UpdatedAt: timeFromPgtype(row.UpdatedAt)}
	if !sub.Enabled {
		return api.EventReplayBackfillJobResponse{}, ErrEventReplayBackfillDisabled
	}
	if row.WorkBound {
		return api.EventReplayBackfillJobResponse{}, ErrEventReplayBackfillUnsupported
	}
	if err := (eventcontract.Subscription{ID: sub.ID, AccountID: sub.AccountID, Source: sub.Source, Type: sub.Type, Filter: sub.Filter}).Validate(); err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("validate replay target: %w", err)
	}
	active, err := q.EventReplayBackfillActiveJobCount(ctx, tx, mustPgUUID(accountID))
	if err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("count active event replay jobs: %w", err)
	}
	if active >= api.EventReplayBackfillActiveJobsMax {
		return api.EventReplayBackfillJobResponse{}, ErrEventReplayBackfillQuota
	}
	earliestRow, err := q.EventReplayPreviewEarliestRetained(ctx, tx, mustPgUUID(accountID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("read earliest retained event: %w", err)
	}
	var earliest pgtype.Timestamptz
	if earliestRow.Valid {
		earliest = earliestRow
	}
	cutoff := eventReplayBackfillCutoff(query.Until, now)
	recipient, err := json.Marshal(PublishedEventRecipient{ID: sub.ID, AccountID: sub.AccountID, AppID: sub.AppID, Source: sub.Source, Type: sub.Type, Filter: sub.Filter, WorkSnapshotCaptured: true})
	if err != nil {
		return api.EventReplayBackfillJobResponse{}, err
	}
	id, err := q.EventReplayBackfillCreate(ctx, tx, sqlc.EventReplayBackfillCreateParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(query.AppID), SubscriptionID: mustPgUUID(query.SubscriptionID),
		SubscriptionRevision: eventReplayPreviewRevision(sub), Recipient: recipient,
		FromAt: eventReplayPreviewPgBoundary(query.From), UntilAt: eventReplayPreviewPgBoundary(query.Until),
		CutoffAt: eventReplayPreviewPgBoundary(cutoff), EarliestRetainedAt: earliest, CursorAt: eventReplayPreviewPgBoundary(query.From),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return api.EventReplayBackfillJobResponse{}, ErrEventReplayBackfillQuota
		}
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("create event replay backfill: %w", err)
	}
	result, err := getEventReplayBackfill(ctx, q, tx, accountID, uuidFromPgtype(id).String())
	if err != nil {
		return api.EventReplayBackfillJobResponse{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("commit event replay backfill: %w", err)
	}
	return result, nil
}

func (s *PgStore) GetEventReplayBackfill(ctx context.Context, accountID, jobID string) (api.EventReplayBackfillJobResponse, error) {
	return getEventReplayBackfill(ctx, sqlc.New(), s.pool, accountID, jobID)
}

func getEventReplayBackfill(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, accountID, jobID string) (api.EventReplayBackfillJobResponse, error) {
	if _, err := uuid.Parse(jobID); err != nil {
		return api.EventReplayBackfillJobResponse{}, ErrEventReplayBackfillQuery
	}
	row, err := q.EventReplayBackfillGet(ctx, db, sqlc.EventReplayBackfillGetParams{ID: mustPgUUID(jobID), AccountID: mustPgUUID(accountID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.EventReplayBackfillJobResponse{}, ErrNotFound
	}
	if err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("read event replay backfill: %w", err)
	}
	response := api.EventReplayBackfillJobResponse{
		ID: uuidFromPgtype(row.ID).String(), AppSlug: row.AppSlug, SubscriptionID: uuidFromPgtype(row.SubscriptionID).String(),
		SubscriptionRevision: row.SubscriptionRevision, From: timeFromPgtype(row.FromAt), Until: timeFromPgtype(row.UntilAt),
		CutoffAt: timeFromPgtype(row.CutoffAt), HistoryComplete: false, DuplicatePolicy: row.DuplicatePolicy,
		State: row.State, ScanComplete: row.ScanComplete, CreatedAt: timeFromPgtype(row.CreatedAt), UpdatedAt: timeFromPgtype(row.UpdatedAt),
		Progress: api.EventReplayBackfillProgress{Scanned: row.ScannedCount, Matched: row.MatchedCount, Filtered: row.FilteredCount,
			Pending: row.PendingCount, Processing: row.ProcessingCount, Enqueued: row.EnqueuedCount, Failed: row.FailedCount, RetryableFailed: row.RetryableFailedCount,
			SkippedCaptured: row.SkippedCapturedCount, SkippedUnknown: row.SkippedUnknownCount,
			SkippedExisting: row.SkippedExistingCount, SkippedUnsettled: row.SkippedUnsettledCount},
	}
	if row.EarliestRetainedAt.Valid {
		t := timeFromPgtype(row.EarliestRetainedAt)
		response.EarliestRetainedAt = &t
	}
	if row.CompletedAt.Valid {
		t := timeFromPgtype(row.CompletedAt)
		response.CompletedAt = &t
	}
	return response, nil
}

func (s *PgStore) ProcessNextEventReplayBackfill(ctx context.Context, now time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	q := sqlc.New()
	job, err := q.EventReplayBackfillNextJob(ctx, tx, api.EventReplayBackfillInFlightMax)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim event replay backfill scan: %w", err)
	}
	var target PublishedEventRecipient
	if err := json.Unmarshal(job.Recipient, &target); err != nil {
		return false, fmt.Errorf("decode event replay target snapshot: %w", err)
	}
	if err := validateEventReplayBackfillTarget(job, target); err != nil {
		return false, fmt.Errorf("validate event replay target snapshot: %w", err)
	}
	inFlight, err := q.EventReplayBackfillInFlightCount(ctx, tx, job.ID)
	if err != nil {
		return false, err
	}
	limit := int64(api.EventReplayBackfillInFlightMax) - inFlight
	if limit > api.EventReplayBackfillPageSize {
		limit = api.EventReplayBackfillPageSize
	}
	rows, err := q.EventReplayBackfillCandidates(ctx, tx, sqlc.EventReplayBackfillCandidatesParams{
		SubscriptionID: target.ID, AppID: target.AppID, AccountID: job.AccountID,
		FromAt: job.FromAt, CutoffAt: job.CutoffAt, CursorAt: job.CursorAt,
		CursorOutboxID: job.CursorOutboxID, PageLimit: int32(limit),
	})
	if err != nil {
		return false, fmt.Errorf("read event replay candidates: %w", err)
	}
	if len(rows) == 0 {
		if err := q.EventReplayBackfillMarkScanned(ctx, tx, job.ID); err != nil {
			return false, err
		}
		if err := q.EventReplayBackfillFinalize(ctx, tx, job.ID); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	progress, err := processEventReplayBackfillPage(ctx, q, tx, job, target, rows, now.UTC())
	if err != nil {
		return false, err
	}
	last := rows[len(rows)-1]
	if err := q.EventReplayBackfillAdvance(ctx, tx, sqlc.EventReplayBackfillAdvanceParams{
		JobID: job.ID, CursorAt: last.CreatedAt, CursorOutboxID: last.ID,
		ScannedDelta: int64(len(rows)), MatchedDelta: progress.matched, FilteredDelta: progress.filtered,
		SkippedCapturedDelta: progress.captured, SkippedUnknownDelta: progress.unknown,
		SkippedExistingDelta: progress.existing, SkippedUnsettledDelta: progress.unsettled,
	}); err != nil {
		return false, err
	}
	if err := q.EventReplayBackfillFinalize(ctx, tx, job.ID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func validateEventReplayBackfillTarget(job sqlc.EventReplayJob, target PublishedEventRecipient) error {
	if !sameMemUUID(target.ID, uuidFromPgtype(job.SubscriptionID).String()) ||
		!sameMemUUID(target.AppID, uuidFromPgtype(job.AppID).String()) ||
		!sameMemUUID(target.AccountID, uuidFromPgtype(job.AccountID).String()) ||
		!target.WorkSnapshotCaptured || target.Work != nil || target.ObjectNotification != nil || len(target.Workflow) != 0 {
		return ErrConflict
	}
	return (eventcontract.Subscription{ID: target.ID, AccountID: target.AccountID, Source: target.Source, Type: target.Type, Filter: target.Filter}).Validate()
}

type eventReplayBackfillPageProgress struct {
	matched, filtered, captured, unknown, existing, unsettled int64
}

func processEventReplayBackfillPage(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, job sqlc.EventReplayJob, target PublishedEventRecipient, rows []sqlc.EventReplayBackfillCandidatesRow, now time.Time) (eventReplayBackfillPageProgress, error) {
	var progress eventReplayBackfillPageProgress
	matcher := eventcontract.Subscription{ID: target.ID, AccountID: target.AccountID, Source: target.Source, Type: target.Type, Filter: target.Filter}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return progress, err
		}
		var envelope eventcontract.Envelope
		if err := json.Unmarshal(row.Payload, &envelope); err != nil {
			if err := insertBackfillFailure(ctx, q, tx, job, row, EventFanoutFailureCodeInternal, "decode retained event envelope"); err != nil {
				return progress, err
			}
			continue
		}
		envelope, err := envelope.Normalize(target.AccountID, timeFromPgtype(row.CreatedAt))
		if err != nil {
			if err := insertBackfillFailure(ctx, q, tx, job, row, EventFanoutFailureCodeInternal, "normalize retained event envelope"); err != nil {
				return progress, err
			}
			continue
		}
		reason, err := matcher.ExplainMatch(envelope)
		if err != nil {
			if err := insertBackfillFailure(ctx, q, tx, job, row, EventFanoutFailureCodeInvalidSubscription, "match retained event envelope"); err != nil {
				return progress, err
			}
			continue
		}
		if reason != eventcontract.MatchReasonWouldDeliver {
			progress.filtered++
			if err := insertBackfillSkipped(ctx, q, tx, job, row, "filtered"); err != nil {
				return progress, err
			}
			continue
		}
		progress.matched++
		switch row.OriginalRecipient {
		case "captured":
			progress.captured++
			if err := insertBackfillSkipped(ctx, q, tx, job, row, "skipped_captured"); err != nil {
				return progress, err
			}
			continue
		case "unknown":
			progress.unknown++
			if err := insertBackfillSkipped(ctx, q, tx, job, row, "skipped_unknown"); err != nil {
				return progress, err
			}
			continue
		}
		if row.State != "delivered" {
			progress.unsettled++
			if err := insertBackfillSkipped(ctx, q, tx, job, row, "skipped_unsettled"); err != nil {
				return progress, err
			}
			continue
		}
		if row.RecipientExists {
			progress.existing++
			if err := insertBackfillSkipped(ctx, q, tx, job, row, "skipped_existing"); err != nil {
				return progress, err
			}
			continue
		}
		var original []PublishedEventRecipient
		if err := json.Unmarshal(row.RecipientSnapshot, &original); err != nil || !eventReplayBackfillSnapshotSupported(original) {
			progress.unsettled++
			if err := insertBackfillSkipped(ctx, q, tx, job, row, "skipped_unsettled"); err != nil {
				return progress, err
			}
			continue
		}
		if err := stageEventReplayBackfillRecipient(ctx, q, tx, job, target, row, now); err != nil {
			if errors.Is(err, ErrConflict) {
				progress.existing++
				if err := insertBackfillSkipped(ctx, q, tx, job, row, "skipped_existing"); err != nil {
					return progress, err
				}
				continue
			}
			return progress, err
		}
	}
	return progress, nil
}

func eventReplayBackfillSnapshotSupported(recipients []PublishedEventRecipient) bool {
	for _, recipient := range recipients {
		if len(recipient.Workflow) != 0 {
			return false
		}
	}
	return true
}

func stageEventReplayBackfillRecipient(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, job sqlc.EventReplayJob, target PublishedEventRecipient, row sqlc.EventReplayBackfillCandidatesRow, now time.Time) error {
	parent, err := q.EventReplayBackfillLockParent(ctx, tx, row.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if parent.State != "delivered" || parent.RecipientSnapshot == nil {
		return ErrConflict
	}
	if !sameMemUUID(uuidFromPgtype(parent.AccountID).String(), uuidFromPgtype(job.AccountID).String()) {
		return ErrConflict
	}
	exists, err := q.EventRoutingLockRecipient(ctx, tx, sqlc.EventRoutingLockRecipientParams{OutboxID: row.ID, SubscriptionID: target.ID})
	if err == nil {
		_ = exists
		return ErrConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if !parent.RecipientClaims {
		n, err := q.EventReplayBackfillAdoptReceipt(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
	}
	if err := q.EventReplayBackfillMaterializeSnapshot(ctx, tx, row.ID); err != nil {
		return err
	}
	n, err := q.EventReplayBackfillInsertItem(ctx, tx, sqlc.EventReplayBackfillInsertItemParams{
		JobID: job.ID, OutboxID: row.ID, AcceptedAt: row.CreatedAt,
		EventSource: row.Source, EventID: row.EventID, EventType: row.EventType, SchemaVersion: row.SchemaVersion,
		State: "pending",
	})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	recipient, err := json.Marshal(target)
	if err != nil {
		return err
	}
	inserted, err := q.EventReplayBackfillInsertRecipient(ctx, tx, sqlc.EventReplayBackfillInsertRecipientParams{
		OutboxID: row.ID, SubscriptionID: target.ID, AppID: mustPgUUID(target.AppID), Recipient: recipient,
		AvailableAt: pgtypeFromTime(now), JobID: job.ID,
	})
	if err != nil {
		return err
	}
	if inserted != 1 {
		return ErrConflict
	}
	return q.EventRecipientSettleReceipt(ctx, tx, row.ID)
}

func insertBackfillSkipped(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, job sqlc.EventReplayJob, row sqlc.EventReplayBackfillCandidatesRow, state string) error {
	_, err := q.EventReplayBackfillInsertItem(ctx, tx, sqlc.EventReplayBackfillInsertItemParams{
		JobID: job.ID, OutboxID: row.ID, AcceptedAt: row.CreatedAt,
		EventSource: row.Source, EventID: row.EventID, EventType: row.EventType, SchemaVersion: row.SchemaVersion, State: state,
	})
	return err
}

func insertBackfillFailure(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, job sqlc.EventReplayJob, row sqlc.EventReplayBackfillCandidatesRow, code, message string) error {
	_, err := q.EventReplayBackfillInsertItem(ctx, tx, sqlc.EventReplayBackfillInsertItemParams{
		JobID: job.ID, OutboxID: row.ID, AcceptedAt: row.CreatedAt,
		EventSource: row.Source, EventID: row.EventID, EventType: row.EventType, SchemaVersion: row.SchemaVersion, State: PublishedEventRecipientFailed,
		FailureCode: code, LastError: message,
	})
	return err
}

func (s *PgStore) RetryFailedEventReplayBackfill(ctx context.Context, accountID, jobID string, limit int) (api.EventReplayBackfillRetryResponse, error) {
	if limit == 0 {
		limit = EventReplayBackfillRetryMax
	}
	if limit < 1 || limit > EventReplayBackfillRetryMax {
		return api.EventReplayBackfillRetryResponse{}, ErrEventReplayBackfillQuery
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.EventReplayBackfillRetryResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	q := sqlc.New()
	if _, err := q.EventReplayBackfillLockAnyJob(ctx, tx, sqlc.EventReplayBackfillLockAnyJobParams{ID: mustPgUUID(jobID), AccountID: mustPgUUID(accountID)}); errors.Is(err, pgx.ErrNoRows) {
		return api.EventReplayBackfillRetryResponse{}, ErrNotFound
	} else if err != nil {
		return api.EventReplayBackfillRetryResponse{}, err
	}
	job, err := getEventReplayBackfill(ctx, q, tx, accountID, jobID)
	if err != nil {
		return api.EventReplayBackfillRetryResponse{}, err
	}
	if job.State != "completed_with_failures" {
		return api.EventReplayBackfillRetryResponse{}, ErrEventReplayBackfillState
	}
	rows, err := q.EventReplayBackfillRetryCandidates(ctx, tx, sqlc.EventReplayBackfillRetryCandidatesParams{
		JobID: mustPgUUID(jobID), AccountID: mustPgUUID(accountID), PageLimit: int32(limit),
	})
	if err != nil {
		return api.EventReplayBackfillRetryResponse{}, err
	}
	now := time.Now().UTC()
	var retried int64
	for _, row := range rows {
		if _, err := q.EventRoutingLockReceipt(ctx, tx, row.OutboxID); err != nil {
			return api.EventReplayBackfillRetryResponse{}, err
		}
		target, err := decodeBackfillTarget(job, row.Recipient)
		if err != nil {
			return api.EventReplayBackfillRetryResponse{}, err
		}
		changed, err := q.EventRecipientReplay(ctx, tx, sqlc.EventRecipientReplayParams{
			NowAt: pgtypeFromTime(now), OutboxID: row.OutboxID, SubscriptionID: target.ID,
		})
		if err != nil {
			return api.EventReplayBackfillRetryResponse{}, err
		}
		if changed == 0 {
			continue
		}
		reset, err := q.EventReplayBackfillResetItem(ctx, tx, sqlc.EventReplayBackfillResetItemParams{JobID: mustPgUUID(jobID), OutboxID: row.OutboxID})
		if err != nil {
			return api.EventReplayBackfillRetryResponse{}, err
		}
		if reset == 0 {
			return api.EventReplayBackfillRetryResponse{}, ErrConflict
		}
		progress := PublishedEventRecipientProgress{State: PublishedEventRecipientPending, Attempts: int(row.TotalAttempts), CapacityDeferrals: int(row.CapacityDeferrals), UpdatedAt: now}
		encoded, _ := json.Marshal(progress)
		if err := q.EventRecipientUpdateProgress(ctx, tx, sqlc.EventRecipientUpdateProgressParams{ID: row.OutboxID, SubscriptionID: target.ID, Progress: encoded}); err != nil {
			return api.EventReplayBackfillRetryResponse{}, err
		}
		if err := appendEventRecipientHistory(ctx, q, tx, row.OutboxID, target.AppID, target.ID, EventFanoutAttemptActionBackfill, progress); err != nil {
			return api.EventReplayBackfillRetryResponse{}, err
		}
		retried++
	}
	if retried > 0 {
		if err := q.EventReplayBackfillLockAccountRange(ctx, tx, mustPgUUID(accountID)); err != nil {
			return api.EventReplayBackfillRetryResponse{}, err
		}
		active, err := q.EventReplayBackfillActiveJobCount(ctx, tx, mustPgUUID(accountID))
		if err != nil {
			return api.EventReplayBackfillRetryResponse{}, err
		}
		if active >= api.EventReplayBackfillActiveJobsMax {
			return api.EventReplayBackfillRetryResponse{}, ErrEventReplayBackfillQuota
		}
		if err := q.EventReplayBackfillSetRunning(ctx, tx, mustPgUUID(jobID)); isUniqueViolation(err) {
			return api.EventReplayBackfillRetryResponse{}, ErrEventReplayBackfillQuota
		} else if err != nil {
			return api.EventReplayBackfillRetryResponse{}, err
		}
	}
	remaining, err := q.EventReplayBackfillCountRetryableFailed(ctx, tx, mustPgUUID(jobID))
	if err != nil {
		return api.EventReplayBackfillRetryResponse{}, err
	}
	updated, err := getEventReplayBackfill(ctx, q, tx, accountID, jobID)
	if err != nil {
		return api.EventReplayBackfillRetryResponse{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.EventReplayBackfillRetryResponse{}, err
	}
	return api.EventReplayBackfillRetryResponse{RetriedCount: retried, RemainingRetryableCount: remaining, Job: updated}, nil
}

func decodeBackfillTarget(job api.EventReplayBackfillJobResponse, encoded []byte) (PublishedEventRecipient, error) {
	var target PublishedEventRecipient
	if err := json.Unmarshal(encoded, &target); err != nil {
		return target, err
	}
	if strings.TrimSpace(target.ID) == "" || target.ID != job.SubscriptionID {
		return target, ErrConflict
	}
	return target, nil
}

func (s *PgStore) PruneEventReplayBackfills(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	return sqlc.New().EventReplayBackfillPruneJobs(ctx, s.pool, sqlc.EventReplayBackfillPruneJobsParams{
		CutoffAt: pgtypeFromTime(now.Add(-api.EventReplayBackfillJobRetention)), PageLimit: int32(min(limit, api.EventReplayBackfillPruneBatch)),
	})
}
