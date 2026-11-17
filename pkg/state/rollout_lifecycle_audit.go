package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// rolloutLifecycleAuditActor attributes the rollout lifecycle rows the
// transitions below write: the system moved the rollout, whoever caused the
// step.
const rolloutLifecycleAuditActor = "system:rollout"

// insertRolloutLifecycleAuditTx records a canary rollout starting or completing
// in the transaction that moves it (ADR-20261028110044 amendment 7). The
// safe-deploy orchestrator only audited transitions it made itself, and the
// live and advance paths make both, so the release timeline never showed a
// rollout starting or completing. The row carries the app's account so
// per-account audit views list it.
func insertRolloutLifecycleAuditTx(ctx context.Context, tx pgx.Tx, dep Deployment, kind DeploymentAuditKind, at time.Time) error {
	data := map[string]any{
		"deployment_id": dep.ID,
		"app_id":        dep.AppID,
		"canary_preset": dep.CanaryPreset,
		"canary_total":  dep.CanaryTotalSteps,
	}
	switch kind {
	case DeployRolloutStarted:
		data["canary_step"] = dep.CanaryStep
		data["started_at"] = at.UTC().Format(time.RFC3339Nano)
	case DeployRolloutCompleted:
		data["final_step"] = dep.CanaryTotalSteps
		data["completed_at"] = at.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Errorf("state: rollout lifecycle audit kind %q: %w", kind, ErrInvalidArgument)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("state: encode rollout lifecycle audit: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`insert into deployment_audit (deployment_id, account_id, kind, actor, at, data)
		 select $1::uuid, a.account_id, $3, $4, $5, $6::jsonb
		   from apps a where a.id = $2::uuid`,
		dep.ID, dep.AppID, string(kind), rolloutLifecycleAuditActor, at, raw); err != nil {
		return fmt.Errorf("state: rollout lifecycle audit %s: %w", kind, err)
	}
	return nil
}
