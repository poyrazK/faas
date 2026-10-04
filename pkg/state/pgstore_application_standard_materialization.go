package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardMaterializationStore = (*PgStore)(nil)

func (s *PgStore) ClaimApplicationStandardOperation(ctx context.Context, owner string) (ApplicationStandardWorkerClaim, error) {
	if !standardWorkerOwnerValid(owner) {
		return ApplicationStandardWorkerClaim{}, ErrInvalidArgument
	}
	row, err := sqlc.New().ClaimApplicationStandardOperation(ctx, s.pool, sqlc.ClaimApplicationStandardOperationParams{Owner: owner, LeaseSeconds: api.ApplicationStandardWorkerLease.Seconds()})
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardWorkerClaim{}, ErrNotFound
	}
	if err != nil {
		return ApplicationStandardWorkerClaim{}, fmt.Errorf("claim standard operation: %w", err)
	}
	return ApplicationStandardWorkerClaim{OperationID: pgUUIDString(row.ID), OrgID: pgUUIDString(row.OrgID), Owner: row.LeaseOwner, Generation: row.LeaseGeneration, Until: row.LeaseUntil.Time}, nil
}

func (s *PgStore) MaterializeNextApplicationStandardTarget(ctx context.Context, c ApplicationStandardWorkerClaim) (ApplicationStandardOperation, error) {
	if !standardWorkerClaimValid(c) {
		return ApplicationStandardOperation{}, ErrInvalidArgument
	}
	for attempt := 0; attempt < api.ApplicationStandardApprovalLockAttempts; attempt++ {
		o, err := s.materializeStandardTargetAttempt(ctx, c)
		if !standardApprovalRetryable(err) {
			return o, err
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

func (s *PgStore) materializeStandardTargetAttempt(ctx context.Context, c ApplicationStandardWorkerClaim) (ApplicationStandardOperation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplicationStandardOperation{}, err
	}
	defer tx.Rollback(ctx)
	q := sqlc.New()
	if _, err := q.LockApplicationStandardWorkerOperation(ctx, tx, sqlc.LockApplicationStandardWorkerOperationParams{OperationID: mustPgUUID(c.OperationID), OrgID: mustPgUUID(c.OrgID), Owner: c.Owner, Generation: c.Generation}); errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardOperation{}, ErrApplicationStandardLeaseLost
	} else if err != nil {
		return ApplicationStandardOperation{}, err
	}
	o, err := readStandardOperation(ctx, tx, c.OrgID, c.OperationID, "")
	if err != nil {
		return o, err
	}
	index := standardNextTarget(o)
	if index < 0 {
		return checkpointStandardMaterialization(ctx, tx, c, o)
	}
	row, err := q.GetApplicationStandardReviewPlan(ctx, tx, sqlc.GetApplicationStandardReviewPlanParams{OrgID: mustPgUUID(o.OrgID), PlanID: mustPgUUID(o.PlanID)})
	if err != nil {
		return o, err
	}
	plan, err := standardReviewPlanRow(row)
	if err != nil {
		return o, err
	}
	if _, err := q.LockApplicationStandardApprovalOrg(ctx, tx, mustPgUUID(o.OrgID)); err != nil {
		return o, err
	}
	t := &o.Targets[index]
	r := plan.Request
	r.Scope, r.ScopeID = "application", t.AppID
	snapshot, err := lockStandardReviewInputs(ctx, tx, o.OrgID, o.ApprovedBy, r)
	if err != nil {
		return o, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	app, err := standardMaterializationInput(snapshot, o, *t, plan.Request, now)
	if errors.Is(err, ErrApplicationStandardReviewStale) || errors.Is(err, ErrApplicationStandardReviewBlocked) {
		return blockStandardMaterialization(ctx, tx, c, o, index, "reviewed_inputs_changed")
	}
	if err != nil {
		return o, err
	}
	projection, err := readStandardControlProjection(ctx, tx, app, t.ApprovedApp, now)
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrApplicationStandardReviewBlocked) {
		return blockStandardMaterialization(ctx, tx, c, o, index, "control_projection_conflict")
	}
	if err != nil {
		return o, err
	}
	enrollment := standardInstalledEnrollment(app, *t, now)
	if err := installStandardControlProjection(ctx, tx, enrollment, projection); err != nil {
		return o, err
	}
	t.State, t.DesiredRevision, t.ErrorCode = "persisted", enrollment.DesiredRevision, ""
	if err := checkpointStandardTarget(ctx, tx, o.ID, *t); err != nil {
		return o, err
	}
	return checkpointStandardMaterialization(ctx, tx, c, o)
}

func blockStandardMaterialization(ctx context.Context, tx pgx.Tx, c ApplicationStandardWorkerClaim, o ApplicationStandardOperation, index int, code string) (ApplicationStandardOperation, error) {
	t := &o.Targets[index]
	t.State, t.ErrorCode = "blocked", code
	if err := checkpointStandardTarget(ctx, tx, o.ID, *t); err != nil {
		return o, err
	}
	return checkpointStandardMaterialization(ctx, tx, c, o)
}

func checkpointStandardTarget(ctx context.Context, tx pgx.Tx, operationID string, t ApplicationStandardOperationTarget) error {
	return sqlc.New().CheckpointApplicationStandardTarget(ctx, tx, sqlc.CheckpointApplicationStandardTargetParams{OperationID: mustPgUUID(operationID), AppID: mustPgUUID(t.AppID), State: t.State, DesiredRevision: t.DesiredRevision, ErrorCode: t.ErrorCode})
}

func checkpointStandardMaterialization(ctx context.Context, tx pgx.Tx, c ApplicationStandardWorkerClaim, o ApplicationStandardOperation) (ApplicationStandardOperation, error) {
	count, err := sqlc.New().CheckpointApplicationStandardWorkerOperation(ctx, tx, sqlc.CheckpointApplicationStandardWorkerOperationParams{OperationID: mustPgUUID(c.OperationID), OrgID: mustPgUUID(c.OrgID), Owner: c.Owner, Generation: c.Generation, State: standardProjectionOperationState(o)})
	if err != nil {
		return o, err
	}
	if count != 1 {
		return o, ErrApplicationStandardLeaseLost
	}
	result, err := readStandardOperation(ctx, tx, c.OrgID, c.OperationID, "")
	if err != nil {
		return o, err
	}
	if err := tx.Commit(ctx); err != nil {
		return o, fmt.Errorf("commit standard materialization: %w", err)
	}
	return result, nil
}

func readStandardControlProjection(ctx context.Context, tx pgx.Tx, app standardReviewAppSnapshot, target ApplicationStandardReviewedApp, now time.Time) (standardControlProjection, error) {
	q := sqlc.New()
	appID := mustPgUUID(app.AppID)
	drainRows, err := q.LockApplicationStandardDrainRows(ctx, tx, appID)
	if err != nil {
		return standardControlProjection{}, err
	}
	signerRows, err := q.LockApplicationStandardSignerRows(ctx, tx, appID)
	if err != nil {
		return standardControlProjection{}, err
	}
	bindingRows, err := q.ListApplicationStandardControlBindings(ctx, tx, appID)
	if err != nil {
		return standardControlProjection{}, err
	}
	backupRows, err := q.ListApplicationStandardControlBackups(ctx, tx, appID)
	if err != nil {
		return standardControlProjection{}, err
	}
	drains, signers := []AppLogDrain{}, []AppTrustedSigner{}
	bindings, backups := []standardControlBinding{}, []standardControlBackup{}
	for _, d := range drainRows {
		drains = append(drains, AppLogDrain{ID: pgUUIDString(d.ID), AppID: pgUUIDString(d.AppID), AccountID: pgUUIDString(d.AccountID), Kind: AppLogDrainKind(d.Kind), TargetURL: d.TargetUrl, AuthHeaderSealed: d.AuthHeaderSealed, Enabled: d.Enabled, CreatedAt: d.CreatedAt.Time, UpdatedAt: d.UpdatedAt.Time})
	}
	for _, signer := range signerRows {
		signers = append(signers, AppTrustedSigner{AppID: pgUUIDString(signer.AppID), AccountID: pgUUIDString(signer.AccountID), SignerName: signer.SignerName, CosignPublicKey: signer.CosignPublicKey, AddedAt: signer.AddedAt.Time, AddedByAccountID: pgUUIDString(signer.AddedByAccountID)})
	}
	for _, b := range bindingRows {
		bindings = append(bindings, standardControlBinding{AppID: pgUUIDString(b.AppID), Field: appstandards.Field(b.Field), ResourceID: pgUUIDString(b.ResourceID), PhysicalID: b.PhysicalID})
	}
	for _, b := range backupRows {
		backups = append(backups, standardControlBackup{AppID: pgUUIDString(b.AppID), Field: appstandards.Field(b.Field), ID: pgUUIDString(b.LogicalID), Body: b.Body, ConfigHash: b.ConfigHash})
	}
	destinations := map[string]ApplicationStandardLogDestination{}
	publishers := map[string]api.ApplicationStandardPublisher{}
	for _, id := range standardReviewStrings(target.Effective.Values[appstandards.LogDestinations]) {
		row, err := q.GetApplicationStandardLogDestination(ctx, tx, sqlc.GetApplicationStandardLogDestinationParams{OrgID: mustPgUUID(app.OrgID), ResourceID: mustPgUUID(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return standardControlProjection{}, err
		}
		d, err := standardLogDestinationRow(row)
		if err != nil {
			return standardControlProjection{}, err
		}
		destinations[id] = d
	}
	for _, id := range standardReviewStrings(target.Effective.Values[appstandards.TrustedPublishers]) {
		row, err := q.GetApplicationStandardPublisher(ctx, tx, sqlc.GetApplicationStandardPublisherParams{OrgID: mustPgUUID(app.OrgID), ResourceID: mustPgUUID(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return standardControlProjection{}, err
		}
		p, err := standardPublisherRow(row)
		if err != nil {
			return standardControlProjection{}, err
		}
		publishers[id] = p
	}
	return buildStandardControlProjection(app, target, drains, signers, bindings, backups, destinations, publishers, now)
}

func installStandardControlProjection(ctx context.Context, tx pgx.Tx, e ApplicationStandardEnrollment, p standardControlProjection) error {
	return installStandardControlProjectionWithClaim(ctx, tx, e, p, nil)
}

func installStandardControlProjectionWithClaim(ctx context.Context, tx pgx.Tx, e ApplicationStandardEnrollment, p standardControlProjection, c *ApplicationStandardEnrollmentClaim) error {
	q := sqlc.New()
	appID, orgID := mustPgUUID(e.AppID), mustPgUUID(e.OrgID)
	for _, b := range p.Backups {
		if err := q.SaveApplicationStandardControlBackup(ctx, tx, sqlc.SaveApplicationStandardControlBackupParams{AppID: appID, Field: string(b.Field), LogicalID: mustPgUUID(b.ID), Body: b.Body, ConfigHash: b.ConfigHash}); err != nil {
			return err
		}
	}
	if err := q.ClearApplicationStandardControlBindings(ctx, tx, appID); err != nil {
		return err
	}
	for _, b := range p.Bindings {
		if err := q.InsertApplicationStandardControlBinding(ctx, tx, sqlc.InsertApplicationStandardControlBindingParams{AppID: appID, Field: string(b.Field), ResourceID: mustPgUUID(b.ResourceID), PhysicalID: b.PhysicalID}); err != nil {
			return err
		}
	}
	base, _ := json.Marshal(e.BaseSettings)
	local, _ := json.Marshal(e.LocalSettings)
	pins, _ := json.Marshal(e.Adoptions)
	effective, _ := json.Marshal(e.Effective)
	var count int64
	var err error
	if c == nil {
		count, err = q.InstallApplicationStandardEnrollmentIntent(ctx, tx, sqlc.InstallApplicationStandardEnrollmentIntentParams{AppID: appID, OrgID: orgID, BaseSettings: base, LocalSettings: local, Additional: standardPgUUIDs(e.AdditionalLogDestinations), Adoptions: pins, Effective: effective, EffectiveHash: e.EffectiveHash, ExceptionExpiresAt: standardNullablePgTime(e.ExceptionExpiresAt), MaterializedFields: standardFieldStrings(e.MaterializedFields), ExpectedRevision: e.DesiredRevision - 1})
	} else {
		count, err = q.InstallAutomaticApplicationStandardIntent(ctx, tx, sqlc.InstallAutomaticApplicationStandardIntentParams{AppID: appID, OrgID: orgID, BaseSettings: base, LocalSettings: local, Additional: standardPgUUIDs(e.AdditionalLogDestinations), Adoptions: pins, Effective: effective, EffectiveHash: e.EffectiveHash, ExceptionExpiresAt: standardNullablePgTime(e.ExceptionExpiresAt), MaterializedFields: standardFieldStrings(e.MaterializedFields), DesiredRevision: c.DesiredRevision, Owner: c.Owner, Generation: c.Generation})
	}
	if err != nil {
		return err
	}
	if count != 1 {
		if c != nil {
			return ErrApplicationStandardLeaseLost
		}
		return ErrApplicationStandardReviewStale
	}
	if err := q.InstallApplicationStandardScalarControls(ctx, tx, sqlc.InstallApplicationStandardScalarControlsParams{AppID: appID, RequireSigned: p.RequireSigned, SecurityPolicy: string(p.SecurityPolicy), Cidrs: p.CIDRs, Ports: standardInt32s(p.Ports)}); err != nil {
		return err
	}
	drainIDs := []string{}
	for _, d := range p.Drains {
		drainIDs = append(drainIDs, d.ID)
	}
	if err := q.RemoveApplicationStandardUnselectedDrains(ctx, tx, sqlc.RemoveApplicationStandardUnselectedDrainsParams{AppID: appID, DrainIds: standardPgUUIDs(drainIDs)}); err != nil {
		return err
	}
	for _, d := range p.Drains {
		if err := q.InstallApplicationStandardDrain(ctx, tx, sqlc.InstallApplicationStandardDrainParams{ID: mustPgUUID(d.ID), AppID: appID, AccountID: mustPgUUID(d.AccountID), Kind: string(d.Kind), TargetUrl: d.TargetURL, AuthHeaderSealed: d.AuthHeaderSealed, Enabled: d.Enabled, CreatedAt: standardPgTime(d.CreatedAt), UpdatedAt: standardPgTime(d.UpdatedAt)}); err != nil {
			return err
		}
	}
	names := []string{}
	for _, signer := range p.Signers {
		names = append(names, signer.SignerName)
	}
	if err := q.RemoveApplicationStandardUnselectedSigners(ctx, tx, sqlc.RemoveApplicationStandardUnselectedSignersParams{AppID: appID, Names: names}); err != nil {
		return err
	}
	for _, signer := range p.Signers {
		if err := q.InstallApplicationStandardSigner(ctx, tx, sqlc.InstallApplicationStandardSignerParams{AppID: appID, AccountID: mustPgUUID(signer.AccountID), Name: signer.SignerName, Key: signer.CosignPublicKey, AddedAt: standardPgTime(signer.AddedAt), AddedBy: mustPgUUID(signer.AddedByAccountID)}); err != nil {
			return err
		}
	}
	count, err = q.PersistApplicationStandardEnrollment(ctx, tx, sqlc.PersistApplicationStandardEnrollmentParams{AppID: appID, OrgID: orgID, DesiredRevision: e.DesiredRevision})
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrApplicationStandardReviewStale
	}
	return q.NotifyApplicationStandardControlsChanged(ctx, tx, e.AppID)
}

func standardPgTime(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
func standardInt32s(ports []int) []int32 {
	out := make([]int32, len(ports))
	for i, p := range ports {
		out[i] = int32(p)
	}
	return out
}
