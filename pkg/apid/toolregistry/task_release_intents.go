package toolregistry

import (
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// TaskReleaseIntent records review and owner decisions without executing a
// release. It does not require or activate the private execution worker pool.
func (e *PrivateExecutor) TaskReleaseIntent() httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		owner := strings.HasSuffix(r.URL.Path, "/decision")
		p, _ := httpkit.PrincipalFrom(r)
		var err error
		r, err = e.freshPublicationRequest(r, acct, owner || p.Key == nil)
		if err != nil {
			respond(w, nil, err)
			return
		}
		if owner && !connectionOwnerAccess(w, r, acct, e.store) || !owner && !personalAccess(w, r, acct, e.store, true) {
			return
		}
		journal, ok := e.store.(state.TaskToolReleaseIntentStore)
		if !ok || !toolpublication.ValidID(r.PathValue("id")) {
			respond(w, nil, state.ErrInvalidArgument)
			return
		}
		p, _ = httpkit.PrincipalFrom(r)
		if r.Method == http.MethodPut && !owner {
			e.prepareTaskReleaseIntent(w, r, acct, p, journal)
			return
		}
		e.inspectTaskReleaseIntent(w, r, acct, p, journal, owner)
	}
}

func (e *PrivateExecutor) prepareTaskReleaseIntent(w http.ResponseWriter, r *http.Request, acct state.Account, p httpkit.Principal, journal state.TaskToolReleaseIntentStore) {
	var in api.TaskToolReleaseIntent
	if p.Key == nil || httpkit.DecodeJSONSized(r, &in, api.TaskToolReleaseIntentMaxBytes) != nil || tasktoolrelease.Validate(in) != nil {
		respond(w, nil, state.ErrInvalidArgument)
		return
	}
	out, err := journal.PrepareTaskToolReleaseIntent(r.Context(), acct.ID, p.Key.ID, r.PathValue("id"), in)
	taskReleaseRespond(w, out, err)
}

func (e *PrivateExecutor) inspectTaskReleaseIntent(w http.ResponseWriter, r *http.Request, acct state.Account, p httpkit.Principal, journal state.TaskToolReleaseIntentStore, owner bool) {
	record, err := journal.GetTaskToolReleaseIntent(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	if !owner && p.Key != nil && p.Key.ID != record.RequesterKeyID && (agentRegistryRequest(r) || !httpkit.APIKeyCarriesScope(*p.Key, api.ScopeAdmin)) {
		respond(w, nil, state.ErrNotFound)
		return
	}
	if r.Method == http.MethodGet {
		respond(w, record, nil)
		return
	}
	in, action, err := decodeTaskReleaseChange(r, p, record, owner)
	if err != nil {
		respond(w, nil, err)
		return
	}
	out, err := journal.ChangeTaskToolReleaseIntent(r.Context(), acct.ID, record.ID, action, in)
	if err == nil && owner && e.opts.Audit != nil {
		e.opts.Audit(r.Context(), "task_tool_release.intent."+in.Decision, acct.ID, map[string]any{"intent_id": record.ID, "intent_sha256": record.IntentSHA256, "status": out.Status})
	}
	taskReleaseRespond(w, out, err)
}

func decodeTaskReleaseChange(r *http.Request, p httpkit.Principal, record api.TaskToolReleaseIntentRecord, owner bool) (api.TaskToolReleaseIntentDecision, string, error) {
	var in api.TaskToolReleaseIntentDecision
	var err error
	action := "decide"
	if owner {
		err = httpkit.DecodeJSONSized(r, &in, api.TaskToolReleaseIntentMaxBytes)
		if in.Decision == "authorize" || tasktoolrelease.IsRecoveryDecision(in.Decision) {
			if p.Key == nil {
				return in, action, state.ErrInvalidArgument
			}
			in.AuthorizerKeyID = p.Key.ID
		}
	} else {
		var check api.TaskToolReleaseIntentCheck
		err = httpkit.DecodeJSONSized(r, &check, api.TaskToolReleaseIntentMaxBytes)
		in.ReleaseFingerprint, action = check.ReleaseFingerprint, "check"
		if check.Execution != nil {
			if p.Key == nil || p.Key.ID != record.RequesterKeyID {
				return in, action, state.ErrNotFound
			}
			in.Execution, action = check.Execution, "execution"
		}
	}
	if err != nil || !toolpublication.ValidID(in.ReleaseFingerprint) {
		return in, action, state.ErrInvalidArgument
	}
	return in, action, nil
}

func taskReleaseRespond(w http.ResponseWriter, out any, err error) {
	if errors.Is(err, state.ErrTaskToolReleaseLimit) {
		api.WriteProblem(w, api.NewProblem(429, "task_tool_release_limit", "Task review journal is full", "retained review identities cannot be reused").
			WithLimit(api.TaskToolReleaseIntentsPerAccount, api.TaskToolReleaseIntentsPerAccount+1).WithDocs("https://gregale.dev/docs/mcp#review-a-durable-task-tool-stack"))
		return
	}
	respond(w, out, err)
}
