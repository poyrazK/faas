package apidserver

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 environment branches, for package apidserver_test.

const RuntimeConfigEnvironmentBranches = runtimeConfigEnvironmentBranches

func (r *server) CreateProjectEnvironmentBranch(w http.ResponseWriter, req *http.Request, acct state.Account) {
	r.createProjectEnvironmentBranch(w, req, acct)
}

// ReconcileEnvironmentBranchData runs one pass of the branch data reconciler.
func (r *server) ReconcileEnvironmentBranchData(ctx context.Context, now time.Time) error {
	return r.reconcileEnvironmentBranchData(ctx, r.store.(state.ProjectEnvironmentBranchDataStore), now)
}
