package toolregistry

import (
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
)

// DelegatedInvocations uses exact native grants, never deployment authority.
// Delegated callers cannot access another key's receipts or the owner journal.
func DelegatedInvocations(store state.Store, audit GrantAudit) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		key, ok := registryKey(w, r, acct, store)
		if !ok {
			return
		}
		id := r.PathValue("id")
		if !toolinvocation.ValidID(id) {
			grantRespond(w, nil, state.ErrInvalidArgument)
			return
		}
		journal, ok := store.(state.ToolInvocationStore)
		if !ok {
			grantRespond(w, nil, errors.New("invocation store unavailable"))
			return
		}
		if r.Method == http.MethodPut {
			beginDelegatedInvocation(w, r, acct, store, journal, key, id, audit)
			return
		}
		record, err := delegatedInvocationRecord(r, acct, store, journal, key, id)
		if err != nil {
			grantRespond(w, nil, err)
			return
		}
		if r.Method == http.MethodGet {
			grantRespond(w, record, nil)
			return
		}
		changeDelegatedInvocation(w, r, acct, store, record, audit)
	}
}

func invocationGrant(r *http.Request, acct state.Account, store state.Store, key state.APIKey, grantID string, in api.ToolInvocationIntent) (api.ToolGrantRecord, api.ToolPublicationRecord, error) {
	var g api.ToolGrantRecord
	var p api.ToolPublicationRecord
	grants, ok := store.(state.ToolGrantStore)
	if !ok {
		return g, p, errors.New("grant store unavailable")
	}
	publications, ok := store.(state.ToolPublicationStore)
	if !ok {
		return g, p, errors.New("publication store unavailable")
	}
	if !toolgrant.ValidID(grantID) || (in.AccessMode != "open" && in.AccessMode != "external-oauth") {
		return g, p, state.ErrNotFound
	}
	g, err := grants.GetToolGrant(r.Context(), acct.ID, grantID)
	if err != nil {
		return g, p, err
	}
	if !toolgrant.CredentialMatches(g, in.AccessMode, in.CredentialBindingSHA256) || !g.Intent.AllowInvocation || g.Intent.KeyID != key.ID || g.Intent.Version != in.Version || g.Intent.Name != in.Name {
		return g, p, state.ErrNotFound
	}
	p, err = publications.GetToolPublication(r.Context(), acct.ID, in.Version)
	if err != nil {
		return g, p, err
	}
	_, allowed, err := accessibleGrant(r, acct, store, key, state.ToolGrantEntry{Grant: g, Publication: p})
	if err != nil {
		return g, p, err
	}
	if !allowed || p.Intent.AccessMode != in.AccessMode {
		return g, p, state.ErrNotFound
	}
	return g, p, nil
}

func delegatedInvocationRecord(r *http.Request, acct state.Account, store state.Store, journal state.ToolInvocationStore, key state.APIKey, id string) (api.ToolInvocationRecord, error) {
	record, err := journal.GetToolInvocation(r.Context(), acct.ID, id)
	if err != nil {
		return record, err
	}
	if record.Delegation == nil || record.Delegation.KeyID != key.ID {
		return api.ToolInvocationRecord{}, state.ErrNotFound
	}
	g, p, err := invocationGrant(r, acct, store, key, record.Delegation.GrantID, record.Intent)
	if err != nil {
		return api.ToolInvocationRecord{}, err
	}
	if g.ProjectID != record.Delegation.ProjectID || g.AppID != record.Target.AppID || g.App != record.Target.App || p.Outcome == nil || p.Outcome.DeploymentID != record.Target.DeploymentID {
		return api.ToolInvocationRecord{}, state.ErrNotFound
	}
	return record, nil
}

func beginDelegatedInvocation(w http.ResponseWriter, r *http.Request, acct state.Account, store state.Store, journal state.ToolInvocationStore, key state.APIKey, id string, audit GrantAudit) {
	var in api.DelegatedToolInvocationIntent
	if httpkit.DecodeJSONSized(r, &in, api.ToolInvocationRequestMaxBytes) != nil || !toolgrant.ValidID(in.GrantID) || toolinvocation.ValidateIntent(in.Intent) != nil || in.Intent.AccessMode != "open" {
		grantRespond(w, nil, state.ErrInvalidArgument)
		return
	}
	if old, err := journal.GetToolInvocation(r.Context(), acct.ID, id); err == nil {
		old, err = delegatedInvocationRecord(r, acct, store, journal, key, id)
		if err != nil {
			grantRespond(w, nil, err)
			return
		}
		if !reflect.DeepEqual(old.Intent, in.Intent) || old.Delegation.GrantID != in.GrantID {
			grantRespond(w, nil, state.ErrConflict)
			return
		}
		grantRespond(w, api.ToolInvocationReceipt{Record: old}, nil)
		return
	} else if !errors.Is(err, state.ErrNotFound) {
		grantRespond(w, nil, err)
		return
	}
	g, p, err := invocationGrant(r, acct, store, key, in.GrantID, in.Intent)
	if err != nil {
		grantRespond(w, nil, err)
		return
	}
	target, err := invocationTarget(r, acct, store, p, in.Intent)
	if err != nil {
		grantRespond(w, nil, err)
		return
	}
	d := api.ToolInvocationDelegation{GrantID: g.ID, KeyID: key.ID, ProjectID: g.ProjectID}
	delegated, ok := store.(state.DelegatedToolInvocationStore)
	if !ok {
		grantRespond(w, nil, errors.New("delegated journal unavailable"))
		return
	}
	out, err := delegated.BeginDelegatedToolInvocation(r.Context(), acct.ID, id, in.Intent, target, d)
	if err == nil && out.Created {
		auditDelegated(r, acct, "tool_invocation.delegated.started", out.Record, audit)
	}
	respond(w, out, err)
}

func changeDelegatedInvocation(w http.ResponseWriter, r *http.Request, acct state.Account, store state.Store, record api.ToolInvocationRecord, audit GrantAudit) {
	action := r.PathValue("action")
	if record.Intent.AccessMode != "open" && action != "cancel" {
		grantRespond(w, nil, state.ErrNotFound)
		return
	}
	if action == "dispatch" && record.Status == "queued" {
		if err := availableInvocationTarget(r, acct, store, record.Target); err != nil {
			respond(w, nil, err)
			return
		}
	}
	var outcome *api.ToolInvocationOutcome
	if action == "outcome" {
		var ok bool
		outcome, ok = readInvocationOutcome(w, r)
		if !ok {
			return
		}
		action = "finish"
	} else if action != "dispatch" && action != "cancel" {
		grantRespond(w, nil, state.ErrInvalidArgument)
		return
	}
	journal, ok := store.(state.DelegatedToolInvocationStore)
	if !ok {
		grantRespond(w, nil, errors.New("delegated journal unavailable"))
		return
	}
	out, err := journal.ChangeDelegatedToolInvocation(r.Context(), acct.ID, record.ID, *record.Delegation, action, outcome)
	if err == nil && (action != "dispatch" || out.DispatchGranted) {
		auditDelegated(r, acct, "tool_invocation.delegated."+action, out.Record, audit)
	}
	respond(w, out, err)
}

func auditDelegated(r *http.Request, acct state.Account, kind string, record api.ToolInvocationRecord, audit GrantAudit) {
	if audit != nil {
		audit(r.Context(), kind, acct.ID, map[string]any{"invocation_id": record.ID, "grant_id": record.Delegation.GrantID, "key_id": record.Delegation.KeyID, "app_id": record.Target.AppID, "version": record.Intent.Version, "tool": record.Intent.Name, "arguments_sha256": record.Intent.ArgumentsSHA256, "status": record.Status})
	}
}

func readInvocationOutcome(w http.ResponseWriter, r *http.Request) (*api.ToolInvocationOutcome, bool) {
	out := &api.ToolInvocationOutcome{}
	if httpkit.DecodeJSONSized(r, out, api.ToolInvocationOutcomeMaxBytes) != nil || toolinvocation.ValidateOutcome(*out) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid invocation result or outcome"))
		return nil, false
	}
	if len(out.Result) == 0 {
		return out, true
	}
	safe, err := toolinvocation.Result(out.Result, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid complete MCP result"))
		return nil, false
	}
	safe.Redacted = safe.Redacted || out.Redacted
	return &safe, true
}
