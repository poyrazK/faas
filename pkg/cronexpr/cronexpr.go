// Package cronexpr owns Gregale's canonical cron grammar. It is deliberately
// independent of schedd so control-plane admission can validate customer
// intent without importing scheduler-owned code.
package cronexpr

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var ErrInvalidSchedule = errors.New("sched: invalid cron schedule")

const DefaultTimezone = "UTC"

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func NormalizeTimezone(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultTimezone, nil
	}
	location, err := time.LoadLocation(raw)
	if err != nil {
		return "", fmt.Errorf("sched: invalid timezone %q: %w", raw, err)
	}
	return location.String(), nil
}

// Parse validates a five-field expression and timezone and returns robfig's
// immutable schedule for scheduler callers that need to calculate fire times.
func Parse(raw, timezone string) (cron.Schedule, error) {
	if raw == "" {
		return nil, fmt.Errorf("%w: empty", ErrInvalidSchedule)
	}
	normalized, err := NormalizeTimezone(timezone)
	if err != nil {
		return nil, errors.Join(ErrInvalidSchedule, err)
	}
	parsed, err := parser.Parse("CRON_TZ=" + normalized + " " + raw)
	if err != nil {
		return nil, errors.Join(ErrInvalidSchedule, err)
	}
	// A day that never exists ("0 0 30 2 *", "0 0 31 4 *") parses, but
	// robfig's Next then returns the zero time. Every caller compares that
	// against the clock as a real instant: the cron dispatcher fired such a
	// schedule on every tick, and a scaling schedule's warm-floor window
	// stayed open forever. Next's five-year horizon covers every calendar
	// date that does occur (29 February included), so zero means never.
	if parsed.Next(time.Now()).IsZero() {
		return nil, fmt.Errorf("%w: %q never fires", ErrInvalidSchedule, raw)
	}
	spec, ok := parsed.(*cron.SpecSchedule)
	if !ok || !fixedWallTime(raw) {
		return parsed, nil
	}
	return dstSchedule{spec: spec}, nil
}

// fixedWallTime reports whether the expression names specific wall-clock
// times: neither its minute nor its hour field uses '*' (or '?'). That is
// Vixie cron's test for which jobs get daylight-saving adjustment; interval
// jobs ("*/5 * * * *", "0 * * * *") simply follow the clock.
func fixedWallTime(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) != 5 {
		return false
	}
	return !strings.ContainsAny(fields[0], "*?") && !strings.ContainsAny(fields[1], "*?")
}

// dstSchedule gives fixed-time schedules the daylight-saving behaviour of
// Vixie cron. robfig matches wall-clock fields literally, so in a zone with
// DST a daily "30 2 * * *" never ran on the spring-forward day (02:30 does
// not exist) and ran twice on the fall-back day (02:30 happens twice) — a
// once-a-day job with side effects silently doubled once a year.
//
//   - A wall time skipped by a spring-forward gap runs at the first instant
//     after the gap.
//   - The second pass through a repeated fall-back hour does not re-run a
//     wall time that already fired.
type dstSchedule struct {
	spec *cron.SpecSchedule
}

func (s dstSchedule) Next(t time.Time) time.Time {
	n := s.spec.Next(t)
	if n.IsZero() {
		return n
	}
	if gap, ok := s.skippedGapFire(t, n); ok {
		return gap
	}
	for i := 0; i < 4 && s.repeatsEarlierWallTime(n); i++ {
		next := s.spec.Next(n)
		if next.IsZero() {
			return next
		}
		n = next
	}
	return n
}

func (s dstSchedule) location(t time.Time) *time.Location {
	if s.spec.Location == nil || s.spec.Location == time.Local {
		return t.Location()
	}
	return s.spec.Location
}

// repeatsEarlierWallTime reports whether n is the second occurrence of its
// wall-clock minute: the clock was set back and the same local time already
// happened under the earlier, larger UTC offset.
func (s dstSchedule) repeatsEarlierWallTime(n time.Time) bool {
	loc := s.location(n)
	_, offNow := n.In(loc).Zone()
	_, offBefore := n.Add(-3 * time.Hour).In(loc).Zone()
	if offBefore <= offNow {
		return false
	}
	earlier := n.Add(-time.Duration(offBefore-offNow) * time.Second)
	return sameWallMinute(earlier.In(loc), n.In(loc))
}

// skippedGapFire finds a spring-forward gap in (t, n) that swallowed a
// matching wall time and returns the first instant after that gap.
func (s dstSchedule) skippedGapFire(t, n time.Time) (time.Time, bool) {
	loc := s.location(t)
	// Offsets change at most a few times a year; walk day boundaries and
	// bisect any change. Bounded so a sparse schedule cannot make Next slow.
	const maxDays = 400
	day := t
	for i := 0; i < maxDays && day.Before(n); i++ {
		next := day.Add(24 * time.Hour)
		if next.After(n) {
			next = n
		}
		_, offA := day.In(loc).Zone()
		_, offB := next.In(loc).Zone()
		if offB > offA {
			gap := firstInstantWithOffset(day, next, loc, offB)
			if gap.After(t) && gap.Before(n) && s.matchesSkippedWallTime(gap, offA, offB, loc) {
				return gap, true
			}
		}
		day = next
	}
	return time.Time{}, false
}

// firstInstantWithOffset bisects (lo, hi] for the first instant whose offset
// in loc is off; offsets change on whole seconds.
func firstInstantWithOffset(lo, hi time.Time, loc *time.Location, off int) time.Time {
	for hi.Sub(lo) > time.Second {
		mid := lo.Add(hi.Sub(lo) / 2)
		if _, o := mid.In(loc).Zone(); o == off {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi.Truncate(time.Second)
}

// matchesSkippedWallTime reports whether any wall-clock minute swallowed by
// the gap starting at gap (offset offA before, offB after) matches the spec.
func (s dstSchedule) matchesSkippedWallTime(gap time.Time, offA, offB int, loc *time.Location) bool {
	// The skipped wall times are [gap as read under offA, gap as read under offB).
	start := gap.In(time.FixedZone("", offA))
	for m := 0; m < (offB-offA)/60; m++ {
		w := start.Add(time.Duration(m) * time.Minute)
		if s.spec.Month&(1<<uint(w.Month())) == 0 ||
			s.spec.Hour&(1<<uint(w.Hour())) == 0 ||
			s.spec.Minute&(1<<uint(w.Minute())) == 0 ||
			!dayMatches(s.spec, w) {
			continue
		}
		return true
	}
	return false
}

// dayMatches mirrors robfig's day-of-month / day-of-week rule: when either
// field is '*' both must match, otherwise either may.
func dayMatches(s *cron.SpecSchedule, t time.Time) bool {
	const starBit = 1 << 63
	domMatch := 1<<uint(t.Day())&s.Dom > 0
	dowMatch := 1<<uint(t.Weekday())&s.Dow > 0
	if s.Dom&starBit > 0 || s.Dow&starBit > 0 {
		return domMatch && dowMatch
	}
	return domMatch || dowMatch
}

func sameWallMinute(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day() &&
		a.Hour() == b.Hour() && a.Minute() == b.Minute()
}

func Validate(raw string) error {
	_, err := Parse(raw, DefaultTimezone)
	return err
}
