package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Plain rollback outcome ledger (ADR-1446). A plain rollback — manual,
// dashboard, or first-wake 5xx auto-rollback — writes one row when it is
// requested; apid's outcome worker resolves it against the target
// deployment. Checked rollbacks keep deployment_rollback_operations.
const (
	RollbackTriggerManual  = "manual"
	RollbackTriggerAuto5xx = "auto_5xx"
	RollbackTriggerAlert   = "alert"
	// RollbackTriggerPostRelease is the post-release watch (ADR-1445).
	RollbackTriggerPostRelease = "post_release"

	RollbackStatusPreparing = "preparing"
	RollbackStatusComplete  = "complete"
	RollbackStatusFailed    = "failed"
)

// RollbackRecord is one plain rollback's outcome.
type RollbackRecord struct {
	ID                 string
	AccountID          string
	AppID              string
	Scope              string
	TargetDeploymentID string
	FromDeploymentID   string
	Trigger            string
	Status             string
	RequestedAt        time.Time
	FinishedAt         *time.Time
	RecoveryMS         *int64
	AuditID            int64
	ErrorCode          string
	Error              string
}

// RollbackOutcome is the worker's verdict for a preparing rollback. Audit is
// written in the same transaction that finishes the row; a complete outcome
// records the audit id as its receipt.
type RollbackOutcome struct {
	Status     string
	FinishedAt time.Time
	RecoveryMS *int64
	ErrorCode  string
	Error      string
	Audit      DeploymentAudit
}

// RollbackOutcomeStore persists plain rollback outcomes.
type RollbackOutcomeStore interface {
	// RecordRollbackRequest inserts a preparing row. A preparing row for the
	// same target is failed first: a newer request supersedes it.
	RecordRollbackRequest(ctx context.Context, r RollbackRecord) (RollbackRecord, error)
	PreparingRollbacks(ctx context.Context, limit int) ([]RollbackRecord, error)
	// FinishRollback applies outcome only while the row is still preparing.
	// finished=false means another worker already resolved it.
	FinishRollback(ctx context.Context, id string, outcome RollbackOutcome) (record RollbackRecord, finished bool, err error)
	RollbackRecordByID(ctx context.Context, accountID, appID, id string) (RollbackRecord, error)
}

var errRollbackOutcomeInvalid = errors.New("state: invalid rollback outcome")

func validRollbackOutcome(o RollbackOutcome) bool {
	switch o.Status {
	case RollbackStatusComplete:
		return o.RecoveryMS != nil && *o.RecoveryMS >= 0 && !o.FinishedAt.IsZero()
	case RollbackStatusFailed:
		return !o.FinishedAt.IsZero()
	default:
		return false
	}
}

const rollbackRecordColumns = `id::text, account_id::text, app_id::text, scope, target_deployment_id::text,
	coalesce(from_deployment_id::text, ''), trigger, status, requested_at, finished_at, recovery_ms,
	coalesce(audit_id, 0), error_code, error`

func scanRollbackRecord(row pgx.Row) (RollbackRecord, error) {
	var r RollbackRecord
	err := row.Scan(&r.ID, &r.AccountID, &r.AppID, &r.Scope, &r.TargetDeploymentID, &r.FromDeploymentID,
		&r.Trigger, &r.Status, &r.RequestedAt, &r.FinishedAt, &r.RecoveryMS, &r.AuditID, &r.ErrorCode, &r.Error)
	return r, err
}

func (s *PgStore) RecordRollbackRequest(ctx context.Context, r RollbackRecord) (RollbackRecord, error) {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RollbackRecord{}, fmt.Errorf("state: record rollback begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `update deployment_rollbacks
		   set status = 'failed', finished_at = clock_timestamp(),
		       error_code = 'rollback_superseded', error = 'a newer rollback request replaced this one'
		 where target_deployment_id = $1 and status = 'preparing'`, r.TargetDeploymentID); err != nil {
		return RollbackRecord{}, fmt.Errorf("state: supersede preparing rollback: %w", err)
	}
	var from any
	if r.FromDeploymentID != "" {
		from = r.FromDeploymentID
	}
	// requested_at uses the database clock, the same clock that stamps the
	// target's rollout_completed_at, so the recovery time has no skew.
	out, err := scanRollbackRecord(tx.QueryRow(ctx, `insert into deployment_rollbacks
		    (id, account_id, app_id, scope, target_deployment_id, from_deployment_id, trigger, status, requested_at)
		 values ($1, $2, $3, $4, $5, $6, $7, 'preparing', clock_timestamp())
		 returning `+rollbackRecordColumns,
		r.ID, r.AccountID, r.AppID, normalizedDeploymentScope(r.Scope), r.TargetDeploymentID, from, r.Trigger))
	if err != nil {
		return RollbackRecord{}, fmt.Errorf("state: record rollback request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RollbackRecord{}, fmt.Errorf("state: record rollback commit: %w", err)
	}
	return out, nil
}

func (s *PgStore) PreparingRollbacks(ctx context.Context, limit int) ([]RollbackRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `select `+rollbackRecordColumns+`
		  from deployment_rollbacks
		 where status = 'preparing'
		 order by requested_at, id
		 limit $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list preparing rollbacks: %w", err)
	}
	defer rows.Close()
	var out []RollbackRecord
	for rows.Next() {
		r, err := scanRollbackRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan preparing rollback: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PgStore) FinishRollback(ctx context.Context, id string, o RollbackOutcome) (RollbackRecord, bool, error) {
	if !validRollbackOutcome(o) {
		return RollbackRecord{}, false, errRollbackOutcomeInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RollbackRecord{}, false, fmt.Errorf("state: finish rollback begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var targetID string
	var requestedAt time.Time
	if err := tx.QueryRow(ctx, `select target_deployment_id::text, requested_at from deployment_rollbacks where id = $1 and status = 'preparing' for update`, id).Scan(&targetID, &requestedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RollbackRecord{}, false, nil
		}
		return RollbackRecord{}, false, fmt.Errorf("state: lock rollback: %w", err)
	}
	if o.Status == RollbackStatusFailed {
		// A rollback reported failed (for example rollback_timeout) must not
		// cut over later: schedd's prime recovery would otherwise re-prime the
		// still-prepared target and activate it after the customer was told it
		// failed. Only this request's preparation is withdrawn; a newer
		// prepare of the same target is left alone.
		if _, err := tx.Exec(ctx, `update deployments
			   set status = case when status = 'snapshotting' then 'superseded' else status end,
			       rollback_prepared_at = null
			 where id = $1 and rollback_prepared_at is not null and rollback_prepared_at <= $2
			   and status <> 'live'`, targetID, requestedAt); err != nil {
			return RollbackRecord{}, false, fmt.Errorf("state: withdraw failed rollback target: %w", err)
		}
	}
	var auditID int64
	if err := tx.QueryRow(ctx, `insert into deployment_audit
		    (deployment_id, account_id, kind, actor, at, data, alert_rule_id)
		 values ($1, $2, $3, $4, coalesce($5, now()), $6::jsonb, $7)
		 returning id`,
		o.Audit.DeploymentID, o.Audit.AccountID, string(o.Audit.Kind), o.Audit.Actor, nullTime(o.Audit.At),
		[]byte(o.Audit.Data), o.Audit.AlertRuleID).Scan(&auditID); err != nil {
		return RollbackRecord{}, false, fmt.Errorf("state: append rollback outcome audit: %w", err)
	}
	out, err := scanRollbackRecord(tx.QueryRow(ctx, `update deployment_rollbacks
		   set status = $2, finished_at = $3, recovery_ms = $4, audit_id = $5, error_code = $6, error = $7
		 where id = $1
		 returning `+rollbackRecordColumns,
		id, o.Status, o.FinishedAt, o.RecoveryMS, auditID, truncateBytes(o.ErrorCode, 64), truncateBytes(o.Error, 1024)))
	if err != nil {
		return RollbackRecord{}, false, fmt.Errorf("state: finish rollback: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RollbackRecord{}, false, fmt.Errorf("state: finish rollback commit: %w", err)
	}
	return out, true, nil
}

func (s *PgStore) RollbackRecordByID(ctx context.Context, accountID, appID, id string) (RollbackRecord, error) {
	if _, err := uuid.Parse(id); err != nil {
		return RollbackRecord{}, ErrNotFound
	}
	r, err := scanRollbackRecord(s.pool.QueryRow(ctx, `select `+rollbackRecordColumns+`
		  from deployment_rollbacks
		 where id = $1 and account_id = $2 and app_id = $3`, id, accountID, appID))
	if err != nil {
		return RollbackRecord{}, mapErr(err)
	}
	return r, nil
}

// truncateBytes bounds a string to n bytes without splitting a UTF-8 rune.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut]
}

func (m *MemStore) RecordRollbackRequest(_ context.Context, r RollbackRecord) (RollbackRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if m.rollbackRecords == nil {
		m.rollbackRecords = map[string]RollbackRecord{}
	}
	now := time.Now().UTC()
	for id, other := range m.rollbackRecords {
		if other.TargetDeploymentID == r.TargetDeploymentID && other.Status == RollbackStatusPreparing {
			finished := now
			other.Status, other.FinishedAt = RollbackStatusFailed, &finished
			other.ErrorCode, other.Error = "rollback_superseded", "a newer rollback request replaced this one"
			m.rollbackRecords[id] = other
		}
	}
	r.Scope = normalizedDeploymentScope(r.Scope)
	r.Status = RollbackStatusPreparing
	r.RequestedAt = now
	r.FinishedAt, r.RecoveryMS, r.AuditID, r.ErrorCode, r.Error = nil, nil, 0, "", ""
	m.rollbackRecords[r.ID] = r
	return r, nil
}

func (m *MemStore) PreparingRollbacks(_ context.Context, limit int) ([]RollbackRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RollbackRecord
	for _, r := range m.rollbackRecords {
		if r.Status == RollbackStatusPreparing {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RequestedAt.Equal(out[j].RequestedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].RequestedAt.Before(out[j].RequestedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) FinishRollback(_ context.Context, id string, o RollbackOutcome) (RollbackRecord, bool, error) {
	if !validRollbackOutcome(o) {
		return RollbackRecord{}, false, errRollbackOutcomeInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rollbackRecords[id]
	if !ok || r.Status != RollbackStatusPreparing {
		return RollbackRecord{}, false, nil
	}
	if o.Status == RollbackStatusFailed {
		// Mirrors PgStore: a failed rollback never cuts over later.
		if preparedAt, prepared := m.rollbackPrepared[r.TargetDeploymentID]; prepared && !preparedAt.After(r.RequestedAt) {
			if target, exists := m.deployments[r.TargetDeploymentID]; exists && target.Status != DeployLive {
				if target.Status == DeploySnapshotting {
					target.Status = DeploySuperseded
					m.putDeploymentLocked(target.ID, target)
				}
				delete(m.rollbackPrepared, r.TargetDeploymentID)
			}
		}
	}
	auditID, err := m.appendDeploymentAuditLocked(o.Audit)
	if err != nil {
		return RollbackRecord{}, false, err
	}
	finished := o.FinishedAt
	r.Status, r.FinishedAt, r.AuditID = o.Status, &finished, auditID
	if o.RecoveryMS != nil {
		ms := *o.RecoveryMS
		r.RecoveryMS = &ms
	}
	r.ErrorCode, r.Error = truncateBytes(o.ErrorCode, 64), truncateBytes(o.Error, 1024)
	m.rollbackRecords[id] = r
	return r, true, nil
}

func (m *MemStore) RollbackRecordByID(_ context.Context, accountID, appID, id string) (RollbackRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rollbackRecords[id]
	if !ok || r.AccountID != accountID || r.AppID != appID {
		return RollbackRecord{}, ErrNotFound
	}
	return r, nil
}
