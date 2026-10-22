package state

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OperationTimelineCursor holds the last consumed position of every timeline
// source (ADR-1195). Each source advances independently, so a page never
// skips or repeats a record while the Operation keeps writing.
type OperationTimelineCursor struct {
	Event    int64
	Attempt  OperationTimelineAttemptKey
	Step     OperationTimelineStepKey
	Delivery OperationTimelineAttemptKey // replay generation, attempt number
}

type OperationTimelineAttemptKey struct{ Generation, Attempt int }

// OperationTimelineStepKey orders step attempts within the operation's run.
type OperationTimelineStepKey struct {
	StartedAt time.Time
	Step      string
	Attempt   int
}

func (k OperationTimelineAttemptKey) after(c OperationTimelineAttemptKey) bool {
	return k.Generation > c.Generation || k.Generation == c.Generation && k.Attempt > c.Attempt
}

func (k OperationTimelineStepKey) after(c OperationTimelineStepKey) bool {
	switch {
	case !k.StartedAt.Equal(c.StartedAt):
		return k.StartedAt.After(c.StartedAt)
	case k.Step != c.Step:
		return k.Step > c.Step
	}
	return k.Attempt > c.Attempt
}

// OperationTimelinePage is one merged page. Next is nil when every source was
// read to its end at read time.
type OperationTimelinePage struct {
	Response api.OperationTimelineResponse
	Next     *OperationTimelineCursor
}

type OperationTimelineStore interface {
	OperationTimeline(ctx context.Context, account, id string, cursor OperationTimelineCursor, limit int) (OperationTimelinePage, error)
}

type operationTimelineItem struct {
	entry   api.OperationTimelineEntry
	advance func(*OperationTimelineCursor)
}

// operationTimelineSource is one source's rows after its cursor, in source
// order. More reports rows beyond the ones read.
type operationTimelineSource struct {
	items []operationTimelineItem
	more  bool
}

func validOperationTimelineRequest(c OperationTimelineCursor, limit int) bool {
	return limit >= 1 && limit <= api.OperationHistoryPageMax && c.Event >= 0 &&
		validOperationTimelineKey(c.Attempt) && validOperationTimelineKey(c.Delivery) &&
		c.Step.Attempt >= 0 && c.Step.Attempt <= math.MaxInt32 &&
		len(c.Step.Step) <= api.OperationWorkflowNameMaxBytes
}

func validOperationTimelineKey(k OperationTimelineAttemptKey) bool {
	return k.Generation >= 0 && k.Generation <= math.MaxInt32 && k.Attempt >= 0 && k.Attempt <= math.MaxInt32
}

// mergeOperationTimeline takes the earliest head across sources until the page
// is full. Sources are passed in kind order, which breaks equal times.
func mergeOperationTimeline(sources []operationTimelineSource, cursor OperationTimelineCursor, limit int) ([]api.OperationTimelineEntry, *OperationTimelineCursor) {
	pos := make([]int, len(sources))
	entries := []api.OperationTimelineEntry{}
	for len(entries) < limit {
		best := -1
		var item operationTimelineItem
		for i, source := range sources {
			if pos[i] >= len(source.items) {
				continue
			}
			if head := source.items[pos[i]]; best < 0 || head.entry.At.Before(item.entry.At) {
				best, item = i, head
			}
		}
		if best < 0 {
			break
		}
		pos[best]++
		entries = append(entries, item.entry)
		item.advance(&cursor)
	}
	for i := range sources {
		if pos[i] < len(sources[i].items) || sources[i].more {
			return entries, &cursor
		}
	}
	return entries, nil
}

func operationTimelineEvents(page api.OperationEventsResponse, after int64, limit int) (operationTimelineSource, bool, error) {
	if page.ResyncRequired {
		if after > page.LatestSequence {
			return operationTimelineSource{}, false, ErrInvalidArgument
		}
		return operationTimelineSource{}, true, nil
	}
	source := operationTimelineSource{more: len(page.Events) >= limit}
	for i := range page.Events {
		event := page.Events[i]
		source.items = append(source.items, operationTimelineItem{
			entry:   api.OperationTimelineEntry{At: event.CreatedAt, Kind: api.OperationTimelineKindEvent, Event: &event},
			advance: func(c *OperationTimelineCursor) { c.Event = event.Sequence },
		})
	}
	return source, false, nil
}

type operationTimelineAttemptRow struct {
	key        OperationTimelineAttemptKey
	attempt    api.OperationTimelineAttempt
	instanceID string
}

func operationTimelineAttempts(rows []operationTimelineAttemptRow, limit int, now time.Time) operationTimelineSource {
	sort.Slice(rows, func(i, j int) bool { return rows[j].key.after(rows[i].key) })
	source := operationTimelineSource{more: len(rows) > limit}
	for _, row := range rows[:min(len(rows), limit)] {
		attempt, key := row.attempt, row.key
		attempt.Logs = operationAttemptLogs(row.instanceID, attempt.StartedAt, attempt.FinishedAt, now)
		source.items = append(source.items, operationTimelineItem{
			entry:   api.OperationTimelineEntry{At: attempt.StartedAt, Kind: api.OperationTimelineKindAttempt, Attempt: &attempt},
			advance: func(c *OperationTimelineCursor) { c.Attempt = key },
		})
	}
	return source
}

type operationTimelineStepRow struct {
	key     OperationTimelineStepKey
	attempt api.OperationTimelineAttempt
}

func operationTimelineSteps(rows []operationTimelineStepRow, limit int) operationTimelineSource {
	sort.Slice(rows, func(i, j int) bool { return rows[j].key.after(rows[i].key) })
	source := operationTimelineSource{more: len(rows) > limit}
	for _, row := range rows[:min(len(rows), limit)] {
		attempt, key := row.attempt, row.key
		source.items = append(source.items, operationTimelineItem{
			entry:   api.OperationTimelineEntry{At: attempt.StartedAt, Kind: api.OperationTimelineKindStep, Attempt: &attempt},
			advance: func(c *OperationTimelineCursor) { c.Step = key },
		})
	}
	return source
}

func operationTimelineDeliveries(rows []AppWebhookDeliveryAttempt, limit int) operationTimelineSource {
	sort.Slice(rows, func(i, j int) bool {
		return OperationTimelineAttemptKey{rows[j].ReplayGeneration, rows[j].AttemptNumber}.after(OperationTimelineAttemptKey{rows[i].ReplayGeneration, rows[i].AttemptNumber})
	})
	source := operationTimelineSource{more: len(rows) > limit}
	for _, a := range rows[:min(len(rows), limit)] {
		delivery := api.OperationDeliveryAttempt{ReplayGeneration: a.ReplayGeneration, AttemptNumber: a.AttemptNumber, Outcome: a.Outcome, ResponseCode: a.ResponseCode, ErrorCode: OperationDeliveryErrorCode(a.ResponseCode, a.Error), StartedAt: a.StartedAt, FinishedAt: a.FinishedAt, NextAttemptAt: a.NextAttemptAt}
		key := OperationTimelineAttemptKey{a.ReplayGeneration, a.AttemptNumber}
		source.items = append(source.items, operationTimelineItem{
			entry:   api.OperationTimelineEntry{At: a.StartedAt, Kind: api.OperationTimelineKindDelivery, Delivery: &delivery},
			advance: func(c *OperationTimelineCursor) { c.Delivery = key },
		})
	}
	return source
}

// operationWorkflowStepOutcome maps step attempt statuses onto the shared
// attempt outcome vocabulary.
func operationWorkflowStepOutcome(status string) string {
	switch status {
	case "retrying":
		return api.OperationAttemptRetry
	case "running", "succeeded", "failed":
		return status
	}
	return api.OperationAttemptUnknown
}

// operationAttemptLogs names the archive days an attempt's instance window
// touches, keeping the newest OperationTimelineLogDatesMax.
func operationAttemptLogs(instanceID string, since time.Time, until *time.Time, now time.Time) *api.OperationAttemptLogs {
	if instanceID == "" {
		return nil
	}
	const day = 24 * time.Hour
	end := now
	if until != nil {
		end = *until
	}
	if end.Before(since) {
		end = since
	}
	last := end.UTC().Truncate(day)
	first := since.UTC().Truncate(day)
	if earliest := last.Add(-(api.OperationTimelineLogDatesMax - 1) * day); first.Before(earliest) {
		first = earliest
	}
	logs := &api.OperationAttemptLogs{InstanceID: instanceID, Since: since, Until: cloneWorkflowTime(until), Correlation: api.OperationAttemptLogCorrelation}
	for d := first; !d.After(last); d = d.Add(day) {
		logs.ArchiveDates = append(logs.ArchiveDates, d.Format(time.DateOnly))
	}
	return logs
}

func operationTimelinePage(op Operation, sources []operationTimelineSource, eventsExpired bool, cursor OperationTimelineCursor, limit int) OperationTimelinePage {
	entries, next := mergeOperationTimeline(sources, cursor, limit)
	return OperationTimelinePage{Next: next, Response: api.OperationTimelineResponse{
		OperationID: op.ID, State: op.State, Generation: op.Generation, Entries: entries, EventsExpired: eventsExpired,
	}}
}

func (m *MemStore) OperationTimeline(ctx context.Context, account, id string, cursor OperationTimelineCursor, limit int) (OperationTimelinePage, error) {
	if !validOperationTimelineRequest(cursor, limit) {
		return OperationTimelinePage{}, ErrInvalidArgument
	}
	fetch := min(limit, api.OperationEventsPageMax)
	page, err := m.OperationEvents(ctx, account, "", id, cursor.Event, fetch)
	if err != nil {
		return OperationTimelinePage{}, err
	}
	events, expired, err := operationTimelineEvents(page, cursor.Event, fetch)
	if err != nil {
		return OperationTimelinePage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	op, ok := data.operations[id]
	if !ok || op.AccountID != account {
		return OperationTimelinePage{}, ErrNotFound
	}
	now := time.Now().UTC()
	sources := []operationTimelineSource{events,
		operationTimelineAttempts(m.operationTimelineAttemptsLocked(data, id, cursor.Attempt), limit, now),
		operationTimelineSteps(m.operationTimelineStepsLocked(data, id, cursor.Step), limit),
		operationTimelineDeliveries(m.operationTimelineDeliveriesLocked(data, op, cursor.Delivery), limit)}
	return operationTimelinePage(op, sources, expired, cursor, limit), nil
}

func (m *MemStore) operationTimelineAttemptsLocked(data *operationMemory, id string, after OperationTimelineAttemptKey) []operationTimelineAttemptRow {
	rows := []operationTimelineAttemptRow{}
	for invocation, operation := range data.executions {
		if operation != id {
			continue
		}
		generation := data.generations[invocation]
		for _, h := range m.invocationAttemptHistory {
			key := OperationTimelineAttemptKey{generation, h.Attempt}
			if h.InvocationID != invocation || h.ReplayGeneration != 0 || !key.after(after) {
				continue
			}
			rows = append(rows, operationTimelineAttemptRow{key: key, instanceID: data.attemptInstances[operationAttemptKey{id, generation, h.Attempt}], attempt: api.OperationTimelineAttempt{
				Generation: generation, ExecutionID: invocation, Attempt: h.Attempt, Outcome: h.Outcome, Error: h.ErrorDetail,
				StartedAt: h.StartedAt, FinishedAt: cloneWorkflowTime(h.FinishedAt), NextAttemptAt: cloneWorkflowTime(h.NextAttemptAt),
			}})
		}
	}
	return rows
}

func (m *MemStore) operationTimelineStepsLocked(data *operationMemory, id string, after OperationTimelineStepKey) []operationTimelineStepRow {
	rows := []operationTimelineStepRow{}
	runs := map[string]bool{}
	for _, execution := range data.workflowExecutions[id] {
		runs[execution.WorkflowRunID] = true
	}
	for run := range runs {
		for key, a := range m.workflowStepAttempts {
			k := OperationTimelineStepKey{a.StartedAt, key.stepName, key.attempt}
			if key.runID != run || !k.after(after) {
				continue
			}
			rows = append(rows, operationTimelineStepRow{key: k, attempt: api.OperationTimelineAttempt{
				ExecutionID: run, Step: key.stepName, Attempt: key.attempt,
				Outcome: operationWorkflowStepOutcome(a.Status), HTTPStatus: cloneWorkflowInt(a.HTTPStatus), Error: derefWorkflowString(a.Error),
				StartedAt: a.StartedAt, FinishedAt: cloneWorkflowTime(a.FinishedAt), NextAttemptAt: cloneWorkflowTime(a.NextAttemptAt),
			}})
		}
	}
	return rows
}

func (m *MemStore) operationTimelineDeliveriesLocked(data *operationMemory, op Operation, after OperationTimelineAttemptKey) []AppWebhookDeliveryAttempt {
	d, ok := m.appWebhookDeliveries[op.CompletionDelivery.DeliveryID]
	if !ok || !operationDeliveryOwned(op, data.definitions[op.DefinitionID], d) {
		return nil
	}
	rows := []AppWebhookDeliveryAttempt{}
	for _, a := range m.appWebhookDeliveryAttempts[d.ID] {
		if (OperationTimelineAttemptKey{a.ReplayGeneration, a.AttemptNumber}).after(after) {
			rows = append(rows, a)
		}
	}
	return rows
}

func derefWorkflowString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func (s *PgStore) OperationTimeline(ctx context.Context, account, id string, cursor OperationTimelineCursor, limit int) (OperationTimelinePage, error) {
	if !validOperationTimelineRequest(cursor, limit) {
		return OperationTimelinePage{}, ErrInvalidArgument
	}
	fetch := min(limit, api.OperationEventsPageMax)
	page, err := s.OperationEvents(ctx, account, "", id, cursor.Event, fetch)
	if err != nil {
		return OperationTimelinePage{}, err
	}
	events, expired, err := operationTimelineEvents(page, cursor.Event, fetch)
	if err != nil {
		return OperationTimelinePage{}, err
	}
	op, err := s.OperationByID(ctx, account, "", id)
	if err != nil {
		return OperationTimelinePage{}, err
	}
	attempts, err := s.operationTimelineAttempts(ctx, op, cursor.Attempt, limit)
	if err != nil {
		return OperationTimelinePage{}, err
	}
	steps, err := s.operationTimelineSteps(ctx, op, cursor.Step, limit)
	if err != nil {
		return OperationTimelinePage{}, err
	}
	deliveries, err := s.operationTimelineDeliveries(ctx, account, op, cursor.Delivery, limit)
	if err != nil {
		return OperationTimelinePage{}, err
	}
	now := time.Now().UTC()
	sources := []operationTimelineSource{events, operationTimelineAttempts(attempts, limit, now), operationTimelineSteps(steps, limit), operationTimelineDeliveries(deliveries, limit)}
	return operationTimelinePage(op, sources, expired, cursor, limit), nil
}

func (s *PgStore) operationTimelineAttempts(ctx context.Context, op Operation, after OperationTimelineAttemptKey, limit int) ([]operationTimelineAttemptRow, error) {
	i, _ := operationUUID(op.ID)
	raw, err := sqlc.New().OperationTimelineAttempts(ctx, s.pool, sqlc.OperationTimelineAttemptsParams{OperationID: i, AfterGeneration: int32(after.Generation), AfterAttempt: int32(after.Attempt), PageLimit: int32(limit + 1)})
	if err != nil {
		return nil, fmt.Errorf("state: read operation timeline attempts: %w", mapErr(err))
	}
	rows := make([]operationTimelineAttemptRow, 0, len(raw))
	for _, r := range raw {
		rows = append(rows, operationTimelineAttemptRow{key: OperationTimelineAttemptKey{int(r.Generation), int(r.Attempt)}, instanceID: r.InstanceID, attempt: api.OperationTimelineAttempt{
			Generation: int(r.Generation), ExecutionID: r.ExecutionID, Attempt: int(r.Attempt), Outcome: r.Outcome, Error: r.ErrorDetail,
			StartedAt: r.StartedAt.Time, FinishedAt: operationTimelineTime(r.FinishedAt), NextAttemptAt: operationTimelineTime(r.NextAttemptAt),
		}})
	}
	return rows, nil
}

func (s *PgStore) operationTimelineSteps(ctx context.Context, op Operation, after OperationTimelineStepKey, limit int) ([]operationTimelineStepRow, error) {
	i, _ := operationUUID(op.ID)
	raw, err := sqlc.New().OperationTimelineStepAttempts(ctx, s.pool, sqlc.OperationTimelineStepAttemptsParams{OperationID: i, AfterStartedAt: pgtype.Timestamptz{Time: after.StartedAt, Valid: true}, AfterStep: after.Step, AfterAttempt: int32(after.Attempt), PageLimit: int32(limit + 1)})
	if err != nil {
		return nil, fmt.Errorf("state: read operation timeline steps: %w", mapErr(err))
	}
	rows := make([]operationTimelineStepRow, 0, len(raw))
	for _, r := range raw {
		var status *int
		if r.HttpStatus.Valid {
			v := int(r.HttpStatus.Int32)
			status = &v
		}
		rows = append(rows, operationTimelineStepRow{key: OperationTimelineStepKey{r.StartedAt.Time, r.StepName, int(r.Attempt)}, attempt: api.OperationTimelineAttempt{
			ExecutionID: r.ExecutionID, Step: r.StepName, Attempt: int(r.Attempt), Outcome: operationWorkflowStepOutcome(r.Status),
			HTTPStatus: status, Error: r.Error, StartedAt: r.StartedAt.Time, FinishedAt: operationTimelineTime(r.FinishedAt), NextAttemptAt: operationTimelineTime(r.NextAttemptAt),
		}})
	}
	return rows, nil
}

func (s *PgStore) operationTimelineDeliveries(ctx context.Context, account string, op Operation, after OperationTimelineAttemptKey, limit int) ([]AppWebhookDeliveryAttempt, error) {
	if op.CompletionDelivery.DeliveryID == "" {
		return nil, nil
	}
	report, err := s.OperationDeliverySnapshot(ctx, account, op.ID)
	if err != nil || report.DeliveryID == "" || report.WebhookID == "" {
		return nil, err
	}
	d, _ := operationUUID(report.DeliveryID)
	w, _ := operationUUID(report.WebhookID)
	a, _ := operationUUID(account)
	raw, err := sqlc.New().OperationTimelineDeliveryAttempts(ctx, s.pool, sqlc.OperationTimelineDeliveryAttemptsParams{DeliveryID: d, WebhookID: w, AccountID: a, AfterGeneration: int32(after.Generation), AfterAttempt: int32(after.Attempt), PageLimit: int32(limit + 1)})
	if err != nil {
		return nil, fmt.Errorf("state: read operation timeline deliveries: %w", mapErr(err))
	}
	rows := make([]AppWebhookDeliveryAttempt, 0, len(raw))
	for _, r := range raw {
		rows = append(rows, AppWebhookDeliveryAttempt{DeliveryID: report.DeliveryID, ReplayGeneration: int(r.ReplayGeneration), AttemptNumber: int(r.AttemptNumber), Outcome: r.Outcome,
			ResponseCode: int(r.ResponseCode), Error: r.Error, StartedAt: r.StartedAt.Time, FinishedAt: r.FinishedAt.Time, NextAttemptAt: operationTimelineTime(r.NextAttemptAt)})
	}
	return rows, nil
}

func operationTimelineTime(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time
	return &t
}

var (
	_ OperationTimelineStore = (*MemStore)(nil)
	_ OperationTimelineStore = (*PgStore)(nil)
)
