package toolregistry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolsource/sourcecheck"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func (e *PrivateExecutor) HostedTaskSourceIntent() httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		owner := strings.HasSuffix(r.URL.Path, "/decision")
		p, _ := httpkit.PrincipalFrom(r)
		r, err := e.freshPublicationRequest(r, acct, owner || p.Key == nil)
		if err != nil {
			respond(w, nil, err)
			return
		}
		if owner && !connectionOwnerAccess(w, r, acct, e.store) || !owner && !personalAccess(w, r, acct, e.store, true) {
			return
		}
		journal, ok := e.store.(state.HostedTaskSourceIntentStore)
		if !ok || !toolpublication.ValidID(r.PathValue("id")) {
			respond(w, nil, state.ErrInvalidArgument)
			return
		}
		p, _ = httpkit.PrincipalFrom(r)
		if r.Method == http.MethodPut && !owner {
			e.prepareHostedTaskSource(w, r, acct, p, journal)
			return
		}
		e.inspectHostedTaskSource(w, r, acct, p, journal, owner)
	}
}
func (e *PrivateExecutor) inspectHostedTaskSource(w http.ResponseWriter, r *http.Request, acct state.Account, p httpkit.Principal, journal state.HostedTaskSourceIntentStore, owner bool) {
	row, err := journal.GetHostedTaskSourceIntent(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	if !owner && p.Key != nil && p.Key.ID != row.Record.RequesterKeyID && (agentRegistryRequest(r) || !httpkit.APIKeyCarriesScope(*p.Key, api.ScopeAdmin)) {
		err = state.ErrNotFound
		respond(w, nil, err)
		return
	}
	if r.Method != http.MethodGet {
		e.changeHostedTaskSource(w, r, acct, p, journal, owner)
		return
	}
	respond(w, row.Record, err)
}

func (e *PrivateExecutor) changeHostedTaskSource(w http.ResponseWriter, r *http.Request, acct state.Account, p httpkit.Principal, journal state.HostedTaskSourceIntentStore, owner bool) {
	var in api.HostedTaskSourceDecision
	var err error
	action := "check"
	actor := state.HostedTaskSourceOwner{}
	if owner {
		action = "decide"
		err = httpkit.DecodeJSONSized(r, &in, api.TaskToolReleaseIntentMaxBytes)
		actor.Session = p.Key == nil
		if p.Key != nil {
			actor.KeyID = p.Key.ID
		}
	} else {
		var check api.HostedTaskSourceCheck
		err = httpkit.DecodeJSONSized(r, &check, api.TaskToolReleaseIntentMaxBytes)
		in.ReviewSHA256 = check.ReviewSHA256
	}
	if err != nil || !toolpublication.ValidID(in.ReviewSHA256) || owner && (!toolpublication.ValidID(in.IntentSHA256) || in.Decision != "approve" && in.Decision != "reject" && in.Decision != "revoke") {
		respond(w, nil, state.ErrInvalidArgument)
		return
	}
	if owner && in.Decision == "approve" && !e.opts.SourceRetentionEnabled {
		api.WriteProblem(w, api.NewProblem(503, "task_source_retention_disabled", "Task source approval unavailable", "enable native source retention before approving its retained review"))
		return
	}
	out, err := journal.ChangeHostedTaskSourceIntent(r.Context(), acct.ID, r.PathValue("id"), action, in, actor)
	if err == nil && owner && e.opts.Audit != nil {
		e.opts.Audit(r.Context(), "task_tool_source.intent."+in.Decision, acct.ID, map[string]any{"intent_id": out.Record.ID, "intent_sha256": out.Record.IntentSHA256, "review_status": out.Record.ReviewStatus})
	}
	respond(w, out.Record, err)
}

func (e *PrivateExecutor) prepareHostedTaskSource(w http.ResponseWriter, r *http.Request, acct state.Account, p httpkit.Principal, journal state.HostedTaskSourceIntentStore) {
	if !e.opts.SourceRetentionEnabled || e.opts.ConnectionRecipients == nil || p.Key == nil {
		api.WriteProblem(w, api.NewProblem(503, "task_source_retention_disabled", "Task source retention unavailable", "enable native source retention and an independent Account seal identity"))
		return
	}
	var in api.HostedTaskSourcePrepare
	if httpkit.DecodeJSONSized(r, &in, api.HostedTaskSourceRequestMaxBytes) != nil {
		respond(w, nil, state.ErrInvalidArgument)
		return
	}
	review, err := sourcecheck.ValidatePrepare(acct.ID, in)
	if err != nil {
		respond(w, nil, state.ErrInvalidArgument)
		return
	}
	recipients, err := e.opts.ConnectionRecipients()
	if err != nil || len(recipients) == 0 {
		api.WriteProblem(w, api.NewProblem(503, "task_source_retention_unavailable", "Task source retention unavailable", "Account-domain sealing is required"))
		return
	}
	body, _ := json.Marshal(in.Sources)
	namespace := "gregale-hosted-task-source:" + acct.ID + ":" + p.Key.ID + ":" + r.PathValue("id") + ":" + review.Fingerprint
	sealed, err := secretbox.SealBytesTo(recipients, namespace, body, api.HostedTaskSourceCiphertextMaxBytes)
	if err != nil || len(sealed) > api.HostedTaskSourceCiphertextMaxBytes {
		respond(w, nil, state.ErrInvalidArgument)
		return
	}
	row, err := journal.PrepareHostedTaskSourceIntent(r.Context(), acct.ID, p.Key.ID, r.PathValue("id"), review, sealed)
	var quota *state.HostedTaskSourceLimitError
	if errors.As(err, &quota) {
		api.WriteProblem(w, api.NewProblem(429, "task_source_retention_limit", "Task source retention is full", "source retention quota exceeded: "+quota.Quota+"; expiry releases only active capacity").WithLimit(quota.Limit, quota.Observed).WithDocs("https://gregale.dev/docs/mcp#durable-hosted-task-source-intents"))
		return
	}
	respond(w, row.Record, err)
}

// StartSourceRetentionCleanup is called by apid's service lifecycle, including
// when new retention is disabled. Tests construct handlers without background work.
func (e *PrivateExecutor) StartSourceRetentionCleanup(ctx context.Context) {
	e.mu.Lock()
	if e.closing || e.sourceCleanupStarted {
		e.mu.Unlock()
		return
	}
	e.sourceCleanupStarted = true
	cleanupCtx, cancel := context.WithCancel(ctx)
	e.sourceCleanupCancel = cancel
	e.workers.Add(1)
	e.mu.Unlock()
	go func() { defer e.workers.Done(); defer cancel(); e.sourceRetentionLoop(cleanupCtx) }()
}
func (e *PrivateExecutor) sourceRetentionLoop(ctx context.Context) {
	journal, ok := e.store.(state.HostedTaskSourceIntentStore)
	if !ok {
		return
	}
	tick := time.NewTicker(api.HostedTaskSourceSweepInterval)
	defer tick.Stop()
	for {
		sweepCtx, cancel := context.WithTimeout(ctx, api.HostedTaskSourceCleanupTimeout)
		_, _ = journal.PurgeExpiredHostedTaskSources(sweepCtx)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
