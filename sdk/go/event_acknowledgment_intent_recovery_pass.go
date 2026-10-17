package faas

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// ErrEventAcknowledgmentIntentRecoveryPassFailed reports an iteration failure
// without exposing driver/transport diagnostics. Inspect the returned progress.
var ErrEventAcknowledgmentIntentRecoveryPassFailed = errors.New("acknowledgment intent recovery pass failed")

// EventAcknowledgmentIntentRecoveryPassOptions bounds one explicitly invoked pass.
// Zero values select 100 attempts, four workers, a 30-second database claim and
// one-second persisted backoff. CallbackOptions keeps lease-relative defaults.
type EventAcknowledgmentIntentRecoveryPassOptions struct {
	MaxAttempts     int
	Concurrency     int
	ClaimDuration   time.Duration
	Backoff         time.Duration
	CallbackOptions EventAcknowledgmentWorkerOptions
}

// EventAcknowledgmentIntentRecoveryPassResult is scalar progress returned by
// value, without per-intent IDs, callback data or error causes. Attempts counts
// admissions, including empty probes. Neither empty nor limit proves global drain.
type EventAcknowledgmentIntentRecoveryPassResult struct {
	StopReason    string `json:"stop_reason"` // limit, empty, interrupted, failed, commit_unknown
	Attempts      int    `json:"attempts"`
	Confirmed     int    `json:"confirmed"`
	Unconfirmed   int    `json:"unconfirmed"`
	Superseded    int    `json:"superseded"`
	Empty         int    `json:"empty"`
	ClaimLost     int    `json:"claim_lost"`
	Interrupted   int    `json:"interrupted"`
	Failed        int    `json:"failed"`
	CommitUnknown int    `json:"commit_unknown"`
}

func eventIntentRecoveryPassOptions(o EventAcknowledgmentIntentRecoveryPassOptions) (EventAcknowledgmentIntentRecoveryPassOptions, error) {
	if o.MaxAttempts == 0 {
		o.MaxAttempts = 100
	}
	if o.Concurrency == 0 {
		o.Concurrency = 4
	}
	if o.ClaimDuration == 0 {
		o.ClaimDuration = 30 * time.Second
	}
	if o.Backoff == 0 {
		o.Backoff = time.Second
	}
	if o.MaxAttempts < 1 || o.MaxAttempts > 10000 || o.Concurrency < 1 || o.Concurrency > 32 || o.ClaimDuration < time.Second || o.ClaimDuration > 5*time.Minute || o.ClaimDuration%time.Millisecond != 0 || o.Backoff < time.Millisecond || o.Backoff > time.Hour || o.Backoff%time.Millisecond != 0 {
		return o, ErrInvalidEventConsumerRequest
	}
	// Validate broad bounds without replacing lease-relative zero defaults. The
	// existing Deliver primitive validates overrides against the selected row.
	if _, err := eventWorkerOptions(o.CallbackOptions, 300); err != nil {
		return o, err
	}
	return o, nil
}

// RecoverEventAcknowledgmentIntents joins a finite number of concurrent saved
// callback recoveries. Empty, cancellation, hard failure or unknown commit closes
// admission; admitted work drains using ctx. Unconfirmed settlements and lost
// claims allow further admissions. No scheduling, business work or renewal runs.
//
// Always inspect progress, including on error. errors.Is recognizes
// ErrEventConsumerCommitUnknown, context.Canceled, context.DeadlineExceeded and
// ErrEventAcknowledgmentIntentRecoveryPassFailed. Unknown commit takes precedence
// in StopReason, then caller cancellation, hard failure, empty and the work limit.
// Multiple categories can match the returned joined error; raw causes are omitted.
func RecoverEventAcknowledgmentIntents(ctx context.Context, db *sql.DB, client *Client, accountID, appID string, options EventAcknowledgmentIntentRecoveryPassOptions) (EventAcknowledgmentIntentRecoveryPassResult, error) {
	var zero EventAcknowledgmentIntentRecoveryPassResult
	if ctx == nil || db == nil || client == nil || client.Client == nil {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	options, err = eventIntentRecoveryPassOptions(options)
	if err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		zero.StopReason = "interrupted"
		return zero, err
	}
	s := eventIntentRecoveryPass{}
	var workers sync.WaitGroup
	count := min(options.Concurrency, options.MaxAttempts)
	workers.Add(count)
	for range count {
		go func() {
			defer workers.Done()
			for s.admit(ctx, options.MaxAttempts) {
				status, err := RecoverNextEventAcknowledgmentIntent(ctx, db, client, account, app, options.ClaimDuration, options.CallbackOptions, options.Backoff)
				s.observe(status, err)
			}
		}()
	}
	workers.Wait()
	return s.finish(ctx)
}

type eventIntentRecoveryPass struct {
	mu       sync.Mutex
	closed   bool
	canceled bool
	deadline bool
	result   EventAcknowledgmentIntentRecoveryPassResult
}

func (s *eventIntentRecoveryPass) admit(ctx context.Context, limit int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || ctx.Err() != nil || s.result.Attempts >= limit {
		return false
	}
	s.result.Attempts++ // Reserve before recovery; never hold the mutex over I/O.
	return true
}

func (s *eventIntentRecoveryPass) observe(status EventAcknowledgmentIntentStatus, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case errors.Is(err, ErrEventConsumerCommitUnknown):
		s.result.CommitUnknown++
		s.closed = true
	case status.State == "confirmed":
		s.result.Confirmed++
	case status.State == "unconfirmed":
		// Deliver can return a committed settlement and a callback error. That
		// uncertainty belongs to the saved intent, not the pass failure count.
		s.result.Unconfirmed++
	case status.State == "superseded":
		s.result.Superseded++
	case errors.Is(err, ErrEventAcknowledgmentIntentEmpty):
		s.result.Empty++
		s.closed = true
	case errors.Is(err, ErrEventAcknowledgmentIntentClaimLost):
		s.result.ClaimLost++
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		s.result.Interrupted++
		s.canceled = s.canceled || errors.Is(err, context.Canceled)
		s.deadline = s.deadline || errors.Is(err, context.DeadlineExceeded)
		s.closed = true
	default:
		s.result.Failed++
		s.closed = true
	}
}

// All workers have joined before finish reads the counters; no goroutine retains
// a reference to the returned value. Never retain errors or context.Cause values.
func (s *eventIntentRecoveryPass) finish(ctx context.Context) (EventAcknowledgmentIntentRecoveryPassResult, error) {
	out := s.result
	interruption := ctx.Err()
	var unknown, failed, canceled, deadline error
	if out.CommitUnknown > 0 {
		unknown = ErrEventConsumerCommitUnknown
	}
	if out.Failed > 0 {
		failed = ErrEventAcknowledgmentIntentRecoveryPassFailed
	}
	if s.canceled {
		canceled = context.Canceled
	}
	if s.deadline {
		deadline = context.DeadlineExceeded
	}
	switch {
	case unknown != nil:
		out.StopReason = "commit_unknown"
	case interruption != nil || out.Interrupted > 0:
		out.StopReason = "interrupted"
	case failed != nil:
		out.StopReason = "failed"
	case out.Empty > 0:
		out.StopReason = "empty"
	default:
		out.StopReason = "limit"
	}
	return out, errors.Join(unknown, interruption, canceled, deadline, failed)
}
