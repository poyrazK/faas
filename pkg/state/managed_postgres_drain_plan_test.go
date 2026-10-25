package state_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestManagedPostgresDrainPlanNeverQueuesUnprovenLifecycleKinds(t *testing.T) {
	id := func() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }
	base := sqlc.ListManagedPostgresDrainPlanInventoryRow{ID: id(), DeploymentID: id(), NodeID: id(), WakeID: id(), Kind: "wake", Mode: "normal", State: "running"}
	for _, tc := range []struct {
		name, want string
		change     func(*sqlc.ListManagedPostgresDrainPlanInventoryRow)
	}{
		{"qualification", "qualification_cleanup_required", func(r *sqlc.ListManagedPostgresDrainPlanInventoryRow) { r.QualificationLive = true }},
		{"migration", "migration_lineage_unproven", func(r *sqlc.ListManagedPostgresDrainPlanInventoryRow) { r.MigratedFromNodeID = id() }},
		{"migrating", "migration_lineage_unproven", func(r *sqlc.ListManagedPostgresDrainPlanInventoryRow) { r.State = "migrating" }},
		{"parked", "parked_instance_unproven", func(r *sqlc.ListManagedPostgresDrainPlanInventoryRow) { r.State = "parked" }},
		{"pending", "undispatched_instance_unproven", func(r *sqlc.ListManagedPostgresDrainPlanInventoryRow) { r.State = "pending" }},
		{"builder", "unsupported_instance_kind", func(r *sqlc.ListManagedPostgresDrainPlanInventoryRow) { r.Kind = "build" }},
		{"missing_deployment", "instance_identity_incomplete", func(r *sqlc.ListManagedPostgresDrainPlanInventoryRow) { r.DeploymentID = pgtype.UUID{} }},
		{"zero_wake", "instance_identity_incomplete", func(r *sqlc.ListManagedPostgresDrainPlanInventoryRow) { r.WakeID = pgtype.UUID{Valid: true} }},
		{"deleted_account", "unsupported_instance_state", func(r *sqlc.ListManagedPostgresDrainPlanInventoryRow) { r.State = "evicting_account_deleting" }},
		{"terminal_still_needs_native_receipt", "", func(r *sqlc.ListManagedPostgresDrainPlanInventoryRow) { r.State = "stopped" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := base
			tc.change(&row)
			if got := state.DrainPlanInventoryBlocker(row); got != tc.want {
				t.Fatalf("blocker = %q, want %q", got, tc.want)
			}
		})
	}
}
