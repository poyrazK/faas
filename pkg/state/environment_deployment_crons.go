package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ADR-20261104120000: `gregale deploy --environment X` stages the manifest's
// HTTP crons into X. A non-production environment has its own revision and
// pending activation, keyed by app and scope; production keeps the app-keyed
// state above. Only the deferred protocol (prepare, cancel, track_upload)
// takes an environment: the activation lands when X's deployment is live with
// all of X's traffic, and replaces only X's manifest crons.

// EnvironmentDeploymentCronStagingStore reads one environment's manifest
// HTTP crons for a deploy's snapshot.
type EnvironmentDeploymentCronStagingStore interface {
	EnvironmentDeploymentCronStagingSnapshot(ctx context.Context, accountID, appID, scope string) (api.DeploymentCronStagingSnapshot, error)
}

func environmentCronKey(appID, scope string) string { return appID + "\x00" + scope }

// environmentCronDisposition is cronDeploymentDisposition for an
// environment's lane: the activation's own environment, never production.
func environmentCronDisposition(lane, status, scope, rollout string, traffic, steps int, explicit, competing bool, id, latest string) (string, string) {
	if scope != lane {
		return "discarded", "deployment_superseded"
	}
	// Within its own lane the production rules apply unchanged.
	return cronDeploymentDisposition(status, "production", rollout, traffic, steps, explicit, competing, id, latest),
		cronDispositionReason(status, "production", rollout, traffic, steps, explicit, competing, id, latest)
}

// --- MemStore ---------------------------------------------------------------

func (m *MemStore) environmentHTTPCronsLocked(appID, scope string) []Cron {
	out := []Cron{}
	for _, c := range m.crons {
		if c.AppID == appID && c.DeploymentScope == scope && len(c.Command) == 0 {
			out = append(out, c)
		}
	}
	return out
}

func (m *MemStore) latestEnvironmentDeploymentLocked(appID, scope string) string {
	var latest Deployment
	for _, d := range m.deployments {
		if d.AppID == appID && d.Scope == scope && (latest.ID == "" || d.CreatedAt.After(latest.CreatedAt) || (d.CreatedAt.Equal(latest.CreatedAt) && d.ID > latest.ID)) {
			latest = d
		}
	}
	return latest.ID
}

func (m *MemStore) EnvironmentDeploymentCronStagingSnapshot(ctx context.Context, accountID, appID, scope string) (api.DeploymentCronStagingSnapshot, error) {
	if scope == "" {
		return m.DeploymentCronStagingSnapshot(ctx, accountID, appID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return api.DeploymentCronStagingSnapshot{}, ErrNotFound
	}
	return api.DeploymentCronStagingSnapshot{AppID: appID, Environment: scope, Revision: m.environmentCronRevisions[environmentCronKey(appID, scope)],
		LatestDeploymentID: m.latestEnvironmentDeploymentLocked(appID, scope), Crons: cronIntents(m.environmentHTTPCronsLocked(appID, scope))}, nil
}

func (m *MemStore) prepareEnvironmentCronActivationLocked(appID string, p DeploymentCronStagingParams) (DeploymentCronStagingResult, error) {
	r := p.Request
	if err := r.Validate(); err != nil {
		return DeploymentCronStagingResult{}, ErrInvalidArgument
	}
	app, ok := m.apps[appID]
	if !ok || app.AccountID != p.AccountID || app.Status == AppDeleted {
		return DeploymentCronStagingResult{}, ErrNotFound
	}
	if appID != r.ExpectedAppID {
		return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
	}
	scope, key := r.Environment, environmentCronKey(appID, r.Environment)
	var prior *cronActivation
	if a, ok := m.environmentCronActivations[key]; ok {
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
		m.environmentCronActivations[key] = *prior
		return activationReceipt(appID, r, *prior), nil
	}
	_, accepted := m.deploymentSubmissionReceipts[submissionKey(appID, r.SessionID)]
	if prior != nil && prior.Intent.SessionID == r.SessionID {
		if upload, ok := m.uploadSessions[prior.UploadID]; ok && upload.Status == "committed" && upload.DeploymentID.Valid {
			accepted = true
		}
	}
	if (prior == nil || prior.Intent.SessionID != r.SessionID) && m.latestEnvironmentDeploymentLocked(appID, scope) != r.ExpectedDeploymentID {
		return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
	}
	current := m.environmentHTTPCronsLocked(appID, scope)
	next, write, err := decideCronPreparation(cronIntents(current), m.environmentCronRevisions[key], prior, r, accepted)
	if err != nil || !write {
		return activationReceipt(appID, r, next), err
	}
	if r.Operation == "prepare" {
		if err := m.checkEnvironmentCronQuotaLocked(app, len(current), len(r.Desired), p); err != nil {
			return DeploymentCronStagingResult{}, err
		}
	}
	if m.environmentCronActivations == nil {
		m.environmentCronActivations = map[string]cronActivation{}
	}
	if m.environmentCronRevisions == nil {
		m.environmentCronRevisions = map[string]int64{}
	}
	m.environmentCronRevisions[key] = max(m.environmentCronRevisions[key], next.Revision)
	if prior != nil && prior.Intent.SessionID != r.SessionID {
		m.archiveCronOutcomeLocked(appID, *prior)
	}
	m.environmentCronActivations[key] = next
	return activationReceipt(appID, r, next), nil
}

func (m *MemStore) checkEnvironmentCronQuotaLocked(app App, oldCount, newCount int, p DeploymentCronStagingParams) error {
	appCount := 0
	for _, c := range m.crons {
		if c.AppID == app.ID {
			appCount++
		}
	}
	accountCount := m.quotaSubjectCronsLocked(m.appBillingSubjectLocked(app.AccountID, app.ID))
	return checkCronReplacementQuota(appCount, accountCount, oldCount, newCount, p)
}

func (m *MemStore) reconcileEnvironmentCronsLocked() []CronActivationChange {
	changes := []CronActivationChange{}
	for key, a := range m.environmentCronActivations {
		if a.Status != "prepared" {
			continue
		}
		appID, scope := a.Intent.ExpectedAppID, a.Intent.Environment
		d, ok := m.cronActivationDeploymentLocked(appID, a)
		if !ok {
			continue
		}
		latest, competing := "", false
		var newest Deployment
		for _, candidate := range m.deployments {
			if candidate.AppID != appID || candidate.Scope != d.Scope {
				continue
			}
			if candidate.ID != d.ID && candidate.Status == DeployLive && candidate.TrafficPercent > 0 {
				competing = true
			}
			if candidate.TrafficPercentExplicit && candidate.TrafficPercent == 0 && candidate.CanaryTotalSteps == 0 {
				continue
			}
			if latest == "" || candidate.CreatedAt.After(newest.CreatedAt) || candidate.CreatedAt.Equal(newest.CreatedAt) && candidate.ID > newest.ID {
				newest, latest = candidate, candidate.ID
			}
		}
		status, reason := environmentCronDisposition(scope, string(d.Status), d.Scope, d.RolloutState, d.TrafficPercent, d.CanaryTotalSteps, d.TrafficPercentExplicit, competing, d.ID, latest)
		if status == "activated" {
			app := m.apps[appID]
			// The billing subject's plan (ADR-202610171904).
			owner, _ := m.appResourceAccountLocked(app)
			limits, known := api.LimitsFor(owner.Plan)
			switch {
			case m.environmentCronRevisions[key] != a.Revision || !api.DeploymentCronsEqual(cronIntents(m.environmentHTTPCronsLocked(appID, scope)), a.Intent.Before):
				status, reason = "conflict", "cron_changed"
			case !known:
				status, reason = "conflict", "plan_unavailable"
			default:
				if err := m.activateEnvironmentCronSetLocked(app, scope, DeploymentCronStagingParams{AccountID: app.AccountID, Request: a.Intent, Limits: limits}); err != nil {
					status, reason = "conflict", cronActivationConflictReason(err)
				} else {
					m.environmentCronRevisions[key] = a.Revision + 1
					changes = append(changes, CronActivationChange{AccountID: app.AccountID, AppID: appID})
				}
			}
		}
		if status == "activated" {
			reason = ""
		}
		a.Status = status
		m.recordCronOutcomeLocked(appID, a, status, reason)
		m.environmentCronActivations[key] = a
	}
	return changes
}

// activateEnvironmentCronSetLocked replaces the environment's manifest HTTP
// crons with the intent's desired set, within the plan's caps.
func (m *MemStore) activateEnvironmentCronSetLocked(app App, scope string, p DeploymentCronStagingParams) error {
	current := m.environmentHTTPCronsLocked(app.ID, scope)
	desired := p.Request.Desired
	if err := m.checkEnvironmentCronQuotaLocked(app, len(current), len(desired), p); err != nil {
		return err
	}
	for _, c := range desired {
		if existing, ok := m.crons[c.ID]; ok && (existing.AppID != app.ID || existing.DeploymentScope != scope || len(existing.Command) > 0) {
			return ErrDeploymentCronConflict
		}
	}
	keep := map[string]bool{}
	for _, row := range desired {
		keep[row.ID] = true
		c, exists := m.crons[row.ID]
		if !exists {
			c = Cron{ID: row.ID, AppID: app.ID, DeploymentScope: scope, CreatedAt: time.Now(), ScheduleRevision: 1, CommandTimeoutSeconds: AppTaskDefaultTimeoutSeconds,
				CommandMaxOutputBytes: AppTaskDefaultMaxOutputBytes, RetryBackoffSeconds: DefaultCronRetryBackoffSeconds}
		}
		if exists && (c.Schedule != row.Schedule || c.Timezone != row.Timezone) {
			c.ScheduleRevision++
		}
		before := c
		c.Schedule, c.Path, c.Enabled, c.Timezone, c.SkipIfRunning = row.Schedule, row.Path, row.Enabled, row.Timezone, row.SkipIfRunning
		if exists {
			c = anchorCronSchedule(before, c, time.Now())
		}
		m.crons[c.ID] = c
	}
	for _, c := range current {
		if !keep[c.ID] {
			delete(m.crons, c.ID)
			delete(m.exclusiveTriggerBindings, "cron\x00"+c.ID)
		}
	}
	return nil
}

// --- PgStore ----------------------------------------------------------------

func (s *PgStore) EnvironmentDeploymentCronStagingSnapshot(ctx context.Context, accountID, appID, scope string) (api.DeploymentCronStagingSnapshot, error) {
	if scope == "" {
		return s.DeploymentCronStagingSnapshot(ctx, accountID, appID)
	}
	row, err := sqlc.New().ReadEnvironmentDeploymentCronSnapshot(ctx, s.pool, sqlc.ReadEnvironmentDeploymentCronSnapshotParams{AppID: appID, AccountID: accountID, Scope: scope})
	if err != nil {
		return api.DeploymentCronStagingSnapshot{}, mapErr(err)
	}
	crons, err := decodeCronSnapshot(row.Crons)
	return api.DeploymentCronStagingSnapshot{AppID: row.AppID, Environment: scope, Revision: row.Revision, LatestDeploymentID: row.LatestDeploymentID, Crons: crons}, err
}

func readEnvironmentCronActivation(ctx context.Context, q *sqlc.Queries, tx sqlc.DBTX, appID, scope string) (*cronActivation, error) {
	row, err := q.ReadEnvironmentCronActivation(ctx, tx, sqlc.ReadEnvironmentCronActivationParams{AppID: appID, Scope: scope})
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

func (s *PgStore) prepareEnvironmentCronActivation(ctx context.Context, appID string, p DeploymentCronStagingParams) (DeploymentCronStagingResult, error) {
	r := p.Request
	if err := r.Validate(); err != nil {
		return DeploymentCronStagingResult{}, ErrInvalidArgument
	}
	scope := r.Environment
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	accountID, subject, err := lockCronQuotaSubjectTx(ctx, tx, appID)
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	snapshot, err := q.ReadEnvironmentDeploymentCronSnapshot(ctx, tx, sqlc.ReadEnvironmentDeploymentCronSnapshotParams{AppID: appID, AccountID: p.AccountID, Scope: scope})
	if err != nil || accountID != p.AccountID {
		if err == nil {
			err = ErrNotFound
		}
		return DeploymentCronStagingResult{}, mapErr(err)
	}
	if appID != r.ExpectedAppID {
		return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
	}
	revision, err := q.LockEnvironmentDeploymentCronState(ctx, tx, sqlc.LockEnvironmentDeploymentCronStateParams{AppID: appID, Scope: scope})
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	prior, err := readEnvironmentCronActivation(ctx, q, tx, appID, scope)
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	if r.Operation == "track_upload" {
		if prior == nil || prior.Intent.SessionID != r.SessionID || prior.Intent.Fingerprint() != r.Fingerprint() {
			return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
		}
		wrote, err := q.TrackEnvironmentCronActivationUpload(ctx, tx, sqlc.TrackEnvironmentCronActivationUploadParams{AppID: appID, Scope: scope, SessionID: r.SessionID, Fingerprint: r.Fingerprint(), UploadID: r.UploadID})
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
	if (prior == nil || prior.Intent.SessionID != r.SessionID) && snapshot.LatestDeploymentID != r.ExpectedDeploymentID {
		return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
	}
	_, receiptErr := q.ReadDeploymentSubmission(ctx, tx, sqlc.ReadDeploymentSubmissionParams{AppID: appID, AttemptID: r.SessionID})
	if receiptErr != nil && !errors.Is(receiptErr, pgx.ErrNoRows) {
		return DeploymentCronStagingResult{}, receiptErr
	}
	uploadAccepted, err := q.EnvironmentCronActivationUploadAccepted(ctx, tx, sqlc.EnvironmentCronActivationUploadAcceptedParams{AppID: appID, Scope: scope, SessionID: r.SessionID})
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	current, err := decodeCronSnapshot(snapshot.Crons)
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	next, write, err := decideCronPreparation(current, revision, prior, r, receiptErr == nil || uploadAccepted)
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	if write {
		if r.Operation == "prepare" {
			counts, err := q.CountDeploymentCrons(ctx, tx, sqlc.CountDeploymentCronsParams{AppID: appID, SubjectID: subject})
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
			if err = q.ArchiveEnvironmentDeploymentCronOutcome(ctx, tx, sqlc.ArchiveEnvironmentDeploymentCronOutcomeParams{AppID: appID, Scope: scope}); err != nil {
				return DeploymentCronStagingResult{}, err
			}
		}
		if err = q.WriteEnvironmentCronActivation(ctx, tx, sqlc.WriteEnvironmentCronActivationParams{AppID: appID, Scope: scope, SessionID: r.SessionID,
			Intent: data, Fingerprint: r.Fingerprint(), Revision: next.Revision, Status: next.Status}); err != nil {
			return DeploymentCronStagingResult{}, err
		}
		if next.Revision > revision {
			if err = q.WriteEnvironmentDeploymentCronState(ctx, tx, sqlc.WriteEnvironmentDeploymentCronStateParams{AppID: appID, Scope: scope, Revision: next.Revision}); err != nil {
				return DeploymentCronStagingResult{}, err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return DeploymentCronStagingResult{}, err
	}
	return activationReceipt(appID, r, next), nil
}

func (s *PgStore) reconcileEnvironmentDeploymentCrons(ctx context.Context) ([]CronActivationChange, error) {
	rows, err := sqlc.New().ListReadyEnvironmentCronActivations(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	changes := []CronActivationChange{}
	for _, row := range rows {
		var change *CronActivationChange
		err := s.retryAfterDetachingCronHistory(ctx, func() error {
			var err error
			change, err = s.reconcileEnvironmentDeploymentCron(ctx, row.AppID, row.DeploymentScope)
			return err
		})
		if err != nil {
			return changes, err
		}
		if change != nil {
			changes = append(changes, *change)
		}
	}
	return changes, nil
}

func (s *PgStore) reconcileEnvironmentDeploymentCron(ctx context.Context, appID, scope string) (*CronActivationChange, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	accountID, subject, err := lockCronQuotaSubjectTx(ctx, tx, appID)
	if err != nil {
		return nil, err
	}
	revision, err := q.LockEnvironmentDeploymentCronState(ctx, tx, sqlc.LockEnvironmentDeploymentCronStateParams{AppID: appID, Scope: scope})
	if err != nil {
		return nil, err
	}
	a, err := readEnvironmentCronActivation(ctx, q, tx, appID, scope)
	if err != nil || a == nil || a.Status != "prepared" {
		return nil, err
	}
	if err = q.LockCronActivationDeployments(ctx, tx, appID); err != nil {
		return nil, err
	}
	d, err := q.ReadEnvironmentCronActivationDeployment(ctx, tx, sqlc.ReadEnvironmentCronActivationDeploymentParams{AppID: appID, Scope: scope, SessionID: a.Intent.SessionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	status, reason := environmentCronDisposition(scope, d.Status, d.Scope, d.RolloutState, int(d.TrafficPercent), int(d.CanaryTotalSteps), d.TrafficPercentExplicit, d.CompetingTraffic, d.DeploymentID, d.LatestID)
	var change *CronActivationChange
	if status == "activated" {
		snapshot, err := q.ReadEnvironmentDeploymentCronSnapshot(ctx, tx, sqlc.ReadEnvironmentDeploymentCronSnapshotParams{AppID: appID, AccountID: accountID, Scope: scope})
		if err != nil {
			return nil, err
		}
		current, err := decodeCronSnapshot(snapshot.Crons)
		if err != nil {
			return nil, err
		}
		if revision != a.Revision || !api.DeploymentCronsEqual(current, a.Intent.Before) {
			status, reason = "conflict", "cron_changed"
		} else {
			plan, err := q.CronActivationOwnerPlan(ctx, tx, appID)
			if err != nil {
				return nil, err
			}
			limits, known := api.LimitsFor(api.Plan(plan))
			if !known {
				status, reason = "conflict", "plan_unavailable"
			} else {
				nested, err := tx.Begin(ctx)
				if err != nil {
					return nil, err
				}
				err = s.writeEnvironmentCronSet(ctx, nested, appID, scope, subject, DeploymentCronStagingParams{AccountID: accountID, Request: a.Intent, Limits: limits}, len(current))
				if err != nil {
					if rollbackErr := nested.Rollback(ctx); rollbackErr != nil {
						return nil, rollbackErr
					}
					var quota *CronQuotaError
					if !errors.Is(err, ErrDeploymentCronConflict) && !errors.As(err, &quota) {
						return nil, err
					}
					status, reason = "conflict", cronActivationConflictReason(err)
				} else {
					if err = nested.Commit(ctx); err != nil {
						return nil, err
					}
					if err = q.WriteEnvironmentDeploymentCronState(ctx, tx, sqlc.WriteEnvironmentDeploymentCronStateParams{AppID: appID, Scope: scope, Revision: a.Revision + 1}); err != nil {
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
	if err = q.RecordEnvironmentDeploymentCronOutcome(ctx, tx, sqlc.RecordEnvironmentDeploymentCronOutcomeParams{AppID: appID, Scope: scope, Status: status, Reason: reason}); err != nil {
		return nil, err
	}
	if err = q.TouchEnvironmentCronActivation(ctx, tx, sqlc.TouchEnvironmentCronActivationParams{AppID: appID, Scope: scope, Status: status}); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return change, nil
}

// writeEnvironmentCronSet replaces the environment's manifest HTTP crons
// within the plan's caps; subject is the app's billing subject, whose cron
// quota lock the caller holds.
func (s *PgStore) writeEnvironmentCronSet(ctx context.Context, tx pgx.Tx, appID, scope, subject string, p DeploymentCronStagingParams, oldCount int) error {
	q := sqlc.New()
	target := p.Request.Desired
	counts, err := q.CountDeploymentCrons(ctx, tx, sqlc.CountDeploymentCronsParams{AppID: appID, SubjectID: subject})
	if err != nil {
		return err
	}
	if err = checkCronReplacementQuota(int(counts.AppCount), int(counts.AccountCount), oldCount, len(target), p); err != nil {
		return err
	}
	data, err := json.Marshal(target)
	if err != nil {
		return err
	}
	removed, err := removedHTTPCronIDsTx(ctx, tx, appID, scope, data)
	if err != nil {
		return err
	}
	if err = prepareCronRemovalTx(ctx, tx, removed); err != nil {
		return err
	}
	if err = q.DeleteEnvironmentHTTPcrons(ctx, tx, sqlc.DeleteEnvironmentHTTPcronsParams{AppID: appID, Scope: scope, Crons: data}); err != nil {
		return err
	}
	wrote, err := q.StageEnvironmentHTTPcrons(ctx, tx, sqlc.StageEnvironmentHTTPcronsParams{AppID: appID, Scope: scope, Crons: data})
	if err != nil {
		return mapErr(err)
	}
	if wrote != int64(len(target)) {
		return ErrDeploymentCronConflict
	}
	return nil
}
