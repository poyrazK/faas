package managedpostgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Reads share the caller's repeatable-read snapshot or existing app lock. This
// helper takes no owner locks, reserves no work and makes no native calls, so
// preflight cannot invert the mutation path's app -> cutover lock order.
func readPreflightRuntime(ctx context.Context, tx pgx.Tx, c Cutover) (*CutoverRuntimeDrain, string, error) {
	q := sqlc.New()
	owner, err := q.ReadManagedPostgresDrainPlanOwner(ctx, tx, c.AppID)
	if err != nil {
		return nil, "", mapPostgresError(err)
	}
	var plan *state.ManagedPostgresDrainPlan
	id, err := q.ReadManagedPostgresPreflightDrainPlan(ctx, tx, sqlc.ReadManagedPostgresPreflightDrainPlanParams{CutoverID: c.ID, AppID: c.AppID})
	if err == nil {
		observed, readErr := state.ObserveManagedPostgresDrainPlan(ctx, tx, id)
		if readErr != nil {
			return nil, "", readErr
		}
		plan = &observed
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, "", mapPostgresError(err)
	}
	// The private digest also covers owner/fence changes when no plan exists,
	// and receipt/incarnation identities that must never be exposed in the API.
	raw, err := json.Marshal(struct {
		Owner sqlc.ReadManagedPostgresDrainPlanOwnerRow
		Plan  *state.ManagedPostgresDrainPlan
	}{owner, plan})
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(raw)
	out := &CutoverRuntimeDrain{State: "missing", Blockers: []string{"drain_plan_missing"}, Members: []CutoverRuntimeDrainMember{}}
	if plan != nil {
		out = runtimeDrainFromPlan(*plan)
	} else if cutoverUUID(owner.ManagedPostgresAdmissionCutoverID) != c.ID || !owner.ManagedPostgresAdmissionFencedAt.Valid {
		out.State, out.Blockers = "not_fenced", []string{"admission_fence_missing"}
	}
	return out, hex.EncodeToString(digest[:]), nil
}

func runtimeDrainFromPlan(plan state.ManagedPostgresDrainPlan) *CutoverRuntimeDrain {
	out := &CutoverRuntimeDrain{State: string(plan.State), InventoryCount: plan.InventoryCount, Pending: plan.Pending,
		Retired: plan.Retired, Blocked: plan.Blocked, Blockers: append([]string{}, plan.Blockers...), Members: []CutoverRuntimeDrainMember{}}
	for _, member := range plan.Members {
		out.Members = append(out.Members, CutoverRuntimeDrainMember{InstanceID: member.InstanceID, DeploymentID: member.DeploymentID,
			Scope: member.Scope, Mode: member.Mode, State: member.State, BlockerCode: member.BlockerCode})
	}
	return out
}
