package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EventExecutionLineageRetryJobsStore interface {
	ListEventExecutionLineageRetryJobControls(context.Context, string, string, string, string, time.Time, int64, int, ...api.EventExecutionLineageRetryJobControlFilters) (api.EventExecutionLineageRetryJobControls, error)
	ControlEventExecutionLineageRetryJob(context.Context, string, string, string, string, string, api.ControlEventExecutionLineageRetryJobRequest) (api.EventExecutionLineageRetryJob, error)
	ListEventExecutionLineageRetryJobs(context.Context, string, string, string, string, string, int, ...api.EventExecutionLineageRetryJobAttentionOptions) (api.EventExecutionLineageRetryJobs, error)
	PreviewEventExecutionLineageRetries(context.Context, string, string, string, string, int64, int) (api.EventExecutionLineageCases, error)
	CreateEventExecutionLineageRetryJob(context.Context, string, string, string, api.CreateEventExecutionLineageRetryJobRequest) (api.EventExecutionLineageRetryJob, error)
	GetEventExecutionLineageRetryJob(context.Context, string, string, string, string) (api.EventExecutionLineageRetryJob, error)
	CancelEventExecutionLineageRetryJob(context.Context, string, string, string, string, api.CancelEventExecutionLineageRetryJobRequest) (api.EventExecutionLineageRetryJob, error)
	AdvanceEventExecutionLineageRetryJobs(context.Context, int) (int, error)
}

func normalizeLineageRetryJob(req api.CreateEventExecutionLineageRetryJobRequest) (api.CreateEventExecutionLineageRetryJobRequest, error) {
	id, err := uuid.Parse(req.RequestID)
	if err != nil || id == uuid.Nil || len(req.Cases) == 0 || len(req.Cases) > api.EventExecutionLineageRetryJobMaxCases {
		return req, ErrInvalidArgument
	}
	req.RequestID = id.String()
	req.Cases = append([]api.EventExecutionLineageRetrySelection(nil), req.Cases...)
	sort.Slice(req.Cases, func(i, j int) bool { return req.Cases[i].OutboxID < req.Cases[j].OutboxID })
	for i, c := range req.Cases {
		if c.OutboxID <= 0 || c.ExpectedRevision <= 0 || c.AcceptedAt.IsZero() || (i > 0 && req.Cases[i-1].OutboxID == c.OutboxID) {
			return req, ErrInvalidArgument
		}
		if c.ExpectedDiagnosticID != "" {
			id, e := uuid.Parse(c.ExpectedDiagnosticID)
			if e != nil || id == uuid.Nil {
				return req, ErrInvalidArgument
			}
			req.Cases[i].ExpectedDiagnosticID = id.String()
		}
		req.Cases[i].AcceptedAt = c.AcceptedAt.UTC()
	}
	return req, nil
}
func sameLineageJobSelection(job api.EventExecutionLineageRetryJob, req api.CreateEventExecutionLineageRetryJobRequest) bool {
	if len(job.Items) != len(req.Cases) {
		return false
	}
	for i, item := range job.Items {
		c := req.Cases[i]
		s := item.Selection
		if s.OutboxID != c.OutboxID || s.ExpectedRevision != c.ExpectedRevision || s.ExpectedDiagnosticID != c.ExpectedDiagnosticID || !s.AcceptedAt.Equal(c.AcceptedAt) {
			return false
		}
	}
	return true
}
func newLineageRetryJob(app, sub string, req api.CreateEventExecutionLineageRetryJobRequest) api.EventExecutionLineageRetryJob {
	now := time.Now().UTC().Truncate(time.Microsecond)
	job := api.EventExecutionLineageRetryJob{RequestID: req.RequestID, AppID: canonicalMemUUID(app), SubscriptionID: canonicalMemUUID(sub), Status: "pending", CreatedAt: now, UpdatedAt: now, RetainUntil: now.Add(api.EventExecutionLedgerRetention), Items: make([]api.EventExecutionLineageRetryJobItem, 0, len(req.Cases))}
	for _, c := range req.Cases {
		job.Items = append(job.Items, api.EventExecutionLineageRetryJobItem{Selection: c, Status: "pending"})
	}
	return job
}
func cloneLineageRetryJob(job api.EventExecutionLineageRetryJob) api.EventExecutionLineageRetryJob {
	if job.LastProgressAt != nil {
		at := *job.LastProgressAt
		job.LastProgressAt = &at
	}
	if job.ControlReceipt != nil {
		receipt := *job.ControlReceipt
		job.ControlReceipt = &receipt
	}
	job.Items = append([]api.EventExecutionLineageRetryJobItem(nil), job.Items...)
	for i, item := range job.Items {
		if item.Result != nil {
			c := cloneLineageCase(*item.Result)
			job.Items[i].Result = &c
		}
	}
	return job
}
func lineageRetryJobFromRow(row sqlc.EventExecutionLineageRetryJob) (api.EventExecutionLineageRetryJob, error) {
	var job api.EventExecutionLineageRetryJob
	err := json.Unmarshal(row.Snapshot, &job)
	job.CreatedAt = timeFromPgtype(row.CreatedAt)
	job.UpdatedAt = timeFromPgtype(row.UpdatedAt)
	job.RetainUntil = timeFromPgtype(row.RetainUntil)
	return job, err
}
func validLineagePreview(reason string, after int64, limit int) (int, error) {
	if after < 0 || len(reason) > api.EventExecutionLineageRetryReasonMaxBytes {
		return 0, ErrInvalidArgument
	}
	return lineageCasePageLimit(limit)
}
func (s *PgStore) PreviewEventExecutionLineageRetries(ctx context.Context, account, app, sub, reason string, after int64, limit int) (api.EventExecutionLineageCases, error) {
	out := api.EventExecutionLineageCases{Cases: []api.EventExecutionLineageCase{}}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	limit, err := validLineagePreview(reason, after, limit)
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
	rows, err := q.PreviewEventExecutionLineageRetries(ctx, s.pool, sqlc.PreviewEventExecutionLineageRetriesParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), Reason: reason, AfterOutboxID: after, PageLimit: int32(limit + 1)})
	if err != nil {
		return out, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		out.NextAfter = rows[limit-1].OutboxID
	}
	for _, r := range rows {
		out.Cases = append(out.Cases, lineageCaseFromRow(r))
	}
	return out, nil
}
func (s *PgStore) GetEventExecutionLineageRetryJob(ctx context.Context, account, app, sub, id string) (api.EventExecutionLineageRetryJob, error) {
	var out api.EventExecutionLineageRetryJob
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	u, err := uuid.Parse(id)
	if err != nil || u == uuid.Nil {
		return out, ErrInvalidArgument
	}
	q := sqlc.New()
	_, err = q.EventConsumerExecutionTarget(ctx, s.pool, sqlc.EventConsumerExecutionTargetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	row, err := q.GetEventExecutionLineageRetryJob(ctx, s.pool, sqlc.GetEventExecutionLineageRetryJobParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), RequestID: mustPgUUID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	return lineageRetryJobFromRow(row)
}
func (s *PgStore) CreateEventExecutionLineageRetryJob(ctx context.Context, account, app, sub string, req api.CreateEventExecutionLineageRetryJobRequest) (api.EventExecutionLineageRetryJob, error) {
	var out api.EventExecutionLineageRetryJob
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	req, err := normalizeLineageRetryJob(req)
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
	_, err = q.LockEventExecutionLineageCoverage(ctx, tx, sqlc.LockEventExecutionLineageCoverageParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	// Reject expired identities until pruned; retained identities always preserve their original selection.
	prior, err := q.EventExecutionLineageRetryJobIdentity(ctx, tx, sqlc.EventExecutionLineageRetryJobIdentityParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), RequestID: mustPgUUID(req.RequestID)})
	if err == nil {
		out, err = lineageRetryJobFromRow(prior)
		if err != nil {
			return out, err
		}
		if !out.RetainUntil.After(time.Now()) || !sameLineageJobSelection(out, req) {
			return out, ErrConflict
		}
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	for _, selected := range req.Cases {
		c, e := q.LockEventExecutionLineageCase(ctx, tx, sqlc.LockEventExecutionLineageCaseParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), OutboxID: selected.OutboxID})
		if errors.Is(e, pgx.ErrNoRows) {
			return out, ErrNotFound
		}
		if e != nil {
			return out, e
		}
		if uuidString(c.DiagnosticID) != selected.ExpectedDiagnosticID || c.Revision != selected.ExpectedRevision || !timeFromPgtype(c.AcceptedAt).Equal(selected.AcceptedAt) || (c.Status != "unresolved" && c.Status != "conflict") {
			return out, ErrConflict
		}
	}
	out = newLineageRetryJob(app, sub, req)
	raw, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	err = q.CreateEventExecutionLineageRetryJob(ctx, tx, sqlc.CreateEventExecutionLineageRetryJobParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), SubscriptionID: mustPgUUID(sub), RequestID: mustPgUUID(req.RequestID), Snapshot: raw, CreatedAt: pgtypeFromTime(out.CreatedAt), RetainUntil: pgtypeFromTime(out.RetainUntil)})
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return api.EventExecutionLineageRetryJob{}, err
	}
	return out, nil
}
func finishLineageRetryJobItem(job *api.EventExecutionLineageRetryJob, result api.EventExecutionLineageCase, err error) error {
	if job.Processed < 0 || job.Processed >= len(job.Items) {
		return ErrInvalidArgument
	}
	item := &job.Items[job.Processed]
	switch {
	case errors.Is(err, ErrConflict):
		item.Status = "changed"
		item.Reason = "event_lineage_case_changed"
	case errors.Is(err, ErrNotFound):
		item.Status = "unavailable"
		item.Reason = "case_or_subscription_unavailable"
	case err != nil:
		return err
	default:
		item.Status = result.Status
		item.Reason = result.Reason
		item.Result = &result
	}
	job.Processed++
	job.UpdatedAt = time.Now().UTC()
	at := job.UpdatedAt
	job.LastProgressAt = &at
	if job.Processed == len(job.Items) {
		job.Status = "completed"
	}
	return nil
}
func lineageJobCaseRequest(job api.EventExecutionLineageRetryJob, c api.EventExecutionLineageRetrySelection) api.RetryEventExecutionLineageCaseRequest {
	// Separate namespace from customer request IDs and stable across process restarts.
	key := job.SubscriptionID + ":" + job.RequestID + ":" + strconv.FormatInt(c.OutboxID, 10)
	return api.RetryEventExecutionLineageCaseRequest{RequestID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("gregale-lineage-bulk:"+key)).String(), ExpectedRevision: c.ExpectedRevision, ExpectedDiagnosticID: c.ExpectedDiagnosticID}
}
func (s *PgStore) advanceLineageRetryJobItem(ctx context.Context) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockNextEventExecutionLineageRetryJob(ctx, tx)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	job, err := lineageRetryJobFromRow(row)
	if err != nil {
		return false, err
	}
	if job.Processed < 0 || job.Processed >= len(job.Items) {
		return false, ErrInvalidArgument
	}
	// Recheck retention after acquiring coverage: a diagnostic backfill may have
	// held it while this job's retention deadline passed.
	_, err = q.LockEventExecutionLineageCoverage(ctx, tx, sqlc.LockEventExecutionLineageCoverageParams{AccountID: row.AccountID, AppID: row.AppID, SubscriptionID: row.SubscriptionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = q.GetEventExecutionLineageRetryJob(ctx, tx, sqlc.GetEventExecutionLineageRetryJobParams{AccountID: row.AccountID, AppID: row.AppID, SubscriptionID: row.SubscriptionID, RequestID: row.RequestID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	c := job.Items[job.Processed].Selection
	result, retryErr := retryEventExecutionLineageCaseTx(ctx, tx, uuidString(row.AccountID), uuidString(row.AppID), uuidString(row.SubscriptionID), c.OutboxID, lineageJobCaseRequest(job, c), &c.AcceptedAt)
	if err = finishLineageRetryJobItem(&job, result, retryErr); err != nil {
		return false, err
	}
	raw, err := json.Marshal(job)
	if err != nil {
		return false, err
	}
	err = q.SaveEventExecutionLineageRetryJob(ctx, tx, sqlc.SaveEventExecutionLineageRetryJobParams{SubscriptionID: row.SubscriptionID, RequestID: row.RequestID, State: job.Status, Snapshot: raw})
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
func (s *PgStore) AdvanceEventExecutionLineageRetryJobs(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > api.EventExecutionLineageRetryJobBatch {
		limit = api.EventExecutionLineageRetryJobBatch
	}
	for n := 0; n < limit; n++ {
		done, err := s.advanceLineageRetryJobItem(ctx)
		if err != nil {
			return n, err
		}
		if !done {
			return n, nil
		}
	}
	return limit, nil
}

func (m *MemStore) PreviewEventExecutionLineageRetries(ctx context.Context, account, app, sub, reason string, after int64, limit int) (api.EventExecutionLineageCases, error) {
	out := api.EventExecutionLineageCases{Cases: []api.EventExecutionLineageCase{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	limit, err := validLineagePreview(reason, after, limit)
	if err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	for id, c := range m.eventExecutionCoverage[canonicalMemUUID(sub)].cases {
		if id > after && c.RetainUntil.After(time.Now()) && (c.Status == "unresolved" || c.Status == "conflict") && (reason == "" || c.Reason == reason) {
			out.Cases = append(out.Cases, cloneLineageCase(c))
		}
	}
	sort.Slice(out.Cases, func(i, j int) bool { return out.Cases[i].OutboxID < out.Cases[j].OutboxID })
	if len(out.Cases) > limit {
		out.Cases = out.Cases[:limit]
		out.NextAfter = out.Cases[limit-1].OutboxID
	}
	return out, nil
}
func (m *MemStore) GetEventExecutionLineageRetryJob(ctx context.Context, account, app, sub, id string) (api.EventExecutionLineageRetryJob, error) {
	var out api.EventExecutionLineageRetryJob
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	u, err := uuid.Parse(id)
	if err != nil || u == uuid.Nil {
		return out, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	job, ok := m.eventExecutionCoverage[canonicalMemUUID(sub)].jobs[u.String()]
	if !ok || !job.RetainUntil.After(time.Now()) {
		return out, ErrNotFound
	}
	return cloneLineageRetryJob(job), nil
}
func (m *MemStore) CreateEventExecutionLineageRetryJob(ctx context.Context, account, app, sub string, req api.CreateEventExecutionLineageRetryJobRequest) (api.EventExecutionLineageRetryJob, error) {
	var out api.EventExecutionLineageRetryJob
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := validSubscriptionControlIDs(account, app, sub); err != nil {
		return out, err
	}
	req, err := normalizeLineageRetryJob(req)
	if err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.eventSubscriptionControlTargetLocked(account, app, sub) {
		return out, ErrNotFound
	}
	sub = canonicalMemUUID(sub)
	coverage := m.eventExecutionCoverage[sub]
	if prior, ok := coverage.jobs[req.RequestID]; ok {
		if !prior.RetainUntil.After(time.Now()) || !sameLineageJobSelection(prior, req) {
			return out, ErrConflict
		}
		return cloneLineageRetryJob(prior), nil
	}
	for _, selected := range req.Cases {
		c, ok := coverage.cases[selected.OutboxID]
		if !ok || !c.RetainUntil.After(time.Now()) {
			return out, ErrNotFound
		}
		if c.DiagnosticID != selected.ExpectedDiagnosticID || c.Revision != selected.ExpectedRevision || !c.AcceptedAt.Equal(selected.AcceptedAt) || (c.Status != "unresolved" && c.Status != "conflict") {
			return out, ErrConflict
		}
	}
	out = newLineageRetryJob(app, sub, req)
	if _, err = json.Marshal(out); err != nil {
		return out, err
	}
	if coverage.jobs == nil {
		coverage.jobs = map[string]api.EventExecutionLineageRetryJob{}
	}
	coverage.jobs[req.RequestID] = cloneLineageRetryJob(out)
	m.eventExecutionCoverage[sub] = coverage
	return out, nil
}
func (m *MemStore) AdvanceEventExecutionLineageRetryJobs(ctx context.Context, limit int) (int, error) {
	if limit <= 0 || limit > api.EventExecutionLineageRetryJobBatch {
		limit = api.EventExecutionLineageRetryJobBatch
	}
	for n := 0; n < limit; n++ {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		done, err := m.advanceLineageRetryJobItem()
		if err != nil {
			return n, err
		}
		if !done {
			return n, nil
		}
	}
	return limit, nil
}
func (m *MemStore) advanceLineageRetryJobItem() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var selected api.EventExecutionLineageRetryJob
	for _, coverage := range m.eventExecutionCoverage {
		for _, job := range coverage.jobs {
			app, exists := m.eventSubscriptionAppLocked(job.AppID)
			if !exists || app.Status == AppDeleted {
				continue
			}
			if job.Status != "pending" || !job.RetainUntil.After(time.Now()) {
				continue
			}
			if selected.RequestID == "" || job.UpdatedAt.Before(selected.UpdatedAt) || (job.UpdatedAt.Equal(selected.UpdatedAt) && job.SubscriptionID+job.RequestID < selected.SubscriptionID+selected.RequestID) {
				selected = job
			}
		}
	}
	if selected.RequestID == "" {
		return false, nil
	}
	if selected.Processed < 0 || selected.Processed >= len(selected.Items) {
		return false, ErrInvalidArgument
	}
	job := cloneLineageRetryJob(selected)
	c := job.Items[job.Processed].Selection
	app, ok := m.eventSubscriptionAppLocked(job.AppID)
	if !ok {
		return false, ErrNotFound
	}
	result, err := m.retryEventExecutionLineageCaseLocked(app.AccountID, job.AppID, job.SubscriptionID, c.OutboxID, lineageJobCaseRequest(job, c), &c.AcceptedAt)
	if err = finishLineageRetryJobItem(&job, result, err); err != nil {
		return false, err
	}
	coverage := m.eventExecutionCoverage[job.SubscriptionID]
	coverage.jobs[job.RequestID] = job
	m.eventExecutionCoverage[job.SubscriptionID] = coverage
	return true, nil
}
