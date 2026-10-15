package state

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventExecutionLineageCasesStore interface {
	ListEventExecutionLineageCases(context.Context, string, string, string, int64, int) (api.EventExecutionLineageCases, error)
	RetryEventExecutionLineageCase(context.Context, string, string, string, int64, api.RetryEventExecutionLineageCaseRequest) (api.EventExecutionLineageCase, error)
}
type lineageCaseRetry struct {
	diagnostic string
	expected   int64
	result     []byte
	retain     time.Time
}

func lineageCasePageLimit(limit int) (int, error) {
	if limit == 0 {
		return api.EventExecutionLineageCasesPageDefault, nil
	}
	if limit < 1 || limit > api.EventExecutionLineageCasesPageMax {
		return 0, ErrInvalidArgument
	}
	return limit, nil
}
func validLineageRetry(id int64, r api.RetryEventExecutionLineageCaseRequest) error {
	if id <= 0 || r.ExpectedRevision <= 0 {
		return ErrInvalidArgument
	}
	u, err := uuid.Parse(r.RequestID)
	if err != nil || u == uuid.Nil {
		return ErrInvalidArgument
	}
	if r.ExpectedDiagnosticID != "" {
		u, e := uuid.Parse(r.ExpectedDiagnosticID)
		if e != nil || u == uuid.Nil {
			return ErrInvalidArgument
		}
	}
	return nil
}
func lineageCaseFromRow(r sqlc.EventExecutionLineageCase) api.EventExecutionLineageCase {
	return api.EventExecutionLineageCase{DiagnosticID: uuidString(r.DiagnosticID), SubscriptionID: uuidString(r.SubscriptionID), AppID: uuidString(r.AppID), OutboxID: r.OutboxID, AcceptedAt: timeFromPgtype(r.AcceptedAt), RootInvocationID: r.RootInvocationID, RootCreatedAt: timestamptzToTimePtr(r.RootCreatedAt), Status: r.Status, Reason: r.Reason, Revision: r.Revision, RetryCount: r.RetryCount, RecordedAt: timeFromPgtype(r.RecordedAt), RetainUntil: timeFromPgtype(r.RetainUntil)}
}
func recordLineageCaseTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, job sqlc.EventExecutionLineageRepairJobsRow, row sqlc.EventExecutionLineageRepairReceiptsRow, outcome string) error {
	status := lineageOutcomeStatus(outcome)
	if status != "unresolved" && status != "conflict" {
		return nil
	}
	id := PublishedEventInvocationID(row.InvocationAccountID, row.Source, row.EventID, uuidString(job.SubscriptionID))
	root, err := q.EventExecutionLineageRepairRoot(ctx, tx, sqlc.EventExecutionLineageRepairRootParams{InvocationID: mustPgUUID(id), AccountID: job.AccountID, AppID: job.AppID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	created := pgtype.Timestamptz{}
	if err == nil {
		created = root.CreatedAt
	}
	n, err := q.RecordEventExecutionLineageCase(ctx, tx, sqlc.RecordEventExecutionLineageCaseParams{SubscriptionID: job.SubscriptionID, OutboxID: row.OutboxID, AccountID: job.AccountID, AppID: job.AppID, AcceptedAt: row.AcceptedAt, RootInvocationID: id, RootCreatedAt: created, Status: status, Reason: lineageOutcomeReason(outcome), RetentionSeconds: int32(api.EventExecutionLedgerRetention / time.Second)})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return err
}
func (s *PgStore) ListEventExecutionLineageCases(ctx context.Context, account, app, sub string, after int64, limit int) (api.EventExecutionLineageCases, error) {
	out := api.EventExecutionLineageCases{Cases: []api.EventExecutionLineageCase{}}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	if after < 0 {
		return out, ErrInvalidArgument
	}
	limit, err := lineageCasePageLimit(limit)
	if err != nil {
		return out, err
	}
	q := sqlc.New()
	_, err = q.EventConsumerExecutionTarget(ctx, s.pool, sqlc.EventConsumerExecutionTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	rows, err := q.ListEventExecutionLineageCases(ctx, s.pool, sqlc.ListEventExecutionLineageCasesParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), AfterOutboxID: after, PageLimit: int32(limit + 1)})
	if err != nil {
		return out, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		out.NextAfter = rows[len(rows)-1].OutboxID
	}
	for _, row := range rows {
		out.Cases = append(out.Cases, lineageCaseFromRow(row))
	}
	return out, nil
}
func (s *PgStore) RetryEventExecutionLineageCase(ctx context.Context, account, app, sub string, id int64, req api.RetryEventExecutionLineageCaseRequest) (api.EventExecutionLineageCase, error) {
	var out api.EventExecutionLineageCase
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	if err := validLineageRetry(id, req); err != nil {
		return out, err
	}
	if req.ExpectedDiagnosticID != "" {
		req.ExpectedDiagnosticID = canonicalMemUUID(req.ExpectedDiagnosticID)
	}
	sub = canonicalMemUUID(sub)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err = retryEventExecutionLineageCaseTx(ctx, tx, account, app, sub, id, req, nil)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return api.EventExecutionLineageCase{}, err
	}
	return out, nil
}

func retryEventExecutionLineageCaseTx(ctx context.Context, tx pgx.Tx, account, app, sub string, id int64, req api.RetryEventExecutionLineageCaseRequest, accepted *time.Time) (api.EventExecutionLineageCase, error) {
	var out api.EventExecutionLineageCase
	var err error
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
	c, err := q.LockEventExecutionLineageCase(ctx, tx, sqlc.LockEventExecutionLineageCaseParams{AccountID: owner.AccountID, AppID: owner.AppID, SubscriptionID: owner.SubscriptionID, OutboxID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if accepted != nil && !timeFromPgtype(c.AcceptedAt).Equal(*accepted) {
		return out, ErrConflict
	}
	prior, err := q.EventExecutionLineageRetryResult(ctx, tx, sqlc.EventExecutionLineageRetryResultParams{SubscriptionID: owner.SubscriptionID, OutboxID: id, RequestID: mustPgUUID(req.RequestID)})
	if err == nil {
		if prior.ExpectedRevision != req.ExpectedRevision {
			return out, ErrConflict
		}
		if err = json.Unmarshal(prior.Result, &out); err != nil {
			return out, err
		}
		if out.DiagnosticID != req.ExpectedDiagnosticID {
			return api.EventExecutionLineageCase{}, ErrConflict
		}
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if uuidString(c.DiagnosticID) != req.ExpectedDiagnosticID {
		return out, ErrConflict
	}
	if c.Revision != req.ExpectedRevision || c.Revision == math.MaxInt64 || c.RetryCount == math.MaxInt64 {
		return out, ErrConflict
	}
	rows, err := q.EventExecutionLineageCaseReceipt(ctx, tx, sqlc.EventExecutionLineageCaseReceiptParams{AccountID: owner.AccountID, AppID: owner.AppID, SubscriptionID: uuidString(owner.SubscriptionID), OutboxID: id})
	if err != nil {
		return out, err
	}
	outcome := "unresolved:missing_receipt_or_membership"
	if len(rows) > 0 {
		r := rows[0]
		if !timeFromPgtype(r.AcceptedAt).Equal(timeFromPgtype(c.AcceptedAt)) {
			return out, ErrConflict
		}
		if c.RootInvocationID != "" && c.RootInvocationID != PublishedEventInvocationID(r.InvocationAccountID, r.Source, r.EventID, sub) {
			return out, ErrConflict
		}
		row := sqlc.EventExecutionLineageRepairReceiptsRow(r)
		job := sqlc.EventExecutionLineageRepairJobsRow(owner)
		outcome, err = repairEventExecutionLineageRow(ctx, q, tx, job, row, timestamptzToTimePtr(c.RootCreatedAt))
		if err != nil {
			return out, err
		}
		if !c.RootCreatedAt.Valid {
			root, lookupErr := q.EventExecutionLineageRepairRoot(ctx, tx, sqlc.EventExecutionLineageRepairRootParams{InvocationID: mustPgUUID(PublishedEventInvocationID(r.InvocationAccountID, r.Source, r.EventID, sub)), AccountID: owner.AccountID, AppID: owner.AppID})
			if lookupErr == nil {
				c.RootCreatedAt = root.CreatedAt
			} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
				return out, lookupErr
			}
		}
	}
	c, err = q.UpdateEventExecutionLineageCaseRetry(ctx, tx, sqlc.UpdateEventExecutionLineageCaseRetryParams{SubscriptionID: owner.SubscriptionID, OutboxID: id, RootCreatedAt: c.RootCreatedAt, Status: lineageOutcomeStatus(outcome), Reason: lineageOutcomeReason(outcome), RetentionSeconds: int32(api.EventExecutionLedgerRetention / time.Second)})
	if err != nil {
		return out, err
	}
	out = lineageCaseFromRow(c)
	raw, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	err = q.SaveEventExecutionLineageRetry(ctx, tx, sqlc.SaveEventExecutionLineageRetryParams{SubscriptionID: owner.SubscriptionID, OutboxID: id, RequestID: mustPgUUID(req.RequestID), ExpectedRevision: req.ExpectedRevision, Result: raw, RetainUntil: c.RetainUntil})
	if err != nil {
		return out, err
	}
	return out, nil
}

func cloneLineageCase(c api.EventExecutionLineageCase) api.EventExecutionLineageCase {
	c.RootCreatedAt = cloneEventReceiptTime(c.RootCreatedAt)
	return c
}
func (m *MemStore) recordLineageCaseLocked(account, app, sub string, r *PublishedEventWork, outcome string) {
	status := lineageOutcomeStatus(outcome)
	if status != "unresolved" && status != "conflict" {
		return
	}
	c := m.eventExecutionCoverage[sub]
	if c.cases == nil {
		c.cases = map[int64]api.EventExecutionLineageCase{}
	}
	previous, exists := c.cases[r.ID]
	if exists && !previous.AcceptedAt.Equal(r.CreatedAt) {
		return
	}
	now := time.Now().UTC()
	item := api.EventExecutionLineageCase{SubscriptionID: sub, AppID: app, OutboxID: r.ID, AcceptedAt: r.CreatedAt, Status: status, Reason: lineageOutcomeReason(outcome), Revision: previous.Revision + 1, RetryCount: previous.RetryCount, RecordedAt: now, RetainUntil: now.Add(api.EventExecutionLedgerRetention), RootCreatedAt: cloneEventReceiptTime(previous.RootCreatedAt), DiagnosticID: previous.DiagnosticID}
	var e eventcontract.Envelope
	if json.Unmarshal(r.Payload, &e) == nil {
		recipient, _ := lineageRepairRecipient(r, app, sub)
		recipientID := sub
		if recipient.ID != "" {
			recipientID = recipient.ID
		}
		item.RootInvocationID = PublishedEventInvocationID(e.AccountID, e.Source, e.ID, recipientID)
		if inv, ok := m.invocations[item.RootInvocationID]; ok && sameMemUUID(inv.AccountID, account) && sameMemUUID(inv.AppID, app) && item.RootCreatedAt == nil {
			item.RootCreatedAt = cloneEventReceiptTime(&inv.CreatedAt)
		}
	}
	if previous.RetainUntil.After(item.RetainUntil) {
		item.RetainUntil = previous.RetainUntil
	}
	c.cases[r.ID] = item
	m.eventExecutionCoverage[sub] = c
}
func (m *MemStore) ListEventExecutionLineageCases(ctx context.Context, account, app, sub string, after int64, limit int) (api.EventExecutionLineageCases, error) {
	out := api.EventExecutionLineageCases{Cases: []api.EventExecutionLineageCase{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	if after < 0 {
		return out, ErrInvalidArgument
	}
	limit, err := lineageCasePageLimit(limit)
	if err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	c := m.eventExecutionCoverage[canonicalMemUUID(sub)]
	now := time.Now()
	for id, item := range c.cases {
		if id > after && item.RetainUntil.After(now) {
			out.Cases = append(out.Cases, cloneLineageCase(item))
		}
	}
	sort.Slice(out.Cases, func(i, j int) bool { return out.Cases[i].OutboxID < out.Cases[j].OutboxID })
	if len(out.Cases) > limit {
		out.Cases = out.Cases[:limit]
		out.NextAfter = out.Cases[limit-1].OutboxID
	}
	return out, nil
}
func (m *MemStore) RetryEventExecutionLineageCase(ctx context.Context, account, app, sub string, id int64, req api.RetryEventExecutionLineageCaseRequest) (api.EventExecutionLineageCase, error) {
	var out api.EventExecutionLineageCase
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	if err := validLineageRetry(id, req); err != nil {
		return out, err
	}
	if req.ExpectedDiagnosticID != "" {
		req.ExpectedDiagnosticID = canonicalMemUUID(req.ExpectedDiagnosticID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.retryEventExecutionLineageCaseLocked(account, app, sub, id, req, nil)
}
func (m *MemStore) retryEventExecutionLineageCaseLocked(account, app, sub string, id int64, req api.RetryEventExecutionLineageCaseRequest, accepted *time.Time) (api.EventExecutionLineageCase, error) {
	var out api.EventExecutionLineageCase
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	sub = canonicalMemUUID(sub)
	c := m.eventExecutionCoverage[sub]
	item, ok := c.cases[id]
	now := time.Now().UTC()
	if !ok || !item.RetainUntil.After(now) {
		return out, ErrNotFound
	}
	if accepted != nil && !item.AcceptedAt.Equal(*accepted) {
		return out, ErrConflict
	}
	key := strconv.FormatInt(id, 10) + ":" + canonicalMemUUID(req.RequestID)
	if prior, ok := c.retries[key]; ok && prior.retain.After(now) {
		if prior.expected != req.ExpectedRevision || prior.diagnostic != req.ExpectedDiagnosticID {
			return out, ErrConflict
		}
		err := json.Unmarshal(prior.result, &out)
		return out, err
	}
	if item.DiagnosticID != req.ExpectedDiagnosticID {
		return out, ErrConflict
	}
	if item.Revision != req.ExpectedRevision || item.Revision == math.MaxInt64 || item.RetryCount == math.MaxInt64 {
		return out, ErrConflict
	}
	outcome := "unresolved:missing_receipt_or_membership"
	var receipt *PublishedEventWork
	for _, candidate := range m.eventFanout {
		if candidate.ID == id {
			receipt = candidate
			break
		}
	}
	if r := receipt; r != nil && sameMemUUID(eventRoutingAccount(r), account) {
		if !r.CreatedAt.Equal(item.AcceptedAt) {
			return out, ErrConflict
		}
		if recipient, ok := lineageRepairRecipient(r, app, sub); ok {
			expected := cloneEventReceiptTime(item.RootCreatedAt)
			var e eventcontract.Envelope
			if json.Unmarshal(r.Payload, &e) == nil {
				rootID := PublishedEventInvocationID(e.AccountID, e.Source, e.ID, recipient.ID)
				if item.RootInvocationID != "" && item.RootInvocationID != rootID {
					return out, ErrConflict
				}
				if inv, ok := m.invocations[rootID]; ok && sameMemUUID(inv.AccountID, account) && sameMemUUID(inv.AppID, app) && item.RootCreatedAt == nil {
					item.RootCreatedAt = cloneEventReceiptTime(&inv.CreatedAt)
					item.RootInvocationID = rootID
				}
			}
			preview := item
			preview.RecordedAt = now
			preview.RetainUntil = now.Add(api.EventExecutionLedgerRetention)
			if _, err := json.Marshal(preview); err != nil {
				return out, err
			}
			outcome = m.repairEventExecutionLineageRowLocked(account, app, sub, r, expected)
		}
	}
	item.Status = lineageOutcomeStatus(outcome)
	item.Reason = lineageOutcomeReason(outcome)
	item.Revision++
	item.RetryCount++
	item.RecordedAt = now
	item.RetainUntil = now.Add(api.EventExecutionLedgerRetention)
	if c.cases[id].RetainUntil.After(item.RetainUntil) {
		item.RetainUntil = c.cases[id].RetainUntil
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return out, err
	}
	c = m.eventExecutionCoverage[sub]
	c.cases[id] = item
	if c.retries == nil {
		c.retries = map[string]lineageCaseRetry{}
	}
	c.retries[key] = lineageCaseRetry{expected: req.ExpectedRevision, diagnostic: req.ExpectedDiagnosticID, result: raw, retain: item.RetainUntil}
	m.eventExecutionCoverage[sub] = c
	return cloneLineageCase(item), nil
}

func (m *MemStore) pruneLineageCasesLocked(now time.Time, limit int) int {
	cases, retries := 0, 0
	for sub, c := range m.eventExecutionCoverage {
		for key, item := range c.retries {
			if retries < limit && !item.retain.After(now) {
				delete(c.retries, key)
				retries++
			}
		}
		for id, item := range c.cases {
			if cases == limit || item.RetainUntil.After(now) {
				continue
			}
			prefix := strconv.FormatInt(id, 10) + ":"
			hasRetry := false
			for key := range c.retries {
				if strings.HasPrefix(key, prefix) {
					hasRetry = true
					break
				}
			}
			if !hasRetry {
				delete(c.cases, id)
				cases++
			}
		}
		m.eventExecutionCoverage[sub] = c
	}
	return cases + retries
}
