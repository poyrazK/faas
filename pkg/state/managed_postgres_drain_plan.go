package state

import (
	"context"
	"time"
)

// A plan covers the app's captured instance catalog across every deployment
// and scope. It does not attest to external SQL sessions, orphan host processes,
// cached routes, target data correctness or permission to publish credentials.
type ManagedPostgresDrainPlanFrame struct {
	ID, CutoverID, AppID, OwnerNodeID string
	OwnerRevision                     int64
	FencedAt                          time.Time
}

func (f ManagedPostgresDrainPlanFrame) Valid() bool {
	for _, id := range []string{f.ID, f.CutoverID, f.AppID, f.OwnerNodeID} {
		if !qualificationRecoveryUUIDValid(id) {
			return false
		}
	}
	return f.OwnerRevision > 0 && !f.FencedAt.IsZero()
}

type ManagedPostgresDrainPlanState string

const (
	ManagedPostgresDrainPlanDraining       ManagedPostgresDrainPlanState = "draining"
	ManagedPostgresDrainPlanBlocked        ManagedPostgresDrainPlanState = "blocked"
	ManagedPostgresDrainPlanCatalogRetired ManagedPostgresDrainPlanState = "catalog_retired"
)

type ManagedPostgresDrainPlanMember struct {
	InstanceID, DeploymentID, NodeID, WakeID string
	Kind, Mode, Scope, ObservedState         string
	State, BlockerCode                       string
	Drain                                    *ManagedPostgresDrain
}

type ManagedPostgresDrainPlan struct {
	Frame ManagedPostgresDrainPlanFrame
	State ManagedPostgresDrainPlanState
	// On inventory_limit_exceeded this is a lower bound (maximum + one), not
	// the size of a complete inventory. No members have been queued in that case.
	InventoryCount            int
	Pending, Retired, Blocked int
	Blockers                  []string
	Members                   []ManagedPostgresDrainPlanMember
}

// Reservation is explicit scheduler intent. Prepare/Verify and discovery never
// install a fence or create a plan. Pending native members enter the existing
// bounded drain-recovery queue in the same transaction as the complete plan.
type ManagedPostgresDrainPlanStore interface {
	ReserveManagedPostgresDrainPlan(context.Context, string, string) (ManagedPostgresDrainPlan, error)
	ManagedPostgresDrainPlan(context.Context, string) (ManagedPostgresDrainPlan, error)
}
