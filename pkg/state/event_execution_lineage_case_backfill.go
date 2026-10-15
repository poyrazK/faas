package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventExecutionLineageCaseBackfillStore interface {
	GetEventExecutionLineageCaseBackfill(context.Context, string, string, string) (api.EventExecutionLineageCaseBackfill, error)
	RequestEventExecutionLineageCaseBackfill(context.Context, string, string, string, api.RequestEventExecutionLineageCaseBackfillRequest) (api.EventExecutionLineageCaseBackfill, error)
	ControlEventExecutionLineageCaseBackfill(context.Context, string, string, string, string, string, api.ControlEventExecutionLineageCaseBackfillRequest) (api.EventExecutionLineageCaseBackfill, error)
	AdvanceEventExecutionLineageCaseBackfills(context.Context, int) (int, error)
}

func cloneLineageCaseBackfill(job api.EventExecutionLineageCaseBackfill) api.EventExecutionLineageCaseBackfill {
	job.CreatedAt = cloneEventReceiptTime(job.CreatedAt)
	job.UpdatedAt = cloneEventReceiptTime(job.UpdatedAt)
	job.RetainUntil = cloneEventReceiptTime(job.RetainUntil)
	return job
}
func activeLineageCaseBackfill(status string) bool {
	return runnableLineageCaseBackfill(status) || status == "paused"
}
func runnableLineageCaseBackfill(status string) bool {
	return status == "pending" || status == "waiting_for_retry_jobs"
}
func lineageCaseBackfillView(app, sub string, job *api.EventExecutionLineageCaseBackfill) api.EventExecutionLineageCaseBackfill {
	if job == nil {
		return api.EventExecutionLineageCaseBackfill{AppID: canonicalMemUUID(app), SubscriptionID: canonicalMemUUID(sub), Status: "not_requested"}
	}
	out := cloneLineageCaseBackfill(*job)
	if activeLineageCaseBackfill(out.Status) && out.RetainUntil != nil && !out.RetainUntil.After(time.Now()) {
		out.Status = "expired"
	}
	return out
}
func normalizeLineageCaseBackfillRequest(req api.RequestEventExecutionLineageCaseBackfillRequest) (api.RequestEventExecutionLineageCaseBackfillRequest, error) {
	id, err := uuid.Parse(req.RequestID)
	if err != nil || id == uuid.Nil {
		return req, ErrInvalidArgument
	}
	req.RequestID = id.String()
	if req.ExpectedPreviousRequestID != "" {
		id, err = uuid.Parse(req.ExpectedPreviousRequestID)
		if err != nil || id == uuid.Nil {
			return req, ErrInvalidArgument
		}
		req.ExpectedPreviousRequestID = id.String()
	}
	return req, nil
}
func lineageCaseBackfillRequestExisting(app, sub string, current *api.EventExecutionLineageCaseBackfill, req api.RequestEventExecutionLineageCaseBackfillRequest) (api.EventExecutionLineageCaseBackfill, bool, error) {
	job := lineageCaseBackfillView(app, sub, current)
	if req.RequestID == job.RequestID {
		if req.ExpectedPreviousRequestID != job.PreviousRequestID {
			return job, true, ErrConflict
		}
		return job, true, nil
	}
	if activeLineageCaseBackfill(job.Status) || req.ExpectedPreviousRequestID != job.RequestID {
		return job, true, ErrConflict
	}
	return job, false, nil
}
func newLineageCaseBackfill(app, sub string, req api.RequestEventExecutionLineageCaseBackfillRequest, upper int64) api.EventExecutionLineageCaseBackfill {
	now := time.Now().UTC().Truncate(time.Microsecond)
	until := now.Add(api.EventExecutionLedgerRetention)
	job := api.EventExecutionLineageCaseBackfill{RequestID: req.RequestID, PreviousRequestID: req.ExpectedPreviousRequestID, AppID: canonicalMemUUID(app), SubscriptionID: canonicalMemUUID(sub), Status: "pending", ThroughOutboxID: upper, CreatedAt: &now, UpdatedAt: &now, RetainUntil: &until}
	if upper == 0 {
		job.Status = "completed"
	}
	return job
}
func saveLineageCaseBackfillTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account, app, sub string, job api.EventExecutionLineageCaseBackfill) error {
	raw, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return q.SaveEventExecutionLineageBackfill(ctx, tx, sqlc.SaveEventExecutionLineageBackfillParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), Backfill: raw})
}
func (s *PgStore) GetEventExecutionLineageCaseBackfill(ctx context.Context, account, app, sub string) (api.EventExecutionLineageCaseBackfill, error) {
	var out api.EventExecutionLineageCaseBackfill
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	q := sqlc.New()
	_, err := q.EventConsumerExecutionTarget(ctx, s.pool, sqlc.EventConsumerExecutionTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	raw, err := q.GetEventExecutionLineageBackfillProgress(ctx, s.pool, sqlc.GetEventExecutionLineageBackfillProgressParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return lineageCaseBackfillView(app, sub, nil), nil
	}
	if err != nil {
		return out, err
	}
	p, err := decodeLineageRepairProgress(raw)
	if err != nil {
		return out, err
	}
	return lineageCaseBackfillView(app, sub, p.CaseBackfill), nil
}
func (s *PgStore) RequestEventExecutionLineageCaseBackfill(ctx context.Context, account, app, sub string, req api.RequestEventExecutionLineageCaseBackfillRequest) (api.EventExecutionLineageCaseBackfill, error) {
	var out api.EventExecutionLineageCaseBackfill
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	req, err := normalizeLineageCaseBackfillRequest(req)
	if err != nil {
		return out, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	_, err = q.EventConsumerExecutionTarget(ctx, tx, sqlc.EventConsumerExecutionTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	owner, err := q.LockEventExecutionLineageCoverage(ctx, tx, sqlc.LockEventExecutionLineageCoverageParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	p, err := decodeLineageRepairProgress(owner.LineageRepairProgress)
	if err != nil {
		return out, err
	}
	prior, handled, err := lineageCaseBackfillRequestExisting(app, sub, p.CaseBackfill, req)
	if err != nil {
		return out, err
	}
	if handled {
		return prior, tx.Commit(ctx)
	}
	upper, err := q.EventExecutionLineageBackfillUpper(ctx, tx, owner.AccountID)
	if err != nil {
		return out, err
	}
	out = newLineageCaseBackfill(app, sub, req, upper)
	if err = saveLineageCaseBackfillTx(ctx, q, tx, account, app, sub, out); err != nil {
		return api.EventExecutionLineageCaseBackfill{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return api.EventExecutionLineageCaseBackfill{}, err
	}
	return out, nil
}
func validateLineageCaseBackfill(job api.EventExecutionLineageCaseBackfill) error {
	if job.RequestID == "" || job.CreatedAt == nil || job.UpdatedAt == nil || job.RetainUntil == nil || job.Cursor < 0 || job.Cursor > job.ThroughOutboxID || job.Scanned < 0 || job.Recorded < 0 || job.Existing < 0 || job.Associated < 0 || job.Unresolved < 0 || job.Conflicts < 0 {
		return ErrInvalidArgument
	}
	if job.Recorded != job.Unresolved+job.Conflicts || job.Scanned != job.Recorded+job.Existing+job.Associated {
		return ErrInvalidArgument
	}
	return nil
}
func recordLineageBackfillCaseTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, job sqlc.EventExecutionLineageRepairJobsRow, row sqlc.EventExecutionLineageRepairReceiptsRow, outcome string) (bool, error) {
	id := PublishedEventInvocationID(row.InvocationAccountID, row.Source, row.EventID, uuidString(job.SubscriptionID))
	root, err := q.EventExecutionLineageRepairRoot(ctx, tx, sqlc.EventExecutionLineageRepairRootParams{InvocationID: mustPgUUID(id), AccountID: job.AccountID, AppID: job.AppID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	n, err := q.InsertEventExecutionLineageBackfillCase(ctx, tx, sqlc.InsertEventExecutionLineageBackfillCaseParams{DiagnosticID: mustPgUUID(uuid.NewString()), SubscriptionID: job.SubscriptionID, OutboxID: row.OutboxID, AccountID: job.AccountID, AppID: job.AppID, AcceptedAt: row.AcceptedAt, RootInvocationID: id, RootCreatedAt: root.CreatedAt, Status: lineageOutcomeStatus(outcome), Reason: lineageOutcomeReason(outcome), RetentionSeconds: int32(api.EventExecutionLedgerRetention / time.Second)})
	return n > 0, err
}
func countLineageBackfillOutcome(job *api.EventExecutionLineageCaseBackfill, outcome string, recorded bool) error {
	switch lineageOutcomeStatus(outcome) {
	case "existing":
		job.Existing++
	case "already_associated":
		job.Associated++
	case "unresolved", "conflict":
		if !recorded {
			job.Existing++
		} else {
			job.Recorded++
			if lineageOutcomeStatus(outcome) == "unresolved" {
				job.Unresolved++
			} else {
				job.Conflicts++
			}
		}
	default:
		return ErrInvalidArgument
	}
	job.Scanned++
	return nil
}
func backfillBatch(limit int) int {
	if limit <= 0 || limit > api.EventExecutionLineageCaseBackfillBatch {
		return api.EventExecutionLineageCaseBackfillBatch
	}
	return limit
}
func (s *PgStore) AdvanceEventExecutionLineageCaseBackfills(ctx context.Context, limit int) (int, error) {
	limit = backfillBatch(limit)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	jobs, err := q.EventExecutionLineageBackfillJobs(ctx, tx, int32(limit))
	if err != nil {
		return 0, err
	}
	remaining := limit
	for _, owner := range jobs {
		if remaining == 0 {
			break
		}
		p, e := decodeLineageRepairProgress(owner.LineageRepairProgress)
		if e != nil {
			return 0, e
		}
		if p.CaseBackfill == nil {
			return 0, ErrInvalidArgument
		}
		job := lineageCaseBackfillView(uuidString(owner.AppID), uuidString(owner.SubscriptionID), p.CaseBackfill)
		if e = validateLineageCaseBackfill(job); e != nil {
			return 0, e
		}
		pending, e := q.EventExecutionLineageBackfillRetryPending(ctx, tx, sqlc.EventExecutionLineageBackfillRetryPendingParams{AccountID: owner.AccountID, AppID: owner.AppID, SubscriptionID: owner.SubscriptionID})
		if e != nil {
			return 0, e
		}
		if job.Status != "expired" {
			if pending {
				job.Status = "waiting_for_retry_jobs"
			} else {
				job.Status = "pending"
				rows, e := q.EventExecutionLineageBackfillReceipts(ctx, tx, sqlc.EventExecutionLineageBackfillReceiptsParams{AccountID: owner.AccountID, AppID: owner.AppID, SubscriptionID: uuidString(owner.SubscriptionID), AfterOutboxID: job.Cursor, ThroughOutboxID: job.ThroughOutboxID, RootLimit: int32(remaining)})
				if e != nil {
					return 0, e
				}
				evaluated := map[int64]bool{}
				for _, candidate := range rows {
					if evaluated[candidate.OutboxID] {
						continue
					}
					evaluated[candidate.OutboxID] = true
					exists, e := q.EventExecutionLineageBackfillCaseExists(ctx, tx, sqlc.EventExecutionLineageBackfillCaseExistsParams{AccountID: owner.AccountID, AppID: owner.AppID, SubscriptionID: owner.SubscriptionID, OutboxID: candidate.OutboxID})
					if e != nil {
						return 0, e
					}
					outcome := "existing"
					recorded := false
					if !exists {
						proofJob := sqlc.EventExecutionLineageRepairJobsRow(owner)
						row := sqlc.EventExecutionLineageRepairReceiptsRow(candidate)
						outcome, e = evaluateEventExecutionLineageRow(ctx, q, tx, proofJob, row, false)
						if e != nil {
							return 0, e
						}
						if lineageOutcomeStatus(outcome) != "already_associated" {
							recorded, e = recordLineageBackfillCaseTx(ctx, q, tx, proofJob, row, outcome)
							if e != nil {
								return 0, e
							}
						}
					}
					if e = countLineageBackfillOutcome(&job, outcome, recorded); e != nil {
						return 0, e
					}
					job.Cursor = candidate.OutboxID
					remaining--
				}
				if len(rows) == 0 {
					job.Cursor = job.ThroughOutboxID
					job.Status = "completed"
				} else if job.Cursor == job.ThroughOutboxID {
					job.Status = "completed"
				}
			}
		}
		now := time.Now().UTC()
		job.UpdatedAt = &now
		if e = saveLineageCaseBackfillTx(ctx, q, tx, uuidString(owner.AccountID), uuidString(owner.AppID), uuidString(owner.SubscriptionID), job); e != nil {
			return 0, e
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return limit - remaining, nil
}
func (m *MemStore) GetEventExecutionLineageCaseBackfill(ctx context.Context, account, app, sub string) (api.EventExecutionLineageCaseBackfill, error) {
	var out api.EventExecutionLineageCaseBackfill
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	return lineageCaseBackfillView(app, sub, m.eventExecutionCoverage[canonicalMemUUID(sub)].repair.CaseBackfill), nil
}
func (m *MemStore) RequestEventExecutionLineageCaseBackfill(ctx context.Context, account, app, sub string, req api.RequestEventExecutionLineageCaseBackfillRequest) (api.EventExecutionLineageCaseBackfill, error) {
	var out api.EventExecutionLineageCaseBackfill
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	req, err := normalizeLineageCaseBackfillRequest(req)
	if err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	sub = canonicalMemUUID(sub)
	c, ok := m.eventExecutionCoverage[sub]
	if !ok {
		return out, ErrNotFound
	}
	prior, handled, err := lineageCaseBackfillRequestExisting(app, sub, c.repair.CaseBackfill, req)
	if err != nil {
		return out, err
	}
	if handled {
		return prior, nil
	}
	upper := int64(0)
	for _, r := range m.eventFanout {
		if sameMemUUID(eventRoutingAccount(r), account) && r.ID > upper {
			upper = r.ID
		}
	}
	out = newLineageCaseBackfill(app, sub, req, upper)
	if _, err = json.Marshal(out); err != nil {
		return api.EventExecutionLineageCaseBackfill{}, err
	}
	stored := cloneLineageCaseBackfill(out)
	c.repair.CaseBackfill = &stored
	m.eventExecutionCoverage[sub] = c
	return out, nil
}
func (m *MemStore) AdvanceEventExecutionLineageCaseBackfills(ctx context.Context, limit int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	limit = backfillBatch(limit)
	m.mu.Lock()
	defer m.mu.Unlock()
	subs := []string{}
	for sub, c := range m.eventExecutionCoverage {
		job := c.repair.CaseBackfill
		if job == nil || !runnableLineageCaseBackfill(job.Status) {
			continue
		}
		app, ok := m.eventSubscriptionAppLocked(job.AppID)
		if !ok || app.Status == AppDeleted {
			continue
		}
		subs = append(subs, sub)
	}
	sort.Slice(subs, func(i, j int) bool {
		a, b := m.eventExecutionCoverage[subs[i]].repair.CaseBackfill, m.eventExecutionCoverage[subs[j]].repair.CaseBackfill
		if a.UpdatedAt.Equal(*b.UpdatedAt) {
			return subs[i] < subs[j]
		}
		return a.UpdatedAt.Before(*b.UpdatedAt)
	})
	subs = subs[:min(len(subs), limit)]
	remaining := limit
	for _, sub := range subs {
		if remaining == 0 {
			break
		}
		if err := ctx.Err(); err != nil {
			return limit - remaining, err
		}
		c := m.eventExecutionCoverage[sub]
		job := lineageCaseBackfillView(c.app, sub, c.repair.CaseBackfill)
		if err := validateLineageCaseBackfill(job); err != nil {
			return limit - remaining, err
		}
		app, _ := m.eventSubscriptionAppLocked(job.AppID)
		pending := false
		for _, retry := range c.jobs {
			if (retry.Status == "pending" || retry.Status == "paused") && retry.RetainUntil.After(time.Now()) {
				pending = true
				break
			}
		}
		if job.Status != "expired" {
			if pending {
				job.Status = "waiting_for_retry_jobs"
			} else {
				job.Status = "pending"
				receipts := []*PublishedEventWork{}
				for _, r := range m.eventFanout {
					if r.ID <= job.Cursor || r.ID > job.ThroughOutboxID || !sameMemUUID(eventRoutingAccount(r), app.AccountID) {
						continue
					}
					if _, ok := lineageRepairRecipient(r, job.AppID, sub); ok {
						receipts = append(receipts, r)
					}
				}
				sort.Slice(receipts, func(i, j int) bool { return receipts[i].ID < receipts[j].ID })
				receipts = receipts[:min(len(receipts), remaining)]
				for _, r := range receipts {
					outcome := "existing"
					recorded := false
					if _, exists := c.cases[r.ID]; !exists {
						outcome = m.evaluateEventExecutionLineageRowLocked(app.AccountID, job.AppID, sub, r, false)
						if lineageOutcomeStatus(outcome) != "already_associated" {
							m.recordLineageCaseLocked(app.AccountID, job.AppID, sub, r, outcome)
							c = m.eventExecutionCoverage[sub]
							_, recorded = c.cases[r.ID]
							if recorded {
								item := c.cases[r.ID]
								item.DiagnosticID = uuid.NewString()
								c.cases[r.ID] = item
								m.eventExecutionCoverage[sub] = c
							}

						}
					}
					if err := countLineageBackfillOutcome(&job, outcome, recorded); err != nil {
						return limit - remaining, err
					}
					job.Cursor = r.ID
					remaining--
				}
				if len(receipts) == 0 {
					job.Cursor = job.ThroughOutboxID
					job.Status = "completed"
				} else if job.Cursor == job.ThroughOutboxID {
					job.Status = "completed"
				}
			}
		}
		now := time.Now().UTC()
		job.UpdatedAt = &now
		c = m.eventExecutionCoverage[sub]
		c.repair.CaseBackfill = &job
		m.eventExecutionCoverage[sub] = c
	}
	return limit - remaining, nil
}
