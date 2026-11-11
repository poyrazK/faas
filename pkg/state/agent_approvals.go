package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrTooManyAgentApprovals refuses a new approval request from a key that
// already has api.MaxPendingAgentApprovals undecided ones.
var ErrTooManyAgentApprovals = errors.New("too many pending agent approvals")

// AgentApproval is a request of an agent key that waits for, or holds, a
// person's decision (ADR-20261104140058).
type AgentApproval struct {
	ID        string
	KeyID     string
	AccountID string // the member who connected the agent
	OrgID     string // the organization the key acts in
	Category  string
	Method    string
	Path      string
	// BodySHA256 binds the request body; nil when the body is not bound.
	BodySHA256 []byte
	Status     string
	DecidedBy  string
	CreatedAt  time.Time
	DecidedAt  time.Time
	ConsumedAt time.Time
	ExpiresAt  time.Time
	// Note is the person's reason for a denial; empty otherwise.
	Note string
	// Reason is what the agent said the request is for
	// (Gregale-Agent-Reason, ADR-20261110182236); empty when it gave none.
	Reason string
	// ReviewURL is the review page, and AgentName the connection's name, for
	// a new request's webhook event and notification (ADR-20261107140000
	// §4, §5). Neither is stored on the request.
	ReviewURL string
	AgentName string
}

// AgentApprovalBinding is what an approval allows: one request of the key
// with this method, path and body.
type AgentApprovalBinding struct {
	KeyID      string
	Method     string
	Path       string
	BodySHA256 []byte
}

// EffectiveStatus is the request's status, with an undecided or unused
// approval past its expiry reported as expired.
func (a AgentApproval) EffectiveStatus(now time.Time) string {
	if (a.Status == api.AgentApprovalPending || a.Status == api.AgentApprovalApproved) && !now.Before(a.ExpiresAt) {
		return api.AgentApprovalExpired
	}
	return a.Status
}

// AgentApprovalStore keeps agent keys' approval categories and their
// approval requests. apid is the only writer.
type AgentApprovalStore interface {
	// SetAPIKeyApprovalCategories records the categories a key must have
	// approved; set once, when the key is minted. ErrConflict when already
	// set or a category is unknown.
	SetAPIKeyApprovalCategories(ctx context.Context, keyID string, categories []string) error
	// APIKeyApprovalCategories returns the key's categories and whether it
	// chose any (false: every category applies).
	APIKeyApprovalCategories(ctx context.Context, keyID string) ([]string, bool, error)
	// ConsumeAgentApproval uses one approved, unexpired request with the
	// binding. It reports false when there is none.
	ConsumeAgentApproval(ctx context.Context, binding AgentApprovalBinding) (string, bool, error)
	// RequestAgentApproval returns the key's pending request with the same
	// binding, or records a new one, with request.ID when it is set, and
	// enqueues its agent.approval.requested webhook event.
	// ErrTooManyAgentApprovals when the key has too many undecided requests.
	RequestAgentApproval(ctx context.Context, request AgentApproval) (AgentApproval, error)
	// AgentApproval returns one request.
	AgentApproval(ctx context.Context, id string) (AgentApproval, error)
	// DecideAgentApproval approves or denies a pending, unexpired request; a
	// denial may carry a note. It enqueues the agent.approval.approved or
	// .denied webhook event. ErrNotFound when there is no such request.
	DecideAgentApproval(ctx context.Context, id string, approve bool, deciderID, note string) error
	// RecentAgentApprovalOutcome returns the key's most recent request with
	// the binding when it was denied, or expired undecided or unused, within
	// window (ADR-20261107140000 §6). It reports false otherwise.
	RecentAgentApprovalOutcome(ctx context.Context, binding AgentApprovalBinding, window time.Duration) (AgentApproval, bool, error)
	// ListAgentApprovalsForKey returns the key's requests, newest first.
	ListAgentApprovalsForKey(ctx context.Context, keyID string, limit int) ([]AgentApproval, error)
	// ListPendingAgentApprovals returns the pending requests of agents the
	// account connected or that act in one of the organizations.
	ListPendingAgentApprovals(ctx context.Context, accountID string, orgIDs []string, limit int) ([]AgentApproval, error)
}

func validApprovalCategories(categories []string) bool {
	for _, category := range categories {
		if !slices.Contains(api.AgentApprovalCategories, category) {
			return false
		}
	}
	return true
}

func (s *PgStore) SetAPIKeyApprovalCategories(ctx context.Context, keyID string, categories []string) error {
	if !validApprovalCategories(categories) {
		return ErrConflict
	}
	return mapErr(sqlc.New().InsertAPIKeyApprovalCategories(ctx, s.pool, sqlc.InsertAPIKeyApprovalCategoriesParams{
		KeyID: mustPgUUID(keyID), Categories: nonNilStrings(categories),
	}))
}

func (s *PgStore) APIKeyApprovalCategories(ctx context.Context, keyID string) ([]string, bool, error) {
	categories, err := sqlc.New().GetAPIKeyApprovalCategories(ctx, s.pool, mustPgUUID(keyID))
	if err != nil {
		if err = mapErr(err); errors.Is(err, ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return nonNilStrings(categories), true, nil
}

func (s *PgStore) ConsumeAgentApproval(ctx context.Context, binding AgentApprovalBinding) (string, bool, error) {
	id, err := sqlc.New().ConsumeAgentApproval(ctx, s.pool, sqlc.ConsumeAgentApprovalParams{
		KeyID: mustPgUUID(binding.KeyID), Method: binding.Method, Path: binding.Path, BodySha256: binding.BodySHA256,
	})
	if err != nil {
		if err = mapErr(err); errors.Is(err, ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return pgUUIDString(id), true, nil
}

func (s *PgStore) RequestAgentApproval(ctx context.Context, request AgentApproval) (AgentApproval, error) {
	q := sqlc.New()
	key := mustPgUUID(request.KeyID)
	if err := q.PruneAgentApprovals(ctx, s.pool, key); err != nil {
		return AgentApproval{}, mapErr(err)
	}
	existing, err := q.FindPendingAgentApproval(ctx, s.pool, sqlc.FindPendingAgentApprovalParams{
		KeyID: key, Method: request.Method, Path: request.Path, BodySha256: request.BodySHA256,
	})
	if err == nil {
		return s.AgentApproval(ctx, pgUUIDString(existing.ID))
	}
	if err = mapErr(err); !errors.Is(err, ErrNotFound) {
		return AgentApproval{}, err
	}
	pending, err := q.CountPendingAgentApprovals(ctx, s.pool, key)
	if err != nil {
		return AgentApproval{}, mapErr(err)
	}
	if int(pending) >= api.MaxPendingAgentApprovals {
		return AgentApproval{}, ErrTooManyAgentApprovals
	}
	if _, err := uuid.Parse(request.ID); err != nil {
		request.ID = uuid.NewString()
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AgentApproval{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := q.InsertAgentApproval(ctx, tx, sqlc.InsertAgentApprovalParams{
		ID: mustPgUUID(request.ID), KeyID: key, AccountID: mustPgUUID(request.AccountID), OrgID: mustPgUUID(request.OrgID),
		Category: request.Category, Method: request.Method, Path: request.Path, BodySha256: request.BodySHA256,
		ExpiresAt: pgTime(request.ExpiresAt), Reason: pgText(request.Reason),
	}); err != nil {
		return AgentApproval{}, mapErr(err)
	}
	request.Status = api.AgentApprovalPending
	if err := enqueueAgentApprovalEvent(ctx, q, tx, api.AgentApprovalRequestedWebhookEvent, request.ID, request); err != nil {
		return AgentApproval{}, err
	}
	if err := insertAgentApprovalNotification(ctx, q, tx, request); err != nil {
		return AgentApproval{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentApproval{}, mapErr(err)
	}
	return s.AgentApproval(ctx, request.ID)
}

// enqueueAgentApprovalEvent queues one agent.approval.* delivery for each
// account receiver of the connector subscribed to event (ADR-20261107140000
// §4). sourceID is unique per event of a request.
func enqueueAgentApprovalEvent(ctx context.Context, q *sqlc.Queries, tx sqlc.DBTX, event, sourceID string, a AgentApproval) error {
	payload := api.AgentApprovalWebhookPayload{
		ApprovalID: a.ID, KeyID: a.KeyID, OrgID: a.OrgID, Category: a.Category, Method: a.Method, Path: a.Path,
		Status: a.Status, ExpiresAt: a.ExpiresAt.UTC().Format(time.RFC3339), Note: a.Note, Reason: a.Reason,
	}
	if event == api.AgentApprovalRequestedWebhookEvent {
		payload.ReviewURL = a.ReviewURL
	}
	if !a.DecidedAt.IsZero() {
		payload.DecidedAt = a.DecidedAt.UTC().Format(time.RFC3339)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = q.InsertAgentApprovalWebhookDeliveries(ctx, tx, sqlc.InsertAgentApprovalWebhookDeliveriesParams{
		Event: event, Payload: body, SourceEventID: mustPgUUID(sourceID), AccountID: mustPgUUID(a.AccountID),
	})
	return mapErr(err)
}

func (s *PgStore) AgentApproval(ctx context.Context, id string) (AgentApproval, error) {
	if _, err := uuid.Parse(id); err != nil {
		return AgentApproval{}, ErrNotFound
	}
	row, err := sqlc.New().GetAgentApproval(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return AgentApproval{}, mapErr(err)
	}
	return agentApprovalFromRow(row), nil
}

func (s *PgStore) DecideAgentApproval(ctx context.Context, id string, approve bool, deciderID, note string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	status, decisionNote := decisionStatusNote(approve, note)
	q := sqlc.New()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := q.DecideAgentApproval(ctx, tx, sqlc.DecideAgentApprovalParams{
		Status: status, DecidedBy: mustPgUUID(deciderID), DecisionNote: pgText(decisionNote), ID: mustPgUUID(id),
	})
	if err != nil {
		return mapErr(err) // no row: ErrNotFound
	}
	event := api.AgentApprovalDeniedWebhookEvent
	if approve {
		event = api.AgentApprovalApprovedWebhookEvent
	}
	// A request is decided once, so a fresh id keys its decision's deliveries.
	if err := enqueueAgentApprovalEvent(ctx, q, tx, event, uuid.NewString(), agentApprovalFromRow(row)); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}

// decisionStatusNote is the status a decision writes and the note it keeps:
// only a denial keeps one, trimmed and cut to api.MaxAgentApprovalNoteChars.
func decisionStatusNote(approve bool, note string) (string, string) {
	if approve {
		return api.AgentApprovalApproved, ""
	}
	note = strings.TrimSpace(note)
	if runes := []rune(note); len(runes) > api.MaxAgentApprovalNoteChars {
		note = strings.TrimSpace(string(runes[:api.MaxAgentApprovalNoteChars]))
	}
	return api.AgentApprovalDenied, note
}

func (s *PgStore) RecentAgentApprovalOutcome(ctx context.Context, binding AgentApprovalBinding, window time.Duration) (AgentApproval, bool, error) {
	row, err := sqlc.New().RecentAgentApprovalOutcome(ctx, s.pool, sqlc.RecentAgentApprovalOutcomeParams{
		KeyID: mustPgUUID(binding.KeyID), Method: binding.Method, Path: binding.Path, BodySha256: binding.BodySHA256,
		WindowSeconds: int32(window / time.Second),
	})
	if err != nil {
		if err = mapErr(err); errors.Is(err, ErrNotFound) {
			return AgentApproval{}, false, nil
		}
		return AgentApproval{}, false, err
	}
	return agentApprovalFromRow(row), true, nil
}

func (s *PgStore) ListAgentApprovalsForKey(ctx context.Context, keyID string, limit int) ([]AgentApproval, error) {
	rows, err := sqlc.New().ListAgentApprovalsForKey(ctx, s.pool, sqlc.ListAgentApprovalsForKeyParams{KeyID: mustPgUUID(keyID), MaxRows: int32(limit)})
	if err != nil {
		return nil, mapErr(err)
	}
	return agentApprovalsFromRows(rows), nil
}

func (s *PgStore) ListPendingAgentApprovals(ctx context.Context, accountID string, orgIDs []string, limit int) ([]AgentApproval, error) {
	rows, err := sqlc.New().ListPendingAgentApprovals(ctx, s.pool, sqlc.ListPendingAgentApprovalsParams{
		AccountID: mustPgUUID(accountID), OrgIds: pgUUIDList(orgIDs), MaxRows: int32(limit),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	return agentApprovalsFromRows(rows), nil
}

func agentApprovalsFromRows(rows []sqlc.AgentApprovalRequest) []AgentApproval {
	out := make([]AgentApproval, 0, len(rows))
	for _, row := range rows {
		out = append(out, agentApprovalFromRow(row))
	}
	return out
}

func agentApprovalFromRow(row sqlc.AgentApprovalRequest) AgentApproval {
	return AgentApproval{
		ID: pgUUIDString(row.ID), KeyID: pgUUIDString(row.KeyID), AccountID: pgUUIDString(row.AccountID), OrgID: pgUUIDString(row.OrgID),
		Category: row.Category, Method: row.Method, Path: row.Path, BodySHA256: row.BodySha256, Status: row.Status,
		DecidedBy: pgUUIDString(row.DecidedBy), CreatedAt: row.CreatedAt.Time, DecidedAt: row.DecidedAt.Time,
		ConsumedAt: row.ConsumedAt.Time, ExpiresAt: row.ExpiresAt.Time, Note: row.DecisionNote.String,
		Reason: row.Reason.String,
	}
}

// --- MemStore ---------------------------------------------------------------

func (m *MemStore) SetAPIKeyApprovalCategories(_ context.Context, keyID string, categories []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validApprovalCategories(categories) {
		return ErrConflict
	}
	if _, ok := m.keyApprovalCategories[keyID]; ok {
		return ErrConflict
	}
	if !m.hasKeyLocked(keyID) {
		return ErrNotFound
	}
	if m.keyApprovalCategories == nil {
		m.keyApprovalCategories = map[string][]string{}
	}
	m.keyApprovalCategories[keyID] = nonNilStrings(slices.Clone(categories))
	return nil
}

func (m *MemStore) APIKeyApprovalCategories(_ context.Context, keyID string) ([]string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	categories, ok := m.keyApprovalCategories[keyID]
	if !ok || !m.hasKeyLocked(keyID) {
		return nil, false, nil
	}
	return slices.Clone(categories), true, nil
}

// hasKeyLocked reports whether the API key exists; its approval rows go with
// it, as ON DELETE CASCADE removes them in Postgres.
func (m *MemStore) hasKeyLocked(keyID string) bool {
	for _, key := range m.keys {
		if key.ID == keyID {
			return true
		}
	}
	return false
}

func sameApprovalBinding(a AgentApproval, b AgentApprovalBinding) bool {
	return a.KeyID == b.KeyID && a.Method == b.Method && a.Path == b.Path && bytes.Equal(a.BodySHA256, b.BodySHA256) &&
		(a.BodySHA256 == nil) == (b.BodySHA256 == nil)
}

func (m *MemStore) ConsumeAgentApproval(_ context.Context, binding AgentApprovalBinding) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var found *AgentApproval
	for id, approval := range m.agentApprovals {
		if approval.Status != api.AgentApprovalApproved || !now.Before(approval.ExpiresAt) || !sameApprovalBinding(approval, binding) || !m.hasKeyLocked(approval.KeyID) {
			continue
		}
		if found == nil || approval.DecidedAt.Before(found.DecidedAt) {
			candidate := m.agentApprovals[id]
			found = &candidate
		}
	}
	if found == nil {
		return "", false, nil
	}
	found.Status, found.ConsumedAt = api.AgentApprovalConsumed, now
	m.agentApprovals[found.ID] = *found
	return found.ID, true, nil
}

func (m *MemStore) RequestAgentApproval(_ context.Context, request AgentApproval) (AgentApproval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasKeyLocked(request.KeyID) {
		return AgentApproval{}, ErrNotFound
	}
	if !slices.Contains(api.AgentApprovalCategories, request.Category) {
		return AgentApproval{}, ErrConflict
	}
	now := time.Now()
	binding := AgentApprovalBinding{KeyID: request.KeyID, Method: request.Method, Path: request.Path, BodySHA256: request.BodySHA256}
	pending := 0
	var reuse *AgentApproval
	for id, approval := range m.agentApprovals {
		if approval.KeyID != request.KeyID {
			continue
		}
		if approval.ExpiresAt.Before(now.Add(-7 * 24 * time.Hour)) {
			delete(m.agentApprovals, id)
			continue
		}
		if approval.Status != api.AgentApprovalPending || !now.Before(approval.ExpiresAt) {
			continue
		}
		pending++
		if sameApprovalBinding(approval, binding) && (reuse == nil || approval.CreatedAt.After(reuse.CreatedAt)) {
			candidate := approval
			reuse = &candidate
		}
	}
	if reuse != nil {
		return cloneAgentApproval(*reuse), nil
	}
	if pending >= api.MaxPendingAgentApprovals {
		return AgentApproval{}, ErrTooManyAgentApprovals
	}
	if _, err := uuid.Parse(request.ID); err != nil {
		request.ID = uuid.NewString()
	}
	reviewURL, agentName := request.ReviewURL, request.AgentName
	request.Status, request.CreatedAt, request.ReviewURL, request.AgentName = api.AgentApprovalPending, now, "", ""
	request.DecidedBy, request.DecidedAt, request.ConsumedAt = "", time.Time{}, time.Time{}
	request.BodySHA256 = bytes.Clone(request.BodySHA256)
	if m.agentApprovals == nil {
		m.agentApprovals = map[string]AgentApproval{}
	}
	m.agentApprovals[request.ID] = request
	event := request
	event.ReviewURL, event.AgentName = reviewURL, agentName
	m.enqueueAgentApprovalEventLocked(api.AgentApprovalRequestedWebhookEvent, request.ID, event)
	m.insertAgentApprovalNotificationLocked(event, now)
	return cloneAgentApproval(request), nil
}

// enqueueAgentApprovalEventLocked mirrors enqueueAgentApprovalEvent.
func (m *MemStore) enqueueAgentApprovalEventLocked(event, sourceID string, a AgentApproval) {
	payload := api.AgentApprovalWebhookPayload{
		ApprovalID: a.ID, KeyID: a.KeyID, OrgID: a.OrgID, Category: a.Category, Method: a.Method, Path: a.Path,
		Status: a.Status, ExpiresAt: a.ExpiresAt.UTC().Format(time.RFC3339), Note: a.Note, Reason: a.Reason,
	}
	if event == api.AgentApprovalRequestedWebhookEvent {
		payload.ReviewURL = a.ReviewURL
	}
	if !a.DecidedAt.IsZero() {
		payload.DecidedAt = a.DecidedAt.UTC().Format(time.RFC3339)
	}
	body, _ := json.Marshal(payload)
	now := time.Now()
	for _, hook := range m.appWebhooks {
		if hook.Scope != AppWebhookScopeAccount || hook.AccountID != a.AccountID || !hook.Enabled ||
			!slices.Contains(hook.EventFilter, event) {
			continue
		}
		if m.appWebhookDeliveries == nil {
			m.appWebhookDeliveries = map[string]AppWebhookDelivery{}
		}
		id := uuid.NewString()
		m.appWebhookDeliveries[id] = AppWebhookDelivery{
			ID: id, WebhookID: hook.ID, AccountID: a.AccountID, Event: AppWebhookEvent(event), Payload: body,
			Status: AppWebhookDeliveryPending, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		}
	}
	_ = sourceID // Postgres dedupes on it; the map holds each event once.
}

func cloneAgentApproval(a AgentApproval) AgentApproval {
	a.BodySHA256 = bytes.Clone(a.BodySHA256)
	return a
}

func (m *MemStore) AgentApproval(_ context.Context, id string) (AgentApproval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	approval, ok := m.agentApprovals[id]
	if !ok || !m.hasKeyLocked(approval.KeyID) {
		return AgentApproval{}, ErrNotFound
	}
	return cloneAgentApproval(approval), nil
}

func (m *MemStore) DecideAgentApproval(_ context.Context, id string, approve bool, deciderID, note string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	approval, ok := m.agentApprovals[id]
	now := time.Now()
	if !ok || approval.Status != api.AgentApprovalPending || !now.Before(approval.ExpiresAt) || !m.hasKeyLocked(approval.KeyID) {
		return ErrNotFound
	}
	approval.Status, approval.Note = decisionStatusNote(approve, note)
	approval.DecidedBy, approval.DecidedAt = deciderID, now
	m.agentApprovals[id] = approval
	event := api.AgentApprovalDeniedWebhookEvent
	if approve {
		event = api.AgentApprovalApprovedWebhookEvent
	}
	m.enqueueAgentApprovalEventLocked(event, uuid.NewString(), approval)
	return nil
}

func (m *MemStore) RecentAgentApprovalOutcome(_ context.Context, binding AgentApprovalBinding, window time.Duration) (AgentApproval, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *AgentApproval
	for _, approval := range m.agentApprovals {
		if !sameApprovalBinding(approval, binding) || !m.hasKeyLocked(approval.KeyID) {
			continue
		}
		if latest == nil || approval.CreatedAt.After(latest.CreatedAt) {
			candidate := approval
			latest = &candidate
		}
	}
	if latest == nil {
		return AgentApproval{}, false, nil
	}
	now := time.Now()
	since := now.Add(-window)
	switch {
	case latest.Status == api.AgentApprovalDenied && latest.DecidedAt.After(since):
	case (latest.Status == api.AgentApprovalPending || latest.Status == api.AgentApprovalApproved) &&
		!now.Before(latest.ExpiresAt) && latest.ExpiresAt.After(since):
	default:
		return AgentApproval{}, false, nil
	}
	return cloneAgentApproval(*latest), true, nil
}

func (m *MemStore) ListAgentApprovalsForKey(_ context.Context, keyID string, limit int) ([]AgentApproval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listAgentApprovalsLocked(limit, func(a AgentApproval) bool { return a.KeyID == keyID }), nil
}

func (m *MemStore) ListPendingAgentApprovals(_ context.Context, accountID string, orgIDs []string, limit int) ([]AgentApproval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	return m.listAgentApprovalsLocked(limit, func(a AgentApproval) bool {
		return a.Status == api.AgentApprovalPending && now.Before(a.ExpiresAt) && (a.AccountID == accountID || slices.Contains(orgIDs, a.OrgID))
	}), nil
}

func (m *MemStore) listAgentApprovalsLocked(limit int, keep func(AgentApproval) bool) []AgentApproval {
	out := []AgentApproval{}
	for _, approval := range m.agentApprovals {
		if keep(approval) && m.hasKeyLocked(approval.KeyID) {
			out = append(out, cloneAgentApproval(approval))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
