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

func decodePeriodicMonitor(body []byte) (api.ProfilePeriodicMonitor, error) {
	var m api.ProfilePeriodicMonitor
	err := json.Unmarshal(body, &m)
	m.Config.Options = api.NormalizeProfileRegressionOptions(m.Config.Options)
	return m, err
}
func (s *PgStore) DiscoverProfilePeriodicMonitors(ctx context.Context, now time.Time) error {
	q := sqlc.New()
	if err := q.PruneProfilePeriodicMonitors(ctx, s.pool, sqlc.PruneProfilePeriodicMonitorsParams{Cutoff: profileCheckTime(now.Add(-api.ProfileAutoReceiptRetention)), MaxRows: api.ProfileAutoBatchSize}); err != nil {
		return err
	}
	rows, err := q.DiscoverProfilePeriodicCandidates(ctx, s.pool, api.ProfileAutoBatchSize)
	if err != nil {
		return err
	}
	for _, body := range rows {
		var c struct {
			profileDeploymentCandidate
			Route string `json:"route"`
		}
		if err := json.Unmarshal(body, &c); err != nil {
			return err
		}
		mon := newPeriodicMonitor(Deployment{ID: c.DeploymentID, AppID: c.AppID, Scope: c.Scope, RolloutCompletedAt: &c.CompletedAt}, c.Policy, c.Route, now)
		data, err := encodePeriodicMonitor(mon)
		if err != nil {
			return err
		}
		if err := q.EnqueueProfilePeriodicMonitor(ctx, s.pool, sqlc.EnqueueProfilePeriodicMonitorParams{ID: mon.ID, AppID: c.AppID, AccountID: c.AccountID, DeploymentID: c.DeploymentID, PolicyRevision: c.Policy.Revision, Route: c.Route, Data: data, Due: profileCheckTime(mon.NextAttemptAt), ObservedAt: profileCheckTime(now)}); err != nil {
			return err
		}
	}
	return nil
}
func (s *PgStore) ClaimProfilePeriodicMonitor(ctx context.Context, now time.Time) (ProfilePeriodicWork, error) {
	token := uuid.NewString()
	body, err := sqlc.New().ClaimProfilePeriodicMonitor(ctx, s.pool, sqlc.ClaimProfilePeriodicMonitorParams{Token: token, ObservedAt: profileCheckTime(now), LeaseUntil: profileCheckTime(now.Add(api.ProfileAutoLeaseDuration))})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProfilePeriodicWork{}, ErrNotFound
	}
	if err != nil {
		return ProfilePeriodicWork{}, err
	}
	var out ProfilePeriodicWork
	if err := json.Unmarshal(body, &out); err != nil {
		return out, err
	}
	out.Token = token
	return out, nil
}
func (s *PgStore) FinishProfilePeriodicMonitor(ctx context.Context, work ProfilePeriodicWork, result ProfilePeriodicResult, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockRoutePolicyApp(ctx, tx, sqlc.LockRoutePolicyAppParams{AppID: work.Monitor.AppID, AccountID: work.AccountID}); err != nil {
		return routePolicyReadError(err)
	}
	body, err := q.LockProfilePeriodicMonitor(ctx, tx, sqlc.LockProfilePeriodicMonitorParams{ID: work.Monitor.ID, AppID: work.Monitor.AppID, AccountID: work.AccountID, Token: work.Token, ObservedAt: profileCheckTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrProfileCheckLease
	}
	if err != nil {
		return err
	}
	mon, err := decodePeriodicMonitor(body)
	if err != nil {
		return err
	}
	if err := validatePeriodicResult(mon, result); err != nil {
		return err
	}
	slug, err := q.ReadProfileAlertOwner(ctx, tx, sqlc.ReadProfileAlertOwnerParams{AppID: mon.AppID, AccountID: work.AccountID})
	if err != nil {
		return err
	}
	o := periodicObservation(mon, result.Assessment, slug, work.AccountID)
	var plans []profileAlertPlan
	if mon.Baseline != nil && (!result.Retry || mon.Attempts >= api.ProfileAutoMaxAttempts) {
		plans, err = prepareProfileAlertsTx(ctx, tx, o)
		if err != nil {
			return err
		}
	}
	id := ""
	if profileAlertHasEvent(plans) {
		id, err = saveAutoProfileInvestigationTx(ctx, tx, work.AccountID, PeriodicProfileCheck(mon), result.Assessment)
		if err != nil && !errors.Is(err, ErrProfileInvestigationQuota) {
			return err
		}
		profileAlertInvestigation(plans, slug, id)
	}
	next := advancePeriodicMonitor(mon, result, plans, id, now)
	data, err := encodePeriodicMonitor(next)
	if err != nil {
		return err
	}
	if err := q.FinishProfilePeriodicMonitor(ctx, tx, sqlc.FinishProfilePeriodicMonitorParams{ID: mon.ID, Token: work.Token, Data: data, Due: profileCheckTime(next.NextAttemptAt), Attempts: int32(next.Attempts), ObservedAt: profileCheckTime(now)}); err != nil {
		return err
	}
	if err := publishProfileAlertsTx(ctx, tx, o, plans); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PgStore) ListProfilePeriodicMonitors(ctx context.Context, account, app string) ([]api.ProfilePeriodicMonitor, error) {
	owned, err := sqlc.New().ProfileInvestigationAppOwned(ctx, s.pool, sqlc.ProfileInvestigationAppOwnedParams{AppID: app, AccountID: account})
	if err != nil {
		return nil, err
	}
	if !owned {
		return nil, ErrNotFound
	}
	rows, err := sqlc.New().ListProfilePeriodicMonitors(ctx, s.pool, sqlc.ListProfilePeriodicMonitorsParams{AppID: app, AccountID: account, MaxRows: api.ProfilePeriodicMaxRows})
	if err != nil {
		return nil, err
	}
	out := []api.ProfilePeriodicMonitor{}
	for _, body := range rows {
		mon, err := decodePeriodicMonitor(body)
		if err != nil {
			return nil, err
		}
		out = append(out, trimPeriodicHistory(mon, time.Now()))
	}
	return out, nil
}
