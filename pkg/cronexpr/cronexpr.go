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
	return parsed, nil
}

func Validate(raw string) error {
	_, err := Parse(raw, DefaultTimezone)
	return err
}
