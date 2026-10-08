package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type WorkflowEventReplayBackfillQuery struct {
	AppID string
	api.WorkflowEventReplayBackfillRequest
}

func (s *PgStore) CreateWorkflowEventReplayBackfill(ctx context.Context, accountID string, query WorkflowEventReplayBackfillQuery) (api.EventReplayBackfillJobResponse, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return api.EventReplayBackfillJobResponse{}, ErrEventReplayBackfillQuery
	}
	if _, err := uuid.Parse(query.AppID); err != nil {
		return api.EventReplayBackfillJobResponse{}, ErrEventReplayBackfillQuery
	}
	if err := query.WorkflowEventReplayBackfillRequest.Validate(); err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("%w: %w", ErrEventReplayBackfillQuery, err)
	}
	query.AppID = canonicalMemUUID(query.AppID)
	query.From, query.Until = query.From.UTC(), query.Until.UTC()
	now := time.Now().UTC()
	cutoff := eventReplayBackfillCutoff(query.Until, now)
	from := eventReplayPreviewPgBoundary(query.From).Time
	if !from.Before(cutoff) {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("%w: from must precede the acceptance cutoff", ErrEventReplayBackfillQuery)
	}
	if cutoff.Sub(from) > api.EventReplayBackfillMaxRange {
		return api.EventReplayBackfillJobResponse{}, ErrEventReplayBackfillRange
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("begin workflow event replay backfill: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	q := sqlc.New()
	if err := q.EventReplayBackfillLockAccountRange(ctx, tx, mustPgUUID(accountID)); err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("lock workflow replay range: %w", err)
	}
	targets, err := q.EventReplayBackfillWorkflowTarget(ctx, tx, sqlc.EventReplayBackfillWorkflowTargetParams{
		AppID: mustPgUUID(query.AppID), AccountID: mustPgUUID(accountID), WorkflowName: query.WorkflowName,
	})
	if err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("read workflow replay target: %w", err)
	}
	if len(targets) == 0 {
		return api.EventReplayBackfillJobResponse{}, ErrNotFound
	}
	if len(targets) != 1 {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("%w: workflow name is not unique in the active deployment", ErrEventReplayBackfillQuery)
	}
	definitionBytes, err := workflowBackfillJSON(targets[0].Workflow)
	if err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("decode workflow replay definition: %w", err)
	}
	var definition api.WorkflowSpec
	if err := json.Unmarshal(definitionBytes, &definition); err != nil || definition.Name != query.WorkflowName || definition.Trigger == nil || definition.Trigger.Type != "event" ||
		(definition.Trigger.Enabled != nil && !*definition.Trigger.Enabled) {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("%w: current workflow is not an enabled event trigger", ErrEventReplayBackfillQuery)
	}
	plan := api.Plan(targets[0].Plan)
	if _, err := api.ValidateWorkflowDAG(definition, plan); err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("%w: validate current workflow definition: %v", ErrEventReplayBackfillQuery, err)
	}
	account := uuidFromPgtype(targets[0].AccountID).String()
	app := uuidFromPgtype(targets[0].AppID).String()
	filter := append(json.RawMessage(nil), definition.Trigger.Filter...)
	if len(filter) == 0 {
		filter = json.RawMessage(`{}`)
	}
	recipient := PublishedEventRecipient{
		ID: workflowEventRecipientID(app, definition.Name), AccountID: account, AppID: app,
		DeploymentID: uuidFromPgtype(targets[0].DeploymentID).String(), Source: definition.Trigger.Source,
		Type: definition.Trigger.EventType, Filter: filter, Workflow: definitionBytes, WorkSnapshotCaptured: true,
	}
	if err := (eventcontract.Subscription{ID: recipient.ID, AccountID: account, Source: recipient.Source, Type: recipient.Type, Filter: recipient.Filter}).Validate(); err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("%w: validate workflow event trigger: %v", ErrEventReplayBackfillQuery, err)
	}
	recipientBytes, err := json.Marshal(recipient)
	if err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("encode workflow replay target: %w", err)
	}
	revisionSum := sha256.Sum256(recipientBytes)
	revision := hex.EncodeToString(revisionSum[:])

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
	id, err := q.EventReplayBackfillCreate(ctx, tx, sqlc.EventReplayBackfillCreateParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(query.AppID), SubscriptionID: mustPgUUID(recipient.ID),
		SubscriptionRevision: revision, Recipient: recipientBytes, ConsumerKind: "workflow", WorkflowName: query.WorkflowName,
		FromAt: eventReplayPreviewPgBoundary(query.From), UntilAt: eventReplayPreviewPgBoundary(query.Until),
		CutoffAt: eventReplayPreviewPgBoundary(cutoff), EarliestRetainedAt: earliest, CursorAt: eventReplayPreviewPgBoundary(query.From),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return api.EventReplayBackfillJobResponse{}, ErrEventReplayBackfillQuota
		}
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("create workflow event replay backfill: %w", err)
	}
	job, err := getEventReplayBackfill(ctx, q, tx, accountID, uuidFromPgtype(id).String())
	if err != nil {
		return api.EventReplayBackfillJobResponse{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.EventReplayBackfillJobResponse{}, fmt.Errorf("commit workflow event replay backfill: %w", err)
	}
	return job, nil
}

func workflowBackfillJSON(value any) ([]byte, error) {
	switch raw := value.(type) {
	case []byte:
		return append([]byte(nil), raw...), nil
	case json.RawMessage:
		return append([]byte(nil), raw...), nil
	case string:
		return []byte(raw), nil
	default:
		return json.Marshal(value)
	}
}

func validateWorkflowEventReplayBackfillTarget(job sqlc.EventReplayJob, target PublishedEventRecipient) error {
	if job.ConsumerKind != "workflow" || job.WorkflowName == "" || target.ID != workflowEventRecipientID(uuidFromPgtype(job.AppID).String(), job.WorkflowName) ||
		!sameMemUUID(target.AppID, uuidFromPgtype(job.AppID).String()) || !sameMemUUID(target.AccountID, uuidFromPgtype(job.AccountID).String()) ||
		!target.WorkSnapshotCaptured || len(target.Workflow) == 0 || target.Work != nil || target.ObjectNotification != nil {
		return ErrConflict
	}
	var definition api.WorkflowSpec
	if err := json.Unmarshal(target.Workflow, &definition); err != nil || definition.Name != job.WorkflowName || definition.Trigger == nil || definition.Trigger.Type != "event" ||
		(definition.Trigger.Enabled != nil && !*definition.Trigger.Enabled) {
		return ErrConflict
	}
	return validateWorkflowEventReplayBackfillDefinition(definition, target)
}

func validateWorkflowEventReplayBackfillDefinition(definition api.WorkflowSpec, target PublishedEventRecipient) error {
	if _, err := api.ValidateWorkflowDAG(definition, api.PlanScale); err != nil {
		return ErrConflict
	}
	if definition.Trigger.Source != target.Source || definition.Trigger.EventType != target.Type ||
		!sameJSONDocument(definition.Trigger.Filter, target.Filter) {
		return ErrConflict
	}
	return (eventcontract.Subscription{ID: target.ID, AccountID: target.AccountID, Source: target.Source, Type: target.Type, Filter: target.Filter}).Validate()
}

func sameJSONDocument(left, right []byte) bool {
	if len(left) == 0 {
		left = []byte(`{}`)
	}
	if len(right) == 0 {
		right = []byte(`{}`)
	}
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	encodedA, errA := json.Marshal(a)
	encodedB, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(encodedA, encodedB)
}

func processWorkflowEventReplayBackfillPage(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, job sqlc.EventReplayJob, target PublishedEventRecipient, rows []sqlc.EventReplayBackfillCandidatesRow) (eventReplayBackfillPageProgress, error) {
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
		matched, err := matcher.Match(envelope)
		if err != nil {
			if err := insertBackfillFailure(ctx, q, tx, job, row, EventFanoutFailureCodeInvalidSubscription, "match retained event envelope"); err != nil {
				return progress, err
			}
			continue
		}
		if !matched {
			progress.filtered++
			if err := insertBackfillSkipped(ctx, q, tx, job, row, "filtered"); err != nil {
				return progress, err
			}
			continue
		}
		progress.matched++
		parent, err := q.EventReplayBackfillLockParent(ctx, tx, row.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			progress.unsettled++
			if err := insertBackfillSkipped(ctx, q, tx, job, row, "skipped_unsettled"); err != nil {
				return progress, err
			}
			continue
		} else if err != nil {
			return progress, err
		}
		if uuidFromPgtype(parent.AccountID).String() != uuidFromPgtype(job.AccountID).String() ||
			parent.Source != row.Source || parent.EventID != row.EventID || !parent.CreatedAt.Time.Equal(row.CreatedAt.Time) {
			return progress, ErrConflict
		}
		if parent.State != "delivered" {
			progress.unsettled++
			if err := insertBackfillSkipped(ctx, q, tx, job, row, "skipped_unsettled"); err != nil {
				return progress, err
			}
			continue
		}
		if parent.RecipientSnapshot == nil {
			progress.unknown++
			if err := insertBackfillSkipped(ctx, q, tx, job, row, "skipped_unknown"); err != nil {
				return progress, err
			}
			continue
		}
		var original []PublishedEventRecipient
		if err := json.Unmarshal(parent.RecipientSnapshot, &original); err != nil {
			if err := insertBackfillFailure(ctx, q, tx, job, row, EventFanoutFailureCodeInternal, "decode original recipient snapshot"); err != nil {
				return progress, err
			}
			continue
		}
		if workflowBackfillSnapshotContains(original, target) {
			progress.captured++
			if err := insertBackfillSkipped(ctx, q, tx, job, row, "skipped_captured"); err != nil {
				return progress, err
			}
			continue
		}
		if _, err := q.EventRoutingLockRecipient(ctx, tx, sqlc.EventRoutingLockRecipientParams{OutboxID: row.ID, SubscriptionID: target.ID}); err == nil {
			progress.existing++
			if err := insertBackfillSkipped(ctx, q, tx, job, row, "skipped_existing"); err != nil {
				return progress, err
			}
			continue
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return progress, err
		}
		_, created, err := admitEventWorkflowTx(ctx, q, tx, row.ID, target, row.Payload)
		if err != nil {
			if code, retryable, message, recordable := workflowBackfillAdmissionFailure(err); recordable {
				if _, insertErr := q.EventReplayBackfillInsertWorkflowFailure(ctx, tx, sqlc.EventReplayBackfillInsertWorkflowFailureParams{
					JobID: job.ID, OutboxID: row.ID, AcceptedAt: row.CreatedAt, EventSource: row.Source, EventID: row.EventID,
					EventType: row.EventType, SchemaVersion: row.SchemaVersion, FailureCode: code, LastError: message, Retryable: retryable,
				}); insertErr != nil {
					return progress, insertErr
				}
				continue
			}
			return progress, err
		}
		if !created {
			progress.existing++
			if err := insertBackfillSkipped(ctx, q, tx, job, row, "skipped_existing"); err != nil {
				return progress, err
			}
			continue
		}
		if _, err := q.EventReplayBackfillInsertItem(ctx, tx, sqlc.EventReplayBackfillInsertItemParams{
			JobID: job.ID, OutboxID: row.ID, AcceptedAt: row.CreatedAt, EventSource: row.Source, EventID: row.EventID,
			EventType: row.EventType, SchemaVersion: row.SchemaVersion, State: "enqueued", Attempts: 1,
		}); err != nil {
			return progress, err
		}
	}
	return progress, nil
}

func workflowBackfillAdmissionFailure(err error) (code string, retryable bool, message string, recordable bool) {
	switch {
	case errors.Is(err, ErrWorkflowRunQuotaExceeded):
		return EventFanoutFailureCodeInvocationEnqueueFailed, true, "workflow run quota is full", true
	case errors.Is(err, ErrWorkflowEventTargetUnavailable):
		return EventFanoutFailureCodeTargetUnavailable, true, "workflow target is temporarily unavailable", true
	case errors.Is(err, ErrNotFound):
		return EventFanoutFailureCodeTargetUnavailable, false, "workflow target is no longer available", true
	case errors.Is(err, ErrWorkflowEventDefinitionInvalid):
		return EventFanoutFailureCodeInvalidSubscription, false, "captured workflow definition is invalid", true
	default:
		return "", false, "", false
	}
}

func (s *PgStore) retryWorkflowEventReplayBackfill(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, accountID, jobID string, job api.EventReplayBackfillJobResponse, limit int) (api.EventReplayBackfillRetryResponse, error) {
	if _, err := q.EventReplayBackfillWorkflowExpireRetry(ctx, tx, sqlc.EventReplayBackfillWorkflowExpireRetryParams{
		JobID: mustPgUUID(jobID), AccountID: mustPgUUID(accountID),
	}); err != nil {
		return api.EventReplayBackfillRetryResponse{}, err
	}
	jobSnapshot, err := q.EventReplayBackfillTargetSnapshot(ctx, tx, sqlc.EventReplayBackfillTargetSnapshotParams{JobID: mustPgUUID(jobID), AccountID: mustPgUUID(accountID)})
	if err != nil {
		return api.EventReplayBackfillRetryResponse{}, err
	}
	var target PublishedEventRecipient
	if err := json.Unmarshal(jobSnapshot.Recipient, &target); err != nil {
		return api.EventReplayBackfillRetryResponse{}, ErrConflict
	}
	validatedJob := sqlc.EventReplayJob{
		ConsumerKind: jobSnapshot.ConsumerKind, WorkflowName: jobSnapshot.WorkflowName,
		AppID: jobSnapshot.AppID, AccountID: jobSnapshot.AccountID,
	}
	if job.ConsumerKind != "workflow" || job.WorkflowName != jobSnapshot.WorkflowName || validateWorkflowEventReplayBackfillTarget(validatedJob, target) != nil {
		return api.EventReplayBackfillRetryResponse{}, ErrConflict
	}
	rows, err := q.EventReplayBackfillWorkflowRetryCandidates(ctx, tx, sqlc.EventReplayBackfillWorkflowRetryCandidatesParams{
		JobID: mustPgUUID(jobID), AccountID: mustPgUUID(accountID), PageLimit: int32(limit),
	})
	if err != nil {
		return api.EventReplayBackfillRetryResponse{}, err
	}
	var retried int64
	for _, row := range rows {
		parent, err := q.EventReplayBackfillLockParent(ctx, tx, row.OutboxID)
		if errors.Is(err, pgx.ErrNoRows) {
			updated, updateErr := q.EventReplayBackfillWorkflowFinishRetry(ctx, tx, sqlc.EventReplayBackfillWorkflowFinishRetryParams{
				State: "failed", FailureCode: EventFanoutFailureCodeTargetUnavailable,
				LastError: "source event is no longer retained", Retryable: false,
				JobID: mustPgUUID(jobID), OutboxID: row.OutboxID,
			})
			if updateErr != nil {
				return api.EventReplayBackfillRetryResponse{}, updateErr
			}
			if updated != 1 {
				return api.EventReplayBackfillRetryResponse{}, ErrConflict
			}
			continue
		}
		if err != nil {
			return api.EventReplayBackfillRetryResponse{}, err
		}
		if uuidFromPgtype(parent.AccountID).String() != uuidFromPgtype(jobSnapshot.AccountID).String() ||
			parent.Source != row.EventSource || parent.EventID != row.EventID || !parent.CreatedAt.Time.Equal(row.AcceptedAt.Time) {
			return api.EventReplayBackfillRetryResponse{}, ErrConflict
		}
		state, failureCode, lastError, retryable := "", "", "", false
		if parent.State != "delivered" {
			state = "skipped_unsettled"
		} else if parent.RecipientSnapshot == nil {
			state = "skipped_unknown"
		} else {
			var snapshot []PublishedEventRecipient
			if err := json.Unmarshal(parent.RecipientSnapshot, &snapshot); err != nil {
				state, failureCode, lastError = "failed", EventFanoutFailureCodeInternal, "decode original recipient snapshot"
			} else if workflowBackfillSnapshotContains(snapshot, target) {
				state = "skipped_captured"
			} else {
				var envelope eventcontract.Envelope
				if err := json.Unmarshal(row.Payload, &envelope); err != nil {
					state, failureCode, lastError = "failed", EventFanoutFailureCodeInternal, "decode retained event envelope"
				} else if envelope, err = envelope.Normalize(target.AccountID, timeFromPgtype(row.AcceptedAt)); err != nil {
					state, failureCode, lastError = "failed", EventFanoutFailureCodeInternal, "normalize retained event envelope"
				} else if matched, matchErr := (eventcontract.Subscription{ID: target.ID, AccountID: target.AccountID, Source: target.Source, Type: target.Type, Filter: target.Filter}).Match(envelope); matchErr != nil {
					state, failureCode, lastError = "failed", EventFanoutFailureCodeInvalidSubscription, "match retained event envelope"
				} else if !matched {
					state = "filtered"
				} else {
					prior, priorErr := q.GetEventWorkflowReceipt(ctx, tx, sqlc.GetEventWorkflowReceiptParams{OutboxID: row.OutboxID, RecipientID: mustPgUUID(target.ID)})
					if priorErr == nil {
						_ = prior
						state = "skipped_existing"
					} else if !errors.Is(priorErr, pgx.ErrNoRows) {
						return api.EventReplayBackfillRetryResponse{}, priorErr
					} else {
						_, created, admissionErr := admitEventWorkflowTx(ctx, q, tx, row.OutboxID, target, row.Payload)
						if admissionErr != nil {
							code, canRetry, safeMessage, recordable := workflowBackfillAdmissionFailure(admissionErr)
							if !recordable {
								return api.EventReplayBackfillRetryResponse{}, admissionErr
							}
							state, failureCode, lastError, retryable = "failed", code, safeMessage, canRetry
						} else if created {
							state = "enqueued"
						} else {
							state = "skipped_existing"
						}
					}
				}
			}
		}
		updated, err := q.EventReplayBackfillWorkflowFinishRetry(ctx, tx, sqlc.EventReplayBackfillWorkflowFinishRetryParams{
			State: state, FailureCode: failureCode, LastError: lastError, Retryable: retryable, JobID: mustPgUUID(jobID), OutboxID: row.OutboxID,
		})
		if err != nil {
			return api.EventReplayBackfillRetryResponse{}, err
		}
		if updated != 1 {
			return api.EventReplayBackfillRetryResponse{}, ErrConflict
		}
		retried++
	}
	if err := q.EventReplayBackfillFinalize(ctx, tx, mustPgUUID(jobID)); err != nil {
		return api.EventReplayBackfillRetryResponse{}, err
	}
	remaining, err := q.EventReplayBackfillWorkflowRetryableCount(ctx, tx, sqlc.EventReplayBackfillWorkflowRetryableCountParams{
		JobID: mustPgUUID(jobID), AccountID: mustPgUUID(accountID),
	})
	if err != nil {
		return api.EventReplayBackfillRetryResponse{}, err
	}
	updatedJob, err := getEventReplayBackfill(ctx, q, tx, accountID, jobID)
	if err != nil {
		return api.EventReplayBackfillRetryResponse{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.EventReplayBackfillRetryResponse{}, err
	}
	return api.EventReplayBackfillRetryResponse{RetriedCount: retried, RemainingRetryableCount: remaining, Job: updatedJob}, nil
}

func workflowBackfillSnapshotContains(snapshot []PublishedEventRecipient, target PublishedEventRecipient) bool {
	for _, recipient := range snapshot {
		if recipient.ID == target.ID && sameMemUUID(recipient.AppID, target.AppID) {
			return true
		}
	}
	return false
}
