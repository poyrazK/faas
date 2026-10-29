package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type DeploymentCronStagingStore interface {
	DeploymentCronStagingSnapshot(context.Context, string, string) (api.DeploymentCronStagingSnapshot, error)
	ApplyDeploymentCronStaging(context.Context, string, DeploymentCronStagingParams) (DeploymentCronStagingResult, error)
}
type DeploymentCronStagingParams struct {
	AccountID string
	Request   api.DeploymentCronStagingRequest
	Limits    api.Limits
}
type DeploymentCronStagingResult struct {
	Receipt api.DeploymentStagingReceipt
	Changed bool
}

var ErrDeploymentCronConflict = errors.New("deployment cron staging precondition changed")

func cronIntent(c Cron) api.DeploymentCronConfig {
	return api.DeploymentCronConfig{ID: c.ID, Schedule: c.Schedule, Path: c.Path, Enabled: c.Enabled, Timezone: c.Timezone, SkipIfRunning: c.SkipIfRunning}
}
func cronIntents(rows []Cron) []api.DeploymentCronConfig {
	out := []api.DeploymentCronConfig{}
	for _, c := range rows {
		if len(c.Command) == 0 {
			out = append(out, cronIntent(c))
		}
	}
	return out
}
func decideCronStaging(current []api.DeploymentCronConfig, meta deploymentPolicyState, r api.DeploymentCronStagingRequest) (deploymentPolicyState, bool, bool, error) {
	if err := r.Validate(); err != nil {
		return meta, false, false, ErrInvalidArgument
	}
	digest := r.Fingerprint()
	if r.Operation == "stage" {
		if meta.Owner == r.SessionID && meta.Fingerprint == digest && meta.Revision == r.ExpectedRevision+1 && api.DeploymentCronsEqual(current, r.Desired) {
			return meta, false, false, nil
		}
		if meta.Owner == r.SessionID || meta.Revision != r.ExpectedRevision || !api.DeploymentCronsEqual(current, r.Before) {
			return meta, false, false, ErrDeploymentCronConflict
		}
		meta = deploymentPolicyState{Revision: meta.Revision + 1, Owner: r.SessionID, Fingerprint: digest}
		return meta, true, !api.DeploymentCronsEqual(current, r.Desired), nil
	}
	if meta.Owner == r.SessionID && meta.Fingerprint == digest && meta.Revision == r.ExpectedRevision+1 && api.DeploymentCronsEqual(current, r.Desired) {
		return deploymentPolicyState{Revision: meta.Revision + 1}, true, !api.DeploymentCronsEqual(current, r.Before), nil
	}
	if api.DeploymentCronsEqual(current, r.Before) {
		if meta.Revision == r.ExpectedRevision {
			meta.Revision++
		}
		return meta, false, false, nil
	}
	return meta, false, false, ErrDeploymentCronConflict
}
func cronStagingReceipt(appID string, r api.DeploymentCronStagingRequest, revision int64) api.DeploymentStagingReceipt {
	status := "staged"
	if r.Operation == "restore" {
		status = "cleaned"
	}
	return api.DeploymentStagingReceipt{SessionID: r.SessionID, AppID: appID, Status: status, Revision: revision}
}
func checkCronReplacementQuota(appCount, accountCount, oldCount, newCount int, p DeploymentCronStagingParams) error {
	for _, q := range []struct {
		scope        CronQuotaScope
		limit, count int
	}{{CronQuotaScopeApp, p.Limits.CronLimitPerApp, appCount - oldCount + newCount}, {CronQuotaScopeAccount, p.Limits.CronLimitPerAccount, accountCount - oldCount + newCount}} {
		if q.count > q.limit {
			return &CronQuotaError{Scope: q.scope, Limit: q.limit, Observed: q.count}
		}
	}
	return nil
}

func (m *MemStore) httpCronsLocked(appID string) []Cron {
	out := []Cron{}
	for _, c := range m.crons {
		if c.AppID == appID && len(c.Command) == 0 {
			out = append(out, c)
		}
	}
	return out
}
func (m *MemStore) invalidateDeploymentCronsLocked(appID string) {
	if m.deploymentCronStates == nil {
		m.deploymentCronStates = map[string]deploymentPolicyState{}
	}
	meta := m.deploymentCronStates[appID]
	meta.Revision++
	meta.Owner, meta.Fingerprint = "", ""
	m.deploymentCronStates[appID] = meta
	delete(m.deploymentCronBefore, appID)
}
func (m *MemStore) DeploymentCronStagingSnapshot(_ context.Context, accountID, appID string) (api.DeploymentCronStagingSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return api.DeploymentCronStagingSnapshot{}, ErrNotFound
	}
	return api.DeploymentCronStagingSnapshot{AppID: appID, Revision: m.deploymentCronStates[appID].Revision, LatestDeploymentID: m.latestPolicyDeploymentLocked(appID), Crons: cronIntents(m.httpCronsLocked(appID))}, nil
}
func (m *MemStore) ApplyDeploymentCronStaging(_ context.Context, appID string, p DeploymentCronStagingParams) (DeploymentCronStagingResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.Request.Operation == "prepare" || p.Request.Operation == "cancel" || p.Request.Operation == "track_upload" {
		return m.prepareCronActivationLocked(appID, p)
	}
	return m.applyDeploymentCronStagingLocked(appID, p)
}
func (m *MemStore) applyDeploymentCronStagingLocked(appID string, p DeploymentCronStagingParams) (DeploymentCronStagingResult, error) {
	app, ok := m.apps[appID]
	r := p.Request
	if !ok || app.Status == AppDeleted || app.AccountID != p.AccountID {
		return DeploymentCronStagingResult{}, ErrNotFound
	}
	if app.ID != r.ExpectedAppID || m.latestPolicyDeploymentLocked(appID) != r.ExpectedDeploymentID {
		return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
	}
	current := m.httpCronsLocked(appID)
	meta := m.deploymentCronStates[appID]
	next, owned, changed, err := decideCronStaging(cronIntents(current), meta, r)
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	if owned && changed {
		target := r.Desired
		if r.Operation == "restore" {
			target = r.Before
		}
		appCount, accountCount := 0, 0
		for _, c := range m.crons {
			if c.AppID == appID {
				appCount++
			}
			a := m.apps[c.AppID]
			if a.AccountID == p.AccountID && a.Status != AppDeleted {
				accountCount++
			}
		}
		if err := checkCronReplacementQuota(appCount, accountCount, len(current), len(target), p); err != nil {
			return DeploymentCronStagingResult{}, err
		}
		for _, c := range target {
			if existing, ok := m.crons[c.ID]; ok && (existing.AppID != appID || len(existing.Command) > 0) {
				return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
			}
		}
		originals := map[string]Cron{}
		for _, c := range m.deploymentCronBefore[appID] {
			originals[c.ID] = c
		}
		keep := map[string]bool{}
		for _, desired := range target {
			keep[desired.ID] = true
			c, exists := m.crons[desired.ID]
			if !exists {
				c = Cron{ID: desired.ID, AppID: appID, CreatedAt: time.Now(), ScheduleRevision: 1, CommandTimeoutSeconds: AppTaskDefaultTimeoutSeconds, CommandMaxOutputBytes: AppTaskDefaultMaxOutputBytes, RetryBackoffSeconds: DefaultCronRetryBackoffSeconds}
			}
			if r.Operation == "restore" {
				old := originals[desired.ID]
				if !exists {
					c = old
					c.ScheduleRevision++
				} else {
					c.CreatedAt = old.CreatedAt
					if old.LastFiredAt.After(c.LastFiredAt) {
						c.LastFiredAt = old.LastFiredAt
					}
				}
			}
			if exists && (c.Schedule != desired.Schedule || c.Timezone != desired.Timezone) {
				c.ScheduleRevision++
			}
			before := c
			c.Schedule, c.Path, c.Enabled, c.Timezone, c.SkipIfRunning = desired.Schedule, desired.Path, desired.Enabled, desired.Timezone, desired.SkipIfRunning
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
	}
	if m.deploymentCronStates == nil {
		m.deploymentCronStates = map[string]deploymentPolicyState{}
	}
	if m.deploymentCronBefore == nil {
		m.deploymentCronBefore = map[string][]Cron{}
	}
	if owned {
		if r.Operation == "stage" {
			m.deploymentCronBefore[appID] = current
		} else {
			delete(m.deploymentCronBefore, appID)
		}
	}
	m.deploymentCronStates[appID] = next
	return DeploymentCronStagingResult{Receipt: cronStagingReceipt(appID, r, next.Revision), Changed: changed}, nil
}

// Only intent enters the client snapshot. Full original rows stay server-side,
// retaining row identities, creation time and firing progress for cleanup.
func decodeCronSnapshot(raw []byte) ([]api.DeploymentCronConfig, error) {
	var out []api.DeploymentCronConfig
	err := json.Unmarshal(raw, &out)
	return out, err
}
func (s *PgStore) DeploymentCronStagingSnapshot(ctx context.Context, accountID, appID string) (api.DeploymentCronStagingSnapshot, error) {
	row, err := sqlc.New().ReadDeploymentCronSnapshot(ctx, s.pool, sqlc.ReadDeploymentCronSnapshotParams{AppID: appID, AccountID: accountID})
	if err != nil {
		return api.DeploymentCronStagingSnapshot{}, mapErr(err)
	}
	crons, err := decodeCronSnapshot(row.Crons)
	return api.DeploymentCronStagingSnapshot{AppID: row.AppID, Revision: row.DeploymentCronRevision, LatestDeploymentID: row.LatestDeploymentID, Crons: crons}, err
}
func (s *PgStore) ApplyDeploymentCronStaging(ctx context.Context, appID string, p DeploymentCronStagingParams) (DeploymentCronStagingResult, error) {
	if p.Request.Operation == "prepare" || p.Request.Operation == "cancel" || p.Request.Operation == "track_upload" {
		return s.prepareCronActivation(ctx, appID, p)
	}
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
	row, err := q.ReadDeploymentCronState(ctx, tx, appID)
	if err != nil {
		return DeploymentCronStagingResult{}, mapErr(err)
	}
	r := p.Request
	if row.Status == "deleted" || accountID != p.AccountID || row.AccountID != p.AccountID {
		return DeploymentCronStagingResult{}, ErrNotFound
	}
	snapshot, err := q.ReadDeploymentCronSnapshot(ctx, tx, sqlc.ReadDeploymentCronSnapshotParams{AppID: appID, AccountID: p.AccountID})
	if err != nil {
		return DeploymentCronStagingResult{}, mapErr(err)
	}
	latest, err := q.ReadDeploySettingsLatest(ctx, tx, appID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return DeploymentCronStagingResult{}, err
	}
	if row.AppID != r.ExpectedAppID || snapshot.LatestDeploymentID != r.ExpectedDeploymentID || latest.ID != r.ExpectedDeploymentID {
		return DeploymentCronStagingResult{}, ErrDeploymentCronConflict
	}
	current, err := decodeCronSnapshot(snapshot.Crons)
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	meta := deploymentPolicyState{Revision: row.DeploymentCronRevision, Owner: row.DeploymentCronOwner, Fingerprint: row.DeploymentCronFingerprint}
	next, owned, changed, err := decideCronStaging(current, meta, r)
	if err != nil {
		return DeploymentCronStagingResult{}, err
	}
	before := row.DeploymentCronBefore
	if owned {
		if r.Operation == "stage" {
			before = snapshot.Crons
		}
		if changed {
			if err = s.writeCronSet(ctx, tx, appID, p, len(current), before); err != nil {
				return DeploymentCronStagingResult{}, err
			}
		}
		if r.Operation == "restore" {
			before = nil
		}
	}
	if next != meta {
		err = q.WriteDeploymentCronState(ctx, tx, sqlc.WriteDeploymentCronStateParams{AppID: appID, Revision: next.Revision, Owner: next.Owner, Fingerprint: next.Fingerprint, BeforeCrons: before})
		if err != nil {
			return DeploymentCronStagingResult{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return DeploymentCronStagingResult{}, err
	}
	return DeploymentCronStagingResult{Receipt: cronStagingReceipt(appID, r, next.Revision), Changed: changed}, nil
}
func (s *PgStore) writeCronSet(ctx context.Context, tx pgx.Tx, appID string, p DeploymentCronStagingParams, oldCount int, before []byte) error {
	q := sqlc.New()
	target := p.Request.Desired
	if p.Request.Operation == "restore" {
		target = p.Request.Before
	}
	counts, err := q.CountDeploymentCrons(ctx, tx, sqlc.CountDeploymentCronsParams{AppID: appID, AccountID: p.AccountID})
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
	if err = q.DeleteDeploymentHTTPcrons(ctx, tx, sqlc.DeleteDeploymentHTTPcronsParams{AppID: appID, Crons: data}); err != nil {
		return err
	}
	var wrote int64
	if p.Request.Operation == "stage" {
		wrote, err = q.StageDeploymentHTTPcrons(ctx, tx, sqlc.StageDeploymentHTTPcronsParams{AppID: appID, Crons: data})
	} else {
		wrote, err = q.RestoreDeploymentHTTPcrons(ctx, tx, sqlc.RestoreDeploymentHTTPcronsParams{AppID: appID, Crons: before})
	}
	if err != nil {
		return mapErr(err)
	}
	if wrote != int64(len(target)) {
		return ErrDeploymentCronConflict
	}
	return nil
}

func newCronID() string { return uuid.NewString() }
