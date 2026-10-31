package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

var _ LogProjectionStore = (*PgStore)(nil)

// deployment_audit_at_gc_idx (at) serves the range; the joins resolve the
// tenant and drop rows whose deployment or app is gone.
const ledgerDeploymentAuditSQL = `
	select da.id, da.at, a.account_id::text, a.id::text, da.deployment_id::text,
	       da.kind, da.actor, coalesce(da.data, '{}'::jsonb)
	  from deployment_audit da
	  join deployments d on d.id = da.deployment_id
	  join apps a on a.id = d.app_id
	 where (da.at, da.id) > ($1, $2) and da.at < $3
	 order by da.at, da.id
	 limit $4`

// builds_finished_at_idx (finished_at) where finished_at is not null serves
// the range.
const ledgerBuildOutcomesSQL = `
	select b.id::text, a.account_id::text, a.id::text, d.id::text, b.kind, b.status,
	       coalesce(b.failure_class, ''), b.started_at, b.finished_at, coalesce(d.error, '')
	  from builds b
	  join deployments d on d.id = b.deployment_id
	  join apps a on a.id = d.app_id
	 where b.finished_at is not null and (b.finished_at, b.id) > ($1, $2::uuid) and b.finished_at < $3
	 order by b.finished_at, b.id
	 limit $4`

// ListDeploymentAuditForLedger implements LogProjectionStore.
func (s *PgStore) ListDeploymentAuditForLedger(ctx context.Context, afterAt time.Time, afterID int64, until time.Time, limit int) ([]LedgerDeploymentAudit, error) {
	rows, err := s.pool.Query(ctx, ledgerDeploymentAuditSQL, afterAt, afterID, until, logProjectionLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("state: list deployment audit for ledger: %w", err)
	}
	defer rows.Close()
	var out []LedgerDeploymentAudit
	for rows.Next() {
		var row LedgerDeploymentAudit
		var kind string
		if err := rows.Scan(&row.ID, &row.At, &row.AccountID, &row.AppID, &row.DeploymentID, &kind, &row.Actor, &row.Data); err != nil {
			return nil, fmt.Errorf("state: scan deployment audit for ledger: %w", err)
		}
		row.Kind, row.At = DeploymentAuditKind(kind), row.At.UTC()
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list deployment audit for ledger: %w", err)
	}
	return out, nil
}

// ListBuildOutcomesForLedger implements LogProjectionStore. An empty afterID
// starts at the nil UUID, which sorts before every build id.
func (s *PgStore) ListBuildOutcomesForLedger(ctx context.Context, afterAt time.Time, afterID string, until time.Time, limit int) ([]LedgerBuildOutcome, error) {
	if afterID == "" {
		afterID = "00000000-0000-0000-0000-000000000000"
	}
	rows, err := s.pool.Query(ctx, ledgerBuildOutcomesSQL, afterAt, afterID, until, logProjectionLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("state: list build outcomes for ledger: %w", err)
	}
	defer rows.Close()
	var out []LedgerBuildOutcome
	for rows.Next() {
		var row LedgerBuildOutcome
		var status, failureClass string
		var started pgtype.Timestamptz
		if err := rows.Scan(&row.BuildID, &row.AccountID, &row.AppID, &row.DeploymentID, &row.Kind, &status,
			&failureClass, &started, &row.FinishedAt, &row.DeploymentError); err != nil {
			return nil, fmt.Errorf("state: scan build outcome for ledger: %w", err)
		}
		row.Status, row.FailureClass, row.FinishedAt = BuildStatus(status), FailureClass(failureClass), row.FinishedAt.UTC()
		if started.Valid {
			row.StartedAt = started.Time.UTC()
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list build outcomes for ledger: %w", err)
	}
	return out, nil
}
