package preview

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// EventOutboxWorkerOptions configures a blocking, caller-owned recovery pool.
// Recovery is copied and validated before goroutines start. Callbacks run on the
// worker goroutines and must be concurrency-safe and honor their context.
type EventOutboxWorkerOptions struct {
	Recovery         EventOutboxRecoveryOptions
	Concurrency      int
	IdleMin          time.Duration
	IdleMax          time.Duration
	ErrorBackoff     time.Duration
	OperationTimeout time.Duration
	ShutdownGrace    time.Duration
}

// EventOutboxWorkerEvent carries sanitized local metadata, never event payloads,
// capabilities or raw driver/server errors. Status is absent when no row result
// was obtained. A snapshot can be stale by the time the callback observes it.
type EventOutboxWorkerEvent struct {
	AccountID        string             `json:"account_id"`
	AppID            string             `json:"app_id"`
	ProducerKey      string             `json:"producer_key,omitempty"`
	Worker           int                `json:"worker"`
	At               time.Time          `json:"at"`
	Outcome          string             `json:"outcome"`
	ErrorCode        string             `json:"error_code,omitempty"`
	RetryAt          *time.Time         `json:"retry_at,omitempty"`
	RetryAfterMillis int64              `json:"retry_after_millis,omitempty"`
	Status           *EventOutboxStatus `json:"status,omitempty"`
}

func eventOutboxWorkerOptions(o EventOutboxWorkerOptions) (EventOutboxWorkerOptions, error) {
	var err error
	o.Recovery, err = eventOutboxRecoveryOptions(o.Recovery)
	if err != nil {
		return o, err
	}
	if o.Concurrency == 0 {
		o.Concurrency = 1
	}
	if o.IdleMin == 0 {
		o.IdleMin = 250 * time.Millisecond
	}
	if o.IdleMax == 0 {
		o.IdleMax = max(5*time.Second, o.IdleMin)
	}
	if o.ErrorBackoff == 0 {
		o.ErrorBackoff = time.Second
	}
	if o.OperationTimeout == 0 {
		o.OperationTimeout = time.Minute
	}
	if o.ShutdownGrace == 0 {
		o.ShutdownGrace = 30 * time.Second
	}
	if o.Concurrency < 1 || o.Concurrency > 32 || o.IdleMin < 10*time.Millisecond || o.IdleMax < o.IdleMin || o.IdleMax > time.Minute || o.ErrorBackoff < 10*time.Millisecond || o.ErrorBackoff > time.Minute || o.OperationTimeout < time.Second || o.OperationTimeout > 10*time.Minute || o.ShutdownGrace < time.Millisecond || o.ShutdownGrace > 5*time.Minute {
		return o, ErrInvalidEventConsumerRequest
	}
	return o, nil
}

// RunEventOutboxWorker blocks until cancellation and drain completion. It bounds
// concurrent recovery calls, backs off empty polls, and continues after per-row
// or storage failures. notify is optional and runs concurrently across workers.
// Shutdown cancels claims immediately. In-flight publication/settlement/callbacks
// get ShutdownGrace before cancellation. It waits for all goroutines, so drivers,
// transports and callbacks must honor context; grace is not forced termination.
// It returns the parent context error after draining and never installs schema.
func RunEventOutboxWorker(ctx context.Context, db *sql.DB, client *Client, accountID, appID string, options EventOutboxWorkerOptions, notify func(context.Context, EventOutboxWorkerEvent)) error {
	if ctx == nil || db == nil || client == nil || client.Client == nil {
		return ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return err
	}
	options, err = eventOutboxWorkerOptions(options)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Explicitly detach the parent cancellation/deadline for grace-period draining,
	// preserving its values. Each operation retains its own timeout below.
	drainCtx, cancelDrain := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelDrain()
	var workers sync.WaitGroup
	workers.Add(options.Concurrency)
	for id := 1; id <= options.Concurrency; id++ {
		go func(worker int) {
			defer workers.Done()
			runEventOutboxWorker(ctx, drainCtx, db, client, account, app, worker, options, notify)
		}(id)
	}
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
		return ctx.Err()
	case <-ctx.Done():
		timer := time.NewTimer(options.ShutdownGrace)
		defer timer.Stop()
		select {
		case <-done:
			return ctx.Err()
		case <-timer.C:
			cancelDrain()
			<-done
			return ctx.Err()
		}
	}
}

func runEventOutboxWorker(ctx, drainCtx context.Context, db *sql.DB, client *Client, account, app string, worker int, options EventOutboxWorkerOptions, notify func(context.Context, EventOutboxWorkerEvent)) {
	idle := options.IdleMin
	wasPaused := false
	wasRateLimited := false
	for ctx.Err() == nil {
		claimCtx, cancelClaim := context.WithTimeout(ctx, options.OperationTimeout)
		deliveryCtx, cancelDelivery := context.WithTimeout(drainCtx, options.OperationTimeout)
		claim, status, err := claimNextEventOutbox(claimCtx, db, account, app, options.Recovery)
		cancelClaim()
		claimed := err == nil
		if claimed {
			status, err = claim.publish(deliveryCtx, db, client, claim.request, options.Recovery)
		}
		paused := errors.Is(err, ErrEventOutboxPublishingPaused)
		limited := errors.Is(err, ErrEventOutboxRateLimited)
		if errors.Is(err, ErrEventOutboxEmpty) || paused || limited {
			if ((paused && !wasPaused) || (limited && !wasRateLimited)) && notify != nil {
				notify(deliveryCtx, eventOutboxWorkerOutcome(account, app, worker, status, err))
			}
			wasPaused = paused
			wasRateLimited = limited
			cancelDelivery()
			delay := idle
			var wait *EventOutboxRateLimitWaitError
			if errors.As(err, &wait) {
				delay = min(options.IdleMax, max(delay, wait.RetryAfter))
			}
			if !eventOutboxWorkerWait(ctx, delay) {
				return
			}
			idle = min(options.IdleMax, 2*idle)
			continue
		}
		wasPaused = false
		wasRateLimited = false
		// Shutdown cancellation of an uncommitted claim is not a storage incident.
		// Unknown commits remain reportable because a lease may have been committed.
		if !claimed && ctx.Err() != nil && status.ProducerKey == "" && !errors.Is(err, ErrEventOutboxCommitUnknown) && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			cancelDelivery()
			return
		}
		event := eventOutboxWorkerOutcome(account, app, worker, status, err)
		if claim.key != "" {
			event.ProducerKey = claim.key
		}
		if notify != nil {
			notify(deliveryCtx, event)
		}
		cancelDelivery()
		idle = options.IdleMin
		if event.Outcome == "storage_error" || event.Outcome == "claim_lost" || event.Outcome == "error" {
			if !eventOutboxWorkerWait(ctx, options.ErrorBackoff) {
				return
			}
		}
	}
}

func eventOutboxWorkerWait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

func eventOutboxWorkerOutcome(account, app string, worker int, status EventOutboxStatus, err error) EventOutboxWorkerEvent {
	out := EventOutboxWorkerEvent{AccountID: account, AppID: app, ProducerKey: status.ProducerKey, Worker: worker, At: time.Now().UTC()}
	if status.ProducerKey != "" {
		out.Status = &status
	}
	switch {
	case errors.Is(err, ErrEventOutboxRateLimited):
		out.Outcome, out.ErrorCode = "rate_limited", "publishing_rate_limited"
		var wait *EventOutboxRateLimitWaitError
		if errors.As(err, &wait) {
			out.RetryAt = &wait.RetryAt
			out.RetryAfterMillis = int64((wait.RetryAfter + time.Millisecond - 1) / time.Millisecond)
		}
	case errors.Is(err, ErrEventOutboxPublishingPaused):
		out.Outcome, out.ErrorCode = "paused", "publishing_paused"
	case errors.Is(err, ErrEventOutboxCommitUnknown):
		out.Outcome, out.ErrorCode = "storage_error", "commit_unknown"
	case errors.Is(err, ErrEventOutboxStore):
		out.Outcome, out.ErrorCode = "storage_error", "store_failed"
		if errors.Is(err, context.DeadlineExceeded) {
			out.ErrorCode = "operation_timeout"
		} else if errors.Is(err, context.Canceled) {
			out.ErrorCode = "operation_canceled"
		}
	case errors.Is(err, ErrEventOutboxClaimLost):
		out.Outcome, out.ErrorCode = "claim_lost", "claim_lost"
	case errors.Is(err, context.DeadlineExceeded):
		out.Outcome, out.ErrorCode = "error", "operation_timeout"
	case errors.Is(err, context.Canceled):
		out.Outcome, out.ErrorCode = "error", "operation_canceled"
	case errors.Is(err, ErrEventOutboxBlocked):
		out.Outcome, out.ErrorCode = "blocked", status.LastCode
	case errors.Is(err, ErrEventOutboxUnconfirmed):
		out.Outcome, out.ErrorCode = "unconfirmed", "unconfirmed"
	case err == nil:
		out.Outcome = "published"
	default:
		out.Outcome, out.ErrorCode = "error", "recovery_failed"
	}
	return out
}
