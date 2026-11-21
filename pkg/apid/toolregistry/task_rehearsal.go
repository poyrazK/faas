package toolregistry

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// HostedTaskRehearsal exposes owner-only plan metadata and decisions.
func (e *PrivateExecutor) HostedTaskRehearsal() httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		r, err := e.freshPublicationRequest(r, acct, true)
		if err != nil {
			respond(w, nil, err)
			return
		}
		p, _ := httpkit.PrincipalFrom(r)
		if p.Key == nil || agentRegistryRequest(r) || p.Key.Label == api.MCPLoginKeyLabel {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Personal administrator key required", "use an unscoped personal administrator API key for rehearsal decisions"))
			return
		}
		if !connectionOwnerAccess(w, r, acct, e.store) {
			return
		}
		e.serveHostedTaskRehearsal(w, r, acct, p.Key.ID)
	}
}

func (e *PrivateExecutor) serveHostedTaskRehearsal(w http.ResponseWriter, r *http.Request, acct state.Account, owner string) {
	journal, ok := e.store.(state.HostedTaskRehearsalStore)
	id := r.PathValue("id")
	if !ok || !toolpublication.ValidID(id) {
		respond(w, nil, state.ErrInvalidArgument)
		return
	}
	in := state.HostedTaskRehearsalChange{OwnerKeyID: owner}
	var action state.HostedTaskRehearsalAction
	var err error
	switch {
	case r.Method == http.MethodGet:
		action = state.ReadHostedTaskRehearsal
	case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/decision"):
		action = state.DecideHostedTaskRehearsal
		err = httpkit.DecodeJSONSized(r, &in.Decision, api.HostedTaskBindingRequestMaxBytes)
	case r.Method == http.MethodPut:
		action = state.PrepareHostedTaskRehearsal
		err = httpkit.DecodeJSONSized(r, &in.Prepare, api.HostedTaskBindingRequestMaxBytes)
	default:
		w.Header().Set("Allow", "GET, PUT")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err != nil {
		respond(w, nil, state.ErrInvalidArgument)
		return
	}
	if (action == state.PrepareHostedTaskRehearsal || action == state.DecideHostedTaskRehearsal && in.Decision.Decision == "approve") &&
		(!e.opts.RehearsalPlanEnabled || !e.opts.SourceRetentionEnabled || !e.opts.BindingVerificationEnabled) {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "task_rehearsal_plan_disabled", "Hosted Task rehearsal planning unavailable", "enable native rehearsal planning, source retention, and binding verification"))
		return
	}
	out, err := journal.ChangeHostedTaskRehearsal(r.Context(), acct.ID, id, action, in)
	respond(w, out.Plan, err)
}
