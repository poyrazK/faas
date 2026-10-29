package preview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

func (s *eventAcknowledgmentWorker) scope(status EventAcknowledgmentStatus) bool {
	return strings.EqualFold(status.InvocationID, s.delivery.InvocationID) && status.Attempt == s.delivery.Attempt && status.ReplayGeneration == s.delivery.ReplayGeneration
}
func (s *eventAcknowledgmentWorker) renewed(status EventAcknowledgmentStatus) error {
	if !s.scope(status) {
		return ErrEventAcknowledgmentLeaseLost
	}
	if status.State != "dispatching" || status.ProcessingState != "processing" || status.Disposition != "" {
		return ErrEventAcknowledgmentLeaseLost
	}
	if status.LeaseExpiresAt == nil || status.LeaseExpiresAt.IsZero() || status.DeadlineAt == nil || status.DeadlineAt.IsZero() || status.LeaseExpiresAt.After(*status.DeadlineAt) {
		return ErrEventAcknowledgmentUnconfirmed
	}
	return nil
}
func (s *eventAcknowledgmentWorker) renew(ctx context.Context) (EventAcknowledgmentStatus, error) {
	callCtx, cancel := context.WithTimeout(ctx, s.options.CallbackTimeout)
	defer cancel()
	return s.client.RenewEventAcknowledgment(callCtx, s.app, s.delivery.InvocationID, EventAcknowledgmentRequest{Token: s.token})
}
func (s *eventAcknowledgmentWorker) startLease(ctx context.Context) error {
	for attempt := 0; attempt < s.options.CallbackAttempts; attempt++ {
		status, err := s.renew(ctx)
		if err == nil {
			return s.renewed(status)
		}
		var problem *faas.APIError
		offered := false
		if errors.As(err, &problem) && problem.Problem.Status == http.StatusConflict {
			callCtx, cancel := context.WithTimeout(ctx, s.options.CallbackTimeout)
			current, readErr := s.client.GetEventAcknowledgment(callCtx, s.app, s.delivery.InvocationID)
			cancel()
			if readErr != nil {
				return eventWorkerCallbackError(readErr, ctx)
			}
			if !s.scope(current) || current.State != "dispatching" || current.ProcessingState != "delivering" {
				return ErrEventAcknowledgmentLeaseLost
			}
			// GET is not token proof. Only a subsequent successful renew starts work.
			offered = true
		}
		delay, retry := s.retryDelay(err)
		if !offered && !retry || attempt+1 == s.options.CallbackAttempts {
			if offered {
				return ErrEventAcknowledgmentUnconfirmed
			}
			return eventWorkerCallbackError(err, ctx)
		}
		if offered {
			delay = s.options.RetryBackoff
		}
		if err := eventWorkerWait(ctx, delay); err != nil {
			return errors.Join(ErrEventAcknowledgmentUnconfirmed, err)
		}
	}
	return ErrEventAcknowledgmentUnconfirmed
}
func (s *eventAcknowledgmentWorker) monitor(ctx context.Context, cancelWork context.CancelCauseFunc) func() {
	monitorCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Duration(s.seconds) * time.Second / 3)
		defer ticker.Stop()
		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-ticker.C:
				status, err := s.renew(monitorCtx)
				if err != nil {
					err = eventWorkerCallbackError(err, monitorCtx)
				} else {
					err = s.renewed(status)
				}
				if err != nil {
					if monitorCtx.Err() == nil {
						cancelWork(err)
					}
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { cancel(); <-done }) }
}
func (s *eventAcknowledgmentWorker) terminal(ctx context.Context, action, reason string, body json.RawMessage) (EventAcknowledgmentStatus, error) {
	for attempt := 0; attempt < s.options.CallbackAttempts; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, s.options.CallbackTimeout)
		var status EventAcknowledgmentStatus
		var err error
		switch action {
		case "complete":
			status, err = s.client.CompleteEventAcknowledgment(callCtx, s.app, s.delivery.InvocationID, EventAcknowledgmentRequest{Token: s.token, Result: body})
		case "retry":
			status, err = s.client.RetryEventAcknowledgment(callCtx, s.app, s.delivery.InvocationID, EventAcknowledgmentFailureRequest{Token: s.token, Reason: reason})
		case "dead-letter":
			status, err = s.client.DeadLetterEventAcknowledgment(callCtx, s.app, s.delivery.InvocationID, EventAcknowledgmentFailureRequest{Token: s.token, Reason: reason})
		default:
			cancel()
			return EventAcknowledgmentStatus{}, ErrInvalidEventConsumerRequest
		}
		cancel()
		if err == nil {
			if !s.validTerminal(status, action, reason) {
				return EventAcknowledgmentStatus{}, ErrEventAcknowledgmentUnconfirmed
			}
			return status, nil
		}
		delay, retry := s.retryDelay(err)
		if !retry || attempt+1 == s.options.CallbackAttempts {
			return EventAcknowledgmentStatus{}, eventWorkerCallbackError(err, ctx)
		}
		if err := eventWorkerWait(ctx, delay); err != nil {
			return EventAcknowledgmentStatus{}, errors.Join(ErrEventAcknowledgmentUnconfirmed, err)
		}
	}
	return EventAcknowledgmentStatus{}, ErrEventAcknowledgmentUnconfirmed
}
func (s *eventAcknowledgmentWorker) validTerminal(status EventAcknowledgmentStatus, action, reason string) bool {
	if !s.scope(status) {
		return false
	}
	if action == "complete" {
		return status.State == "completed" && status.ProcessingState == "completed" && status.Disposition == "" && status.FailureReason == ""
	}
	if status.Disposition != action || status.FailureReason != reason || status.LeaseExpiresAt != nil {
		return false
	}
	if action == "retry" && status.State == "pending" {
		return status.ProcessingState == "waiting" && status.NextAttemptAt != nil && !status.NextAttemptAt.IsZero()
	}
	return status.State == "dead_letter" && status.ProcessingState == "dead_letter" && status.NextAttemptAt == nil
}
func (s *eventAcknowledgmentWorker) retryDelay(err error) (time.Duration, bool) {
	delay := s.options.RetryBackoff
	var problem *faas.APIError
	if !errors.As(err, &problem) {
		return delay, true
	}
	status := problem.Problem.Status
	if status != http.StatusRequestTimeout && status != http.StatusTooEarly && status != http.StatusTooManyRequests && (status < 500 || status > 599) {
		return 0, false
	}
	horizon := time.Duration(s.seconds) * time.Second / 3
	if seconds := problem.Problem.RetryAfterSeconds; seconds != nil {
		if *seconds < 0 || *seconds > int64(horizon/time.Second) {
			return 0, false
		}
		delay = max(delay, time.Duration(*seconds)*time.Second)
	} else if raw := strings.TrimSpace(problem.Problem.RetryAfter); raw != "" {
		seconds, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr == nil {
			if seconds < 0 || seconds > int64(horizon/time.Second) {
				return 0, false
			}
			delay = max(delay, time.Duration(seconds)*time.Second)
		} else {
			deadline, dateErr := http.ParseTime(raw)
			if dateErr != nil {
				return 0, false
			}
			delay = max(delay, time.Until(deadline))
		}
	}
	return delay, delay <= horizon
}
func eventWorkerWait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func eventWorkerCallbackError(err error, ctx context.Context) error {
	// Remote error bodies and transport diagnostics can echo credentials. Expose
	// only a bounded status plus sentinels, never the raw callback error text.
	var problem *faas.APIError
	if errors.As(err, &problem) {
		status := problem.Problem.Status
		if status == http.StatusConflict || status == http.StatusNotFound || status == http.StatusGone {
			return ErrEventAcknowledgmentLeaseLost
		}
		if status >= 100 && status <= 599 {
			return fmt.Errorf("%w (HTTP %d)", ErrEventAcknowledgmentUnconfirmed, status)
		}
	}
	return errors.Join(ErrEventAcknowledgmentUnconfirmed, ctx.Err())
}
