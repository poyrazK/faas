package state

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type WorkflowEventReplayPreviewStore interface {
	PreviewWorkflowEventReplay(context.Context, string, WorkflowEventReplayPreviewQuery) (api.WorkflowEventReplayPreviewResponse, error)
}

type WorkflowEventReplayPreviewQuery struct {
	AppID        string
	WorkflowName string
	api.WorkflowEventReplayPreviewOptions
}

type workflowEventReplayPreviewCursor struct {
	Version      int       `json:"v"`
	AccountID    string    `json:"account_id"`
	AppID        string    `json:"app_id"`
	WorkflowName string    `json:"workflow_name"`
	From         time.Time `json:"from"`
	Until        time.Time `json:"until"`
	CutoffAt     time.Time `json:"cutoff_at"`
	AcceptedAt   time.Time `json:"accepted_at"`
	OutboxID     int64     `json:"outbox_id"`
}

const workflowEventReplayPreviewCursorPrefix = "werp1."

type workflowEventReplayPreviewCandidate struct {
	id                int64
	acceptedAt        time.Time
	source            string
	eventID           string
	eventType         string
	schemaVersion     string
	payload           []byte
	snapshotCaptured  bool
	recipient         []byte
	routingState      string
	admissionRecorded bool
	workflowRunID     string
	workflowRunStatus string
}

func validateWorkflowEventReplayPreviewQuery(accountID string, q *WorkflowEventReplayPreviewQuery) (workflowEventReplayPreviewCursor, error) {
	for _, id := range []string{accountID, q.AppID} {
		if _, err := uuid.Parse(id); err != nil {
			return workflowEventReplayPreviewCursor{}, fmt.Errorf("%w: account and app identifiers must be UUIDs", ErrEventReplayPreviewQuery)
		}
	}
	if strings.TrimSpace(q.WorkflowName) == "" || len(q.WorkflowName) > 256 {
		return workflowEventReplayPreviewCursor{}, fmt.Errorf("%w: workflow name must contain 1 to 256 non-whitespace bytes", ErrEventReplayPreviewQuery)
	}
	q.AppID = canonicalMemUUID(q.AppID)
	q.From, q.Until = q.From.UTC(), q.Until.UTC()
	if err := q.WorkflowEventReplayPreviewOptions.Validate(); err != nil {
		return workflowEventReplayPreviewCursor{}, fmt.Errorf("%w: %w", ErrEventReplayPreviewQuery, err)
	}
	if q.Limit == 0 {
		q.Limit = api.EventReplayPreviewPageDefault
	}
	c := workflowEventReplayPreviewCursor{Version: 1, AccountID: canonicalMemUUID(accountID), AppID: q.AppID,
		WorkflowName: q.WorkflowName, From: q.From, Until: q.Until, CutoffAt: minTime(time.Now().UTC(), q.Until)}
	if !q.From.Before(c.CutoffAt) {
		return c, fmt.Errorf("%w: from must precede the current acceptance cutoff", ErrEventReplayPreviewQuery)
	}
	if q.After == "" {
		return c, nil
	}
	invalid := fmt.Errorf("%w: invalid or mismatched workflow replay preview cursor", ErrEventReplayPreviewQuery)
	if !strings.HasPrefix(q.After, workflowEventReplayPreviewCursorPrefix) {
		return c, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(q.After, workflowEventReplayPreviewCursorPrefix))
	var prior workflowEventReplayPreviewCursor
	if err != nil || json.Unmarshal(data, &prior) != nil || prior.Version != 1 || prior.AccountID != c.AccountID ||
		prior.AppID != c.AppID || prior.WorkflowName != c.WorkflowName || !prior.From.Equal(q.From) || !prior.Until.Equal(q.Until) ||
		prior.CutoffAt.IsZero() || prior.AcceptedAt.IsZero() || prior.CutoffAt.After(c.CutoffAt) || !q.From.Before(prior.CutoffAt) ||
		prior.OutboxID <= 0 || prior.AcceptedAt.Before(q.From) || !prior.AcceptedAt.Before(prior.CutoffAt) {
		return c, invalid
	}
	return prior, nil
}

func newWorkflowEventReplayPreview(appSlug string, query WorkflowEventReplayPreviewQuery, c workflowEventReplayPreviewCursor, now time.Time, earliest *time.Time) api.WorkflowEventReplayPreviewResponse {
	return api.WorkflowEventReplayPreviewResponse{
		AppSlug: appSlug, WorkflowName: query.WorkflowName, From: c.From, Until: c.Until, CutoffAt: c.CutoffAt,
		ObservedAt: now, Coverage: "retained_envelopes", Retention: api.EventReplayPreviewRetention{
			SettledRetentionSeconds: int64(PublishedEventIdentityRetention / time.Second), EarliestRetainedAt: earliest,
		}, Matches: []api.WorkflowEventReplayPreviewMatch{},
	}
}

func evaluateWorkflowEventReplayPreview(ctx context.Context, out api.WorkflowEventReplayPreviewResponse, accountID string,
	c workflowEventReplayPreviewCursor, candidates []workflowEventReplayPreviewCandidate, limit int) (api.WorkflowEventReplayPreviewResponse, error) {
	hasMore := len(candidates) > limit
	if hasMore {
		candidates = candidates[:limit]
	}
	out.ScannedCount = len(candidates)
	for _, row := range candidates {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if !row.snapshotCaptured {
			out.UnknownRecipientCount++
			continue
		}
		if len(row.recipient) == 0 {
			out.NotCapturedCount++
			continue
		}
		out.CapturedCount++
		var recipient PublishedEventRecipient
		if err := json.Unmarshal(row.recipient, &recipient); err != nil {
			return out, fmt.Errorf("decode captured workflow recipient: %w", err)
		}
		var envelope eventcontract.Envelope
		if err := json.Unmarshal(row.payload, &envelope); err != nil {
			return out, fmt.Errorf("decode retained event: %w", err)
		}
		if err := envelope.Validate(); err != nil {
			return out, fmt.Errorf("validate retained event: %w", err)
		}
		if !sameMemUUID(envelope.AccountID, accountID) {
			return out, errors.New("retained workflow event tenant mismatch")
		}
		matched, err := (eventcontract.Subscription{ID: recipient.ID, AccountID: recipient.AccountID,
			Source: recipient.Source, Type: recipient.Type, Filter: recipient.Filter}).Match(envelope)
		if err != nil {
			return out, fmt.Errorf("match captured workflow trigger: %w", err)
		}
		if !matched {
			out.FilterMismatchCount++
			continue
		}
		out.MatchedCount++
		if row.admissionRecorded {
			out.AlreadyAdmittedCount++
		} else {
			out.PotentialAdmissionCount++
		}
		out.Matches = append(out.Matches, api.WorkflowEventReplayPreviewMatch{
			EventID: row.eventID, EventSource: row.source, EventType: row.eventType, SchemaVersion: row.schemaVersion,
			AcceptedAt: row.acceptedAt, OriginalRecipient: "captured", RoutingState: workflowPreviewRoutingState(row.routingState),
			FilterMatched: true, AdmissionRecorded: row.admissionRecorded, WorkflowRunID: row.workflowRunID,
			WorkflowRunStatus: row.workflowRunStatus,
		})
	}
	if hasMore {
		last := candidates[len(candidates)-1]
		c.AcceptedAt, c.OutboxID = last.acceptedAt, last.id
		data, err := json.Marshal(c)
		if err != nil {
			return out, fmt.Errorf("encode workflow replay preview cursor: %w", err)
		}
		out.NextAfter = workflowEventReplayPreviewCursorPrefix + base64.RawURLEncoding.EncodeToString(data)
	}
	return out, ctx.Err()
}

func workflowPreviewRoutingState(state string) string {
	if state == "" {
		return PublishedEventRecipientPending
	}
	return state
}

func (s *PgStore) PreviewWorkflowEventReplay(ctx context.Context, accountID string, query WorkflowEventReplayPreviewQuery) (api.WorkflowEventReplayPreviewResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventReplayPreviewReadTimeout)
	defer cancel()
	c, err := validateWorkflowEventReplayPreviewQuery(accountID, &query)
	if err != nil {
		return api.WorkflowEventReplayPreviewResponse{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.WorkflowEventReplayPreviewResponse{}, fmt.Errorf("begin workflow event preview: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.EventReplayPreviewReadTimeout)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := sqlc.New()
	target, err := q.WorkflowEventReplayPreviewApp(ctx, tx, sqlc.WorkflowEventReplayPreviewAppParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(query.AppID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.WorkflowEventReplayPreviewResponse{}, ErrNotFound
	}
	if err != nil {
		return api.WorkflowEventReplayPreviewResponse{}, fmt.Errorf("read workflow replay preview app: %w", err)
	}
	now := time.Now().UTC()
	first, err := q.EventReplayPreviewEarliestRetained(ctx, tx, mustPgUUID(accountID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return api.WorkflowEventReplayPreviewResponse{}, fmt.Errorf("read earliest retained event: %w", err)
	}
	var earliest *time.Time
	if first.Valid {
		t := timeFromPgtype(first)
		earliest = &t
	}
	afterAt := c.AcceptedAt
	if afterAt.IsZero() {
		afterAt = query.From
	}
	rows, err := q.WorkflowEventReplayPreviewCandidates(ctx, tx, sqlc.WorkflowEventReplayPreviewCandidatesParams{
		AccountID: mustPgUUID(accountID), AppID: query.AppID, WorkflowName: query.WorkflowName,
		FromAt: eventReplayPreviewPgBoundary(query.From), CutoffAt: eventReplayPreviewPgBoundary(c.CutoffAt),
		AfterAt: pgtype.Timestamptz{Time: afterAt, Valid: true}, AfterID: c.OutboxID, PageLimit: int32(query.Limit + 1),
	})
	if err != nil {
		return api.WorkflowEventReplayPreviewResponse{}, fmt.Errorf("read retained workflow event candidates: %w", err)
	}
	candidates := make([]workflowEventReplayPreviewCandidate, len(rows))
	for i, r := range rows {
		candidates[i] = workflowEventReplayPreviewCandidate{r.ID, timeFromPgtype(r.CreatedAt), r.Source, r.EventID, r.EventType,
			r.SchemaVersion, r.Payload, r.SnapshotCaptured, r.Recipient, r.RoutingState, r.AdmissionRecorded, r.WorkflowRunID, r.WorkflowRunStatus}
	}
	out, err := evaluateWorkflowEventReplayPreview(ctx, newWorkflowEventReplayPreview(target.Slug, query, c, now, earliest), accountID, c, candidates, query.Limit)
	if err != nil {
		return out, err
	}
	if err := tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("commit workflow event preview read: %w", err)
	}
	return out, nil
}

func (m *MemStore) PreviewWorkflowEventReplay(ctx context.Context, accountID string, query WorkflowEventReplayPreviewQuery) (api.WorkflowEventReplayPreviewResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventReplayPreviewReadTimeout)
	defer cancel()
	c, err := validateWorkflowEventReplayPreviewQuery(accountID, &query)
	if err != nil {
		return api.WorkflowEventReplayPreviewResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.eventSubscriptionAppLocked(query.AppID)
	if !ok || app.Status == AppDeleted || !sameMemUUID(app.AccountID, accountID) {
		return api.WorkflowEventReplayPreviewResponse{}, ErrNotFound
	}
	candidates := make([]workflowEventReplayPreviewCandidate, 0, query.Limit+1)
	var earliest *time.Time
	for key, work := range m.eventFanout {
		if err := ctx.Err(); err != nil {
			return api.WorkflowEventReplayPreviewResponse{}, err
		}
		if !strings.HasPrefix(key, canonicalMemUUID(accountID)+"\x00") {
			continue
		}
		if earliest == nil || work.CreatedAt.Before(*earliest) {
			t := work.CreatedAt
			earliest = &t
		}
		if work.CreatedAt.Before(query.From) || !work.CreatedAt.Before(c.CutoffAt) ||
			(!c.AcceptedAt.IsZero() && (work.CreatedAt.Before(c.AcceptedAt) || work.CreatedAt.Equal(c.AcceptedAt) && work.ID <= c.OutboxID)) {
			continue
		}
		var identity publishedEventIdentity
		if err := json.Unmarshal(work.Payload, &identity); err != nil {
			return api.WorkflowEventReplayPreviewResponse{}, fmt.Errorf("decode retained event: %w", err)
		}
		candidate := workflowEventReplayPreviewCandidate{id: work.ID, acceptedAt: work.CreatedAt,
			source: identity.Source, eventID: identity.ID, eventType: identity.Type, schemaVersion: identity.SchemaVersion,
			payload: work.Payload, snapshotCaptured: work.SnapshotCaptured}
		if work.SnapshotCaptured {
			for _, recipient := range work.RecipientSnapshot {
				if !sameMemUUID(recipient.AppID, query.AppID) || len(recipient.Workflow) == 0 {
					continue
				}
				var spec api.WorkflowSpec
				if json.Unmarshal(recipient.Workflow, &spec) == nil && spec.Name == query.WorkflowName {
					candidate.recipient, err = json.Marshal(recipient)
					if err != nil {
						return api.WorkflowEventReplayPreviewResponse{}, fmt.Errorf("encode captured workflow recipient: %w", err)
					}
					progress := work.RecipientProgress[recipient.ID]
					candidate.routingState = progress.State
					if routed := work.routingRecipients[recipient.ID]; routed != nil && routed.State != "" {
						candidate.routingState = routed.State
					}
					key := eventWorkflowReceiptKey(work.ID, recipient.ID)
					runID, recorded := m.eventWorkflowReceipts[key]
					candidate.admissionRecorded = recorded
					if recorded && runID != "" {
						candidate.workflowRunID = runID
						if run, exists := m.workflowRuns[runID]; exists {
							candidate.workflowRunStatus = run.Status
						} else {
							candidate.workflowRunID = ""
						}
					}
					break
				}
			}
		}
		insert := sort.Search(len(candidates), func(i int) bool {
			return candidates[i].acceptedAt.After(candidate.acceptedAt) || candidates[i].acceptedAt.Equal(candidate.acceptedAt) && candidates[i].id > candidate.id
		})
		if insert > query.Limit {
			continue
		}
		candidates = append(candidates, workflowEventReplayPreviewCandidate{})
		copy(candidates[insert+1:], candidates[insert:])
		candidates[insert] = candidate
		if len(candidates) > query.Limit+1 {
			candidates = candidates[:query.Limit+1]
		}
	}
	now := time.Now().UTC()
	out, err := evaluateWorkflowEventReplayPreview(ctx, newWorkflowEventReplayPreview(app.Slug, query, c, now, earliest), accountID, c, candidates, query.Limit)
	if err != nil {
		return out, err
	}
	return out, nil
}
