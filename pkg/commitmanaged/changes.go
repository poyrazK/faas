package commitmanaged

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// acceptChange enqueues a changes source's captured row change into its
// app's named queue, under the same admission as a queue send
// (ADR-20261112140000 §3). A missing binding or a full queue is retried: the
// change waits in the customer's outbox.
func (a *acceptor) acceptChange(ctx context.Context, e commitwork.Event) (commitwork.Receipt, error) {
	if e.Routing != nil {
		return commitwork.Receipt{}, &commitwork.PermanentError{Code: "invalid_routing"}
	}
	app, err := a.store.AppByID(ctx, a.source.AppID)
	if errors.Is(err, state.ErrNotFound) {
		return commitwork.Receipt{}, &commitwork.PermanentError{Code: "destination_unqualified"}
	}
	if err != nil {
		return commitwork.Receipt{}, err
	}
	if app.AccountID != a.source.AccountID || app.PlatformTenantRequired {
		return commitwork.Receipt{}, &commitwork.PermanentError{Code: "destination_unqualified"}
	}
	account, err := a.store.AccountByID(ctx, a.source.AccountID)
	if err != nil {
		return commitwork.Receipt{}, err
	}
	limits := api.MustLimitsFor(account.Plan)
	if limits.MaxQueueDepth == 0 {
		// The plan has no queues (Free): nothing can deliver this change.
		return commitwork.Receipt{}, &commitwork.PermanentError{Code: "destination_unqualified"}
	}
	envelope, err := (events.Envelope{ID: e.ID, Source: "gregale.commit." + a.source.ID, Type: e.Type, Data: e.Data}).Normalize(account.ID, time.Now().UTC())
	if err != nil {
		return commitwork.Receipt{}, &commitwork.PermanentError{Code: "invalid_event"}
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return commitwork.Receipt{}, err
	}
	if len(payload) > limits.MaxSourceBytesPerInvocation {
		return commitwork.Receipt{}, &commitwork.PermanentError{Code: "payload_too_large"}
	}
	policy := app.RetryPolicy()
	policy.MaxAttempts = api.EffectiveRetryMaxAttempts(policy.MaxAttempts, limits.MaxQueueAttempts)
	retryPolicy, err := json.Marshal(policy)
	if err != nil {
		return commitwork.Receipt{}, err
	}
	inv := state.Invocation{
		AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue,
		QueueName:       a.source.Changes.QueueName,
		DeploymentScope: state.DefaultInvocationDeploymentScope(app),
		Payload:         payload, DueAt: time.Now().UTC(), RetryPolicyJSON: retryPolicy,
		AdmissionMaxPending:      limits.MaxQueueDepth,
		AdmissionMaxBacklogBytes: limits.MaxQueueBacklogBytesPerAccount,
	}
	inv, _, err = state.ResolveInvocationVersion(ctx, a.store, inv)
	if err != nil {
		return commitwork.Receipt{}, err
	}
	r, err := a.store.AcceptCommitChange(ctx, account.ID, a.source.ID, e.ID, e.Type, e.Data, inv)
	return convert(r, err)
}
