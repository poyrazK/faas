package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Release approval vocabulary (ADR-20261020085706).
const (
	ReleaseApproversAdmin     = "admin"
	ReleaseApproversDeveloper = "developer"

	ReleaseApprovalApprove = "approve"
	ReleaseApprovalReject  = "reject"

	// ReleaseApproverRolePersonal is the role recorded for the one person who
	// owns an app outside an organization.
	ReleaseApproverRolePersonal = "personal"
)

// ReleaseApprovalPolicy is an app's approval policy.
type ReleaseApprovalPolicy struct {
	AppID             string
	AccountID         string
	RequiredApprovals int
	AfterStage        int
	TimeoutMinutes    int
	Approvers         string
	AllowSelfApproval bool
	// NotifyChannelIDs are the notification channels told about the app's
	// approvals; empty sends nothing.
	NotifyChannelIDs []string
	UpdatedBy        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ReleaseApproval is one person's decision on a release.
type ReleaseApproval struct {
	ID                string
	AccountID         string
	AppID             string
	DeploymentID      string
	ApproverAccountID string
	Approver          string
	ApproverRole      string
	Decision          string
	Comment           string
	CreatedAt         time.Time
}

// ReleaseApprovalStore persists approval policies and decisions.
type ReleaseApprovalStore interface {
	// SetReleaseApprovalPolicy creates or replaces the app's policy.
	SetReleaseApprovalPolicy(ctx context.Context, p ReleaseApprovalPolicy, now time.Time) (ReleaseApprovalPolicy, error)
	// ReleaseApprovalPolicy returns the app's policy (ErrNotFound otherwise).
	ReleaseApprovalPolicy(ctx context.Context, appID string) (ReleaseApprovalPolicy, error)
	// DeleteReleaseApprovalPolicy removes the app's policy (ErrNotFound otherwise).
	DeleteReleaseApprovalPolicy(ctx context.Context, appID string) (ReleaseApprovalPolicy, error)
	// RecordReleaseApproval stores a decision; a second decision by the same
	// person on the same release returns ErrConflict.
	RecordReleaseApproval(ctx context.Context, a ReleaseApproval, now time.Time) (ReleaseApproval, error)
	// ListReleaseApprovals returns a release's decisions, oldest first.
	ListReleaseApprovals(ctx context.Context, deploymentID string) ([]ReleaseApproval, error)
}

// Release approval events and their delivery states
// (release_approval_events).
const (
	ReleaseApprovalEventRequested = "requested"
	ReleaseApprovalEventApproved  = "approved"
	ReleaseApprovalEventRejected  = "rejected"
	ReleaseApprovalEventExpired   = "expired"

	ReleaseApprovalEventPending   = "pending"
	ReleaseApprovalEventDelivered = "delivered"
	ReleaseApprovalEventFailed    = "failed"
)

// ReleaseApprovalEvent is one notification about a release's approval,
// recorded once per release and event; Payload is the message snapshot.
type ReleaseApprovalEvent struct {
	ID            string
	AccountID     string
	AppID         string
	DeploymentID  string
	Event         string
	ChannelIDs    []string
	Payload       json.RawMessage
	State         string
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
	DeliveredAt   *time.Time
}

// ReleaseApprovalEventAttempt is the outcome of one delivery attempt: State
// stays pending to retry at NextAttemptAt, or ends as delivered or failed.
type ReleaseApprovalEventAttempt struct {
	State         string
	LastError     string
	NextAttemptAt time.Time
	At            time.Time
}

// ReleaseApprovalEventStore is the notification outbox for approvals.
type ReleaseApprovalEventStore interface {
	// EnqueueReleaseApprovalEvent records e unless the release already has
	// this event; it reports whether e was recorded.
	EnqueueReleaseApprovalEvent(ctx context.Context, e ReleaseApprovalEvent, now time.Time) (bool, error)
	// DueReleaseApprovalEvents lists pending events due at now, oldest first.
	DueReleaseApprovalEvents(ctx context.Context, now time.Time, limit int) ([]ReleaseApprovalEvent, error)
	// FinishReleaseApprovalEvent records an attempt on a pending event
	// (ErrNotFound when it is no longer pending).
	FinishReleaseApprovalEvent(ctx context.Context, id string, attempt ReleaseApprovalEventAttempt) error
}

var _ ReleaseApprovalStore = (*MemStore)(nil)
var _ ReleaseApprovalEventStore = (*MemStore)(nil)

func (m *MemStore) EnqueueReleaseApprovalEvent(_ context.Context, e ReleaseApprovalEvent, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.releaseApprovalEvents {
		if existing.DeploymentID == e.DeploymentID && existing.Event == e.Event {
			return false, nil
		}
	}
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	now = now.UTC()
	e.State, e.Attempts, e.NextAttemptAt, e.CreatedAt, e.LastError, e.DeliveredAt = ReleaseApprovalEventPending, 0, now, now, "", nil
	e.ChannelIDs = append([]string(nil), e.ChannelIDs...)
	e.Payload = append(json.RawMessage(nil), e.Payload...)
	if m.releaseApprovalEvents == nil {
		m.releaseApprovalEvents = map[string]ReleaseApprovalEvent{}
	}
	m.releaseApprovalEvents[e.ID] = e
	return true, nil
}

func (m *MemStore) DueReleaseApprovalEvents(_ context.Context, now time.Time, limit int) ([]ReleaseApprovalEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ReleaseApprovalEvent
	for _, e := range m.releaseApprovalEvents {
		if e.State == ReleaseApprovalEventPending && !e.NextAttemptAt.After(now) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].NextAttemptAt.Equal(out[j].NextAttemptAt) {
			return out[i].NextAttemptAt.Before(out[j].NextAttemptAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) FinishReleaseApprovalEvent(_ context.Context, id string, attempt ReleaseApprovalEventAttempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.releaseApprovalEvents[id]
	if !ok || e.State != ReleaseApprovalEventPending {
		return ErrNotFound
	}
	e.State, e.LastError, e.NextAttemptAt = attempt.State, attempt.LastError, attempt.NextAttemptAt.UTC()
	e.Attempts++
	if attempt.State == ReleaseApprovalEventDelivered {
		at := attempt.At.UTC()
		e.DeliveredAt = &at
	}
	m.releaseApprovalEvents[id] = e
	return nil
}

func (m *MemStore) SetReleaseApprovalPolicy(_ context.Context, p ReleaseApprovalPolicy, now time.Time) (ReleaseApprovalPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now = now.UTC()
	p.CreatedAt, p.UpdatedAt = now, now
	p.NotifyChannelIDs = append([]string{}, p.NotifyChannelIDs...)
	if existing, ok := m.releaseApprovalPolicies[p.AppID]; ok {
		p.CreatedAt = existing.CreatedAt
	}
	if m.releaseApprovalPolicies == nil {
		m.releaseApprovalPolicies = map[string]ReleaseApprovalPolicy{}
	}
	m.releaseApprovalPolicies[p.AppID] = p
	return p, nil
}

func (m *MemStore) ReleaseApprovalPolicy(_ context.Context, appID string) (ReleaseApprovalPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.releaseApprovalPolicies[appID]
	if !ok {
		return ReleaseApprovalPolicy{}, ErrNotFound
	}
	return p, nil
}

func (m *MemStore) DeleteReleaseApprovalPolicy(_ context.Context, appID string) (ReleaseApprovalPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.releaseApprovalPolicies[appID]
	if !ok {
		return ReleaseApprovalPolicy{}, ErrNotFound
	}
	delete(m.releaseApprovalPolicies, appID)
	return p, nil
}

func (m *MemStore) RecordReleaseApproval(_ context.Context, a ReleaseApproval, now time.Time) (ReleaseApproval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.releaseApprovals {
		if existing.DeploymentID == a.DeploymentID && existing.ApproverAccountID == a.ApproverAccountID {
			return ReleaseApproval{}, ErrConflict
		}
	}
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	a.CreatedAt = now.UTC()
	if m.releaseApprovals == nil {
		m.releaseApprovals = map[string]ReleaseApproval{}
	}
	m.releaseApprovals[a.ID] = a
	return a, nil
}

func (m *MemStore) ListReleaseApprovals(_ context.Context, deploymentID string) ([]ReleaseApproval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ReleaseApproval
	for _, a := range m.releaseApprovals {
		if a.DeploymentID == deploymentID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
