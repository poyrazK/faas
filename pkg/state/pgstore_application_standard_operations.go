package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardOperationStore = (*PgStore)(nil)

func readStandardOperation(ctx context.Context, db sqlc.DBTX, orgID, operationID, planID string) (ApplicationStandardOperation, error) {
	raw, err := sqlc.New().ReadApplicationStandardOperation(ctx, db, sqlc.ReadApplicationStandardOperationParams{OrgID: mustPgUUID(orgID), OperationID: operationID, PlanID: planID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardOperation{}, ErrNotFound
	}
	if err != nil {
		return ApplicationStandardOperation{}, fmt.Errorf("read standard operation: %w", err)
	}
	var o ApplicationStandardOperation
	var private struct {
		Targets []struct {
			Input appstandards.ApplicationApprovalInput `json:"input"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		return o, fmt.Errorf("decode standard operation: %w", err)
	}
	if err := json.Unmarshal(raw, &private); err != nil {
		return o, fmt.Errorf("decode standard operation inputs: %w", err)
	}
	// jsonb timestamps reflect the database session's zone. Keep approval,
	// reads and operator controls consistent for the same persisted instant.
	o.CreatedAt, o.UpdatedAt = o.CreatedAt.UTC(), o.UpdatedAt.UTC()
	for i := range o.Targets {
		t := &o.Targets[i]
		t.UpdatedAt = t.UpdatedAt.UTC()
		t.approvalInput = private.Targets[i].Input
		hash, err := standardReviewDigest(t.ApprovedApp.Effective)
		if err != nil || t.AppID != t.ApprovedApp.AppID || t.approvalInput.AppID != t.AppID || t.approvalInput.OrgID != o.OrgID || hash != t.approvalInput.EffectiveHash {
			return ApplicationStandardOperation{}, fmt.Errorf("stored standard operation target proof is invalid")
		}
	}
	return o, nil
}

func (s *PgStore) GetApplicationStandardOperation(ctx context.Context, orgID, operationID string) (ApplicationStandardOperation, error) {
	if !validStandardResourceRead(orgID, operationID) {
		return ApplicationStandardOperation{}, ErrInvalidArgument
	}
	return readStandardOperation(ctx, s.pool, orgID, operationID, "")
}

func standardApprovalRetryable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.Is(err, ErrApplicationStandardReviewBusy) || errors.As(err, &pgErr) && (pgErr.Code == "55P03" || pgErr.Code == "40P01" || pgErr.Code == "40001")
}

func (s *PgStore) ApproveApplicationStandardReview(ctx context.Context, orgID, actorID, planID, expected string) (ApplicationStandardOperation, error) {
	if !standardApprovalIdentityValid(orgID, actorID, planID) {
		return ApplicationStandardOperation{}, ErrInvalidArgument
	}
	for attempt := 0; attempt < api.ApplicationStandardApprovalLockAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ApplicationStandardOperation{}, err
		}
		o, err := s.approveStandardReviewAttempt(ctx, orgID, actorID, planID, expected)
		if !standardApprovalRetryable(err) {
			return o, err
		}
		if attempt+1 == api.ApplicationStandardApprovalLockAttempts {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * api.ApplicationStandardApprovalLockRetry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ApplicationStandardOperation{}, ctx.Err()
		case <-timer.C:
		}
	}
	return ApplicationStandardOperation{}, ErrApplicationStandardReviewBusy
}

func (s *PgStore) approveStandardReviewAttempt(ctx context.Context, orgID, actorID, planID, expected string) (ApplicationStandardOperation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplicationStandardOperation{}, fmt.Errorf("begin standard approval: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if _, err := q.LockApplicationStandardApprovalOrg(ctx, tx, mustPgUUID(orgID)); errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardOperation{}, ErrNotFound
	} else if err != nil {
		return ApplicationStandardOperation{}, fmt.Errorf("lock approval organization: %w", err)
	}
	row, err := q.LockApplicationStandardReviewPlan(ctx, tx, sqlc.LockApplicationStandardReviewPlanParams{OrgID: mustPgUUID(orgID), PlanID: mustPgUUID(planID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardOperation{}, ErrNotFound
	}
	if err != nil {
		return ApplicationStandardOperation{}, fmt.Errorf("lock approval plan: %w", err)
	}
	saved, err := standardReviewPlanRow(row)
	if err != nil {
		return ApplicationStandardOperation{}, err
	}
	snapshot, err := lockStandardReviewInputs(ctx, tx, orgID, actorID, saved.Request)
	if err != nil {
		return ApplicationStandardOperation{}, standardReviewFreshnessError(err)
	}
	if err := authorizeStandardApproval(snapshot); err != nil {
		return ApplicationStandardOperation{}, err
	}
	if expected != saved.ApprovalHash {
		return ApplicationStandardOperation{}, ErrApplicationStandardReviewStale
	}
	prior, err := readStandardOperation(ctx, tx, orgID, "", saved.ID)
	if err == nil {
		return prior, nil
	} // Idempotence still requires current authorization.
	if !errors.Is(err, ErrNotFound) {
		return ApplicationStandardOperation{}, err
	}
	now := time.Now().UTC()
	fresh, err := buildStandardReview(snapshot, saved.Request, saved.ID, saved.CreatedBy, now)
	if err != nil {
		return ApplicationStandardOperation{}, standardReviewFreshnessError(err)
	}
	if err := validateStandardReviewHash(saved, fresh, expected, now); err != nil {
		return ApplicationStandardOperation{}, err
	}
	active, err := q.HasApplicationStandardActiveOperation(ctx, tx, mustPgUUID(saved.Request.AssignmentID))
	if err != nil {
		return ApplicationStandardOperation{}, fmt.Errorf("read active standard operation: %w", err)
	}
	if active {
		return ApplicationStandardOperation{}, ErrApplicationStandardOperationInProgress
	}
	o, err := newStandardOperation(fresh, actorID, now)
	if err != nil {
		return ApplicationStandardOperation{}, err
	}
	if err := persistStandardApproval(ctx, tx, fresh.Request, o); err != nil {
		return ApplicationStandardOperation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApplicationStandardOperation{}, fmt.Errorf("commit standard approval: %w", err)
	}
	return o, nil
}

func standardReviewLockIDs(s standardReviewSnapshot, actorID string, r ApplicationStandardReviewRequest) (projects, accounts []string) {
	accounts = []string{canonicalStandardUUID(actorID)}
	if s.ScopeOwnerID != "" {
		accounts = append(accounts, s.ScopeOwnerID)
	}
	if r.Scope == "project" {
		projects = append(projects, r.ScopeID)
	}
	for _, a := range s.Applications {
		accounts = append(accounts, a.AccountID)
		if a.ProjectID != "" {
			projects = append(projects, a.ProjectID)
		}
	}
	if r.Scope == "organization" {
		for _, a := range s.Assignments {
			if a.Active && a.Scope == "project" {
				projects = append(projects, a.ScopeID)
			}
		}
	}
	for _, ids := range [][]string{projects, accounts} {
		slices.Sort(ids)
	}
	return slices.Compact(projects), slices.Compact(accounts)
}

func standardPgUUIDs(ids []string) []pgtype.UUID {
	result := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		result = append(result, mustPgUUID(id))
	}
	return result
}

func lockStandardReviewInputs(ctx context.Context, tx pgx.Tx, orgID, actorID string, r ApplicationStandardReviewRequest) (standardReviewSnapshot, error) {
	initial, err := readStandardReviewSnapshot(ctx, tx, orgID, actorID, r)
	if err != nil {
		return initial, err
	}
	projects, accounts := standardReviewLockIDs(initial, actorID, r)
	q := sqlc.New()
	if _, err := q.LockApplicationStandardApprovalProjects(ctx, tx, standardPgUUIDs(projects)); err != nil {
		return initial, fmt.Errorf("lock approval projects: %w", err)
	}
	if _, err := q.LockApplicationStandardApprovalAccounts(ctx, tx, standardPgUUIDs(accounts)); err != nil {
		return initial, fmt.Errorf("lock approval accounts: %w", err)
	}
	locked, err := q.TryLockApplicationStandardApprovalQuotas(ctx, tx, standardPgUUIDs(accounts))
	if err != nil {
		return initial, fmt.Errorf("lock approval quotas: %w", err)
	}
	if !locked {
		return initial, ErrApplicationStandardReviewBusy
	}
	if _, err := q.LockApplicationStandardApprovalMemberships(ctx, tx, mustPgUUID(orgID)); err != nil {
		return initial, fmt.Errorf("lock approval memberships: %w", err)
	}
	apps, err := q.LockApplicationStandardApprovalApps(ctx, tx, sqlc.LockApplicationStandardApprovalAppsParams{OrgID: mustPgUUID(orgID), Scope: r.Scope, ScopeID: mustPgUUID(r.ScopeID)})
	if err != nil {
		return initial, fmt.Errorf("lock approval applications: %w", err)
	}
	if _, err := q.LockApplicationStandardApprovalEnrollments(ctx, tx, apps); err != nil {
		return initial, fmt.Errorf("lock approval enrollments: %w", err)
	}
	locked, err = q.TryLockApplicationStandardApprovalControls(ctx, tx, apps)
	if err != nil {
		return initial, fmt.Errorf("lock approval controls: %w", err)
	}
	if !locked {
		return initial, ErrApplicationStandardReviewBusy
	}
	artifacts, err := q.LockApplicationStandardApprovalArtifacts(ctx, tx, apps)
	if err != nil {
		return initial, fmt.Errorf("lock approval artifacts: %w", err)
	}
	locked, err = q.TryLockApplicationStandardApprovalArtifactChildren(ctx, tx, artifacts)
	if err != nil {
		return initial, fmt.Errorf("lock approval artifact children: %w", err)
	}
	if !locked {
		return initial, ErrApplicationStandardReviewBusy
	}
	fresh, err := readStandardReviewSnapshot(ctx, tx, orgID, actorID, r)
	if err != nil {
		return fresh, err
	}
	// A legacy owner change may have committed before its app was locked.
	// Reacquire the entire lock set instead of reading an unlocked entitlement.
	freshProjects, freshAccounts := standardReviewLockIDs(fresh, actorID, r)
	if !slices.Equal(projects, freshProjects) || !slices.Equal(accounts, freshAccounts) {
		return fresh, ErrApplicationStandardReviewBusy
	}
	return fresh, nil
}

func persistStandardApproval(ctx context.Context, tx pgx.Tx, r ApplicationStandardReviewRequest, o ApplicationStandardOperation) error {
	q, now := sqlc.New(), pgtype.Timestamptz{Time: o.CreatedAt, Valid: true}
	if r.ExpectedRevision == 0 {
		err := q.InsertApplicationStandardApprovedAssignment(ctx, tx, sqlc.InsertApplicationStandardApprovedAssignmentParams{ID: mustPgUUID(r.AssignmentID), OrgID: mustPgUUID(o.OrgID), Scope: r.Scope, ScopeID: mustPgUUID(r.ScopeID), StandardID: mustPgUUID(r.StandardID), AdmissionVersion: r.AdmissionVersion, Active: r.Active, ActorID: mustPgUUID(o.ApprovedBy), Now: now})
		if err != nil {
			return fmt.Errorf("insert approved assignment: %w", err)
		}
	} else {
		count, err := q.UpdateApplicationStandardApprovedAssignment(ctx, tx, sqlc.UpdateApplicationStandardApprovedAssignmentParams{ID: mustPgUUID(r.AssignmentID), OrgID: mustPgUUID(o.OrgID), AdmissionVersion: r.AdmissionVersion, Active: r.Active, ExpectedRevision: r.ExpectedRevision, Now: now})
		if err != nil {
			return fmt.Errorf("update approved assignment: %w", err)
		}
		if count != 1 {
			return ErrApplicationStandardReviewStale
		}
	}
	if err := q.InsertApplicationStandardOperation(ctx, tx, sqlc.InsertApplicationStandardOperationParams{ID: mustPgUUID(o.ID), OrgID: mustPgUUID(o.OrgID), PlanID: mustPgUUID(o.PlanID), AssignmentID: mustPgUUID(o.AssignmentID), ApprovalHash: o.ApprovalHash, ApprovedBy: mustPgUUID(o.ApprovedBy), BatchSize: int32(o.BatchSize), Now: now}); err != nil {
		return fmt.Errorf("insert approved operation: %w", err)
	}
	for _, target := range o.Targets {
		body, err := json.Marshal(standardOperationTargetBody{Application: target.ApprovedApp, Input: target.approvalInput})
		if err != nil {
			return err
		}
		if err := q.InsertApplicationStandardOperationTarget(ctx, tx, sqlc.InsertApplicationStandardOperationTargetParams{OperationID: mustPgUUID(o.ID), AppID: mustPgUUID(target.AppID), Position: int32(target.Position), ApprovedApp: body, Now: now}); err != nil {
			return fmt.Errorf("insert approved operation target: %w", err)
		}
	}
	audit := standardApprovalAudit(o)
	if err := q.InsertApplicationStandardOperationAudit(ctx, tx, sqlc.InsertApplicationStandardOperationAuditParams{ID: pgtype.UUID{Bytes: audit.ID, Valid: true}, Kind: audit.Kind, Now: now, Data: audit.Data}); err != nil {
		return fmt.Errorf("insert standard approval audit: %w", err)
	}
	return nil
}
