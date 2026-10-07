package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// Materialization is recoverable separately from provider preparation and
// deployment creation. The private receipt retains the environment lifetime
// and original desired heads; retries never overwrite an edited target.
type ProjectEnvironmentCloneMaterializationStore interface {
	MaterializeProjectEnvironmentCloneForLease(context.Context, ProjectEnvironmentCloneLease, []string, int, api.Limits) (ProjectEnvironment, error)
}

type projectCloneMaterializedWorkload struct {
	SpecID string `json:"spec_id"`
	Hash   string `json:"hash"`
}

var _ ProjectEnvironmentCloneMaterializationStore = (*PgStore)(nil)
