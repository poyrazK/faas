// Package freeze decides when deploy freezes withhold production releases
// (ADR-20261019071733). It holds no state: apid reads freezes from the store
// and asks this package which ones are active.
package freeze

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// NextHorizon is how far ahead Status looks for the next freeze.
const NextHorizon = 7 * 24 * time.Hour

var dayNames = [7]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// ProductionEnvironment is the project environment whose promotions are
// production releases.
const ProductionEnvironment = "production"

// IsProductionScope reports whether a deployment in scope is a production
// release: the default scope, or the production environment's scope that
// project promotions deploy into.
func IsProductionScope(scope string) bool {
	return scope == "" || scope == api.DefaultEnvScope || scope == ProductionEnvironment
}

// Covers reports whether f applies to appID: an app freeze covers its app,
// an account freeze covers every app.
func Covers(f state.ReleaseFreeze, appID string) bool {
	return f.AppID == "" || f.AppID == appID
}

// IsActive reports whether f withholds releases at now. After-rollback
// policies are never active themselves.
func IsActive(f state.ReleaseFreeze, now time.Time) bool {
	if f.LiftedAt != nil {
		return false
	}
	switch f.Kind {
	case state.ReleaseFreezeWindow:
		return f.StartsAt != nil && !now.Before(*f.StartsAt) && (f.EndsAt == nil || now.Before(*f.EndsAt))
	case state.ReleaseFreezeWeekly:
		_, ok := weeklyOccurrence(f, now, false)
		return ok
	}
	return false
}

// Active returns the freezes that withhold production releases of appID at
// now, in input order.
func Active(freezes []state.ReleaseFreeze, appID string, now time.Time) []state.ReleaseFreeze {
	var out []state.ReleaseFreeze
	for _, f := range freezes {
		if Covers(f, appID) && IsActive(f, now) {
			out = append(out, f)
		}
	}
	return out
}

// ActiveFor reads the account's freezes and returns those that withhold a
// production release of appID in scope. A store without freezes withholds
// nothing.
func ActiveFor(ctx context.Context, store any, accountID, appID, scope string, now time.Time) ([]state.ReleaseFreeze, error) {
	if !IsProductionScope(scope) {
		return nil, nil
	}
	freezes, ok := store.(state.ReleaseFreezeStore)
	if !ok {
		return nil, nil
	}
	all, err := freezes.ListReleaseFreezes(ctx, accountID, now)
	if err != nil {
		return nil, fmt.Errorf("list release freezes: %w", err)
	}
	return Active(all, appID, now), nil
}

// Summary describes active freezes in one line for surfaces without RFC 7807
// problems, such as a GitHub commit status.
func Summary(appSlug string, active []state.ReleaseFreeze) string {
	parts := make([]string, 0, len(active))
	for _, f := range active {
		until := "until lifted"
		if f.EndsAt != nil {
			until = "until " + f.EndsAt.UTC().Format(time.RFC3339)
		} else if f.Kind == state.ReleaseFreezeWeekly {
			until = "during its weekly window"
		}
		parts = append(parts, fmt.Sprintf("%q %s", f.Reason, until))
	}
	return fmt.Sprintf("production releases of %s are frozen: %s; lift the freeze or deploy with --override-freeze", appSlug, strings.Join(parts, "; "))
}

// NextStart reports when f next starts after now, within NextHorizon. An
// active freeze has no next start.
func NextStart(f state.ReleaseFreeze, now time.Time) (time.Time, bool) {
	if f.LiftedAt != nil || IsActive(f, now) {
		return time.Time{}, false
	}
	switch f.Kind {
	case state.ReleaseFreezeWindow:
		if f.StartsAt != nil && f.StartsAt.After(now) && f.StartsAt.Sub(now) <= NextHorizon {
			return *f.StartsAt, true
		}
	case state.ReleaseFreezeWeekly:
		return weeklyOccurrence(f, now, true)
	}
	return time.Time{}, false
}

// weeklyOccurrence finds the weekly occurrence containing now (next=false)
// or the first one starting after now (next=true). Occurrences are built in
// the freeze's time zone from wall-clock fields, so they follow daylight
// saving changes; one may last up to a week, so the search spans a week back
// and a week ahead.
func weeklyOccurrence(f state.ReleaseFreeze, now time.Time, next bool) (time.Time, bool) {
	loc, err := time.LoadLocation(f.TimeZone)
	if err != nil || f.WeeklyDays == 0 || f.WeeklyDurationMinutes <= 0 {
		return time.Time{}, false
	}
	local := now.In(loc)
	for offset := -7; offset <= 7; offset++ {
		day := time.Date(local.Year(), local.Month(), local.Day()+offset, 0, 0, 0, 0, loc)
		if f.WeeklyDays&(1<<int(day.Weekday())) == 0 {
			continue
		}
		start := time.Date(day.Year(), day.Month(), day.Day(), f.WeeklyStartMinute/60, f.WeeklyStartMinute%60, 0, 0, loc)
		end := start.Add(time.Duration(f.WeeklyDurationMinutes) * time.Minute)
		if !next && !now.Before(start) && now.Before(end) {
			return start, true
		}
		if next && start.After(now) {
			return start, true
		}
	}
	return time.Time{}, false
}

// AfterRollbackPolicy returns the longest after-rollback policy covering
// appID.
func AfterRollbackPolicy(freezes []state.ReleaseFreeze, appID string) (state.ReleaseFreeze, bool) {
	var best state.ReleaseFreeze
	found := false
	for _, f := range freezes {
		if f.Kind == state.ReleaseFreezeAfterRollback && f.LiftedAt == nil && Covers(f, appID) &&
			(!found || f.AfterRollbackMinutes > best.AfterRollbackMinutes) {
			best, found = f, true
		}
	}
	return best, found
}

// ValidateReason checks a freeze or override reason.
func ValidateReason(field, reason string) *api.Problem {
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > api.ReleaseFreezeReasonMax {
		return api.ErrValidation(fmt.Sprintf("%s must be 1-%d characters", field, api.ReleaseFreezeReasonMax))
	}
	return nil
}

// FromRequest validates a create request. appID is the resolved app (empty
// for an account-wide freeze).
func FromRequest(req api.CreateReleaseFreezeRequest, accountID, appID, createdBy string, now time.Time) (state.ReleaseFreeze, *api.Problem) {
	if p := ValidateReason("reason", req.Reason); p != nil {
		return state.ReleaseFreeze{}, p
	}
	f := state.ReleaseFreeze{AccountID: accountID, AppID: appID, Kind: req.Kind,
		Reason: strings.TrimSpace(req.Reason), CreatedBy: createdBy}
	switch req.Kind {
	case api.ReleaseFreezeKindWindow:
		if req.Weekly != nil || req.AfterRollbackMinutes != 0 {
			return f, api.ErrValidation("a window freeze takes only starts_at and ends_at")
		}
		start := now.UTC()
		if req.StartsAt != "" {
			parsed, err := time.Parse(time.RFC3339, req.StartsAt)
			if err != nil {
				return f, api.ErrValidation("starts_at must be an RFC 3339 time")
			}
			start = parsed.UTC()
		}
		f.StartsAt = &start
		if req.EndsAt != "" {
			end, err := time.Parse(time.RFC3339, req.EndsAt)
			if err != nil {
				return f, api.ErrValidation("ends_at must be an RFC 3339 time")
			}
			end = end.UTC()
			if !end.After(start) || !end.After(now) {
				return f, api.ErrValidation("ends_at must be after starts_at and in the future")
			}
			f.EndsAt = &end
		}
	case api.ReleaseFreezeKindWeekly:
		if req.Weekly == nil || req.StartsAt != "" || req.EndsAt != "" || req.AfterRollbackMinutes != 0 {
			return f, api.ErrValidation("a weekly freeze takes only weekly")
		}
		days, start, p := parseWeekly(*req.Weekly)
		if p != nil {
			return f, p
		}
		f.WeeklyDays, f.WeeklyStartMinute = days, start
		f.WeeklyDurationMinutes, f.TimeZone = req.Weekly.DurationMinutes, req.Weekly.TimeZone
	case api.ReleaseFreezeKindAfterRollback:
		if req.Weekly != nil || req.StartsAt != "" || req.EndsAt != "" {
			return f, api.ErrValidation("an after_rollback freeze takes only after_rollback_minutes")
		}
		max := int(api.ReleaseFreezeAfterRollbackMax / time.Minute)
		if req.AfterRollbackMinutes < 1 || req.AfterRollbackMinutes > max {
			return f, api.ErrValidation(fmt.Sprintf("after_rollback_minutes must be 1-%d", max))
		}
		f.AfterRollbackMinutes = req.AfterRollbackMinutes
	default:
		return f, api.ErrValidation("kind must be window, weekly, or after_rollback")
	}
	return f, nil
}

func parseWeekly(w api.ReleaseFreezeWeekly) (int, int, *api.Problem) {
	if len(w.Days) == 0 {
		return 0, 0, api.ErrValidation("weekly.days must name at least one day (sun..sat)")
	}
	days := 0
	for _, d := range w.Days {
		found := false
		for i, name := range dayNames {
			if strings.EqualFold(strings.TrimSpace(d), name) {
				days |= 1 << i
				found = true
			}
		}
		if !found {
			return 0, 0, api.ErrValidation(fmt.Sprintf("weekly.days: %q is not one of sun, mon, tue, wed, thu, fri, sat", d))
		}
	}
	start, ok := parseClock(w.Start)
	if !ok {
		return 0, 0, api.ErrValidation("weekly.start must be HH:MM (00:00-23:59)")
	}
	if w.DurationMinutes < 1 || w.DurationMinutes > api.ReleaseFreezeWeeklyMaxMinutes {
		return 0, 0, api.ErrValidation(fmt.Sprintf("weekly.duration_minutes must be 1-%d", api.ReleaseFreezeWeeklyMaxMinutes))
	}
	if w.TimeZone == "" || len(w.TimeZone) > 64 {
		return 0, 0, api.ErrValidation("weekly.time_zone must be an IANA time zone, such as Europe/Istanbul or UTC")
	}
	if _, err := time.LoadLocation(w.TimeZone); err != nil {
		return 0, 0, api.ErrValidation(fmt.Sprintf("weekly.time_zone: unknown time zone %q", w.TimeZone))
	}
	return days, start, nil
}

func parseClock(v string) (int, bool) {
	h, m, ok := strings.Cut(strings.TrimSpace(v), ":")
	if !ok || len(h) != 2 || len(m) != 2 {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// View renders f for the API; appSlug is the covered app's slug.
func View(f state.ReleaseFreeze, appSlug string, now time.Time) api.ReleaseFreeze {
	out := api.ReleaseFreeze{ID: f.ID, App: appSlug, Kind: f.Kind, Reason: f.Reason, Source: f.Source,
		SourceDeploymentID: f.SourceDeploymentID, CreatedBy: f.CreatedBy,
		CreatedAt: f.CreatedAt.UTC().Format(time.RFC3339), AfterRollbackMinutes: f.AfterRollbackMinutes,
		Active: IsActive(f, now)}
	if f.StartsAt != nil {
		out.StartsAt = f.StartsAt.UTC().Format(time.RFC3339)
	}
	if f.EndsAt != nil {
		out.EndsAt = f.EndsAt.UTC().Format(time.RFC3339)
	}
	if f.Kind == state.ReleaseFreezeWeekly {
		w := &api.ReleaseFreezeWeekly{Start: fmt.Sprintf("%02d:%02d", f.WeeklyStartMinute/60, f.WeeklyStartMinute%60),
			DurationMinutes: f.WeeklyDurationMinutes, TimeZone: f.TimeZone}
		for i, name := range dayNames {
			if f.WeeklyDays&(1<<i) != 0 {
				w.Days = append(w.Days, name)
			}
		}
		out.Weekly = w
	}
	if next, ok := NextStart(f, now); ok {
		out.NextStartsAt = next.UTC().Format(time.RFC3339)
	}
	return out
}
