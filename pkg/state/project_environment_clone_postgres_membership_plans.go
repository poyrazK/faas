package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
)

// One private encrypted grant plan per independently prepared target. Exact
// inventory, bootstrap-pin and role-plan ciphertexts remain its prerequisites.
// Seed SQL OIDs/time and all raw grants stay inside authenticated encryption.
type ProjectEnvironmentClonePostgresMembershipPlan struct {
	Sealed                                                                          copyroles.SealedMemberships
	TargetDatabaseID                                                                string
	InventoryCiphertextSHA256, TargetPinsCiphertextSHA256, RolePlanCiphertextSHA256 string
	CapturedAt                                                                      time.Time
}

type ProjectEnvironmentClonePostgresMembershipPlanStore interface {
	ProjectEnvironmentClonePostgresMembershipPlanForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresMembershipPlan, error)
	RecordProjectEnvironmentClonePostgresMembershipPlan(context.Context, ProjectEnvironmentCloneLease, string, string, copyroles.SealedMemberships) (ProjectEnvironmentClonePostgresMembershipPlan, bool, error)
}

var _ ProjectEnvironmentClonePostgresMembershipPlanStore = (*PgStore)(nil)
