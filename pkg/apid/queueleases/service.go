// Package queueleases is apid's pull-lease surface for worker and job queue
// consumers (ADR-1140): lease, ack, nack and extend by receipt. apidserver
// owns the routes and the credential dispatch (API key or workload token);
// everything this package needs from apid goes through Host.
package queueleases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

const errorMaxBytes = 1024

// leaseRecheck bounds how long a waiting lease relies on notifications alone:
// a delayed message that becomes due fires none, and the notify hub drops
// wake-ups for a slow subscriber.
const leaseRecheck = 2 * time.Second

// Host is what the lease surface needs from apid.
type Host interface {
	Store() state.Store
	// WorkloadVerifier is nil when workload trust is not configured.
	WorkloadVerifier() *workloadidentity.Verifier
	// Subscribe streams Postgres notifications; a waiting lease listens for
	// invocation_due.
	Subscribe(ctx context.Context, channels []string) (<-chan db.Notification, func(), error)
}

// Service is the pull-lease surface.
type Service struct {
	Host Host
	// Waiters bounds lease long-polls per account and per apid; nil leaves
	// them unbounded (tests).
	Waiters *WaiterSlots
}

// WaiterSlots counts lease long-polls in flight. Each waiter reruns a lease
// transaction that locks its binding on every wake and recheck, so an
// unbounded number of idle consumers made idle database work grow with them
// (queue review 2026-11-12, ADR-20261112200000). A call over the cap is not
// refused: it leases once without waiting, which the SDK consumers already
// treat as an empty poll.
type WaiterSlots struct {
	mu         sync.Mutex
	perAccount map[string]int
	total      int
}

func (w *WaiterSlots) acquire(accountID string) (func(), bool) {
	if w == nil {
		return func() {}, true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.perAccount[accountID] >= api.QueueLeaseWaitersPerAccount || w.total >= api.QueueLeaseWaitersPerAPID {
		return nil, false
	}
	if w.perAccount == nil {
		w.perAccount = map[string]int{}
	}
	w.perAccount[accountID]++
	w.total++
	var once sync.Once
	return func() {
		once.Do(func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.total--
			if w.perAccount[accountID]--; w.perAccount[accountID] <= 0 {
				delete(w.perAccount, accountID)
			}
		})
	}, true
}

// Handler is a lease verb bound to an authenticated account and app.
// instanceID is set when the caller presented a workload-identity token.
type Handler func(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, instanceID string)

// WorkloadBearer reports a compact JWS bearer; API keys never have that shape.
func WorkloadBearer(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && strings.HasPrefix(token, "eyJ") && strings.Count(token, ".") == 2
}

// WorkloadScope binds a gregale:queues token to a live instance of the app
// named in the path, so a token minted for one app cannot lease another's
// queue. Leases serve the production binding only, so the instance must run
// a production deployment: a staging or preview deployment of the same app
// must not lease, settle or extend production messages (ADR-1141 amendment
// 2026-10-27).
func (s Service) WorkloadScope(r *http.Request) (state.Account, state.App, string, error) {
	return s.WorkloadScopeFor(r, workloadidentity.QueuesAudience, true)
}

// workloadPathNamesApp reports whether the path's slug addresses the token's
// app: its current slug, or a slug it had before a rename that no other app
// holds now. Workers name their app by the slug they were configured with, so
// requiring the current slug broke every running pull consumer on a rename
// until its code changed and it was redeployed (ADR-20261113180000, R8-1).
// The token, not the slug, carries the authority.
func workloadPathNamesApp(r *http.Request, store state.Store, app state.App) bool {
	slug := r.PathValue("slug")
	if slug == app.Slug {
		return true
	}
	other, err := store.AppBySlug(r.Context(), slug)
	return errors.Is(err, state.ErrNotFound) || err == nil && other.ID == app.ID
}

// WorkloadScopeFor binds a token for audience to a live instance of the app
// named in the path. productionOnly also refuses an instance that runs a
// stage deployment, or a token that names one.
func (s Service) WorkloadScopeFor(r *http.Request, audience string, productionOnly bool) (state.Account, state.App, string, error) {
	verifier := s.Host.WorkloadVerifier()
	if verifier == nil {
		return state.Account{}, state.App{}, "", errors.New("workload trust is not configured")
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	c, err := verifier.Verify(token, audience, time.Now())
	if err != nil {
		return state.Account{}, state.App{}, "", err
	}
	store := s.Host.Store()
	app, err := store.AppByID(r.Context(), c.AppID)
	if err != nil || app.AccountID != c.AccountID || app.Status == state.AppDeleted || !workloadPathNamesApp(r, store, app) {
		return state.Account{}, state.App{}, "", errors.New("token does not name this app")
	}
	acct, err := state.AppResourceAccount(r.Context(), store, app)
	if err != nil || !acct.Active() {
		return state.Account{}, state.App{}, "", errors.New("account is not active")
	}
	if organization := r.PathValue("organization"); organization != "" {
		org, err := store.OrgByID(r.Context(), app.OrgID)
		if err != nil || org.Personal || org.Slug != organization {
			return state.Account{}, state.App{}, "", errors.New("token does not name this organization")
		}
	}
	instance, err := store.InstanceByID(r.Context(), c.InstanceID)
	if err != nil || instance.AppID != app.ID || !state.State(instance.State).CountsForConcurrency() {
		return state.Account{}, state.App{}, "", errors.New("instance is not live")
	}
	if !productionOnly {
		return acct, app, instance.ID, nil
	}
	if !state.ProductionDeploymentScope(c.Environment) {
		return state.Account{}, state.App{}, "", errors.New("token names a stage environment")
	}
	deployment, err := store.DeploymentByID(r.Context(), instance.DeploymentID)
	if err != nil || deployment.AppID != app.ID || !state.ProductionDeploymentScope(deployment.Scope) {
		return state.Account{}, state.App{}, "", errors.New("instance does not run a production deployment")
	}
	return acct, app, instance.ID, nil
}

// WriteWorkloadUnauthorized is the response for a rejected workload token.
func WriteWorkloadUnauthorized(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required",
		"a live workload token for gregale:queues from an instance of this app is required"))
}

func (s Service) leaseStore(w http.ResponseWriter, acct state.Account) (state.QueueLeaseStore, bool) {
	if api.MustLimitsFor(acct.Plan).MaxQueueDepth == 0 {
		api.WriteProblem(w, api.ErrPlanFeatureGated("queues", acct.Plan))
		return nil, false
	}
	store, ok := s.Host.Store().(state.QueueLeaseStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("queue leases unavailable"))
		return nil, false
	}
	return store, true
}

// Lease is POST /v1/apps/{slug}/queues/{queue}/leases.
func (s Service) Lease(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, instanceID string) {
	store, ok := s.leaseStore(w, acct)
	if !ok {
		return
	}
	var req api.QueueLeaseRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.WaitSeconds < 0 || req.WaitSeconds > api.QueueLeaseMaxWaitSeconds {
		writeError(w, acct, r.PathValue("queue"), state.ErrInvalidArgument)
		return
	}
	leases, err := s.lease(r.Context(), store, state.QueueLeaseRequest{AccountID: acct.ID, AppID: app.ID, QueueName: r.PathValue("queue"),
		MaxMessages: req.MaxMessages, VisibilitySeconds: req.VisibilityTimeoutSeconds, InstanceID: instanceID},
		time.Duration(req.WaitSeconds)*time.Second)
	if errors.Is(err, state.ErrOrgAccessRevoked) {
		writeError(w, acct, r.PathValue("queue"), err)
		return
	}
	if r.Context().Err() != nil {
		// The consumer went away before it could receive these leases.
		// Waiting out the visibility timeout spent an attempt it never had
		// (ADR-20261112200000); return them to pending instead.
		if len(leases) > 0 {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
			defer cancel()
			_ = store.ReleaseQueueLeases(releaseCtx, app.ID, leases)
		}
		return
	}
	if err != nil {
		writeError(w, acct, r.PathValue("queue"), err)
		return
	}
	resp := api.QueueLeaseResponse{Messages: make([]api.QueueLeaseMessage, 0, len(leases))}
	for _, lease := range leases {
		resp.Messages = append(resp.Messages, message(lease))
	}
	httpkit.WriteJSON(w, http.StatusOK, resp)
}

// lease leases ready messages, and when there are none waits up to wait for
// one: an invocation_due notification for the app's queue wakes it at once,
// and leaseRecheck catches delayed messages and dropped notifications.
func (s Service) lease(ctx context.Context, store state.QueueLeaseStore, req state.QueueLeaseRequest, wait time.Duration) ([]state.QueueLease, error) {
	if wait <= 0 {
		return store.LeaseQueueMessages(ctx, req)
	}
	release, ok := s.Waiters.acquire(req.AccountID)
	if !ok {
		// Over the waiter cap the call leases once and, finding nothing,
		// holds the request for its wait without touching the database.
		// Returning at once made the SDK consumers, which poll again on an
		// empty answer, spin lease transactions back to back
		// (ADR-20261113180000, R5-1).
		leases, err := store.LeaseQueueMessages(ctx, req)
		if err != nil || len(leases) > 0 {
			return leases, err
		}
		idle := time.NewTimer(wait)
		defer idle.Stop()
		select {
		case <-ctx.Done():
		case <-idle.C:
		}
		return leases, nil
	}
	defer release()
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	// Subscribe before the first lease so a message sent in between wakes us.
	wakes, unsubscribe, err := s.Host.Subscribe(waitCtx, []string{db.NotifyInvocationDue})
	if err != nil {
		wakes = nil // poll on the recheck timer alone
	} else {
		defer unsubscribe()
	}
	recheck := time.NewTicker(leaseRecheck)
	defer recheck.Stop()
	for {
		leases, err := store.LeaseQueueMessages(ctx, req)
		if err != nil || len(leases) > 0 {
			return leases, err
		}
		if !waitForQueueWake(waitCtx, &wakes, recheck.C, req.AppID, req.QueueName) {
			return leases, nil
		}
	}
}

// waitForQueueWake returns true when the lease should try again, false when
// the wait is over. A closed notification stream leaves the recheck timer.
func waitForQueueWake(ctx context.Context, wakes *<-chan db.Notification, recheck <-chan time.Time, appID, queueName string) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-recheck:
			return true
		case n, ok := <-*wakes:
			if !ok {
				*wakes = nil
				continue
			}
			if queueMessageDue(n.Payload, appID, queueName) {
				coalesceQueueWakes(ctx, *wakes)
				return true
			}
		}
	}
}

// leaseWakeCoalesce is how long a woken waiter lets a burst of sends land
// before it leases: a batch send fires one notification per message, and
// every one used to cost each waiting consumer a lease transaction.
const leaseWakeCoalesce = 25 * time.Millisecond

// coalesceQueueWakes waits leaseWakeCoalesce, discarding the notifications
// that arrive meanwhile; the lease that follows sees all of their messages.
func coalesceQueueWakes(ctx context.Context, wakes <-chan db.Notification) {
	timer := time.NewTimer(leaseWakeCoalesce)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			return
		case _, ok := <-wakes:
			if !ok {
				return
			}
		}
	}
}

// queueMessageDue matches an invocation_due payload for a queue message of
// appID. The payload is only a hint; the next lease re-checks everything. A
// payload that names a queue matches only queueName (ADR-20261113180000,
// R5-10): every message of the app used to wake every waiter of the app. A
// payload without the key, from a database before that migration, matches
// any queue. Messages sent under a binding's earlier name are found by the
// recheck.
func queueMessageDue(payload, appID, queueName string) bool {
	var p struct {
		AppID     string  `json:"app_id"`
		Source    string  `json:"source"`
		QueueName *string `json:"queue_name"`
	}
	if json.Unmarshal([]byte(payload), &p) != nil || p.AppID != appID || p.Source != string(state.InvocationQueue) {
		return false
	}
	return p.QueueName == nil || *p.QueueName == queueName
}

func message(lease state.QueueLease) api.QueueLeaseMessage {
	inv := lease.Invocation
	var headers map[string]string
	_ = json.Unmarshal(inv.Headers, &headers)
	msg := api.QueueLeaseMessage{ID: inv.ID, Receipt: lease.Receipt, Payload: inv.Payload, Headers: headers,
		Attempt: inv.Attempts, EnqueuedAt: inv.CreatedAt}
	if inv.LeaseExpiresAt != nil {
		msg.LeaseExpiresAt = *inv.LeaseExpiresAt
	}
	return msg
}

// Ack completes leased messages.
func (s Service) Ack(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, _ string) {
	s.settle(w, r, acct, func(store state.QueueLeaseStore, req api.QueueLeaseReceiptsRequest) (state.QueueLeaseOutcome, error) {
		return store.AckQueueLeases(r.Context(), acct.ID, app.ID, r.PathValue("queue"), req.Receipts)
	})
}

// Nack returns leased messages for a retry under their retry policy.
func (s Service) Nack(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, _ string) {
	s.settle(w, r, acct, func(store state.QueueLeaseStore, req api.QueueLeaseReceiptsRequest) (state.QueueLeaseOutcome, error) {
		if req.DelaySeconds < 0 || req.DelaySeconds > api.QueueBindingRetryMaxSeconds {
			return state.QueueLeaseOutcome{}, state.ErrInvalidArgument
		}
		lastError := safetext.Truncate(req.Error, errorMaxBytes)
		return store.NackQueueLeases(r.Context(), acct.ID, app.ID, r.PathValue("queue"), req.Receipts,
			time.Duration(req.DelaySeconds)*time.Second, lastError)
	})
}

// Extend moves the visibility deadline of leased messages.
func (s Service) Extend(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, _ string) {
	s.settle(w, r, acct, func(store state.QueueLeaseStore, req api.QueueLeaseReceiptsRequest) (state.QueueLeaseOutcome, error) {
		return store.ExtendQueueLeases(r.Context(), acct.ID, app.ID, r.PathValue("queue"), req.Receipts, req.VisibilityTimeoutSeconds)
	})
}

func (s Service) settle(w http.ResponseWriter, r *http.Request, acct state.Account,
	apply func(state.QueueLeaseStore, api.QueueLeaseReceiptsRequest) (state.QueueLeaseOutcome, error)) {
	store, ok := s.leaseStore(w, acct)
	if !ok {
		return
	}
	var req api.QueueLeaseReceiptsRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	out, err := apply(store, req)
	if err != nil {
		writeError(w, acct, r.PathValue("queue"), err)
		return
	}
	resp := api.QueueLeaseReceiptsResponse{Succeeded: out.Succeeded, Failed: make([]api.QueueLeaseReceiptFailure, 0, len(out.Failed))}
	for _, f := range out.Failed {
		resp.Failed = append(resp.Failed, api.QueueLeaseReceiptFailure{Receipt: f.Receipt, Reason: f.Reason})
	}
	httpkit.WriteJSON(w, http.StatusOK, resp)
}

func writeError(w http.ResponseWriter, acct state.Account, queue string, err error) {
	limits := api.MustLimitsFor(acct.Plan)
	switch {
	case errors.Is(err, state.ErrOrgAccessRevoked):
		api.WriteProblem(w, api.ErrOrgRoleForbidden("consume queues after organization access was revoked"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Pull binding not found",
			fmt.Sprintf("queue %q has no enabled pull binding on this app", queue)))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid queue lease request",
			fmt.Sprintf("lease 1-%d messages for at most %d seconds, wait at most %d seconds, and pass 1-%d receipts",
				api.QueueLeaseMaxMessages, limits.MaxAsyncInvocationDeadlineSeconds, api.QueueLeaseMaxWaitSeconds, api.QueueLeaseMaxReceipts)))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Account inactive", "the account cannot consume queue messages"))
	default:
		api.WriteProblem(w, api.ErrCapacity("queue lease"))
	}
}
