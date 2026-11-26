package managedpostgres

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// DatabaseActivity is why a database was awake in a period
// (ADR-20261125231222): its recorded starts, newest first, each cause's
// share and what to do about the largest ones.
type DatabaseActivity struct {
	Database Database
	Since    time.Time
	Until    time.Time
	// Starts holds at most api.ManagedPostgresActivityMaxStarts; Truncated
	// says more were recorded.
	Starts       []ComputeStart
	Truncated    bool
	Causes       []ComputeStartCause
	StartCount   int
	AwakeSeconds int64
	// SuspendTimeoutSeconds is the timeout in force, or 0 when unknown or
	// the database is always on.
	SuspendTimeoutSeconds int64
	Hints                 []ActivityHint
}

// ActivityHint is one suggestion for a cause that keeps a database awake.
// Command, when set, is the CLI command that acts on it.
type ActivityHint struct {
	Code    string
	Cause   string
	AppSlug string
	Message string
	Command string
}

// Activity reads why one of the account's databases was awake since a time,
// clamped to the retention.
func (s *Service) Activity(ctx context.Context, accountID, databaseID string, since, now time.Time) (DatabaseActivity, error) {
	if accountID == "" || databaseID == "" || !since.Before(now) {
		return DatabaseActivity{}, ErrInvalid
	}
	store, ok := s.store.(ComputeActivityStore)
	if !ok {
		return DatabaseActivity{}, ErrUnsupported
	}
	database, err := customerDatabase(ctx, s.store, accountID, databaseID)
	if err != nil {
		return DatabaseActivity{}, err
	}
	if floor := now.Add(-api.ManagedPostgresActivityRetention); since.Before(floor) {
		since = floor
	}
	out := DatabaseActivity{Database: database, Since: since, Until: now}
	if out.Starts, err = store.ComputeStarts(ctx, database.ID, since, api.ManagedPostgresActivityMaxStarts+1); err != nil {
		return DatabaseActivity{}, err
	}
	if len(out.Starts) > api.ManagedPostgresActivityMaxStarts {
		out.Starts, out.Truncated = out.Starts[:api.ManagedPostgresActivityMaxStarts], true
	}
	if out.Causes, err = store.ComputeStartCauses(ctx, database.ID, since, now); err != nil {
		return DatabaseActivity{}, err
	}
	sort.SliceStable(out.Causes, func(i, j int) bool {
		if out.Causes[i].AwakeSeconds != out.Causes[j].AwakeSeconds {
			return out.Causes[i].AwakeSeconds > out.Causes[j].AwakeSeconds
		}
		return out.Causes[i].Starts > out.Causes[j].Starts
	})
	for _, cause := range out.Causes {
		out.StartCount += cause.Starts
		out.AwakeSeconds += cause.AwakeSeconds
	}
	if database.Spec.ScaleToZero {
		if timeouts, err := s.SuspendTimeouts(ctx, []Database{database}); err == nil {
			out.SuspendTimeoutSeconds = timeouts[database.ID].Seconds
		}
	}
	out.Hints = ActivityHints(database, out.Causes, out.SuspendTimeoutSeconds)
	return out, nil
}

// activityHintMinShare is the share of awake time below which a cause gets
// no hint: a hint for every cause is noise.
const activityHintMinShare = 0.2

// ActivityHints suggests what to do about the causes that hold at least a
// fifth of the awake time, largest first. Requests get none: serving them is
// what the database is for.
func ActivityHints(database Database, causes []ComputeStartCause, suspendTimeoutSeconds int64) []ActivityHint {
	var total int64
	for _, cause := range causes {
		total += cause.AwakeSeconds
	}
	if total == 0 || !database.Spec.ScaleToZero {
		return nil
	}
	name := database.Name
	var hints []ActivityHint
	for _, cause := range causes {
		if float64(cause.AwakeSeconds) < activityHintMinShare*float64(total) {
			continue
		}
		hint := ActivityHint{Cause: cause.Cause, AppSlug: cause.AppSlug}
		switch cause.Cause {
		case CauseCron:
			hint.Code = "cron_wakes_database"
			hint.Message = fmt.Sprintf("Cron %s on %s started the database %d times. Each start keeps it awake for at least the suspend timeout; run the job less often, or lower the timeout.",
				orDash(cause.Detail), orDash(cause.AppSlug), cause.Starts)
			hint.Command = "gregale cron list --app " + cause.AppSlug
		case CauseAppWake:
			hint.Code = "app_wake_starts_database"
			hint.Message = fmt.Sprintf("The database starts with its apps (wake-with-app) %d times. Turn it off if the first query can wait for the start.", cause.Starts)
			hint.Command = "gregale postgres app-wake set " + name + " off"
		case CauseAppStart:
			hint.Code = "app_start_wakes_database"
			hint.Message = fmt.Sprintf("%s connects when it starts, not only when a request needs the database. Connect lazily on first use.", orDash(cause.AppSlug))
		case CauseRunningApp:
			hint.Code = "running_app_holds_connections"
			hint.Message = fmt.Sprintf("%s kept the database awake while it ran. Close idle pool connections sooner than the suspend timeout, or let the app idle out sooner.", orDash(cause.AppSlug))
		case CauseExternal:
			hint.Code = "external_client"
			hint.Message = "Something outside Gregale connected: a local client, a BI tool, a monitor or a copied connection string. Rotate the credentials if you do not know it."
			hint.Command = "gregale postgres bindings list " + name
		default:
			continue
		}
		if suspendTimeoutSeconds > int64(api.ManagedPostgresSuspendTimeoutMinSeconds) && (cause.Cause == CauseCron || cause.Cause == CauseExternal) {
			hint.Message += fmt.Sprintf(" The suspend timeout is %d s: `gregale postgres suspend-timeout set %s 1m` shortens each stay.",
				suspendTimeoutSeconds, name)
		}
		hints = append(hints, hint)
	}
	return hints
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// DatabaseUsage splits an account's compute since a time by database, most
// first, with each one's recorded starts and awake time. A database deleted
// since keeps its compute with an empty name.
func (s *Service) DatabaseUsage(ctx context.Context, accountID string, since, now time.Time) ([]DatabaseActivityTotals, map[string]string, error) {
	store, ok := s.store.(ComputeActivityStore)
	if !ok {
		return nil, nil, ErrUnsupported
	}
	totals, err := store.AccountActivityTotals(ctx, accountID, since, now)
	if err != nil {
		return nil, nil, err
	}
	databases, err := s.List(ctx, accountID)
	if err != nil {
		return nil, nil, err
	}
	names := make(map[string]string, len(databases))
	for _, database := range databases {
		names[database.ID] = database.Name
	}
	out := make([]DatabaseActivityTotals, 0, len(totals))
	for _, row := range totals {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ComputeUnitSeconds != out[j].ComputeUnitSeconds {
			return out[i].ComputeUnitSeconds > out[j].ComputeUnitSeconds
		}
		return out[i].DatabaseID < out[j].DatabaseID
	})
	return out, names, nil
}
