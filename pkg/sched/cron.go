package sched

import (
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// ErrInvalidSchedule is returned by ParseSchedule for malformed cron
// expressions. The stringified error carries the parser message so
// apid's API layer can surface it in the 400 response.
var ErrInvalidSchedule = errors.New("sched: invalid cron schedule")

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
	if raw == "" {
		return nil, fmt.Errorf("%w: empty", ErrInvalidSchedule)
	}
	parsed, err := cronParser.Parse(raw)
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
