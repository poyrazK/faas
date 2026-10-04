package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/cronexpr"
)

// ScalingSchedule raises an app's warm floor for a recurring window
// (ADR-195).
//
// Cron expresses instants, not intervals, so a window is a fire plus a
// duration: each time Cron fires, MinInstances applies for DurationS seconds.
// That shape was chosen over a days/start/end triple because the platform
// already teaches cron (cron triggers, with per-plan limits in
// pkg/api/limits.go) and because it needs no special case for a window that
// crosses midnight — `0 22 * * *` for 8h is just a window, where
// `start: 22:00, end: 06:00` would need a wraps-midnight rule.
//
// Schedules only ever raise the floor. They cannot lower one and cannot cap
// MaxInstances: a rule that only adds capacity cannot take an app down, while
// a scheduled cap would throttle an app during an unforecast spike — exactly
// when nobody is available to remove it.
type ScalingSchedule struct {
	// Cron is a five-field expression evaluated in the policy's Timezone.
	Cron string `json:"cron,omitempty"`
	// DurationS is how long the window stays open after each fire.
	DurationS int `json:"duration_s,omitempty"`
	// MinInstances is the warm floor while the window is open.
	MinInstances int `json:"min_instances,omitempty"`
}

// Window reports whether this schedule's window is open at t, and is the one
// place the interval semantics live.
//
// robfig's Schedule has Next but no Prev, so "am I inside a window?" is
// answered forwards: the first fire strictly after t-duration is the only
// fire whose window could still be open at t, because any earlier fire's
// window has already elapsed. The window is open iff that fire is at or
// before t, which makes windows half-open — [fire, fire+duration).
//
// Returns false for a schedule that does not parse rather than erroring.
// Validation rejects a bad expression at write time; a stored row that
// somehow holds one must not wedge the scheduler sweep or, worse, be read as
// permanently active.
func (s ScalingSchedule) Window(t time.Time, timezone string) bool {
	if s.MinInstances <= 0 || s.DurationS <= 0 || s.Cron == "" {
		return false
	}
	schedule, err := cronexpr.Parse(s.Cron, timezone)
	if err != nil {
		return false
	}
	duration := time.Duration(s.DurationS) * time.Second
	fire := schedule.Next(t.Add(-duration))
	return !fire.After(t)
}

// ScheduledMinInstancesAt returns the highest floor demanded by the schedules
// whose windows are open at t, or 0 when none are.
//
// Overlapping windows take the max rather than the last match, so the order
// of the list carries no meaning and a customer cannot lose capacity by
// reordering it.
func (p *ScalingPolicy) ScheduledMinInstancesAt(t time.Time) int {
	if p == nil {
		return 0
	}
	high := 0
	for _, s := range p.Schedules {
		if !s.Window(t, p.Timezone) {
			continue
		}
		if s.MinInstances > high {
			high = s.MinInstances
		}
	}
	return high
}

// EffectiveMinInstanceSecondsInMinute integrates the maximum warm floor over
// the billed UTC minute. Schedule windows are half-open and may end partway
// through a minute; the static app and deployment floors remain lower bounds.
func (a *App) EffectiveMinInstanceSecondsInMinute(minute time.Time, deploymentFloor int) int64 {
	minute = minute.UTC().Truncate(time.Minute)
	var floors [60]int
	if a == nil {
		return int64(max(0, deploymentFloor)) * 60
	}
	policy := ScalingPolicyOrDefault(a.ScalingPolicy)
	base := max(0, a.MinInstances, policy.MinInstances, deploymentFloor)
	for i := range floors {
		floors[i] = base
	}
	for _, window := range policy.Schedules {
		if window.MinInstances <= 0 || window.DurationS <= 0 || window.Cron == "" {
			continue
		}
		schedule, err := cronexpr.Parse(window.Cron, policy.Timezone)
		if err != nil {
			continue
		}
		duration := time.Duration(window.DurationS) * time.Second
		// Parse each rule once and bound work to the minute's 60 seconds,
		// including rules whose recurring windows overlap for many hours.
		for i := range floors {
			if window.MinInstances <= floors[i] {
				continue
			}
			at := minute.Add(time.Duration(i) * time.Second)
			fire := schedule.Next(at.Add(-duration))
			if !fire.IsZero() && !fire.After(at) {
				floors[i] = window.MinInstances
			}
		}
	}
	var seconds int64
	for _, floor := range floors {
		seconds += int64(floor)
	}
	return seconds
}

// MaxReachableMinInstances is the largest floor this policy can ever demand:
// the static value and every schedule's value, regardless of the clock.
//
// The plan gate uses it rather than the static field alone. min_instances is
// a Hobby+ feature, so a Free app declaring `min_instances: 0` with a
// schedule of `min_instances: 3` would otherwise buy exactly the warm floor
// the gate exists to deny.
func (p *ScalingPolicy) MaxReachableMinInstances() int {
	if p == nil {
		return 0
	}
	high := p.MinInstances
	for _, s := range p.Schedules {
		if s.MinInstances > high {
			high = s.MinInstances
		}
	}
	return high
}
