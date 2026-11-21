package state

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// pgRebalanceAbortedTraffic moves an aborted canary's traffic to its live
// siblings in the same scope, the way an explicit abort does
// (recoverRolloutTx). Safe Deploy stamps a rollout aborted without the
// recovery path when its app was deleted mid-canary; the canary used to keep
// its weight, so `gregale app restore` brought the app back still splitting
// traffic to a dead canary that never finished, never parked and answered
// 502 (prod 2026-11-15 → 2026-11-19: 90/10, 99/1 and 50/50 apps).
//
// It runs in a savepoint: when a route gate refuses a recipient, only the
// rebalance is undone and the stamp is kept, as before.
func pgRebalanceAbortedTraffic(ctx context.Context, tx pgx.Tx, dep Deployment) (bool, error) {
	if dep.Status != DeployLive || dep.TrafficPercent <= 0 {
		return false, nil
	}
	sp, err := tx.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("state: aborted rebalance savepoint: %w", err)
	}
	rebalanced, err := pgRebalanceAbortedTrafficTx(ctx, sp, dep)
	if err != nil || !rebalanced {
		if rbErr := sp.Rollback(ctx); rbErr != nil {
			return false, fmt.Errorf("state: aborted rebalance rollback: %w", rbErr)
		}
		return false, nil //nolint:nilerr // a refused rebalance keeps the plain stamp
	}
	if err := sp.Commit(ctx); err != nil {
		return false, fmt.Errorf("state: aborted rebalance release savepoint: %w", err)
	}
	return true, nil
}

func pgRebalanceAbortedTrafficTx(ctx context.Context, tx pgx.Tx, dep Deployment) (bool, error) {
	rows, err := tx.Query(ctx,
		`select id, traffic_percent
		   from deployments
		  where app_id = $1 and scope = $3 and status = 'live' and id != $2
		  order by id`,
		dep.AppID, dep.ID, normalizedDeploymentScope(dep.Scope))
	if err != nil {
		return false, err
	}
	var siblings []struct {
		ID    string
		Prior int
	}
	for rows.Next() {
		var sibling struct {
			ID    string
			Prior int
		}
		if err := rows.Scan(&sibling.ID, &sibling.Prior); err != nil {
			rows.Close()
			return false, err
		}
		siblings = append(siblings, sibling)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(siblings) == 0 {
		// The aborted row is the only live one: nothing can take its traffic.
		return false, nil
	}
	if err := pgAuthorizeRouteRemoval(ctx, tx); err != nil {
		return false, err
	}
	ids, err := sqlc.New().ReadLifecycleTrafficCandidates(ctx, tx, sqlc.ReadLifecycleTrafficCandidatesParams{AppID: dep.AppID, DeploymentID: dep.ID})
	if err != nil {
		return false, err
	}
	for _, recipientID := range ids {
		if recipientID != dep.ID {
			if err := pgAuthorizeProductionLifecycle(ctx, tx, recipientID, true); err != nil {
				return false, err
			}
		}
	}
	if _, err := tx.Exec(ctx, `update deployments set traffic_percent = 0 where id = $1`, dep.ID); err != nil {
		return false, err
	}
	weights := RedistributeTraffic(siblings, 100)
	for i, sibling := range siblings {
		if _, err := tx.Exec(ctx, `update deployments set traffic_percent = $2 where id = $1`, sibling.ID, weights[i]); err != nil {
			return false, err
		}
	}
	return true, nil
}

// rebalanceAbortedTrafficLocked is the MemStore mirror of
// pgRebalanceAbortedTraffic. Caller holds m.mu.
func (m *MemStore) rebalanceAbortedTrafficLocked(d Deployment) {
	if d.Status != DeployLive || d.TrafficPercent <= 0 {
		return
	}
	var siblings []struct {
		ID    string
		Prior int
	}
	for id, other := range m.deployments {
		if id == d.ID || other.AppID != d.AppID || other.Status != DeployLive ||
			normalizedDeploymentScope(other.Scope) != normalizedDeploymentScope(d.Scope) {
			continue
		}
		siblings = append(siblings, struct {
			ID    string
			Prior int
		}{id, other.TrafficPercent})
	}
	if len(siblings) == 0 {
		return
	}
	sortTrafficSiblings(siblings)
	weights := RedistributeTraffic(siblings, 100)
	for i, sibling := range siblings {
		other := m.deployments[sibling.ID]
		other.TrafficPercent = weights[i]
		m.putDeploymentLocked(sibling.ID, other)
	}
	d.TrafficPercent = 0
	m.putDeploymentLocked(d.ID, d)
}

func sortTrafficSiblings(siblings []struct {
	ID    string
	Prior int
}) {
	for i := 1; i < len(siblings); i++ {
		for j := i; j > 0 && siblings[j].ID < siblings[j-1].ID; j-- {
			siblings[j], siblings[j-1] = siblings[j-1], siblings[j]
		}
	}
}
