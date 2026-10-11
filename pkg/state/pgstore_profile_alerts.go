package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Call only while holding the owned app lock and the profiling result lease.
func prepareProfileAlertsTx(ctx context.Context, tx pgx.Tx, o profileAlertObservation) ([]profileAlertPlan, error) {
	if o.Assessment.Baseline.DeploymentID == "" || o.Assessment.Candidate.DeploymentID == "" {
		return nil, nil
	}
	p, err := readProfileDeploymentPolicy(ctx, tx, o.AccountID, o.AppID)
	if err != nil {
		return nil, err
	}
	if p.Revision != o.Revision || !p.Config.Enabled || (!p.Config.NotifyRouteRegressions && o.Source != "periodic") {
		return nil, nil
	}
	o.Config = p.Config
	slug, err := sqlc.New().ReadProfileAlertOwner(ctx, tx, sqlc.ReadProfileAlertOwnerParams{AppID: o.AppID, AccountID: o.AccountID})
	if err != nil {
		return nil, err
	}
	o.Slug = slug
	allowed := map[string]bool{}
	for _, route := range p.Config.Options.Routes {
		allowed[route] = true
	}
	var plans []profileAlertPlan
	for _, r := range o.Assessment.RouteChecks {
		if !allowed[r.Route] {
			continue
		}
		key := profileAlertKey(o, r.Route)
		body, err := sqlc.New().ReadProfileAlertState(ctx, tx, sqlc.ReadProfileAlertStateParams{ContextKey: key, AppID: o.AppID, AccountID: o.AccountID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		var previous profileAlertState
		if len(body) > 0 {
			if err := json.Unmarshal(body, &previous); err != nil {
				return nil, err
			}
		}
		plan, err := profileAlertTransition(previous, o, r)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}
func publishProfileAlertsTx(ctx context.Context, tx pgx.Tx, o profileAlertObservation, plans []profileAlertPlan) error {
	q := sqlc.New()
	for _, plan := range plans {
		body, err := json.Marshal(plan.State)
		if err != nil {
			return err
		}
		if err := q.WriteProfileAlertState(ctx, tx, sqlc.WriteProfileAlertStateParams{ContextKey: plan.Key, AppID: o.AppID, AccountID: o.AccountID, DeploymentID: o.Assessment.Candidate.DeploymentID, State: body, UpdatedAt: NewPgtypeTime(o.Assessment.CheckedAt)}); err != nil {
			return err
		}
		if plan.Event != "" && (o.Source != "periodic" || o.Config.NotifyRouteRegressions) {
			if err := q.EnqueueProfileAlertNotification(ctx, tx, sqlc.EnqueueProfileAlertNotificationParams{AppID: o.AppID, AccountID: o.AccountID, Event: string(plan.Event), SourceID: plan.SourceID, Payload: plan.Payload}); err != nil {
				return err
			}
		}
	}
	return nil
}
