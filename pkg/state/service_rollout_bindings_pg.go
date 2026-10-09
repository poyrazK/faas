package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func saveServiceHandoff(ctx context.Context, tx pgx.Tx, target Deployment) error {
	payload, err := json.Marshal(target.ServiceRolloutHandoff)
	if err != nil {
		return fmt.Errorf("encode service binding handoff: %w", err)
	}
	return sqlc.New().SaveServiceRolloutHandoff(ctx, tx, sqlc.SaveServiceRolloutHandoffParams{DeploymentID: mustPgUUID(target.ID), Handoff: payload})
}

// A missing grant commits only a durable request. Traffic and audit remain
// untouched; APID must evaluate fresh evidence before attempting the switch.
func (s *PgStore) prepareServiceBinding(ctx context.Context, tx pgx.Tx, target Deployment, action, recipientID, predecessorID, reason string) (Deployment, error) {
	if err := serviceBindingRequestMatches(ctx, target, action, recipientID, predecessorID); err != nil {
		return target, err
	}
	if requestID, _ := ctx.Value(serviceBindingRequestKey{}).(string); requestID == "" && target.ServiceRolloutHandoff.BindingsCheck != nil && target.ServiceRolloutHandoff.BindingsCheck.Action == action && target.ServiceRolloutHandoff.BindingsCheck.Status != "passed" {
		return target, ErrBindingReleaseRequired
	}
	if recipientID == "" {
		return target, nil
	}
	q := sqlc.New()
	traffic, err := q.ServiceRolloutRecipientTraffic(ctx, tx, mustPgUUID(recipientID))
	if err != nil {
		return target, mapErr(err)
	}
	requestID, _ := ctx.Value(serviceBindingRequestKey{}).(string)
	if traffic < 100 || requestID != "" {
		enforced, err := q.ServiceRolloutBindingEnforced(ctx, tx, sqlc.ServiceRolloutBindingEnforcedParams{AppID: mustPgUUID(target.AppID), Scope: normalizedDeploymentScope(target.Scope)})
		if err != nil {
			return target, err
		}
		hasRecipientFence := false
		for _, fence := range bindingReleaseFences(ctx) {
			if sameDeploymentID(fence.DeploymentID, recipientID) {
				hasRecipientFence = true
			}
		}
		if enforced && !hasRecipientFence {
			target = queueServiceBindingCheck(target, action, recipientID, predecessorID, reason)
			if err := saveServiceHandoff(ctx, tx, target); err != nil {
				return target, err
			}
			if err := tx.Commit(ctx); err != nil {
				return target, err
			}
			return target, ErrBindingReleaseRequired
		}
	}
	// Explicit worker grants are rechecked even when an abort recipient
	// already serves all traffic. A scheduler's later drain cleanup has no grant.
	if err := s.authorizeProductionLifecycle(ctx, tx, recipientID, action == "abort"); err != nil {
		return target, err
	}
	if err := pgAuthorizeBindingRelease(ctx, tx); err != nil {
		return target, err
	}
	if requestID, _ := ctx.Value(serviceBindingRequestKey{}).(string); requestID != "" {
		ready, err := q.ServiceRolloutRecipientReady(ctx, tx, sqlc.ServiceRolloutRecipientReadyParams{AppID: mustPgUUID(target.AppID), DeploymentID: mustPgUUID(recipientID), Action: action})
		if err != nil {
			return target, err
		}
		if !ready {
			return target, ErrServiceRolloutNotReady
		}
	}
	return target, nil
}

func completeServiceBinding(ctx context.Context, tx pgx.Tx, target Deployment) (Deployment, error) {
	if requestID, _ := ctx.Value(serviceBindingRequestKey{}).(string); requestID == "" {
		return target, nil
	}
	auditID, err := sqlc.New().AppendServiceRolloutBindingAudit(ctx, tx, sqlc.AppendServiceRolloutBindingAuditParams{DeploymentID: mustPgUUID(target.ID), AppID: mustPgUUID(target.AppID), Kind: string(DeployTrafficChanged), Actor: "apid:binding_service_rollout", Data: serviceBindingAudit(ctx, target)})
	if err != nil {
		return target, fmt.Errorf("audit service binding transition: %w", err)
	}
	target.ServiceRolloutHandoff.BindingsCheck = passedServiceBindingGate(ctx, target, strconv.FormatInt(auditID, 10))
	return target, nil
}

func (s *PgStore) UpdateServiceRolloutBindingStatus(ctx context.Context, id, requestID, code string, blockers []api.BindingCheckFinding) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	target, _, err := s.loadAndLockServiceRollout(ctx, tx, id)
	if err != nil {
		return err
	}
	gate := target.ServiceRolloutHandoff.BindingsCheck
	if gate == nil || gate.RequestID != requestID || gate.Status == "passed" {
		return ErrServiceRolloutInvalid
	}
	target.ServiceRolloutHandoff.BindingsCheck = blockedServiceBindingGate(gate, code, blockers)
	target.ServiceRolloutHandoff.LastError = target.ServiceRolloutHandoff.BindingsCheck.Code
	if err := saveServiceHandoff(ctx, tx, target); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) RequestServiceRolloutAbort(ctx context.Context, appID, id, predecessorID, reason string) (Deployment, int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Deployment{}, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	target, rows, err := s.loadAndLockServiceRollout(ctx, tx, id)
	if err != nil || target.AppID != appID {
		if err == nil {
			err = ErrNotFound
		}
		return target, 0, err
	}
	previous, found := previousServiceRolloutRow(target, rows)
	if !found || previous.id != predecessorID {
		return target, 0, ErrServiceRolloutInvalid
	}
	target = queueServiceBindingCheck(target, ServiceRolloutActionAbort, predecessorID, predecessorID, reason)
	if err := saveServiceHandoff(ctx, tx, target); err != nil {
		return target, 0, err
	}
	auditID, err := sqlc.New().AppendServiceRolloutBindingAudit(ctx, tx, sqlc.AppendServiceRolloutBindingAuditParams{DeploymentID: mustPgUUID(id), AppID: mustPgUUID(appID), Kind: string(DeployRolledBack), Actor: "operator:cli:recover_rollout", Data: serviceBindingIntentAudit(target, reason)})
	if err != nil {
		return target, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return target, 0, err
	}
	return target, auditID, nil
}
