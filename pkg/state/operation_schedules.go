// adr: 20261018161833
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cronexpr"
)

// OperationSchedule is one customer's schedule plus the fields only the
// store and firer see.
type OperationSchedule struct {
	api.OperationScheduleResponse
	AccountID  string
	Input      json.RawMessage
	LeaseToken string
}

// OperationScheduleInput creates a schedule for one customer.
type OperationScheduleInput struct {
	AccountID        string
	PlatformTenantID string
	Request          api.OperationScheduleRequest
	Now              time.Time
}

// OperationScheduleFire records one fire of a schedule the firer claimed.
// Refusal, when set, records why admission was refused (an apid gate)
// instead of admitting.
type OperationScheduleFire struct {
	Schedule OperationSchedule
	Refusal  string
	Now      time.Time
}

// Schedule actions a customer or account may apply.
const (
	OperationSchedulePause  = "pause"
	OperationScheduleResume = "resume"
	OperationScheduleCancel = "cancel"

	operationScheduleListMax = 1000
)

// OperationScheduleStore owns customer_operation_schedules. tenantID scopes
// reads and changes to one customer; empty is the owning account's view.
type OperationScheduleStore interface {
	CreateOperationSchedule(ctx context.Context, in OperationScheduleInput) (OperationSchedule, error)
	OperationSchedule(ctx context.Context, accountID, tenantID, id string) (OperationSchedule, error)
	ListOperationSchedules(ctx context.Context, accountID, tenantID, appID string) ([]OperationSchedule, error)
	ChangeOperationSchedule(ctx context.Context, accountID, tenantID, id, action string, now time.Time) (OperationSchedule, error)
	// ClaimDueOperationSchedules leases up to limit due schedules to one firer.
	ClaimDueOperationSchedules(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]OperationSchedule, error)
	// FireOperationSchedule admits the claimed schedule's Operation (or records
	// the refusal) and advances it, in one transaction.
	FireOperationSchedule(ctx context.Context, fire OperationScheduleFire) (OperationSchedule, error)
}

// validateOperationScheduleRequest checks a request against the plan and
// returns its kind and first fire time. The definition and input are checked
// by the store, which holds the definition.
func validateOperationScheduleRequest(req api.OperationScheduleRequest, limits api.OperationPlanLimits, now time.Time) (kind, timezone string, first time.Time, err error) {
	switch {
	case req.At != nil && req.Cron == "" && req.Timezone == "":
		at := req.At.UTC()
		if !at.After(now) || at.Sub(now) > api.OperationScheduleOnceHorizon {
			return "", "", time.Time{}, fmt.Errorf("%w: at must be in the future and at most %d days ahead", ErrInvalidArgument, int(api.OperationScheduleOnceHorizon/(24*time.Hour)))
		}
		return api.OperationScheduleOnce, "", at.Truncate(time.Microsecond), nil
	case req.At == nil && req.Cron != "":
		timezone = req.Timezone
		if timezone == "" {
			timezone = "UTC"
		}
		timezone, err = cronexpr.NormalizeTimezone(timezone)
		if err != nil {
			return "", "", time.Time{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
		}
		schedule, err := cronexpr.Parse(req.Cron, timezone)
		if err != nil {
			return "", "", time.Time{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
		}
		first = schedule.Next(now)
		if first.IsZero() {
			return "", "", time.Time{}, fmt.Errorf("%w: cron never fires", ErrInvalidArgument)
		}
		// The shortest gap across the next fires bounds the rate; irregular
		// expressions are checked over enough fires to see their pattern.
		min := time.Duration(limits.ScheduleMinIntervalSeconds) * time.Second
		previous := first
		for i := 0; i < 64; i++ {
			next := schedule.Next(previous)
			if next.IsZero() {
				break
			}
			if next.Sub(previous) < min {
				return "", "", time.Time{}, NewOperationLimitError("schedule_min_interval_seconds", int64(limits.ScheduleMinIntervalSeconds), int64(next.Sub(previous)/time.Second))
			}
			previous = next
		}
		return api.OperationScheduleCron, timezone, first.UTC().Truncate(time.Microsecond), nil
	default:
		return "", "", time.Time{}, fmt.Errorf("%w: set either at, or cron with an optional timezone", ErrInvalidArgument)
	}
}

// nextOperationScheduleFire returns the cron schedule's first fire strictly
// after now. A schedule that missed fires while apid was down fires once for
// the latest of them, then continues from the next future time.
func nextOperationScheduleFire(s OperationSchedule, now time.Time) (*time.Time, error) {
	if s.Kind != api.OperationScheduleCron {
		return nil, nil
	}
	schedule, err := cronexpr.Parse(s.Cron, s.Timezone)
	if err != nil {
		return nil, err
	}
	next := schedule.Next(now)
	if next.IsZero() {
		return nil, nil
	}
	next = next.UTC().Truncate(time.Microsecond)
	return &next, nil
}

// operationScheduleIdempotencyKey makes one fire of one schedule admit one
// Operation, whichever replica or retry fires it.
func operationScheduleIdempotencyKey(scheduleID string, fireAt time.Time) string {
	return "schedule:" + scheduleID + ":" + strconv.FormatInt(fireAt.UTC().UnixMicro(), 10)
}

// operationScheduleRefusal reports whether an admission error is a refusal the
// schedule records and moves past, rather than a fault to retry.
func operationScheduleRefusal(err error) (string, bool) {
	var limit *OperationLimitError
	switch {
	case err == nil:
		return "", false
	case errors.As(err, &limit):
		return fmt.Sprintf("plan limit %s reached (limit %d)", limit.Kind, limit.Limit), true
	case errors.Is(err, ErrPlatformTenantSuspended):
		return "customer is suspended", true
	case errors.Is(err, ErrNotFound):
		return "definition, customer or deployment no longer exists", true
	case errors.Is(err, ErrOperationInputConflict), errors.Is(err, ErrOperationExpired), errors.Is(err, ErrConflict):
		return "admission conflicts with retained work", true
	case errors.Is(err, ErrInvalidArgument):
		return truncateOperationScheduleError("input or definition is no longer valid: " + err.Error()), true
	default:
		return "", false
	}
}

func truncateOperationScheduleError(s string) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= api.OperationScheduleLastErrorBytes {
		return s
	}
	cut := api.OperationScheduleLastErrorBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// operationScheduleChange applies an action to a schedule's state.
func operationScheduleChange(s OperationSchedule, action string, now time.Time) (string, *time.Time, error) {
	switch {
	case action == OperationScheduleCancel && (s.State == api.OperationScheduleActive || s.State == api.OperationSchedulePaused):
		return api.OperationScheduleCancelled, nil, nil
	case action == OperationSchedulePause && s.State == api.OperationScheduleActive && s.Kind == api.OperationScheduleCron:
		return api.OperationSchedulePaused, nil, nil
	case action == OperationScheduleResume && s.State == api.OperationSchedulePaused:
		next, err := nextOperationScheduleFire(s, now)
		if err != nil || next == nil {
			return "", nil, fmt.Errorf("%w: schedule cannot resume", ErrConflict)
		}
		return api.OperationScheduleActive, next, nil
	case action == OperationScheduleCancel || action == OperationSchedulePause || action == OperationScheduleResume:
		return "", nil, fmt.Errorf("%w: schedule is %s", ErrConflict, s.State)
	default:
		return "", nil, fmt.Errorf("%w: unknown schedule action", ErrInvalidArgument)
	}
}

// operationScheduleAfterFire returns the schedule's state after one fire.
func operationScheduleAfterFire(s OperationSchedule, admitted bool, now time.Time) (string, *time.Time, error) {
	if s.Kind == api.OperationScheduleOnce {
		if admitted {
			return api.OperationScheduleCompleted, nil, nil
		}
		return api.OperationScheduleFailed, nil, nil
	}
	next, err := nextOperationScheduleFire(s, now)
	if err != nil {
		return "", nil, err
	}
	if next == nil {
		return api.OperationScheduleCompleted, nil, nil
	}
	return api.OperationScheduleActive, next, nil
}
