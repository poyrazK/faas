package mail

import (
	"fmt"
	"regexp"
)

// Managed PostgreSQL awake notices (ADR-20261125231222): once a week, for a
// scale-to-zero database that one cause kept awake most of the time.

// ManagedPostgresAwakeNotice is the frozen snapshot one awake email reports.
type ManagedPostgresAwakeNotice struct {
	DatabaseName     string
	AwakeHoursPerDay int
	Starts           int
	Cause            string
	AppSlug          string
	// Detail is the cron's path for a cron cause.
	Detail      string
	CauseStarts int
}

var (
	managedPostgresAwakeAppSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	// managedPostgresAwakeCronPath keeps customer text in the email to a
	// plain path.
	managedPostgresAwakeCronPath = regexp.MustCompile(`^/[A-Za-z0-9._~/-]{0,200}$`)
)

// ManagedPostgresAwakeBodyV1 renders one awake email.
func ManagedPostgresAwakeBodyV1(email string, n ManagedPostgresAwakeNotice) (subject, body string, ok bool) {
	if !managedPostgresDatabaseName.MatchString(n.DatabaseName) || n.AwakeHoursPerDay < 1 || n.AwakeHoursPerDay > 24 ||
		n.Starts < 1 || n.CauseStarts < 1 || n.CauseStarts > n.Starts ||
		(n.AppSlug != "" && !managedPostgresAwakeAppSlug.MatchString(n.AppSlug)) {
		return "", "", false
	}
	app := n.AppSlug
	var cause, fix string
	switch n.Cause {
	case "cron":
		if app == "" || (n.Detail != "" && !managedPostgresAwakeCronPath.MatchString(n.Detail)) {
			return "", "", false
		}
		what := "A cron of " + app
		if n.Detail != "" {
			what = fmt.Sprintf("The cron %s of %s", n.Detail, app)
		}
		cause = fmt.Sprintf("%s started it %d of those times.", what, n.CauseStarts)
		fix = "Each start keeps it awake for at least its suspend timeout, even when the job has nothing to do. Run the job less often, let it skip the database when there is nothing to do, or shorten the timeout with gregale postgres suspend-timeout set " + n.DatabaseName + " 1m."
	case "app_wake":
		cause = fmt.Sprintf("Wake-with-app started it %d of those times.", n.CauseStarts)
		fix = "Every wake of an app bound to it starts it, even one that never queries it. Turn it off with gregale postgres app-wake set " + n.DatabaseName + " off if the first query can wait for the start."
	case "app_start", "running_app":
		if app == "" {
			return "", "", false
		}
		cause = fmt.Sprintf("%s connected to it %d of those times without a request needing it.", app, n.CauseStarts)
		fix = "Connect when a request first needs the database, and close idle pool connections sooner than the suspend timeout."
	case "external":
		cause = fmt.Sprintf("A client outside Gregale started it %d of those times.", n.CauseStarts)
		fix = "That can be a local client, a BI tool, a monitor or a copied connection string. If you do not know it, rotate the database's credentials."
	default:
		return "", "", false
	}
	subject = fmt.Sprintf("Your database %s was awake %d hours a day", n.DatabaseName, n.AwakeHoursPerDay)
	body = fmt.Sprintf(`Hi,

This is about a managed PostgreSQL database of your Gregale account (%s).
%s suspends when idle, but last week it was awake %d hours a day on average and started %d times. Compute is billed while it is awake.
%s
%s

See every start and its likely cause with gregale postgres activity %s --since 7d. Causes are best guesses from timing.

You get this at most once a week for a database.

— Gregale
`, safeRecipient(email), n.DatabaseName, n.AwakeHoursPerDay, n.Starts, cause, fix, n.DatabaseName)
	return subject, body, true
}
