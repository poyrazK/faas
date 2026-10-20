package state

import (
	"context"
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
	UpdatedBy         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
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

var _ ReleaseApprovalStore = (*MemStore)(nil)

func (m *MemStore) SetReleaseApprovalPolicy(_ context.Context, p ReleaseApprovalPolicy, now time.Time) (ReleaseApprovalPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now = now.UTC()
	p.CreatedAt, p.UpdatedAt = now, now
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
