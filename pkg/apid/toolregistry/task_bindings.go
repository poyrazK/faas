package toolregistry

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// HostedTaskBindings returns closed metadata only. All changes require fresh
// native owner authority; the original requester can only read status.
func (e *PrivateExecutor) HostedTaskBindings() httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		owner := r.Method != http.MethodGet
		p, _ := httpkit.PrincipalFrom(r)
		r, err := e.freshPublicationRequest(r, acct, owner || p.Key == nil)
		if err != nil {
			respond(w, nil, err)
			return
		}
		if owner && !connectionOwnerAccess(w, r, acct, e.store) || !owner && !personalAccess(w, r, acct, e.store, true) {
			return
		}
		e.serveHostedTaskBindings(w, r, acct, owner)
	}
}

func (e *PrivateExecutor) serveHostedTaskBindings(w http.ResponseWriter, r *http.Request, acct state.Account, owner bool) {
	journal, ok := e.store.(state.HostedTaskBindingStore)
	source, sourceOK := e.store.(state.HostedTaskSourceIntentStore)
	if !ok || !sourceOK || !toolpublication.ValidID(r.PathValue("id")) {
		respond(w, nil, state.ErrInvalidArgument)
		return
	}
	p, _ := httpkit.PrincipalFrom(r)
	row, err := source.GetHostedTaskSourceIntent(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	if !owner && p.Key != nil && p.Key.ID != row.Record.RequesterKeyID && (agentRegistryRequest(r) || !httpkit.APIKeyCarriesScope(*p.Key, api.ScopeAdmin)) {
		respond(w, nil, state.ErrNotFound)
		return
	}
	actor := state.HostedTaskSourceOwner{Session: p.Key == nil}
	if p.Key != nil {
		actor.KeyID = p.Key.ID
	}
	if !owner {
		out, err := journal.ChangeHostedTaskBindings(r.Context(), acct.ID, r.PathValue("id"), "get", state.HostedTaskBindingChange{})
		respond(w, out.Public, err)
		return
	}
	e.changeHostedTaskBindings(w, r, acct, actor, journal)
}

func (e *PrivateExecutor) changeHostedTaskBindings(w http.ResponseWriter, r *http.Request, acct state.Account, owner state.HostedTaskSourceOwner, journal state.HostedTaskBindingStore) {
	id := r.PathValue("id")
	if strings.HasSuffix(r.URL.Path, "/revoke") {
		var in api.HostedTaskBindingCheck
		if httpkit.DecodeJSONSized(r, &in, api.HostedTaskBindingRequestMaxBytes) != nil || !toolpublication.ValidID(in.BindingSHA256) {
			respond(w, nil, state.ErrInvalidArgument)
			return
		}
		out, err := journal.ChangeHostedTaskBindings(r.Context(), acct.ID, id, "revoke", state.HostedTaskBindingChange{Owner: owner, BindingSHA256: in.BindingSHA256})
		respond(w, out.Public, err)
		return
	}
	if !e.opts.BindingVerificationEnabled || !e.opts.SourceRetentionEnabled || e.opts.Identities == nil || e.opts.ConnectionIdentities == nil {
		api.WriteProblem(w, api.NewProblem(503, "task_binding_verification_disabled", "Task binding verification unavailable", "enable native binding verification, source retention and separate Account/runtime seal identities"))
		return
	}
	if r.Method == http.MethodPut {
		var in api.HostedTaskBindingSelection
		if httpkit.DecodeJSONSized(r, &in, api.HostedTaskBindingRequestMaxBytes) != nil || tasktoolbinding.ValidateSelection(in) != nil {
			respond(w, nil, state.ErrInvalidArgument)
			return
		}
		policy, err := e.hostedBindingPolicy(r.Context(), acct.ID, id)
		if err != nil {
			respond(w, nil, err)
			return
		}
		out, err := journal.ChangeHostedTaskBindings(r.Context(), acct.ID, id, "select", state.HostedTaskBindingChange{Owner: owner, Selection: in, Policy: policy})
		respond(w, out.Public, err)
		return
	}
	var in api.HostedTaskBindingCheck
	if !strings.HasSuffix(r.URL.Path, "/check") || httpkit.DecodeJSONSized(r, &in, api.HostedTaskBindingRequestMaxBytes) != nil || !toolpublication.ValidID(in.BindingSHA256) {
		respond(w, nil, state.ErrInvalidArgument)
		return
	}
	out, err := e.checkHostedTaskBindings(r.Context(), acct.ID, id, owner, in, journal)
	respond(w, out.Public, err)
}
func (e *PrivateExecutor) checkHostedTaskBindings(ctx context.Context, account, id string, owner state.HostedTaskSourceOwner, in api.HostedTaskBindingCheck, journal state.HostedTaskBindingStore) (state.HostedTaskBindingRecord, error) {
	release, err := e.reserve()
	if err != nil {
		return state.HostedTaskBindingRecord{}, state.ErrConflict
	}
	defer release()
	claim := uuid.NewString()
	change := state.HostedTaskBindingChange{Owner: owner, BindingSHA256: in.BindingSHA256, Claim: claim}
	row, err := journal.ChangeHostedTaskBindings(ctx, account, id, "claim", change)
	if err != nil || row.Claim != claim || row.Public.Status != "verifying" {
		return row, err
	}
	probeCtx, cancel := taskBindingProbeContext(ctx, e.ctx.Done())
	defer cancel()
	change.Checks = e.probeHostedTaskBindings(probeCtx, account, row)
	// A cancellation must not leave a claim eligible for retry. The native
	// journal rechecks all pins/authority and makes changed facts terminal.
	cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), api.HostedTaskBindingCleanupTimeout)
	defer done()
	return journal.ChangeHostedTaskBindings(cleanup, account, id, "record", change)
}
