package queueaccess

import (
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func Usage(store state.Store) authmw.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, owner state.Account) {
		reader, ok := store.(state.OrgQueueUsageStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("queue usage unavailable"))
			return
		}
		usage, err := reader.OrgQueueUsage(r.Context(), owner.ResourceOwnerID())
		if err != nil {
			api.WriteProblem(w, problem(err))
			return
		}
		limits := api.MustLimitsFor(owner.Plan)
		usage.Plan, usage.MaxAsyncInvocations, usage.MaxTriggers = owner.Plan, limits.MaxAsyncInvocationsPerAccount, limits.TriggerLimitPerAccount
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(usage)
	}
}
