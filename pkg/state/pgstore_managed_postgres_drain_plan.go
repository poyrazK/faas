package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ManagedPostgresDrainPlanStore = (*PgStore)(nil)

func (s *PgStore) ReserveManagedPostgresDrainPlan(ctx context.Context, cutoverID, ownerNodeID string) (ManagedPostgresDrainPlan, error) {
	if !qualificationRecoveryUUIDValid(cutoverID) || !qualificationRecoveryUUIDValid(ownerNodeID) {
		return ManagedPostgresDrainPlan{}, ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.ManagedPostgresCutoverDrainTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManagedPostgresDrainPlan{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	authority, err := q.LockManagedPostgresDrainPlanReservation(ctx, tx, sqlc.LockManagedPostgresDrainPlanReservationParams{CutoverID: cutoverID, OwnerNodeID: ownerNodeID})
	if err != nil {
		return ManagedPostgresDrainPlan{}, drainAuthorityError(err)
	}
	plan, err := q.GetManagedPostgresDrainPlanForFence(ctx, tx, sqlc.GetManagedPostgresDrainPlanForFenceParams{
		CutoverID: cutoverID, OwnerNodeID: ownerNodeID, OwnerRevision: authority.OwnerRevision, FencedAt: authority.FencedAt})
	if errors.Is(err, pgx.ErrNoRows) {
		plan, err = reserveManagedPostgresDrainPlan(ctx, tx, q, cutoverID, authority)
	}
	if err != nil {
		return ManagedPostgresDrainPlan{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ManagedPostgresDrainPlan{}, mapErr(err)
	}
	return s.ManagedPostgresDrainPlan(ctx, pgUUIDString(plan.ID))
}

func reserveManagedPostgresDrainPlan(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, cutoverID string,
	authority sqlc.LockManagedPostgresDrainPlanReservationRow) (sqlc.ManagedPostgresCutoverDrainPlan, error) {
	rows, err := q.ListManagedPostgresDrainPlanInventory(ctx, tx, sqlc.ListManagedPostgresDrainPlanInventoryParams{
		AppID: authority.AppID, PageLimit: api.ManagedPostgresCutoverDrainPlanMembersMax + 1})
	if err != nil {
		return sqlc.ManagedPostgresCutoverDrainPlan{}, err
	}
	blocker := ""
	if len(rows) == 0 {
		blocker = "inventory_empty"
	} else if len(rows) > api.ManagedPostgresCutoverDrainPlanMembersMax {
		blocker = "inventory_limit_exceeded"
	}
	plan, err := q.InsertManagedPostgresDrainPlan(ctx, tx, sqlc.InsertManagedPostgresDrainPlanParams{
		ID: uuid.NewString(), CutoverID: cutoverID, AppID: authority.AppID, OwnerNodeID: authority.OwnerNodeID,
		OwnerRevision: authority.OwnerRevision, FencedAt: authority.FencedAt, InventoryCount: int32(len(rows)), BlockerCode: blocker})
	if err != nil || blocker != "" {
		return plan, err
	}
	for _, row := range rows {
		member := sqlc.InsertManagedPostgresDrainPlanMemberParams{PlanID: pgUUIDString(plan.ID), InstanceID: pgUUIDString(row.ID),
			DeploymentID: row.DeploymentID, NodeID: pgUUIDString(row.NodeID), WakeID: pgUUIDString(row.WakeID),
			InstanceKind: row.Kind, InstanceMode: row.Mode, ObservedState: row.State, DeploymentScope: row.DeploymentScope,
			MigratedFromNodeID: row.MigratedFromNodeID, QualificationLive: row.QualificationLive, BlockerCode: drainPlanInventoryBlocker(row)}
		if member.BlockerCode == "" {
			drain, err := q.InsertManagedPostgresDrain(ctx, tx, sqlc.InsertManagedPostgresDrainParams{ID: uuid.NewString(), CutoverID: cutoverID,
				AppID: authority.AppID, DeploymentID: pgUUIDString(row.DeploymentID), InstanceID: member.InstanceID,
				NodeID: member.NodeID, WakeID: member.WakeID, OwnerNodeID: authority.OwnerNodeID,
				OwnerRevision: authority.OwnerRevision, FencedAt: authority.FencedAt})
			if err != nil {
				return plan, err
			}
			member.DrainID = drain.ID
		}
		if err := q.InsertManagedPostgresDrainPlanMember(ctx, tx, member); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

func drainPlanInventoryBlocker(row sqlc.ListManagedPostgresDrainPlanInventoryRow) string {
	for _, id := range []pgtype.UUID{row.ID, row.DeploymentID, row.NodeID, row.WakeID} {
		if !qualificationRecoveryUUIDValid(pgUUIDString(id)) {
			return "instance_identity_incomplete"
		}
	}
	if row.Kind != "wake" {
		return "unsupported_instance_kind"
	}
	if row.MigratedFromNodeID.Valid || row.State == "migrating" {
		return "migration_lineage_unproven"
	}
	if row.QualificationLive {
		return "qualification_cleanup_required"
	}
	switch row.State {
	case "parked":
		return "parked_instance_unproven"
	case "pending":
		return "undispatched_instance_unproven"
	case "waking", "cold_booting", "running", "draining", "snapshotting", "warm", "failed", "stopped":
		return ""
	default:
		return "unsupported_instance_state"
	}
}

// Status is a current, read-only observation under the app lock. A future
// publication transaction must recheck it along with its other prerequisites;
// the returned catalog verdict is never a reusable activation token.
func (s *PgStore) ManagedPostgresDrainPlan(ctx context.Context, id string) (ManagedPostgresDrainPlan, error) {
	if !qualificationRecoveryUUIDValid(id) {
		return ManagedPostgresDrainPlan{}, ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.ManagedPostgresCutoverDrainTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManagedPostgresDrainPlan{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	plan, err := readManagedPostgresDrainPlan(ctx, tx, id, true)
	if err != nil {
		return plan, err
	}
	return plan, mapErr(tx.Commit(ctx))
}

// ObserveManagedPostgresDrainPlan reads existing evidence without taking owner
// locks or reserving work. The caller must supply a repeatable-read transaction
// or hold the app lock to stabilize authority, inventory and retirement receipts.
// This observation is never authority to retire a VM or publish credentials.
func ObserveManagedPostgresDrainPlan(ctx context.Context, tx pgx.Tx, id string) (ManagedPostgresDrainPlan, error) {
	if !qualificationRecoveryUUIDValid(id) {
		return ManagedPostgresDrainPlan{}, ErrInvalidArgument
	}
	return readManagedPostgresDrainPlan(ctx, tx, id, false)
}

func readManagedPostgresDrainPlan(ctx context.Context, tx pgx.Tx, id string, lockOwner bool) (ManagedPostgresDrainPlan, error) {
	q := sqlc.New()
	header, err := q.GetManagedPostgresDrainPlan(ctx, tx, id)
	if err != nil {
		return ManagedPostgresDrainPlan{}, mapErr(err)
	}
	f := ManagedPostgresDrainPlanFrame{ID: id, CutoverID: pgUUIDString(header.CutoverID), AppID: pgUUIDString(header.AppID),
		OwnerNodeID: pgUUIDString(header.OwnerNodeID), OwnerRevision: header.OwnerRevision, FencedAt: header.FencedAt.Time.UTC()}
	plan := ManagedPostgresDrainPlan{Frame: f, InventoryCount: int(header.InventoryCount), State: ManagedPostgresDrainPlanDraining}
	if !f.Valid() || !header.FencedAt.Valid || header.InventoryCount < 0 {
		return plan, ErrConflict
	}
	if header.BlockerCode != "" {
		plan.Blockers = append(plan.Blockers, header.BlockerCode)
	}
	var owner sqlc.LockManagedPostgresDrainPlanOwnerRow
	if lockOwner {
		owner, err = q.LockManagedPostgresDrainPlanOwner(ctx, tx, f.AppID)
	} else {
		var row sqlc.ReadManagedPostgresDrainPlanOwnerRow
		row, err = q.ReadManagedPostgresDrainPlanOwner(ctx, tx, f.AppID)
		owner = sqlc.LockManagedPostgresDrainPlanOwnerRow(row)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		plan.Blockers = append(plan.Blockers, "authority_missing")
	} else if err != nil {
		return plan, mapErr(err)
	} else if code := drainPlanAuthorityBlocker(f, owner); code != "" {
		plan.Blockers = append(plan.Blockers, code)
	}
	rows, err := q.ListManagedPostgresDrainPlanMembers(ctx, tx, sqlc.ListManagedPostgresDrainPlanMembersParams{PlanID: id, PageLimit: api.ManagedPostgresCutoverDrainPlanMembersMax + 1})
	if err != nil {
		return plan, mapErr(err)
	}
	if header.BlockerCode == "" && (len(rows) != plan.InventoryCount || len(rows) == 0 || len(rows) > api.ManagedPostgresCutoverDrainPlanMembersMax) {
		plan.Blockers = append(plan.Blockers, "membership_incomplete")
	}
	unplanned, err := q.ManagedPostgresDrainPlanHasUnplannedInstances(ctx, tx, sqlc.ManagedPostgresDrainPlanHasUnplannedInstancesParams{AppID: f.AppID, PlanID: id})
	if err != nil {
		return plan, mapErr(err)
	}
	if unplanned {
		plan.Blockers = append(plan.Blockers, "inventory_changed")
	}
	for _, row := range rows {
		member, err := observeManagedPostgresDrainPlanMember(ctx, tx, q, f, row)
		if err != nil {
			return plan, err
		}
		plan.Members = append(plan.Members, member)
		switch member.State {
		case "retired":
			plan.Retired++
		case "pending":
			plan.Pending++
		default:
			plan.Blocked++
		}
	}
	if len(plan.Blockers) > 0 || plan.Blocked > 0 {
		plan.State = ManagedPostgresDrainPlanBlocked
	} else if plan.Retired == plan.InventoryCount && plan.InventoryCount > 0 {
		plan.State = ManagedPostgresDrainPlanCatalogRetired
	}
	return plan, nil
}

func drainPlanAuthorityBlocker(f ManagedPostgresDrainPlanFrame, owner sqlc.LockManagedPostgresDrainPlanOwnerRow) string {
	switch {
	case pgUUIDString(owner.NodeID) != f.OwnerNodeID:
		return "owner_changed"
	case owner.ScalingPolicyRevision != f.OwnerRevision:
		return "owner_revision_changed"
	case pgUUIDString(owner.ManagedPostgresAdmissionCutoverID) != f.CutoverID || !owner.ManagedPostgresAdmissionFencedAt.Valid || !owner.ManagedPostgresAdmissionFencedAt.Time.Equal(f.FencedAt):
		return "fence_changed"
	case owner.CutoverState != "verified" && owner.CutoverState != "verifying":
		return "cutover_not_active"
	case owner.Status != "active" && owner.Status != "evicted_cold":
		return "app_not_active"
	default:
		return ""
	}
}

func observeManagedPostgresDrainPlanMember(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, f ManagedPostgresDrainPlanFrame,
	row sqlc.ListManagedPostgresDrainPlanMembersRow) (ManagedPostgresDrainPlanMember, error) {
	m := ManagedPostgresDrainPlanMember{InstanceID: pgUUIDString(row.InstanceID), DeploymentID: pgUUIDString(row.DeploymentID),
		NodeID: pgUUIDString(row.NodeID), WakeID: pgUUIDString(row.WakeID), Kind: row.InstanceKind, Mode: row.InstanceMode,
		Scope: row.DeploymentScope, ObservedState: row.ObservedState, State: "blocked", BlockerCode: row.BlockerCode}
	if m.BlockerCode != "" {
		return m, nil
	}
	receipt, err := q.GetManagedPostgresDrain(ctx, tx, pgUUIDString(row.DrainID))
	if errors.Is(err, pgx.ErrNoRows) {
		m.BlockerCode = "receipt_missing"
		return m, nil
	}
	if err != nil {
		return m, mapErr(err)
	}
	drain, err := managedPostgresDrainFromSQL(receipt)
	// A malformed persisted receipt is an inspectable blocker, like a missing
	// receipt. Unexpected decoder errors remain failures of the observation.
	if errors.Is(err, ErrConflict) {
		m.BlockerCode = "receipt_invalid"
		return m, nil
	}
	if err != nil {
		return m, err
	}
	expected := ManagedPostgresDrainFrame{ID: pgUUIDString(row.DrainID), CutoverID: f.CutoverID, AppID: f.AppID,
		DeploymentID: m.DeploymentID, InstanceID: m.InstanceID, NodeID: m.NodeID, WakeID: m.WakeID,
		OwnerNodeID: f.OwnerNodeID, OwnerRevision: f.OwnerRevision, FencedAt: f.FencedAt}
	if !drain.Frame.Equal(expected) {
		m.BlockerCode = "receipt_invalid"
		return m, nil
	}
	m.Drain = &drain
	switch {
	case row.CurrentInstanceID != "" && (row.CurrentInstanceID != m.InstanceID || row.CurrentAppID != f.AppID || row.CurrentDeploymentID != m.DeploymentID || row.CurrentNodeID != m.NodeID || row.CurrentWakeID != m.WakeID || row.CurrentKind != m.Kind || row.CurrentMode != m.Mode || row.CurrentMigratedFromNodeID != ""):
		m.BlockerCode = "instance_changed"
	case row.CurrentQualificationLive:
		m.BlockerCode = "qualification_cleanup_required"
	case drain.Proof != nil && row.CurrentInstanceID != "" && row.CurrentState != "stopped":
		m.BlockerCode = "receipt_lifecycle_mismatch"
	case drain.Proof != nil:
		m.State, m.BlockerCode = "retired", ""
	case row.CurrentInstanceID == "":
		m.BlockerCode = "instance_missing"
	case row.CurrentState != "stopped" && !CanTransition(State(row.CurrentState), StateStopped):
		m.BlockerCode = "unsupported_instance_state"
	default:
		m.State, m.BlockerCode = "pending", "native_retirement_unconfirmed"
	}
	return m, nil
}
