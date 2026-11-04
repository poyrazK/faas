package receivers

import (
	"context"
	"errors"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	issueRetryCapacity   = 128
	issueRetryPerApp     = 8
	issuePendingCapacity = 1024
	issuePendingPerApp   = 128
)

var errHTTPIssueSkipped = errors.New("HTTP issue was not eligible for ingestion")

type issueAppGate struct {
	slot  chan struct{}
	users int
}

// Serialize writes for one app before taking a shared telemetry slot. The
// database already serializes these on the app row; waiting there while
// holding the shared gate would let one noisy app occupy every slot.
func (a *appErrorsReceiver) acquireIssueApp(ctx context.Context, app string) (func(), error) {
	a.issueAppMu.Lock()
	g := a.issueAppGates[app]
	if g == nil {
		g = &issueAppGate{slot: make(chan struct{}, 1)}
		g.slot <- struct{}{}
		a.issueAppGates[app] = g
	}
	g.users++
	a.issueAppMu.Unlock()
	done := func() {
		a.issueAppMu.Lock()
		g.users--
		if g.users == 0 {
			delete(a.issueAppGates, app)
		}
		a.issueAppMu.Unlock()
	}
	select {
	case <-ctx.Done():
		done()
		return nil, ctx.Err()
	case <-g.slot:
		return func() { g.slot <- struct{}{}; done() }, nil
	}
}

func (a *appErrorsReceiver) acquireIssueWrite(ctx context.Context, app string) (func(), error) {
	releaseApp, err := a.acquireIssueApp(ctx, app)
	if err != nil {
		return nil, err
	}
	releaseGate, err := a.gate.acquire(ctx)
	if err != nil {
		releaseApp()
		return nil, err
	}
	return func() { releaseGate(); releaseApp() }, nil
}

func retryableHTTPIssueError(err error) bool {
	var limit *state.IssueLimitError
	if errors.As(err, &limit) {
		return limit.Rate || limit.Resource == "events"
	}
	return err != nil && !errors.Is(err, errHTTPIssueSkipped) && !errors.Is(err, state.ErrInvalidArgument) && !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrIssueEventConflict) && !errors.Is(err, state.ErrIssueQuota) && !errors.Is(err, state.ErrIssuePolicyChanged)
}

// enqueueHTTPIssue keeps Issue storage latency off the non-idempotent legacy
// counting path. Its bounded in-memory queue remains best effort on restart.
func (a *appErrorsReceiver) enqueueHTTPIssue(req *apidpb.IncrementAppErrorRequest) string {
	if req.GetHttpStatus() < 500 || req.GetDeploymentId() == "" || req.GetRequestId() == "" {
		return ""
	}
	// Snapshot before returning to the streaming handler, which may reuse req.
	snapshot := &apidpb.IncrementAppErrorRequest{
		AccountId: req.GetAccountId(), AppId: req.GetAppId(), DeploymentId: req.GetDeploymentId(),
		RequestId: req.GetRequestId(), IssueEventId: req.GetIssueEventId(), ReceivedAtUnixMs: req.GetReceivedAtUnixMs(),
		HttpStatus: req.GetHttpStatus(), ErrorClass: req.GetErrorClass(), SampleMessage: req.GetSampleMessage(),
		RouteTemplate: req.GetRouteTemplate(), Fingerprint: req.GetFingerprint(), Count: req.GetCount(),
		IssueTimeBuckets: append([]uint32(nil), req.GetIssueTimeBuckets()...),
	}
	app := snapshot.GetAppId()
	a.issueRetryMu.Lock()
	if a.issueAttemptCount >= issueRetryCapacity || a.issueAttemptApps[app] >= issueRetryPerApp {
		if len(a.issuePending) >= issuePendingCapacity || a.issuePendingApps[app] >= issuePendingPerApp {
			a.issueRetryMu.Unlock()
			a.ops.ObserveIssueEvent("retry_dropped")
			return "retry"
		}
		a.issuePending = append(a.issuePending, snapshot)
		a.issuePendingApps[app]++
		a.issueRetryMu.Unlock()
		return "queued"
	}
	a.issueAttemptCount++
	a.issueAttemptApps[app]++
	a.issueRetryMu.Unlock()
	a.startHTTPIssue(snapshot)
	return "queued"
}

// startHTTPIssue owns one initial-attempt slot; completion wakes the oldest
// eligible queued app, so a busy app cannot occupy the global backlog.
func (a *appErrorsReceiver) startHTTPIssue(snapshot *apidpb.IncrementAppErrorRequest) {
	app := snapshot.GetAppId()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		attempts := 0
		outcome := httpIssueSkipped
		attempt := func(ctx context.Context) error {
			attempts++
			release, err := a.acquireIssueWrite(ctx, app)
			if err != nil {
				return err
			}
			defer release()
			outcome, err = recordHTTPIssue(ctx, a.store, snapshot)
			if err == nil && outcome == httpIssueSkipped {
				return errHTTPIssueSkipped
			}
			return err
		}
		callCtx, callCancel := context.WithTimeout(ctx, 10*time.Second)
		err := attempt(callCtx)
		callCancel()
		a.issueRetryMu.Lock()
		a.issueAttemptCount--
		a.issueAttemptApps[app]--
		if a.issueAttemptApps[app] == 0 {
			delete(a.issueAttemptApps, app)
		}
		var next *apidpb.IncrementAppErrorRequest
		for i, waiting := range a.issuePending {
			waitingApp := waiting.GetAppId()
			if a.issueAttemptApps[waitingApp] >= issueRetryPerApp {
				continue
			}
			next = waiting
			copy(a.issuePending[i:], a.issuePending[i+1:])
			a.issuePending[len(a.issuePending)-1] = nil
			a.issuePending = a.issuePending[:len(a.issuePending)-1]
			a.issuePendingApps[waitingApp]--
			if a.issuePendingApps[waitingApp] == 0 {
				delete(a.issuePendingApps, waitingApp)
			}
			a.issueAttemptCount++
			a.issueAttemptApps[waitingApp]++
			break
		}
		a.issueRetryMu.Unlock()
		if next != nil {
			a.startHTTPIssue(next)
		}
		if retryableHTTPIssueError(err) && a.acquireHTTPIssueRetry(ctx, app) {
			err = retryHTTPIssueUntil(ctx, attempt, time.Second, time.Minute)
			a.releaseHTTPIssueRetry(app)
		}
		switch {
		case errors.Is(err, errHTTPIssueSkipped):
			a.ops.ObserveIssueEvent("retry_skipped")
		case err != nil:
			a.ops.ObserveIssueEvent("retry_dropped")
		case attempts > 1:
			a.ops.ObserveIssueEvent("retry_recovered")
		case outcome == httpIssueDuplicate:
			a.ops.ObserveIssueEvent("duplicate")
		default:
			a.ops.ObserveIssueEvent("accepted")
		}
	}()
}

// acquireHTTPIssueRetry bounds waiting records independently of active retry
// workers. A burst of transient failures can wait for a slot instead of being
// discarded merely because eight retries for the app are already active.
func (a *appErrorsReceiver) acquireHTTPIssueRetry(ctx context.Context, app string) bool {
	a.issueRetryMu.Lock()
	if ctx.Err() == nil && a.issueRetryCount < issueRetryCapacity && a.issueRetryApps[app] < issueRetryPerApp {
		a.issueRetryCount++
		a.issueRetryApps[app]++
		a.issueRetryMu.Unlock()
		return true
	}
	if a.issueRetryWaiting >= issuePendingCapacity || a.issueRetryWaitApps[app] >= issuePendingPerApp {
		a.issueRetryMu.Unlock()
		return false
	}
	a.issueRetryWaiting++
	a.issueRetryWaitApps[app]++
	defer func() {
		a.issueRetryWaiting--
		a.issueRetryWaitApps[app]--
		if a.issueRetryWaitApps[app] == 0 {
			delete(a.issueRetryWaitApps, app)
		}
		a.issueRetryMu.Unlock()
	}()
	for {
		if ctx.Err() != nil {
			return false
		}
		if a.issueRetryCount < issueRetryCapacity && a.issueRetryApps[app] < issueRetryPerApp {
			a.issueRetryCount++
			a.issueRetryApps[app]++
			return true
		}
		changed := a.issueRetryChanged
		a.issueRetryMu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		a.issueRetryMu.Lock()
	}
}

func (a *appErrorsReceiver) releaseHTTPIssueRetry(app string) {
	a.issueRetryMu.Lock()
	defer a.issueRetryMu.Unlock()
	a.issueRetryCount--
	a.issueRetryApps[app]--
	if a.issueRetryApps[app] == 0 {
		delete(a.issueRetryApps, app)
	}
	close(a.issueRetryChanged)
	a.issueRetryChanged = make(chan struct{})
}

func retryHTTPIssueUntil(ctx context.Context, attempt func(context.Context) error, delay, maxDelay time.Duration) error {
	for {
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := attempt(callCtx)
		cancel()
		if err == nil || !retryableHTTPIssueError(err) {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay = min(maxDelay, delay*2)
	}
}
