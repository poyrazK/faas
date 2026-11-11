package support

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"slices"
	"sort"
	"time"
)

func (c Calendar) validate() error {
	if c.Timezone == "Local" {
		return invalid("calendar timezone must be explicit, not host-local")
	}
	if c.Timezone == "" {
		c.Timezone = "UTC"
	}
	if _, e := time.LoadLocation(c.Timezone); e != nil {
		return invalid("calendar timezone must be an IANA timezone")
	}
	if len(c.Weekdays) == 0 {
		if len(c.Holidays) > 0 {
			return invalid("holidays require a working-week calendar")
		}
		return nil
	}
	if len(c.Weekdays) > 7 || c.StartMinute < 0 || c.EndMinute > 24*60 || c.EndMinute <= c.StartMinute {
		return invalid("invalid daily working window")
	}
	seen := map[int]bool{}
	for _, d := range c.Weekdays {
		if d < 0 || d > 6 || seen[d] {
			return invalid("invalid or duplicate weekday")
		}
		seen[d] = true
	}
	if len(c.Holidays) > api.SupportCalendarHolidaysMax {
		return invalid("too many holidays")
	}
	for _, d := range c.Holidays {
		if _, e := time.Parse("2006-01-02", d); e != nil {
			return invalid("holidays must be YYYY-MM-DD")
		}
	}
	return nil
}

// Civil dates are represented in UTC so skipped local dates cannot stall iteration.
func civil(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

type interval struct{ start, end time.Time }

// Intersect the requested local wall interval with each constant-offset zone
// interval. This handles missing and repeated hours, including partial windows.
func (c Calendar) windows(day time.Time) []interval {
	if !slices.Contains(c.Weekdays, int(day.Weekday())) || slices.Contains(c.Holidays, day.Format("2006-01-02")) {
		return nil
	}
	loc := c.location()
	base := civil(day)
	cursor := base.Add(-24 * time.Hour)
	horizon := base.Add(48 * time.Hour)
	out := []interval{}
	for cursor.Before(horizon) {
		local := cursor.In(loc)
		_, offset := local.Zone()
		zoneStart, zoneEnd := local.ZoneBounds()
		a := base.Add(time.Duration(c.StartMinute)*time.Minute - time.Duration(offset)*time.Second)
		b := base.Add(time.Duration(c.EndMinute)*time.Minute - time.Duration(offset)*time.Second)
		if !zoneStart.IsZero() && a.Before(zoneStart) {
			a = zoneStart
		}
		if !zoneEnd.IsZero() && b.After(zoneEnd) {
			b = zoneEnd
		}
		if b.After(a) {
			out = append(out, interval{a, b})
		}
		if zoneEnd.IsZero() || !zoneEnd.After(cursor) {
			break
		}
		cursor = zoneEnd
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start.Before(out[j].start) })
	return out
}
func (c Calendar) location() *time.Location {
	loc, e := time.LoadLocation(c.Timezone)
	if e != nil {
		return time.UTC
	}
	return loc
}
func (c Calendar) deadline(start time.Time, budget time.Duration) (time.Time, error) {
	if budget <= 0 {
		return start, nil
	}
	if len(c.Weekdays) == 0 {
		return start.Add(budget), nil
	}
	day := civil(start.In(c.location()))
	for range api.SupportCalendarSearchDays {
		for _, w := range c.windows(day) {
			a, b := w.start, w.end
			if a.Before(start) {
				a = start
			}
			if !b.After(a) {
				continue
			}
			span := b.Sub(a)
			if budget <= span {
				return a.Add(budget).UTC(), nil
			}
			budget -= span
		}
		day = day.AddDate(0, 0, 1)
	}
	return time.Time{}, fmt.Errorf("support calendar exceeds search horizon")
}
func (c Calendar) working(start, end time.Time) time.Duration {
	if !end.After(start) {
		return 0
	}
	if len(c.Weekdays) == 0 {
		return end.Sub(start)
	}
	loc := c.location()
	day, last := civil(start.In(loc)), civil(end.In(loc))
	var total time.Duration
	for range api.SupportCalendarSearchDays {
		if day.After(last) {
			break
		}
		for _, w := range c.windows(day) {
			a, b := w.start, w.end
			if a.Before(start) {
				a = start
			}
			if b.After(end) {
				b = end
			}
			if b.After(a) {
				total += b.Sub(a)
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return total
}
func (c Calendar) onDuty(now time.Time) bool {
	if len(c.Weekdays) == 0 {
		return true
	}
	for _, w := range c.windows(civil(now.In(c.location()))) {
		if !now.Before(w.start) && now.Before(w.end) {
			return true
		}
	}
	return false
}
func newEpisode(p Policy, plan, priority, category string, now time.Time) (Episode, error) {
	e := Episode{ID: newID(), StartedAt: now, Plan: plan, Priority: priority, Category: category, Policy: p}
	var err error
	e.FirstResponseDue, err = p.Calendar.deadline(now, time.Duration(p.FirstResponseSeconds)*time.Second)
	if err != nil {
		return e, err
	}
	e.ResolutionDue, err = p.Calendar.deadline(now, time.Duration(p.ResolutionSeconds)*time.Second)
	return e, err
}
func (e *Episode) observe(now time.Time) {
	if e.ResolvedAt == nil && e.WaitSince == nil && now.After(e.ResolutionDue) {
		e.ResolutionBreached = true
	}
}
func (e *Episode) reply(now time.Time) {
	if e.FirstResponseAt == nil {
		e.FirstResponseAt = &now
	}
	if e.NextReplyDue != nil {
		e.NextReplyCompleted++
		if !now.After(*e.NextReplyDue) {
			e.NextReplyMet++
		}
		e.NextReplyDue = nil
	}
}
func (e *Episode) pause(now time.Time) error {
	if e.WaitSince != nil {
		return conflict("already waiting for customer")
	}
	if e.NextReplyDue != nil || e.FirstResponseAt == nil {
		return conflict("reply before waiting for the customer")
	}
	e.observe(now)
	e.RemainingUS = e.Policy.Calendar.working(now, e.ResolutionDue).Microseconds()
	e.WaitSince = &now
	return nil
}
func (e *Episode) resume(now time.Time) error {
	if e.WaitSince == nil {
		return nil
	}
	e.PausedUS += e.Policy.Calendar.working(*e.WaitSince, now).Microseconds()
	e.WaitSince = nil
	due, err := e.Policy.Calendar.deadline(now, time.Duration(e.RemainingUS)*time.Microsecond)
	e.ResolutionDue = due
	return err
}
func (e *Episode) customerReply(now time.Time) error {
	if err := e.resume(now); err != nil {
		return err
	}
	if e.FirstResponseAt == nil {
		return nil
	}
	if e.NextReplyDue == nil {
		due, err := e.Policy.Calendar.deadline(now, time.Duration(e.Policy.NextReplySeconds)*time.Second)
		if err != nil {
			return err
		}
		e.NextReplyDue = &due
	}
	return nil
}
func (e *Episode) resolve(now time.Time) error {
	if e.ResolvedAt != nil {
		return nil
	}
	if err := e.resume(now); err != nil {
		return err
	}
	e.observe(now)
	e.ResolvedAt = &now
	return nil
}
