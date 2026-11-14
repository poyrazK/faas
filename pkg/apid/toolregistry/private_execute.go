package toolregistry

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type PrivateExecutionOptions struct {
	Enabled              bool
	Identities           func(context.Context) []*age.X25519Identity
	ConnectionIdentities func(context.Context) []*age.X25519Identity
	ConnectionRecipients func() ([]*age.X25519Recipient, error)
	Endpoint             func(context.Context, state.App, state.Deployment) (string, error)
	VerifyPin            func(context.Context, api.ToolInvocationTarget, string) error
	HTTP                 *http.Client
	Audit                GrantAudit
}

// PrivateExecutor owns only bounded protocol workers. Secret values remain in
// this API process and are never returned by a public credential endpoint.
type PrivateExecutor struct {
	store   state.Store
	opts    PrivateExecutionOptions
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closing bool
	active  int
	workers sync.WaitGroup
}

func NewPrivateExecutor(store state.Store, opts PrivateExecutionOptions) *PrivateExecutor {
	ctx, cancel := context.WithCancel(context.Background())
	return &PrivateExecutor{store: store, opts: opts, ctx: ctx, cancel: cancel}
}

func (e *PrivateExecutor) Close() {
	e.mu.Lock()
	e.closing = true
	e.cancel()
	e.mu.Unlock()
	e.workers.Wait()
	if e.opts.HTTP != nil {
		e.opts.HTTP.CloseIdleConnections()
	}
}

func (e *PrivateExecutor) reserve() (func(), error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closing || e.active >= api.ToolPrivateInvocationWorkers {
		return nil, errors.New("private workers unavailable")
	}
	e.active++
	e.workers.Add(1)
	return func() { e.mu.Lock(); e.active--; e.mu.Unlock(); e.workers.Done() }, nil
}

func (e *PrivateExecutor) Handler() httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		w.Header().Set("Cache-Control", "no-store")
		if !e.opts.Enabled || e.opts.Identities == nil || e.opts.Endpoint == nil || e.opts.VerifyPin == nil {
			api.WriteProblem(w, api.NewProblem(503, "private_invocation_disabled", "Private invocation unavailable", "the operator must enable credential-bound invocation"))
			return
		}
		key, ok := registryKey(w, r, acct, e.store)
		if !ok {
			return
		}
		var in api.PrivateToolInvocationRequest
		id := r.PathValue("id")
		if !toolinvocation.ValidID(id) || httpkit.DecodeJSONSized(r, &in, api.ToolPrivateInvocationRequestMaxBytes) != nil || in.Intent.AccessMode != "external-oauth" || toolinvocation.ValidateIntent(in.Intent) != nil {
			grantRespond(w, nil, state.ErrInvalidArgument)
			return
		}
		args, err := toolinvocation.CanonicalArguments([]byte(in.ArgumentsJSON))
		if err != nil || toolpublication.Digest(args) != in.Intent.ArgumentsSHA256 {
			grantRespond(w, nil, state.ErrInvalidArgument)
			return
		}
		out, err := e.begin(r, acct, key, id, in, args)
		if errors.Is(err, state.ErrToolOAuthBusy) || errors.Is(err, state.ErrToolOAuthReconnect) {
			connectionRespond(w, nil, err)
			return
		}
		var limit *state.ToolInvocationLimitError
		if errors.As(err, &limit) {
			respond(w, nil, err)
			return
		}
		grantRespond(w, out, err)
	}
}

func (e *PrivateExecutor) begin(r *http.Request, acct state.Account, key state.APIKey, id string, in api.PrivateToolInvocationRequest, args []byte) (api.ToolInvocationReceipt, error) {
	journal, ok := e.store.(state.ToolInvocationStore)
	if !ok {
		return api.ToolInvocationReceipt{}, errors.New("invocation store unavailable")
	}
	if old, err := journal.GetToolInvocation(r.Context(), acct.ID, id); err == nil {
		old, err = delegatedInvocationRecord(r, acct, e.store, journal, key, id)
		if err != nil {
			return api.ToolInvocationReceipt{}, err
		}
		if !reflect.DeepEqual(old.Intent, in.Intent) || old.Delegation.GrantID != in.GrantID {
			return api.ToolInvocationReceipt{}, state.ErrConflict
		}
		return api.ToolInvocationReceipt{Record: old}, nil
	} else if !errors.Is(err, state.ErrNotFound) {
		return api.ToolInvocationReceipt{}, err
	}
	g, p, err := invocationGrant(r, acct, e.store, key, in.GrantID, in.Intent)
	if err != nil {
		return api.ToolInvocationReceipt{}, err
	}
	target, err := invocationTarget(r, acct, e.store, p, in.Intent)
	if err != nil {
		return api.ToolInvocationReceipt{}, err
	}
	release, err := e.reserve()
	if err != nil {
		return api.ToolInvocationReceipt{}, err
	}
	token, err := e.credential(r.Context(), g)
	if err != nil {
		release()
		return api.ToolInvocationReceipt{}, err
	}
	redactions, err := e.connectionRedactions(r.Context(), g)
	if err != nil {
		release()
		return api.ToolInvocationReceipt{}, err
	}
	delegated, ok := e.store.(state.DelegatedToolInvocationStore)
	if !ok {
		release()
		return api.ToolInvocationReceipt{}, errors.New("delegated store unavailable")
	}
	binding := api.ToolInvocationDelegation{GrantID: g.ID, KeyID: key.ID, ProjectID: g.ProjectID}
	out, err := delegated.BeginDelegatedToolInvocation(r.Context(), acct.ID, id, in.Intent, target, binding)
	if err != nil || !out.Created {
		release()
		return out, err
	}
	auditDelegated(r, acct, "tool_invocation.private.started", out.Record, e.opts.Audit)
	e.launch(r, acct, key, out.Record, g, token, args, redactions, release) //nolint:contextcheck // Accepted calls use the API-owned bounded context and survive request cancellation.
	return out, nil
}

func (e *PrivateExecutor) credential(ctx context.Context, g api.ToolGrantRecord) (string, error) {
	if g.Credential != nil && g.Credential.ConnectionID != "" {
		return e.connectionToken(ctx, g)
	}
	secret, err := credentialSource(ctx, e.store, g)
	if err != nil {
		return "", err
	}
	values, err := secretbox.OpenMulti(e.opts.Identities(ctx), secret.Ciphertext)
	key := strings.TrimPrefix(g.Intent.CredentialSecretRef, api.SecretRefPrefix)
	token := values[key]
	if err != nil || !api.ValidMCPProbeToken(token) || strings.HasPrefix(token, "fp_") || strings.HasPrefix(token, "gmcp_") {
		return "", state.ErrNotFound
	}
	return token, nil
}

func (e *PrivateExecutor) launch(r *http.Request, acct state.Account, key state.APIKey, record api.ToolInvocationRecord, g api.ToolGrantRecord, token string, args []byte, redactions []string, release func()) {
	deadline := record.CreatedAt.Add(time.Duration(record.Intent.TimeoutSeconds) * time.Second)
	if g.Intent.ExpiresAt.Before(deadline) {
		deadline = g.Intent.ExpiresAt
	}
	ctx, cancel := context.WithDeadline(e.ctx, deadline)
	native := &privateNative{executor: e, request: r, account: acct, keyID: key.ID, record: record}
	go func() {
		defer release()
		defer cancel()
		_, _ = mcphosting.RunRegisteredInvocation(ctx, mcphosting.RegisteredInvocationOptions{Native: native, ID: record.ID, Arguments: args, Delegated: true, ManagedCredentialBinding: record.Intent.CredentialBindingSHA256, Token: token, SensitiveValues: redactions, VerifyPin: e.opts.VerifyPin, HTTP: e.opts.HTTP})
	}()
}
