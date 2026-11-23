package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// RevokeUngrantedWorkloadCredentials runs the post-apply revocation for
// changed apps.
func (r *server) RevokeUngrantedWorkloadCredentials(changed []state.App) {
	r.revokeUngrantedWorkloadCredentials(context.Background(), changed)
}

// SweepWorkloadCredentials runs one workload credential sweep as of now
// (ADR-20261122191423 slice 2).
func (r *server) SweepWorkloadCredentials(now time.Time) {
	if store, ok := r.store.(state.ObjectWorkloadCredentialStore); ok {
		r.object.SweepWorkloadCredentials(context.Background(), store, now)
	}
}
