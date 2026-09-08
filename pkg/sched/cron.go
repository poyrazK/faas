package sched

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// ErrInvalidSchedule is returned by ParseSchedule for malformed cron
// expressions. The stringified error carries the parser message so
// apid's API layer can surface it in the 400 response.
var ErrInvalidSchedule = errors.New("sched: invalid cron schedule")

// DefaultCronTimezone is used for legacy rows and requests that omit a
// timezone. UTC keeps behavior deterministic across schedd hosts.
const DefaultCronTimezone = "UTC"

// cronParser is the package-private parser used by ParseSchedule. The
// 5-field syntax (minute hour day-of-month month day-of-week) is what
// the apid create-cron endpoint documents; we keep the descriptor in
// one place so a future 6-field cron (with seconds) is a one-line edit.
var cronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

// Schedule wraps a parsed cron schedule. Holds a sentinel "next fire"
// cache so we can answer "when does this next fire?" cheaply on every
// scheduler tick (the loop runs every minute; recomputing parse each
// call is fine for our load but a cache lets us swap parsers later).
type Schedule struct {
	raw  string
	next cron.Schedule
}

// ParseSchedule validates the cron expression and returns a Schedule
// suitable for NextFireAt. Empty string is rejected; a non-empty
// malformed string returns ErrInvalidSchedule wrapping the parser's
// own diagnostic.
func ParseSchedule(raw string) (*Schedule, error) {
	return ParseScheduleWithTimezone(raw, DefaultCronTimezone)
}

// NormalizeTimezone validates an IANA timezone name and applies the UTC
// default used by legacy cron rows. The returned name is the location's
// canonical String value, suitable for CRON_TZ= parsing and persistence.
func NormalizeTimezone(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultCronTimezone, nil
	}
	loc, err := time.LoadLocation(raw)
	if err != nil {
		return "", fmt.Errorf("sched: invalid timezone %q: %w", raw, err)
	}
	return loc.String(), nil
}

// ParseScheduleWithTimezone validates a 5-field expression in the supplied
// IANA timezone. robfig/cron's CRON_TZ prefix gives each schedule its own DST
// rules while preserving the raw expression on the API object.
func ParseScheduleWithTimezone(raw, timezone string) (*Schedule, error) {
	if raw == "" {
		return nil, fmt.Errorf("%w: empty", ErrInvalidSchedule)
	}
	normalized, err := NormalizeTimezone(timezone)
	if err != nil {
		return nil, errors.Join(ErrInvalidSchedule, err)
	}
	spec := "CRON_TZ=" + normalized + " " + raw
	parsed, err := cronParser.Parse(spec)
	if err != nil {
		// errorlint: errors.Join keeps ErrInvalidSchedule in the chain
		// (so `errors.Is` matches) and surfaces the parser message
		// without dropping the wrap.
		return nil, errors.Join(ErrInvalidSchedule, err)
	}
	return &Schedule{raw: raw, next: parsed}, nil
}

// Raw returns the original expression. Used in pg_notify payloads.
func (s *Schedule) Raw() string { return s.raw }

// NextFireAt returns the next fire time strictly after `from`. The
// robfig parser treats `from` as exclusive — calling
// ParseSchedule("*/5 * * * *").NextFireAt(12:00:00) returns 12:05, not
// 12:00. This matches the spec convention: the cron fires *after* its
// minute boundary.
func (s *Schedule) NextFireAt(from time.Time) time.Time {
	return s.next.Next(from)
}
