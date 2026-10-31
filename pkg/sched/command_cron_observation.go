package sched

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type commandCronClass int

const (
	commandCronDue commandCronClass = iota
	commandCronOverlapDue
	commandCronReplaceWait
	commandCronDeploymentMissing
	commandCronExclusiveDue
	commandCronDisabled
	commandCronSuspended
	commandCronAccountHeld
	commandCronNotDue
	commandCronUnknown
)

func (c commandCronClass) String() string {
	switch c {
	case commandCronDue:
		return "due"
	case commandCronOverlapDue:
		return "overlap_due"
	case commandCronReplaceWait:
		return "replacement_wait"
	case commandCronDeploymentMissing:
		return "deployment_missing"
	case commandCronExclusiveDue:
		return "exclusive_due"
	case commandCronDisabled:
		return "disabled"
	case commandCronSuspended:
		return "suspended"
	case commandCronAccountHeld:
		return "account_held"
	case commandCronNotDue:
		return "not_due"
	default:
		return "unknown"
	}
}

type commandCronSnapshot [10]state.AppTaskObservationCohort

// observeCommandCrons parses the canonical timezone/DST grammar once per row.
// Only the first unconsumed boundary is needed: it is NOT the time at which a
// command became eligible. Actual deadline/coalescing/overlap outcomes come
// from committed occurrence returns, never an observer's prediction.
func observeCommandCrons(ctx context.Context, rows []state.CommandCronObservation, now time.Time) (commandCronSnapshot, error) {
	var out commandCronSnapshot
	for _, r := range rows {
		if err := ctx.Err(); err != nil {
			return commandCronSnapshot{}, err
		}
		c, at := classifyCommandCron(r, now)
		p := &out[c]
		p.Pending++
		if at.IsZero() {
			p.UnknownAge++
		} else if p.OldestAt.IsZero() || at.Before(p.OldestAt) {
			p.OldestAt = at
		}
	}
	return out, nil
}
func classifyCommandCron(r state.CommandCronObservation, now time.Time) (commandCronClass, time.Time) {
	if !r.Enabled {
		return commandCronDisabled, time.Time{}
	}
	if r.Suspended || r.AppDeleted {
		return commandCronSuspended, time.Time{}
	}
	if !r.AccountActive {
		return commandCronAccountHeld, time.Time{}
	}
	if !r.TimingValid || !r.PolicyValid || !r.BindingValid || !r.CreatedAt.After(time.Unix(0, 0)) || r.CreatedAt.After(now) ||
		(!r.LastFiredAt.IsZero() && (!r.LastFiredAt.After(time.Unix(0, 0)) || r.LastFiredAt.After(now))) {
		return commandCronUnknown, time.Time{}
	}
	schedule, err := ParseScheduleWithTimezone(r.Schedule, r.Timezone)
	if err != nil {
		return commandCronUnknown, time.Time{}
	}
	cursor := r.LastFiredAt
	if cursor.IsZero() {
		cursor = r.CreatedAt
	}
	// The scheduler counts from a later schedule anchor (ADR-20261026201726).
	if r.ScheduleAnchorAt.After(cursor) {
		cursor = r.ScheduleAnchorAt
	}
	due := schedule.NextFireAt(cursor)
	if due.IsZero() || !due.After(cursor) {
		return commandCronUnknown, time.Time{}
	}
	if due.After(now) {
		return commandCronNotDue, time.Time{}
	}
	overlap := "allow"
	if r.SkipIfRunning {
		overlap = "skip"
	}
	if r.Policy != nil {
		overlap = r.Policy.Overlap
	}
	if !r.ExclusiveBound && overlap == "replace" && r.ActiveTasks {
		return commandCronReplaceWait, due
	}
	if (overlap != "allow" && r.ActiveTasks && !r.ExclusiveBound) || (r.ExclusiveBound && (overlap != "allow" || r.SkipIfRunning) && r.ActiveOrdinaryTasks) {
		return commandCronOverlapDue, due
	}
	if !r.LiveDeployment {
		return commandCronDeploymentMissing, due
	}
	if r.ExclusiveBound {
		return commandCronExclusiveDue, due
	}
	return commandCronDue, due
}
func validateCommandCronSnapshot(s commandCronSnapshot, now time.Time) error {
	for c, p := range s {
		known := p.Pending - p.UnknownAge
		unknownOnly := c >= int(commandCronDisabled)
		if p.Pending < 0 || p.UnknownAge < 0 || p.UnknownAge > p.Pending || (unknownOnly && known != 0) ||
			!(known == 0 && p.OldestAt.IsZero() || known > 0 && primeUnix(p.OldestAt) > 0 && !p.OldestAt.After(now)) {
			return errors.New("inconsistent command cron observation")
		}
	}
	return nil
}
