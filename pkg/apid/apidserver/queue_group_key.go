package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// queueGroupKeyWork turns a queue message's group_key into the work selector
// of the built-in ordered policy (ADR-20261112193306), behind its release flag.
func (s *server) queueGroupKeyWork(acct state.Account, groupKey string, work *api.InvokeWork) (*api.InvokeWork, *api.Problem) {
	if groupKey == "" {
		return work, nil
	}
	if !s.platformFlagEnabled(platformflags.QueueMessageGroups, acct) {
		return nil, api.NewProblem(http.StatusForbidden, "queue_groups_unavailable", "Ordered message groups unavailable",
			"group_key is not released to this account yet")
	}
	out, err := api.GroupKeyWork(groupKey, work)
	if err != nil {
		return nil, api.ErrValidation(err.Error())
	}
	return out, nil
}

// appWorkPolicyForEnqueue reads the named policy, creating the built-in
// ordered policy on its first use. A pre-existing policy under the reserved
// name with other settings is refused rather than reinterpreted.
func appWorkPolicyForEnqueue(ctx context.Context, store state.AppWorkPolicyStore, accountID, appID, name string) (state.AppWorkPolicy, *api.Problem) {
	var record state.AppWorkPolicy
	var err error
	if name == workpolicy.Ordered {
		record, err = state.OrderedWorkPolicy(ctx, store, accountID, appID)
	} else {
		record, err = store.AppWorkPolicyByName(ctx, appID, name)
	}
	switch {
	case errors.Is(err, state.ErrWorkPolicyReserved):
		return state.AppWorkPolicy{}, api.NewProblem(http.StatusConflict, "work_policy_reserved", "Reserved work policy",
			"this app has its own policy named "+workpolicy.Ordered+"; rename it to use group_key")
	case errors.Is(err, state.ErrNotFound):
		return state.AppWorkPolicy{}, api.ErrValidation("unknown work policy")
	case err != nil:
		return state.AppWorkPolicy{}, api.ErrCapacity("lookup work policy")
	}
	return record, nil
}
