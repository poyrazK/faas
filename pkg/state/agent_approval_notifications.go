package state

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Agent approval notification states (ADR-20261107140000 §5).
const (
	AgentApprovalNotificationPending = "pending"
	AgentApprovalNotificationSent    = "sent"
	AgentApprovalNotificationSkipped = "skipped"
	AgentApprovalNotificationFailed  = "failed"
)

// AgentApprovalNotification is a new agent approval request to tell people
// about. apid writes it with the request; meterd delivers it.
type AgentApprovalNotification struct {
	ApprovalID string
	AccountID  string // the member who connected the agent
	OrgID      string
	KeyID      string
	AgentName  string
	ReviewURL  string
	Attempts   int
}

// PendingAgentApprovalLine is one still-pending request an email lists.
type PendingAgentApprovalLine struct {
	ApprovalID string
	ReviewURL  string
	Category   string
	Method     string
	Path       string
	ExpiresAt  time.Time
}

// AgentApprovalNotificationStore is meterd's side of the notification outbox.
type AgentApprovalNotificationStore interface {
	// ClaimAgentApprovalNotifications leases up to limit due notifications.
	ClaimAgentApprovalNotifications(ctx context.Context, limit int, lease time.Duration) ([]AgentApprovalNotification, error)
	// AgentApprovalRequestState returns a request's stored status and expiry.
	AgentApprovalRequestState(ctx context.Context, approvalID string) (string, time.Time, error)
	// PendingAgentApprovalNotificationsForKey lists the key's waiting
	// notifications whose requests are still pending and unexpired.
	PendingAgentApprovalNotificationsForKey(ctx context.Context, keyID string) ([]PendingAgentApprovalLine, error)
	// FinishAgentApprovalNotifications records the outcome of pending rows.
	FinishAgentApprovalNotifications(ctx context.Context, approvalIDs []string, state, lastError string) error
	// RetryAgentApprovalNotification counts a failed attempt and makes the
	// row due after delay; at maxAttempts it fails.
	RetryAgentApprovalNotification(ctx context.Context, approvalID, lastError string, delay time.Duration, maxAttempts int) error
	// DeferAgentApprovalNotification makes a pending row due at until.
	DeferAgentApprovalNotification(ctx context.Context, approvalID string, until time.Time) error
	// AgentApprovalEmailLastSent reports when the recipient was last emailed
	// about the key.
	AgentApprovalEmailLastSent(ctx context.Context, recipientID, keyID string) (time.Time, bool, error)
	// RecordAgentApprovalEmailSent notes an email to the recipient now.
	RecordAgentApprovalEmailSent(ctx context.Context, recipientID, keyID string) error
}

func truncateLastError(s string) string {
	if runes := []rune(s); len(runes) > 256 {
		return string(runes[:256])
	}
	return s
}

func insertAgentApprovalNotification(ctx context.Context, q *sqlc.Queries, tx sqlc.DBTX, a AgentApproval) error {
	if a.ReviewURL == "" {
		// Nobody can act on a request without a review page.
		return nil
	}
	name := a.AgentName
	if name == "" {
		name = "an agent"
	}
	if runes := []rune(name); len(runes) > 200 {
		name = string(runes[:200])
	}
	return mapErr(q.InsertAgentApprovalNotification(ctx, tx, sqlc.InsertAgentApprovalNotificationParams{
		ApprovalID: mustPgUUID(a.ID), AccountID: mustPgUUID(a.AccountID), OrgID: mustPgUUID(a.OrgID),
		KeyID: mustPgUUID(a.KeyID), AgentName: name, ReviewUrl: a.ReviewURL,
	}))
}

func (s *PgStore) ClaimAgentApprovalNotifications(ctx context.Context, limit int, lease time.Duration) ([]AgentApprovalNotification, error) {
	rows, err := sqlc.New().ClaimAgentApprovalNotifications(ctx, s.pool, sqlc.ClaimAgentApprovalNotificationsParams{
		LeaseSeconds: int32(lease / time.Second), MaxRows: int32(limit),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]AgentApprovalNotification, 0, len(rows))
	for _, row := range rows {
		out = append(out, AgentApprovalNotification{
			ApprovalID: pgUUIDString(row.ApprovalID), AccountID: pgUUIDString(row.AccountID), OrgID: pgUUIDString(row.OrgID),
			KeyID: pgUUIDString(row.KeyID), AgentName: row.AgentName, ReviewURL: row.ReviewUrl, Attempts: int(row.Attempts),
		})
	}
	return out, nil
}

func (s *PgStore) AgentApprovalRequestState(ctx context.Context, approvalID string) (string, time.Time, error) {
	row, err := sqlc.New().GetAgentApprovalRequestState(ctx, s.pool, mustPgUUID(approvalID))
	if err != nil {
		return "", time.Time{}, mapErr(err)
	}
	return row.Status, row.ExpiresAt.Time, nil
}

func (s *PgStore) PendingAgentApprovalNotificationsForKey(ctx context.Context, keyID string) ([]PendingAgentApprovalLine, error) {
	rows, err := sqlc.New().PendingAgentApprovalNotificationsForKey(ctx, s.pool, mustPgUUID(keyID))
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]PendingAgentApprovalLine, 0, len(rows))
	for _, row := range rows {
		out = append(out, PendingAgentApprovalLine{
			ApprovalID: pgUUIDString(row.ApprovalID), ReviewURL: row.ReviewUrl, Category: row.Category,
			Method: row.Method, Path: row.Path, ExpiresAt: row.ExpiresAt.Time,
		})
	}
	return out, nil
}

func (s *PgStore) FinishAgentApprovalNotifications(ctx context.Context, approvalIDs []string, state, lastError string) error {
	if len(approvalIDs) == 0 {
		return nil
	}
	return mapErr(sqlc.New().FinishAgentApprovalNotifications(ctx, s.pool, sqlc.FinishAgentApprovalNotificationsParams{
		State: state, LastError: pgText(truncateLastError(lastError)), ApprovalIds: pgUUIDList(approvalIDs),
	}))
}

func (s *PgStore) RetryAgentApprovalNotification(ctx context.Context, approvalID, lastError string, delay time.Duration, maxAttempts int) error {
	return mapErr(sqlc.New().RetryAgentApprovalNotification(ctx, s.pool, sqlc.RetryAgentApprovalNotificationParams{
		LastError: pgText(truncateLastError(lastError)), DelaySeconds: int32(delay / time.Second),
		MaxAttempts: int32(maxAttempts), ApprovalID: mustPgUUID(approvalID),
	}))
}

func (s *PgStore) DeferAgentApprovalNotification(ctx context.Context, approvalID string, until time.Time) error {
	return mapErr(sqlc.New().DeferAgentApprovalNotification(ctx, s.pool, sqlc.DeferAgentApprovalNotificationParams{
		AvailableAt: pgTime(until), ApprovalID: mustPgUUID(approvalID),
	}))
}

func (s *PgStore) AgentApprovalEmailLastSent(ctx context.Context, recipientID, keyID string) (time.Time, bool, error) {
	at, err := sqlc.New().GetAgentApprovalEmailLastSent(ctx, s.pool, sqlc.GetAgentApprovalEmailLastSentParams{
		RecipientAccountID: mustPgUUID(recipientID), KeyID: mustPgUUID(keyID),
	})
	if err != nil {
		if err = mapErr(err); errors.Is(err, ErrNotFound) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, err
	}
	return at.Time, true, nil
}

func (s *PgStore) RecordAgentApprovalEmailSent(ctx context.Context, recipientID, keyID string) error {
	return mapErr(sqlc.New().RecordAgentApprovalEmailSent(ctx, s.pool, sqlc.RecordAgentApprovalEmailSentParams{
		RecipientAccountID: mustPgUUID(recipientID), KeyID: mustPgUUID(keyID),
	}))
}

// --- MemStore ---------------------------------------------------------------

type memAgentApprovalNotification struct {
	AgentApprovalNotification
	State       string
	AvailableAt time.Time
	LeaseUntil  time.Time
	LastError   string
	CreatedAt   time.Time
}

type memEmailLogKey struct{ recipient, key string }

func (m *MemStore) insertAgentApprovalNotificationLocked(a AgentApproval, now time.Time) {
	if a.ReviewURL == "" {
		return
	}
	if m.agentApprovalNotifications == nil {
		m.agentApprovalNotifications = map[string]*memAgentApprovalNotification{}
	}
	if _, ok := m.agentApprovalNotifications[a.ID]; ok {
		return
	}
	name := a.AgentName
	if name == "" {
		name = "an agent"
	}
	m.agentApprovalNotifications[a.ID] = &memAgentApprovalNotification{
		AgentApprovalNotification: AgentApprovalNotification{
			ApprovalID: a.ID, AccountID: a.AccountID, OrgID: a.OrgID, KeyID: a.KeyID, AgentName: name, ReviewURL: a.ReviewURL,
		},
		State: AgentApprovalNotificationPending, AvailableAt: now, CreatedAt: now,
	}
}

func (m *MemStore) ClaimAgentApprovalNotifications(_ context.Context, limit int, lease time.Duration) ([]AgentApprovalNotification, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	due := []*memAgentApprovalNotification{}
	for id, n := range m.agentApprovalNotifications {
		if _, ok := m.agentApprovals[id]; !ok || !m.hasKeyLocked(n.KeyID) {
			delete(m.agentApprovalNotifications, id) // ON DELETE CASCADE
			continue
		}
		if n.State == AgentApprovalNotificationPending && !n.AvailableAt.After(now) && n.LeaseUntil.Before(now) {
			due = append(due, n)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].AvailableAt.Before(due[j].AvailableAt) })
	if len(due) > limit {
		due = due[:limit]
	}
	out := make([]AgentApprovalNotification, 0, len(due))
	for _, n := range due {
		n.LeaseUntil = now.Add(lease)
		out = append(out, n.AgentApprovalNotification)
	}
	return out, nil
}

func (m *MemStore) AgentApprovalRequestState(_ context.Context, approvalID string) (string, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.agentApprovals[approvalID]
	if !ok || !m.hasKeyLocked(a.KeyID) {
		return "", time.Time{}, ErrNotFound
	}
	return a.Status, a.ExpiresAt, nil
}

func (m *MemStore) PendingAgentApprovalNotificationsForKey(_ context.Context, keyID string) ([]PendingAgentApprovalLine, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	type line struct {
		PendingAgentApprovalLine
		created time.Time
	}
	lines := []line{}
	for id, n := range m.agentApprovalNotifications {
		a, ok := m.agentApprovals[id]
		if !ok || n.KeyID != keyID || n.State != AgentApprovalNotificationPending || a.Status != api.AgentApprovalPending || !now.Before(a.ExpiresAt) {
			continue
		}
		lines = append(lines, line{PendingAgentApprovalLine{
			ApprovalID: id, ReviewURL: n.ReviewURL, Category: a.Category, Method: a.Method, Path: a.Path, ExpiresAt: a.ExpiresAt,
		}, a.CreatedAt})
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].created.Before(lines[j].created) })
	out := make([]PendingAgentApprovalLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.PendingAgentApprovalLine)
	}
	return out, nil
}

func (m *MemStore) FinishAgentApprovalNotifications(_ context.Context, approvalIDs []string, state, lastError string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, n := range m.agentApprovalNotifications {
		if slices.Contains(approvalIDs, id) && n.State == AgentApprovalNotificationPending {
			n.State, n.LeaseUntil, n.LastError = state, time.Time{}, truncateLastError(lastError)
		}
	}
	return nil
}

func (m *MemStore) RetryAgentApprovalNotification(_ context.Context, approvalID, lastError string, delay time.Duration, maxAttempts int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.agentApprovalNotifications[approvalID]
	if !ok || n.State != AgentApprovalNotificationPending {
		return nil
	}
	n.Attempts++
	n.LeaseUntil, n.LastError, n.AvailableAt = time.Time{}, truncateLastError(lastError), time.Now().Add(delay)
	if n.Attempts >= maxAttempts {
		n.State = AgentApprovalNotificationFailed
	}
	return nil
}

func (m *MemStore) DeferAgentApprovalNotification(_ context.Context, approvalID string, until time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n, ok := m.agentApprovalNotifications[approvalID]; ok && n.State == AgentApprovalNotificationPending {
		n.LeaseUntil, n.AvailableAt = time.Time{}, until
	}
	return nil
}

func (m *MemStore) AgentApprovalEmailLastSent(_ context.Context, recipientID, keyID string) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	at, ok := m.agentApprovalEmailLog[memEmailLogKey{recipientID, keyID}]
	return at, ok, nil
}

func (m *MemStore) RecordAgentApprovalEmailSent(_ context.Context, recipientID, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.agentApprovalEmailLog == nil {
		m.agentApprovalEmailLog = map[memEmailLogKey]time.Time{}
	}
	m.agentApprovalEmailLog[memEmailLogKey{recipientID, keyID}] = time.Now()
	return nil
}
