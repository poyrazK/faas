package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/objectstorage/grantrevocation"
)

// Private recovery intent; retained plans and dispatch acknowledgements are
// not provider revocation/drainage proofs. Request writers remain independent.
type ProjectEnvironmentCloneObjectGrantRevocation struct {
	Plan                                     grantrevocation.Plan
	State, RevocationID                      string
	RetainedAt, RequestStartedAt, ObservedAt time.Time
	DrainedAt                                time.Time
}

type ProjectEnvironmentCloneObjectGrantRevocationStore interface {
	ReserveProjectEnvironmentCloneObjectGrantRevocation(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneObjectGrantRevocation, error)
	ProjectEnvironmentCloneObjectGrantRevocationForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneObjectGrantRevocation, error)
	DispatchProjectEnvironmentCloneObjectGrantRevocation(context.Context, ProjectEnvironmentCloneLease, grantrevocation.Plan) (ProjectEnvironmentCloneObjectGrantRevocation, error)
	RecordProjectEnvironmentCloneObjectGrantRevocation(context.Context, ProjectEnvironmentCloneLease, grantrevocation.Plan, grantrevocation.Observation) (ProjectEnvironmentCloneObjectGrantRevocation, error)
}
