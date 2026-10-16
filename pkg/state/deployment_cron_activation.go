package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// DeploymentCronActivationStore is polled by apid; activation is atomic with
// the cron guard and writes. A CLI connection is never required to finish.
type DeploymentCronActivationStore interface {
	ReconcileDeploymentCrons(context.Context) ([]CronActivationChange, error)
}
type CronActivationChange struct{ AccountID, AppID string }
type cronActivation struct {
	Intent   api.DeploymentCronStagingRequest
	Revision int64
	UploadID string
	Status   string
}

func decideCronPreparation(current []api.DeploymentCronConfig, revision int64, prior *cronActivation, r api.DeploymentCronStagingRequest, accepted bool) (cronActivation, bool, error) {
	if err := r.Validate(); err != nil {
		return cronActivation{}, false, ErrInvalidArgument
	}
	if prior != nil && prior.Intent.SessionID == r.SessionID {
		if prior.Intent.Fingerprint() != r.Fingerprint() {
			return cronActivation{}, false, ErrDeploymentCronConflict
		}
		if r.Operation == "prepare" {
			if prior.Status == "canceled" {
				return cronActivation{}, false, ErrDeploymentCronConflict
			}
			return *prior, false, nil
		}
		if accepted || prior.Status == "activated" {
			return cronActivation{}, false, ErrDeploymentCronConflict
		}
		next := *prior
		next.Status = "canceled"
		return next, prior.Status != "canceled", nil
	}
	if accepted || revision != r.ExpectedRevision || !api.DeploymentCronsEqual(current, r.Before) {
		return cronActivation{}, false, ErrDeploymentCronConflict
	}
	status := "prepared"
	if r.Operation == "cancel" {
		status = "canceled"
	}
	return cronActivation{Intent: r, Revision: revision + 1, Status: status}, true, nil
}
func activationReceipt(appID string, r api.DeploymentCronStagingRequest, a cronActivation) DeploymentCronStagingResult {
	status := a.Status
	if r.Operation == "cancel" {
		status = "cleaned"
	}
	return DeploymentCronStagingResult{Receipt: api.DeploymentStagingReceipt{SessionID: r.SessionID, AppID: appID, Revision: a.Revision, Status: status}}
}
func (m *MemStore) prepareCronActivationLocked(appID string, p DeploymentCronStagingParams) (DeploymentCronStagingResult, error) {
	app, ok := m.apps[appID]
	r := p.Request
	if !ok || app.AccountID != p.AccountID || app.Status == AppDeleted {
		return DeploymentCronStagingResult{}, ErrNotFound
	}
	if appID != r.ExpectedAppID {
		return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
	}
	var prior *cronActivation
	if a, ok := m.deploymentCronActivations[appID]; ok {
		prior = &a
	}
	if r.Operation == "track_upload" {
		if prior == nil || prior.Intent.SessionID != r.SessionID || prior.Intent.Fingerprint() != r.Fingerprint() || prior.Status != "prepared" {
			return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
		}
		upload, ok := m.uploadSessions[r.UploadID]
		if !ok || app.Slug != upload.AppSlug || canonicalMemUUID(p.AccountID) != upload.AccountID.String() {
			return DeploymentCronStagingResult{}, ErrNotFound
		}
		if old, ok := m.uploadSessions[prior.UploadID]; ok && old.Status == "committed" && prior.UploadID != r.UploadID {
			return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
		}
		prior.UploadID = r.UploadID
		m.deploymentCronActivations[appID] = *prior
		return activationReceipt(appID, r, *prior), nil
	}
	// A duplicate accepted preparation is safe, but cancellation is never safe.
	_, accepted := m.deploymentSubmissionReceipts[submissionKey(appID, r.SessionID)]
	if prior != nil && prior.Intent.SessionID == r.SessionID {
		if upload, ok := m.uploadSessions[prior.UploadID]; ok && upload.Status == "committed" && upload.DeploymentID.Valid {
			accepted = true
		}
	}
	if (prior == nil || prior.Intent.SessionID != r.SessionID) && m.latestPolicyDeploymentLocked(appID) != r.ExpectedDeploymentID {
		return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
	}
	next, write, err := decideCronPreparation(cronIntents(m.httpCronsLocked(appID)), m.deploymentCronStates[appID].Revision, prior, r, accepted)
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	if write {
		if r.Operation == "prepare" {
			appCount, accountCount := 0, 0
			for _, c := range m.crons {
				if c.AppID == appID {
					appCount++
				}
				if a := m.apps[c.AppID]; a.AccountID == p.AccountID && a.Status != AppDeleted {
					accountCount++
				}
			}
			if err := checkCronReplacementQuota(appCount, accountCount, len(m.httpCronsLocked(appID)), len(r.Desired), p); err != nil {
				return DeploymentCronStagingResult{}, err
			}
		}
		if m.deploymentCronActivations == nil {
			m.deploymentCronActivations = map[string]cronActivation{}
		}
		if m.deploymentCronStates == nil {
			m.deploymentCronStates = map[string]deploymentPolicyState{}
		}
		meta := m.deploymentCronStates[appID]
		if next.Revision > meta.Revision {
			meta.Revision = next.Revision
			meta.Owner, meta.Fingerprint = "", ""
			m.deploymentCronStates[appID] = meta
			delete(m.deploymentCronBefore, appID)
		}
		if prior != nil && prior.Intent.SessionID != r.SessionID {
			m.archiveCronOutcomeLocked(appID, *prior)
		}
		m.deploymentCronActivations[appID] = next
	}
	return activationReceipt(appID, r, next), nil
}
func (s *PgStore) prepareCronActivation(ctx context.Context, appID string, p DeploymentCronStagingParams) (DeploymentCronStagingResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	accountID, err := q.LockCronAccountForApp(ctx, tx, appID)
	if err != nil {
		return DeploymentCronStagingResult{}, mapErr(err)
	}
	meta, err := q.ReadDeploymentCronState(ctx, tx, appID)
	if err != nil {
		return DeploymentCronStagingResult{}, mapErr(err)
	}
	r := p.Request
	if accountID != p.AccountID || meta.Status == "deleted" {
		return DeploymentCronStagingResult{}, ErrNotFound
	}
	if appID != r.ExpectedAppID {
		return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
	}
	prior, err := readCronActivation(ctx, q, tx, appID)
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	if r.Operation == "track_upload" {
		if prior == nil || prior.Intent.SessionID != r.SessionID || prior.Intent.Fingerprint() != r.Fingerprint() {
			return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
		}
		wrote, err := q.TrackCronActivationUpload(ctx, tx, sqlc.TrackCronActivationUploadParams{AppID: appID, SessionID: r.SessionID, Fingerprint: r.Fingerprint(), UploadID: r.UploadID})
		if err != nil {
			return DeploymentCronStagingResult{}, err
		}
		if wrote != 1 {
			return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return DeploymentCronStagingResult{}, err
		}
		return activationReceipt(appID, r, *prior), nil
	}
	snapshot, err := q.ReadDeploymentCronSnapshot(ctx, tx, sqlc.ReadDeploymentCronSnapshotParams{AppID: appID, AccountID: p.AccountID})
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	latest, err := q.ReadDeploySettingsLatest(ctx, tx, appID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return DeploymentCronStagingResult{}, err
	}
	if (prior == nil || prior.Intent.SessionID != r.SessionID) && (snapshot.LatestDeploymentID != r.ExpectedDeploymentID || latest.ID != r.ExpectedDeploymentID) {
		return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
	}
	_, receiptErr := q.ReadDeploymentSubmission(ctx, tx, sqlc.ReadDeploymentSubmissionParams{AppID: appID, AttemptID: r.SessionID})
	if receiptErr != nil && !errors.Is(receiptErr, pgx.ErrNoRows) {
		return DeploymentCronStagingResult{}, receiptErr
	}
	uploadAccepted, err := q.CronActivationUploadAccepted(ctx, tx, sqlc.CronActivationUploadAcceptedParams{AppID: appID, SessionID: r.SessionID})
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	current, err := decodeCronSnapshot(snapshot.Crons)
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	next, write, err := decideCronPreparation(current, meta.DeploymentCronRevision, prior, r, receiptErr == nil || uploadAccepted)
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	if write {
		if r.Operation == "prepare" {
			counts, err := q.CountDeploymentCrons(ctx, tx, sqlc.CountDeploymentCronsParams{AppID: appID, AccountID: p.AccountID})
			if err != nil {
				return DeploymentCronStagingResult{}, err
			}
			if err = checkCronReplacementQuota(int(counts.AppCount), int(counts.AccountCount), len(current), len(r.Desired), p); err != nil {
				return DeploymentCronStagingResult{}, err
			}
		}
		data, err := json.Marshal(r)
		if err != nil {
			return DeploymentCronStagingResult{}, err
		}
		if prior != nil && prior.Intent.SessionID != r.SessionID {
			if err = q.ArchiveDeploymentCronOutcome(ctx, tx, appID); err != nil {
				return DeploymentCronStagingResult{}, err
			}
		}
		err = q.WriteCronActivation(ctx, tx, sqlc.WriteCronActivationParams{AppID: appID, SessionID: r.SessionID, Intent: data, Fingerprint: r.Fingerprint(), Revision: next.Revision, Status: next.Status})
		if err != nil {
			return DeploymentCronStagingResult{}, err
		}
		revision := max(meta.DeploymentCronRevision, next.Revision)
		if revision > meta.DeploymentCronRevision {
			err = q.WriteDeploymentCronState(ctx, tx, sqlc.WriteDeploymentCronStateParams{AppID: appID, Revision: revision})
		}
		if err != nil {
			return DeploymentCronStagingResult{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return DeploymentCronStagingResult{}, err
	}
	return activationReceipt(appID, r, next), nil
}
func readCronActivation(ctx context.Context, q *sqlc.Queries, tx sqlc.DBTX, appID string) (*cronActivation, error) {
	row, err := q.ReadCronActivation(ctx, tx, appID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a := &cronActivation{Revision: row.Revision, Status: row.Status, UploadID: row.UploadID}
	err = json.Unmarshal(row.Intent, &a.Intent)
	return a, err
}

func cronDeploymentDisposition(status, scope, rollout string, traffic, steps int, explicit, competing bool, id, latest string) string {
	if rollout == "aborted" || scope != "" && scope != DefaultEnvScope || explicit && traffic == 0 && steps == 0 {
		return "discarded"
	}
	switch status {
	case "failed", "cancelled", "canceled", "superseded", "aborted":
		return "discarded"
	}
	if id != latest {
		return "discarded"
	}
	if status != "live" || traffic != 100 || competing || rollout == "rolling_out" || steps > 0 && rollout != "complete" {
		return "prepared"
	}
	return "activated"
}
func (m *MemStore) ReconcileDeploymentCrons(_ context.Context) ([]CronActivationChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	changes := []CronActivationChange{}
	for appID, a := range m.deploymentCronActivations {
		if a.Status != "prepared" {
			continue
		}
		receipt, ok := m.deploymentSubmissionReceipts[submissionKey(appID, a.Intent.SessionID)]
		if !ok {
			if upload, found := m.uploadSessions[a.UploadID]; found && upload.Status == "committed" && upload.DeploymentID.Valid {
				receipt.DeploymentID = upload.DeploymentID.String
				for id := range m.deployments {
					if sameDeploymentID(id, receipt.DeploymentID) {
						receipt.DeploymentID = id
						break
					}
				}
			} else {
				continue
			}
		}
		d, ok := m.deployments[receipt.DeploymentID]
		if !ok {
			a.Status = "discarded"
			m.deploymentCronActivations[appID] = a
			continue
		}
		latest := ""
		var newest Deployment
		competing := false
		for _, candidate := range m.deployments {
			if candidate.AppID != appID || normalizedDeploymentScope(candidate.Scope) != DefaultEnvScope {
				continue
			}
			if candidate.ID != d.ID && candidate.Status == DeployLive && candidate.TrafficPercent > 0 {
				competing = true
			}
			if candidate.TrafficPercentExplicit && candidate.TrafficPercent == 0 && candidate.CanaryTotalSteps == 0 {
				continue
			}
			if latest == "" || candidate.CreatedAt.After(newest.CreatedAt) || candidate.CreatedAt.Equal(newest.CreatedAt) && candidate.ID > newest.ID {
				newest = candidate
				latest = candidate.ID
			}
		}
		a.Status = cronDeploymentDisposition(string(d.Status), d.Scope, d.RolloutState, d.TrafficPercent, d.CanaryTotalSteps, d.TrafficPercentExplicit, competing, d.ID, latest)
		reason := cronDispositionReason(string(d.Status), d.Scope, d.RolloutState, d.TrafficPercent, d.CanaryTotalSteps, d.TrafficPercentExplicit, competing, d.ID, latest)
		if a.Status == "activated" {
			app := m.apps[appID]
			r := a.Intent
			r.Operation = "stage"
			r.ExpectedRevision = a.Revision
			r.ExpectedDeploymentID = m.latestPolicyDeploymentLocked(appID)
			if m.deploymentCronStates[appID].Revision != a.Revision || !api.DeploymentCronsEqual(cronIntents(m.httpCronsLocked(appID)), r.Before) {
				a.Status, reason = "conflict", "cron_changed"
			} else {
				limits, known := api.LimitsFor(m.accounts[app.AccountID].Plan)
				if !known {
					a.Status, reason = "conflict", "plan_unavailable"
				} else if err := m.activateDeploymentCronSetLocked(appID, DeploymentCronStagingParams{AccountID: app.AccountID, Request: r, Limits: limits}); err != nil {
					a.Status, reason = "conflict", cronActivationConflictReason(err)
				} else {
					m.deploymentCronStates[appID] = deploymentPolicyState{Revision: a.Revision + 1}
					delete(m.deploymentCronBefore, appID)
					changes = append(changes, CronActivationChange{AccountID: app.AccountID, AppID: appID})
				}
			}
		}
		if a.Status == "activated" {
			reason = ""
		}
		m.recordCronOutcomeLocked(appID, a, a.Status, reason)
		m.deploymentCronActivations[appID] = a
	}
	return changes, nil
}
func (s *PgStore) ReconcileDeploymentCrons(ctx context.Context) ([]CronActivationChange, error) {
	ids, err := sqlc.New().ListReadyCronActivations(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	changes := []CronActivationChange{}
	for _, id := range ids {
		change, err := s.reconcileDeploymentCron(ctx, id)
		if err != nil {
			return changes, err
		}
		if change != nil {
			changes = append(changes, *change)
		}
	}
	return changes, nil
}
func (s *PgStore) reconcileDeploymentCron(ctx context.Context, appID string) (*CronActivationChange, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	accountID, err := q.LockCronAccountForApp(ctx, tx, appID)
	if err != nil {
		return nil, mapErr(err)
	}
	meta, err := q.ReadDeploymentCronState(ctx, tx, appID)
	if err != nil {
		return nil, err
	}
	a, err := readCronActivation(ctx, q, tx, appID)
	if err != nil || a == nil || a.Status != "prepared" {
		return nil, err
	}
	if err = q.LockCronActivationDeployments(ctx, tx, appID); err != nil {
		return nil, err
	}
	d, err := q.ReadCronActivationDeployment(ctx, tx, sqlc.ReadCronActivationDeploymentParams{AppID: appID, SessionID: a.Intent.SessionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	status := cronDeploymentDisposition(d.Status, d.Scope, d.RolloutState, int(d.TrafficPercent), int(d.CanaryTotalSteps), d.TrafficPercentExplicit, d.CompetingTraffic, d.DeploymentID, d.LatestID)
	reason := cronDispositionReason(d.Status, d.Scope, d.RolloutState, int(d.TrafficPercent), int(d.CanaryTotalSteps), d.TrafficPercentExplicit, d.CompetingTraffic, d.DeploymentID, d.LatestID)
	var change *CronActivationChange
	if status == "activated" {
		snapshot, err := q.ReadDeploymentCronSnapshot(ctx, tx, sqlc.ReadDeploymentCronSnapshotParams{AppID: appID, AccountID: accountID})
		if err != nil {
			return nil, err
		}
		current, err := decodeCronSnapshot(snapshot.Crons)
		if err != nil {
			return nil, err
		}
		if meta.Status == "deleted" || meta.DeploymentCronRevision != a.Revision || !api.DeploymentCronsEqual(current, a.Intent.Before) {
			status, reason = "conflict", "cron_changed"
		} else {
			plan, err := q.CronActivationPlan(ctx, tx, accountID)
			if err != nil {
				return nil, err
			}
			limits, known := api.LimitsFor(api.Plan(plan))
			if !known {
				status, reason = "conflict", "plan_unavailable"
			} else {
				r := a.Intent
				r.Operation = "stage"
				// A savepoint rolls back every cron mutation if a late quota or ID guard fails.
				nested, err := tx.Begin(ctx)
				if err != nil {
					return nil, err
				}
				err = s.writeCronSet(ctx, nested, appID, DeploymentCronStagingParams{AccountID: accountID, Request: r, Limits: limits}, len(current), nil)
				if err != nil {
					if rollbackErr := nested.Rollback(ctx); rollbackErr != nil {
						return nil, rollbackErr
					}
					var quota *CronQuotaError
					if errors.Is(err, ErrDeploymentCronConflict) || errors.As(err, &quota) {
						status, reason = "conflict", cronActivationConflictReason(err)
					} else {
						return nil, err
					}
				} else {
					if err = nested.Commit(ctx); err != nil {
						return nil, err
					}
					if err = q.WriteDeploymentCronState(ctx, tx, sqlc.WriteDeploymentCronStateParams{AppID: appID, Revision: a.Revision + 1}); err != nil {
						return nil, err
					}
					change = &CronActivationChange{AccountID: accountID, AppID: appID}
				}
			}
		}
	}
	if status == "activated" {
		reason = ""
	}
	if err = q.RecordDeploymentCronOutcome(ctx, tx, sqlc.RecordDeploymentCronOutcomeParams{AppID: appID, Status: status, Reason: reason}); err != nil {
		return nil, err
	}
	if err = q.TouchCronActivation(ctx, tx, sqlc.TouchCronActivationParams{AppID: appID, Status: status}); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return change, nil
}

// PreparedDeploymentCrons prevents source-manifest ingestion from applying
// the same HTTP cron declarations early when a saved CLI deploy owns them.
type PreparedDeploymentCronStore interface {
	PreparedDeploymentCrons(context.Context, string) (bool, error)
}

// LegacyDeploymentCronStore preserves ownership of already staged CLI journals.
type LegacyDeploymentCronStore interface {
	LegacyDeploymentCrons(context.Context, string) (bool, error)
}

func (m *MemStore) LegacyDeploymentCrons(ctx context.Context, appID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := deploymentSubmissionAttempt(ctx)
	return id != "" && m.deploymentCronStates[appID].Owner == id, nil
}

func (s *PgStore) LegacyDeploymentCrons(ctx context.Context, appID string) (bool, error) {
	id := deploymentSubmissionAttempt(ctx)
	if id == "" {
		return false, nil
	}
	meta, err := sqlc.New().ReadDeploymentCronState(ctx, s.pool, appID)
	return err == nil && meta.DeploymentCronOwner == id, err
}

func (m *MemStore) PreparedDeploymentCrons(ctx context.Context, appID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.deploymentCronActivations[appID]
	return ok && a.Status == "prepared" && a.Intent.SessionID == deploymentSubmissionAttempt(ctx), nil
}
func (s *PgStore) PreparedDeploymentCrons(ctx context.Context, appID string) (bool, error) {
	id := deploymentSubmissionAttempt(ctx)
	if id == "" {
		return false, nil
	}
	return sqlc.New().HasPreparedCronActivation(ctx, s.pool, sqlc.HasPreparedCronActivationParams{AppID: appID, SessionID: id})
}

// Check final counts even for identical before/desired sets: the account plan
// can have changed since preparation. PostgreSQL performs this same check.
func (m *MemStore) activateDeploymentCronSetLocked(appID string, p DeploymentCronStagingParams) error {
	appCount, accountCount := 0, 0
	for _, c := range m.crons {
		if c.AppID == appID {
			appCount++
		}
		if a := m.apps[c.AppID]; a.AccountID == p.AccountID && a.Status != AppDeleted {
			accountCount++
		}
	}
	if err := checkCronReplacementQuota(appCount, accountCount, len(m.httpCronsLocked(appID)), len(p.Request.Desired), p); err != nil {
		return err
	}
	_, err := m.applyDeploymentCronStagingLocked(appID, p)
	return err
}
