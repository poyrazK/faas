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
// queue.
func (s Service) WorkloadScope(r *http.Request) (state.Account, state.App, string, error) {
	verifier := s.Host.WorkloadVerifier()
	if verifier == nil {
		return state.Account{}, state.App{}, "", errors.New("workload trust is not configured")
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	c, err := verifier.Verify(token, workloadidentity.QueuesAudience, time.Now())
	if err != nil {
		return state.Account{}, state.App{}, "", err
	}
	store := s.Host.Store()
	app, err := store.AppByID(r.Context(), c.AppID)
	if err != nil || app.AccountID != c.AccountID || app.Slug != r.PathValue("slug") || app.Status == state.AppDeleted {
		return state.Account{}, state.App{}, "", errors.New("token does not name this app")
	}
	acct, err := store.AccountByID(r.Context(), c.AccountID)
	if err != nil || acct.Status != state.AccountActive {
		return state.Account{}, state.App{}, "", errors.New("account is not active")
	}
	instance, err := store.InstanceByID(r.Context(), c.InstanceID)
	if err != nil || instance.AppID != app.ID || !state.State(instance.State).CountsForConcurrency() {
		return state.Account{}, state.App{}, "", errors.New("instance is not live")
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
	if r.Context().Err() != nil {
		return // the consumer went away; anything leased is redelivered after its timeout
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
		if !waitForQueueWake(waitCtx, &wakes, recheck.C, req.AppID) {
			return leases, nil
		}
	}
}

// waitForQueueWake returns true when the lease should try again, false when
// the wait is over. A closed notification stream leaves the recheck timer.
func waitForQueueWake(ctx context.Context, wakes *<-chan db.Notification, recheck <-chan time.Time, appID string) bool {
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
			if queueMessageDue(n.Payload, appID) {
				return true
			}
		}
	}
}

// queueMessageDue matches an invocation_due payload for a queue message of
// appID. The payload is only a hint; the next lease re-checks everything.
func queueMessageDue(payload, appID string) bool {
	var p struct {
		AppID  string `json:"app_id"`
		Source string `json:"source"`
	}
	return json.Unmarshal([]byte(payload), &p) == nil && p.AppID == appID && p.Source == string(state.InvocationQueue)
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
