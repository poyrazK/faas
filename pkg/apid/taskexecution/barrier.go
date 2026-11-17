// Package taskexecution fences trusted local Task executor requests at the
// native mutation boundary. It grants no scope beyond ordinary authorization.
package taskexecution

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// Wrap keeps the durable journal lock until synchronous intent writes settle.
// Long-running builders/schedulers are reconciled from native facts separately.
func Wrap(store any, next middleware.AccountHandler) middleware.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, account state.Account) {
		f := state.TaskToolExecutionFence{IntentID: r.Header.Get(api.TaskExecutionIntentHeader), OperationID: r.Header.Get(api.TaskExecutionOperationHeader), ClaimToken: r.Header.Get(api.TaskExecutionClaimHeader)}
		if len(r.Header.Values(api.TaskExecutionIntentHeader))+len(r.Header.Values(api.TaskExecutionOperationHeader))+len(r.Header.Values(api.TaskExecutionClaimHeader)) == 0 {
			next(w, r, account)
			return
		}
		p, _ := httpkit.PrincipalFrom(r)
		barrier, ok := store.(state.TaskToolExecutionBarrierStore)
		if !ok || p.Key == nil || !toolpublication.ValidID(f.IntentID) || !toolpublication.ValidID(f.OperationID) || !toolpublication.ValidID(f.ClaimToken) ||
			len(r.Header.Values(api.TaskExecutionIntentHeader)) != 1 || len(r.Header.Values(api.TaskExecutionOperationHeader)) != 1 || len(r.Header.Values(api.TaskExecutionClaimHeader)) != 1 ||
			r.Method == http.MethodGet || r.Method == http.MethodHead || strings.HasPrefix(r.URL.Path, "/v1/task-tool-release-intents/") {
			refuse(w)
			return
		}
		release, err := barrier.AcquireTaskToolExecutionEffect(r.Context(), account.ID, p.Key.ID, f)
		if err != nil {
			refuse(w)
			return
		}
		defer release()
		next(w, r, account)
	}
}
func refuse(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusConflict, "task_execution_fenced", "Task executor is fenced", "Inspect the native operation; recovery needs a settled handover and separate owner approval"))
}
