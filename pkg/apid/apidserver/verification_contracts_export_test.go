package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// RunVerificationQueriesOnce runs one pass of the SQL check worker.
func (r *server) RunVerificationQueriesOnce(ctx context.Context) {
	if queries, ok := r.store.(state.VerificationQueryStore); ok {
		r.runVerificationQueriesOnce(ctx, queries)
	}
}
