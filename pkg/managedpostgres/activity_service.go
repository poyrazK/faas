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
// Command, when set, is the CLI command that looks into it; FixCommands are
// the ready-to-run commands that change the setting the hint names, in
// order, with every value filled in.
type ActivityHint struct {
	Code        string
	Cause       string
	AppSlug     string
	Message     string
	Command     string
	FixCommands []string
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
	hc := ActivityHintContext{Ref: s.databaseRef(ctx, accountID, database), SuspendTimeoutSeconds: out.SuspendTimeoutSeconds}
	if on, err := s.WakeWithApp(ctx, []Database{database}); err == nil {
		hc.WakeWithApp = on[database.ID]
	}
	out.Hints = ActivityHints(database, out.Causes, hc)
	return out, nil
}

// databaseRef is how a command names the database: its name when no other
// database of the account shares it, else its ID, which the CLI accepts too.
func (s *Service) databaseRef(ctx context.Context, accountID string, database Database) string {
	databases, err := s.List(ctx, accountID)
	if err != nil || database.Name == "" {
		return database.ID
	}
	for _, other := range databases {
		if other.ID != database.ID && other.Name == database.Name {
			return database.ID
		}
	}
	return database.Name
}

// activityHintMinShare is the share of awake time below which a cause gets
// no hint: a hint for every cause is noise.
const activityHintMinShare = 0.2

// ActivityHintContext is what the hints need beyond the causes.
type ActivityHintContext struct {
	// Ref names the database in commands; empty means its name.
	Ref string
	// SuspendTimeoutSeconds is the timeout in force, 0 when unknown.
	SuspendTimeoutSeconds int64
	// WakeWithApp reports whether wake-with-app is on now.
	WakeWithApp bool
}

// ActivityHints suggests what to do about the causes that hold at least a
// fifth of the awake time, largest first. Requests get none: serving them is
// what the database is for. A fix command is offered only while it would
// change something, and each once.
func ActivityHints(database Database, causes []ComputeStartCause, hc ActivityHintContext) []ActivityHint {
	var total int64
	for _, cause := range causes {
		total += cause.AwakeSeconds
	}
	if total == 0 || !database.Spec.ScaleToZero {
		return nil
	}
	ref := hc.Ref
	if ref == "" {
		ref = database.Name
	}
	shorten := hc.SuspendTimeoutSeconds > int64(api.ManagedPostgresSuspendTimeoutMinSeconds)
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
			if cause.AppSlug != "" { // empty once the app is deleted
				hint.Command = "gregale cron list --app " + cause.AppSlug
			}
		case CauseAppWake:
			hint.Code = "app_wake_starts_database"
			hint.Message = fmt.Sprintf("The database starts with its apps (wake-with-app) %d times. Turn it off if the first query can wait for the start.", cause.Starts)
			if hc.WakeWithApp {
				fix := "gregale postgres app-wake set " + ref + " off"
				// Command keeps the fix for clients that predate FixCommands.
				hint.Command, hint.FixCommands = fix, []string{fix}
			}
		case CauseAppStart:
			hint.Code = "app_start_wakes_database"
			hint.Message = fmt.Sprintf("%s connects when it starts, not only when a request needs the database. Connect lazily on first use.", orDash(cause.AppSlug))
		case CauseRunningApp:
			hint.Code = "running_app_holds_connections"
			hint.Message = fmt.Sprintf("%s kept the database awake while it ran. Close idle pool connections sooner than the suspend timeout, or let the app idle out sooner.", orDash(cause.AppSlug))
		case CauseExternal:
			hint.Code = "external_client"
			hint.Message = "Something outside Gregale connected: a local client, a BI tool, a monitor or a copied connection string. Rotate the credentials if you do not know it."
			hint.Command = "gregale postgres bindings list " + ref
		default:
			continue
		}
		if shorten && (cause.Cause == CauseCron || cause.Cause == CauseExternal) {
			fix := "gregale postgres suspend-timeout set " + ref + " 1m"
			// The message keeps the command for clients that predate
			// FixCommands.
			hint.Message += fmt.Sprintf(" The suspend timeout is %d s: `%s` shortens each stay.", hc.SuspendTimeoutSeconds, fix)
			hint.FixCommands = append(hint.FixCommands, fix)
			shorten = false // one database-wide setting: offered once
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
