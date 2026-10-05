package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	ErrHostingVerificationFinalized = errors.New("state: hosting candidate already finalized")
	ErrHostingVerificationDeferred  = errors.New("state: hosting verification retry is not yet eligible")
	ErrHostingVerificationExpired   = errors.New("state: hosting verification recovery deadline expired")
)

var (
	_ DeploymentHostingVerificationStore = (*PgStore)(nil)
	_ DeploymentHostingVerificationStore = (*MemStore)(nil)
)

type HostingVerificationProgress struct {
	StartedAt      time.Time  `json:"started_at"`
	DeadlineAt     time.Time  `json:"deadline_at"`
	Attempts       int        `json:"attempts"`
	LastErrorCode  string     `json:"last_error_code,omitempty"`
	RetryNotBefore *time.Time `json:"retry_not_before,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

type HostingVerificationAction string

const (
	HostingVerificationBegin    HostingVerificationAction = "begin"
	HostingVerificationRetry    HostingVerificationAction = "verification_retry"
	HostingVerificationComplete HostingVerificationAction = "complete"
)

type HostingVerificationUpdate struct {
	Action         HostingVerificationAction
	Attempt        int
	ErrorCode      string
	At             time.Time
	RetryNotBefore time.Time
}

func advanceHostingVerification(prior *HostingVerificationProgress, u HostingVerificationUpdate) (HostingVerificationProgress, error) {
	if u.At.IsZero() {
		return HostingVerificationProgress{}, ErrInvalidArgument
	}
	at := u.At.UTC()
	p := HostingVerificationProgress{StartedAt: at, DeadlineAt: at.Add(api.HostingVerificationRecoveryWindow)}
	if prior != nil {
		p = *prior
	}
	if p.StartedAt.IsZero() || !p.DeadlineAt.After(p.StartedAt) || p.Attempts < 0 {
		return p, ErrInvalidArgument
	}
	if u.Action == HostingVerificationBegin {
		if apihostingreceipt.IsVerificationRecoveryCode(p.LastErrorCode) && !at.Before(p.DeadlineAt) {
			return p, ErrHostingVerificationExpired
		}
		if p.RetryNotBefore != nil && at.Before(*p.RetryNotBefore) {
			return p, ErrHostingVerificationDeferred
		}
		p.Attempts++
		p.RetryNotBefore, p.CompletedAt = nil, nil
		return p, nil
	}
	if prior == nil || u.Attempt <= 0 || p.Attempts != u.Attempt {
		return p, ErrConflict
	}
	switch u.Action {
	case HostingVerificationRetry:
		code := u.ErrorCode
		if code == "" {
			code = apihostingreceipt.SmokeErrorAuthorizationUnavailable
		}
		if !apihostingreceipt.IsVerificationRecoveryCode(code) {
			return p, ErrInvalidArgument
		}
		if u.RetryNotBefore.Before(at) {
			return p, ErrInvalidArgument
		}
		next := u.RetryNotBefore.UTC()
		if next.After(p.DeadlineAt) {
			next = p.DeadlineAt
		}
		p.LastErrorCode = code
		p.RetryNotBefore, p.CompletedAt = &next, nil
	case HostingVerificationComplete:
		p.LastErrorCode, p.RetryNotBefore, p.CompletedAt = "", nil, &at
	default:
		return p, ErrInvalidArgument
	}
	return p, nil
}

func updateHostingVerification(status DeploymentStatus, raw []byte, u HostingVerificationUpdate) (HostingVerificationProgress, []byte, error) {
	if status.IsTerminal() || status == DeployLive {
		return HostingVerificationProgress{}, nil, ErrHostingVerificationFinalized
	}
	if status != DeploySnapshotting {
		return HostingVerificationProgress{}, nil, ErrInvalidStateTransition
	}
	var stages StageState
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &stages); err != nil {
			return HostingVerificationProgress{}, nil, fmt.Errorf("decode hosting verification: %w", err)
		}
	}
	p, err := advanceHostingVerification(stages.HostingVerification, u)
	if err != nil {
		return p, nil, err
	}
	encoded, err := json.Marshal(p)
	return p, encoded, err
}

func (s *PgStore) UpdateDeploymentHostingVerification(ctx context.Context, id string, u HostingVerificationUpdate) (HostingVerificationProgress, error) {
	var p HostingVerificationProgress
	deploymentID, err := parsePgUUID(id)
	if err != nil {
		return p, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return p, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockDeploymentHostingVerification(ctx, tx, deploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, fmt.Errorf("lock hosting verification: %w", err)
	}
	p, encoded, err := updateHostingVerification(DeploymentStatus(row.Status), row.StageState, u)
	if err != nil {
		return p, err
	}
	rows, err := q.WriteDeploymentHostingVerification(ctx, tx, sqlc.WriteDeploymentHostingVerificationParams{DeploymentID: deploymentID, Progress: encoded})
	if err != nil {
		return p, fmt.Errorf("write hosting verification: %w", err)
	}
	if rows != 1 {
		return p, ErrInvalidStateTransition
	}
	if err := tx.Commit(ctx); err != nil {
		return p, fmt.Errorf("commit hosting verification: %w", err)
	}
	return p, nil
}

func (m *MemStore) UpdateDeploymentHostingVerification(ctx context.Context, id string, u HostingVerificationUpdate) (HostingVerificationProgress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var p HostingVerificationProgress
	if err := ctx.Err(); err != nil {
		return p, err
	}
	d, ok := m.deployments[id]
	if !ok {
		return p, ErrNotFound
	}
	p, _, err := updateHostingVerification(d.Status, d.StageState, u)
	if err != nil {
		return p, err
	}
	var stages StageState
	if len(d.StageState) != 0 {
		if err := json.Unmarshal(d.StageState, &stages); err != nil {
			return p, err
		}
	}
	stages.HostingVerification = &p
	d.StageState, err = json.Marshal(stages)
	if err != nil {
		return p, err
	}
	m.deployments[id] = d
	return p, nil
}
