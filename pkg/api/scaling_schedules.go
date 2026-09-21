package api

import (
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/cronexpr"
)

// ValidateScalingSchedules checks a declared schedule list (ADR-195): the
// cron grammar, the timezone, the window bounds, and the list size.
//
// It does NOT check the floor values against the plan. That is deliberate:
// the plan gate needs the account, and it must compare against
// MaxReachableMinInstances — the static floor and every schedule's floor
// together — so it lives in the handler beside the other plan gates rather
// than being half-applied here.
//
// One implementation, called by the manifest loader, the PATCH handler and
// the deploy-diff quota gate. ADR-194 shipped after three hand-maintained
// copies of a metric set had drifted into three different answers; a second
// customer-facing list is not going to repeat it.
func ValidateScalingSchedules(field, timezone string, schedules []ScalingSchedule) *Problem {
	if len(schedules) == 0 {
		// An empty list with a timezone set is harmless but meaningless.
		// Validate the zone anyway so a typo surfaces at write time
		// rather than the first time a schedule is added.
		return validateScheduleTimezone(timezone)
	}
	if len(schedules) > MaxScalingSchedules {
		return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
			"Invalid scaling policy",
			fmt.Sprintf("%s declares %d schedules; at most %d are allowed.",
				field, len(schedules), MaxScalingSchedules))
	}
	if problem := validateScheduleTimezone(timezone); problem != nil {
		return problem
	}
	for i, s := range schedules {
		if s.Cron == "" {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid scaling policy",
				fmt.Sprintf("%s[%d].cron is required.", field, i))
		}
		// Parse with the policy's timezone rather than validating the
		// expression alone: cronexpr.Parse is what the scheduler calls,
		// so anything it rejects at read time must be rejected here.
		if _, err := cronexpr.Parse(s.Cron, timezone); err != nil {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid scaling policy",
				fmt.Sprintf("%s[%d].cron %q is not a valid five-field cron expression: %v.",
					field, i, s.Cron, err))
		}
		if s.DurationS < MinScalingScheduleDurationS || s.DurationS > MaxScalingScheduleDurationS {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid scaling policy",
				fmt.Sprintf("%s[%d].duration_s must be between %d and %d seconds; got %d.",
					field, i, MinScalingScheduleDurationS, MaxScalingScheduleDurationS, s.DurationS))
		}
		// A schedule exists to raise the floor. Zero would be a no-op the
		// author almost certainly believed was doing something, and a
		// negative value has no meaning at all — neither can lower a
		// floor, because schedules compose by max.
		if s.MinInstances <= 0 {
			return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
				"Invalid scaling policy",
				fmt.Sprintf("%s[%d].min_instances must be > 0; a schedule raises the floor and cannot lower it. Got %d.",
					field, i, s.MinInstances))
		}
	}
	return nil
}

func validateScheduleTimezone(timezone string) *Problem {
	if timezone == "" {
		return nil
	}
	if _, err := cronexpr.NormalizeTimezone(timezone); err != nil {
		return NewProblem(http.StatusUnprocessableEntity, CodeValidation,
			"Invalid scaling policy",
			fmt.Sprintf("timezone %q is not a valid IANA zone (for example Europe/Istanbul or UTC).", timezone))
	}
	return nil
}

// MaxReachableMinInstances is the largest warm floor a policy can demand at
// any instant: its static min_instances, or any schedule's, whichever is
// highest.
//
// Every plan gate on min_instances reads this rather than the static field.
// min_instances is a Hobby+ feature with a per-plan cap (Hobby 1, Pro 3,
// Scale 10), and a schedule buys exactly the same warm capacity — so a Free
// app declaring `min_instances: 0` alongside a schedule of `min_instances: 3`
// would otherwise walk straight through the gate that exists to deny it.
func (s *ScalingPolicy) MaxReachableMinInstances() int {
	if s == nil {
		return 0
	}
	high := s.MinInstances
	for _, sched := range s.Schedules {
		if sched.MinInstances > high {
			high = sched.MinInstances
		}
	}
	return high
}
