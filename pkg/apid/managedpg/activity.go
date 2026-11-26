package managedpg

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// ActivityService is the managed PostgreSQL service's activity surface;
// *managedpostgres.Service satisfies it.
type ActivityService interface {
	Activity(ctx context.Context, accountID, databaseID string, since, now time.Time) (managedpostgres.DatabaseActivity, error)
}

// Activity serves GET /v1/postgres/databases/{id}/activity: why the
// database was awake (ADR-20261125231222).
type Activity struct {
	// Service is nil when managed PostgreSQL is not configured.
	Service       ActivityService
	Problem       func(http.ResponseWriter, error)
	NotConfigured func(http.ResponseWriter)
	Now           func() time.Time
}

// Get reads the period ?since= names: a duration back from now (24h, 7d)
// or an RFC 3339 time, by default the last day.
func (h Activity) Get(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	if h.Service == nil {
		h.NotConfigured(w)
		return
	}
	now := time.Now().UTC()
	if h.Now != nil {
		now = h.Now().UTC()
	}
	since, ok := ParseActivitySince(r.URL.Query().Get("since"), now)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, "invalid_activity_since", "Invalid activity period",
			"since must be a duration such as 24h or 7d, or an RFC 3339 time, within the last 30 days").WithDocs(api.ManagedPostgresActivityDocsURL))
		return
	}
	activity, err := h.Service.Activity(r.Context(), acct.ID, r.PathValue("id"), since, now)
	if err != nil {
		h.Problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, ActivityView(activity))
}

// ParseActivitySince reads a period start: empty is the default period, a
// duration (with d for days) counts back from now, else an RFC 3339 time.
// Retention clamps it later; a future or non-positive one is refused.
func ParseActivitySince(raw string, now time.Time) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return now.Add(-api.ManagedPostgresActivityDefaultPeriod), true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), t.Before(now)
	}
	var d time.Duration
	if days, found := strings.CutSuffix(raw, "d"); found {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 || n > 366 {
			return time.Time{}, false
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(raw); err != nil || d <= 0 {
			return time.Time{}, false
		}
	}
	return now.Add(-d), true
}

// ActivityView is the API shape of a database's activity.
func ActivityView(a managedpostgres.DatabaseActivity) api.ManagedPostgresActivity {
	view := api.ManagedPostgresActivity{DatabaseID: a.Database.ID, DatabaseName: a.Database.Name, Since: a.Since, Until: a.Until,
		Starts: a.StartCount, AwakeSeconds: a.AwakeSeconds, SuspendTimeoutSeconds: a.SuspendTimeoutSeconds, Truncated: a.Truncated,
		Causes:        make([]api.ManagedPostgresActivityCause, 0, len(a.Causes)),
		ComputeStarts: make([]api.ManagedPostgresActivityComputeStart, 0, len(a.Starts)),
		Hints:         make([]api.ManagedPostgresActivityHint, 0, len(a.Hints)),
		DocsURL:       api.ManagedPostgresActivityDocsURL}
	for _, c := range a.Causes {
		view.Causes = append(view.Causes, api.ManagedPostgresActivityCause{Cause: c.Cause, AppID: c.AppID, AppSlug: c.AppSlug,
			Detail: c.Detail, Starts: c.Starts, AwakeSeconds: c.AwakeSeconds, FirstStartedAt: c.FirstStartedAt})
	}
	for _, s := range a.Starts {
		start := api.ManagedPostgresActivityComputeStart{StartedAt: s.StartedAt, Cause: s.Cause, AppID: s.AppID, AppSlug: s.AppSlug, Detail: s.Detail}
		if !s.SuspendedAt.IsZero() {
			suspended := s.SuspendedAt
			start.SuspendedAt = &suspended
		}
		view.ComputeStarts = append(view.ComputeStarts, start)
	}
	for _, hint := range a.Hints {
		view.Hints = append(view.Hints, api.ManagedPostgresActivityHint{Code: hint.Code, Cause: hint.Cause, AppSlug: hint.AppSlug,
			Message: hint.Message, Command: hint.Command})
	}
	return view
}

// DatabaseUsageView is the usage view's split by database; names holds the
// databases that still exist.
func DatabaseUsageView(totals []managedpostgres.DatabaseActivityTotals, names map[string]string) []api.ManagedPostgresDatabaseUsage {
	out := make([]api.ManagedPostgresDatabaseUsage, 0, len(totals))
	for _, t := range totals {
		out = append(out, api.ManagedPostgresDatabaseUsage{DatabaseID: t.DatabaseID, Name: names[t.DatabaseID],
			ComputeUnitSeconds: t.ComputeUnitSeconds, Starts: t.Starts, AwakeSeconds: t.AwakeSeconds})
	}
	return out
}
