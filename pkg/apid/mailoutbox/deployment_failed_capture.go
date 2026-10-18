package mailoutbox

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

// CaptureDeploymentFailures is a bounded reconciliation pass over durable
// source stamps. LISTEN is never the sole delivery guarantee. Deployment state
// is already committed and cannot be rolled back by mail capacity or outages.
func (s *Service) CaptureDeploymentFailures(ctx context.Context) error {
	if !s.deploymentFailedEnabled() {
		return internalmail.ErrDisabled
	}
	pass, cancel := context.WithTimeout(ctx, api.InternalMailProducerOutboxPassTimeout)
	defer cancel()
	// After an explicit database recovery, advance the discovery boundary to
	// this generation. Unobserved source rows from the restored past cannot
	// become freshly issued mail. Same-generation restarts retain the boundary.
	if err := s.enrollDeploymentCapture(pass, s.deploymentFailedEnabled); err != nil {
		return err
	}
	candidates, err := s.queries.DeploymentFailureCandidates(pass, s.pool, q.DeploymentFailureCandidatesParams{Namespace: s.namespace, ProducerID: s.producer,
		TtlSeconds: int32(api.InternalMailDeploymentFailedTTL / time.Second), BatchSize: api.InternalMailDeploymentFailedCaptureBatch})
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if err := s.captureDeploymentFailure(pass, candidate); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) captureDeploymentFailure(ctx context.Context, c q.DeploymentFailureCandidatesRow) error {
	if !s.deploymentFailedEnabled() {
		return internalmail.ErrDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return err
	}
	recovery, err := s.recovery(ctx, tx)
	if err != nil {
		return err
	}
	if recovery.Held {
		return internalmail.ErrRecoveryHeld
	}
	capture, err := s.queries.DeploymentFailureCapture(ctx, tx, q.DeploymentFailureCaptureParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return err
	}
	if capture.RecoveryID != recovery.RecoveryID {
		return internalmail.ErrRecoveryHeld
	}
	a, err := s.queries.ShareAccount(ctx, tx, c.MailFailureAccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	app, err := s.queries.LockDeploymentFailureApp(ctx, tx, c.AppID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	authorized := true
	if app.AccountID != a.ID {
		if !app.OrgID.Valid {
			authorized = false
		} else {
			_, err = s.queries.ShareDeploymentFailureMember(ctx, tx, q.ShareDeploymentFailureMemberParams{OrgID: app.OrgID, AccountID: a.ID})
			if errors.Is(err, pgx.ErrNoRows) {
				authorized = false
			} else if err != nil {
				return err
			}
		}
	}
	success, err := s.queries.ShareDeploymentFailureFence(ctx, tx, q.ShareDeploymentFailureFenceParams{AppID: c.AppID, Scope: c.Scope})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	d, err := s.queries.DeploymentFailureSnapshot(ctx, tx, q.DeploymentFailureSnapshotParams{ID: c.ID, SourceMaxBytes: api.InternalMailDeploymentFailedSourceMaxBytes})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if d.AppID != c.AppID || d.Scope != c.Scope || d.MailFailureRevision != c.MailFailureRevision || d.MailFailureAccountID != a.ID {
		return nil
	}
	observed, err := s.queries.DeploymentFailureObserved(ctx, tx, q.DeploymentFailureObservedParams{DeploymentID: d.ID, FailureRevision: d.MailFailureRevision})
	if err != nil {
		return err
	}
	if observed {
		return nil
	}
	disposition := ""
	switch {
	case !d.MailFailedAt.Valid || d.MailFailedAt.Time.Before(capture.StartedAt.Time):
		return nil
	case !d.MailFailedAt.Time.Add(api.InternalMailDeploymentFailedTTL).After(time.Now()):
		disposition = "expired"
	case d.Status != "failed" || d.DeletedAt.Valid || app.Status != "active" || !d.MailFailureAppRevision.Valid || d.MailFailureAppRevision.Int64 != app.DeploymentMailRevision || !d.MailFailureSuccessRevision.Valid || d.MailFailureSuccessRevision.Int64 != success:
		disposition = "stale"
	case !authorized || !quotaWarningAccountAvailable(a.Status) || a.DeletionRequestedAt.Valid || !d.MailFailureAccountRevision.Valid || d.MailFailureAccountRevision.Int64 != a.DeploymentMailRevision:
		disposition = "unavailable"
	case app.LastDeployFailedEmailAt.Valid && !d.MailFailedAt.Time.After(app.LastDeployFailedEmailAt.Time.Add(api.InternalMailDeploymentFailedCooldown)):
		// This also covers a legacy inline claim during an issuance rollback.
		// Waiting an hour or restarting cannot turn that old failure into a
		// second notice after the flag is explicitly re-enabled.
		disposition = "cooldown"
	}
	address, addressErr := internalmail.NormalizeSuppressionAddress(a.Email)
	if addressErr != nil {
		disposition = "unavailable"
	}
	var intent pgtype.UUID
	if disposition == "" {
		_, err := s.queries.ClaimDeploymentFailureCooldown(ctx, tx, q.ClaimDeploymentFailureCooldownParams{ID: app.ID, CooldownSeconds: int32(api.InternalMailDeploymentFailedCooldown / time.Second)})
		if errors.Is(err, pgx.ErrNoRows) {
			disposition = "cooldown"
		} else if err != nil {
			return err
		} else {
			intent, err = s.saveDeploymentFailed(ctx, tx, a.ID, address, recovery.RecoveryID, app.Slug, d)
			if err != nil {
				return err
			}
			disposition = "queued"
		}
	}
	if err := s.queries.RecordDeploymentFailureObservation(ctx, tx, q.RecordDeploymentFailureObservationParams{DeploymentID: d.ID, FailureRevision: d.MailFailureRevision, AccountID: a.ID, Disposition: disposition, IntentID: intent}); err != nil {
		return err
	}
	if !s.deploymentFailedEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}

// Account is already shared by dispatch. No deployment row locks are taken:
// source writers commit their changes through the separate scope fence.
func (s *Service) deploymentFailureCurrent(ctx context.Context, tx pgx.Tx, r q.PlatformMailProducerOutbox) (bool, error) {
	app, err := s.queries.ShareDeploymentFailureApp(ctx, tx, r.DeploymentFailureAppID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if app.AccountID != r.AccountID {
		if !app.OrgID.Valid {
			return false, nil
		}
		_, err = s.queries.ShareDeploymentFailureMember(ctx, tx, q.ShareDeploymentFailureMemberParams{OrgID: app.OrgID, AccountID: r.AccountID})
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	_, err = s.queries.ShareDeploymentFailureFence(ctx, tx, q.ShareDeploymentFailureFenceParams{AppID: app.ID, Scope: r.DeploymentFailureScope.String})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return s.queries.DeploymentFailureCurrent(ctx, tx, q.DeploymentFailureCurrentParams{Namespace: r.Namespace, ID: r.ID, RecipientAddress: r.RecipientAddress})
}
