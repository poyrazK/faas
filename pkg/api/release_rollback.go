package api

import (
	"fmt"
	"net/http"
	"strings"
)

// RollbackCrossedRelease is a release command that ran after a rollback
// target last served (ADR-1448). A rollback does not undo it.
type RollbackCrossedRelease struct {
	DeploymentID string `json:"deployment_id"`
	Revision     int    `json:"revision,omitempty"`
	// Status is the release task's status; a failed release may still have
	// committed changes.
	Status    string `json:"status"`
	StartedAt string `json:"started_at"`
}

// ReleaseRestorePoint is the moment before the earliest crossed release for
// one managed PostgreSQL database the app's release commands can migrate,
// with the command that restores it into a new database.
type ReleaseRestorePoint struct {
	DatabaseID   string `json:"database_id"`
	DatabaseName string `json:"database_name"`
	PointInTime  string `json:"point_in_time"`
	Command      string `json:"command"`
}

// ReleaseDatabaseRef names a managed PostgreSQL database a release command
// can migrate.
type ReleaseDatabaseRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DeploymentRelease reports a deployment's release command run (ADR-1448).
type DeploymentRelease struct {
	// Status is the release task's status: queued, restoring, running,
	// succeeded, failed, timed_out, or cancelled; pending before imaged has
	// admitted the task.
	Status     string `json:"status"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	// MigrationDatabases are the managed PostgreSQL databases whose
	// migration binding the release task receives.
	MigrationDatabases []ReleaseDatabaseRef `json:"migration_databases,omitempty"`
}

// ErrRollbackTargetDatabaseRestored refuses a rollback to a deployment whose
// own release command a database switch undid (ADR-20261023181931). It
// cannot be acknowledged.
func ErrRollbackTargetDatabaseRestored(target string) *Problem {
	return NewProblem(http.StatusConflict, CodeRollbackTargetDatabaseRestored,
		"Rollback target's database changes were undone",
		fmt.Sprintf("A database switch restored the app's database to before %s's release command ran, so %s's code would run against a schema it does not expect.", target, target)).
		WithHint("Deploy that code again; its release command runs before it serves.").
		WithDocs(docsBase + "/deploys#rollbacks-that-restore-the-database")
}

// ErrDatabaseSwitchUnavailable refuses a rollback that asked to restore the
// database (ADR-20261023181931).
func ErrDatabaseSwitchUnavailable(detail, hint string) *Problem {
	p := NewProblem(http.StatusConflict, CodeDatabaseSwitchUnavailable, "Database switch unavailable", detail).
		WithDocs(docsBase + "/deploys#rollbacks-that-restore-the-database")
	if hint != "" {
		p = p.WithHint(hint)
	}
	return p
}

// ErrDatabaseSwitchesActive refuses a switch while the account already runs
// DatabaseSwitchesActivePerAccount unfinished ones.
func ErrDatabaseSwitchesActive(limit int) *Problem {
	p := NewProblem(http.StatusTooManyRequests, "database_switches_active", "Too many database switches in progress",
		fmt.Sprintf("the account already runs %d unfinished database switches, the most it may run at once", limit))
	l := int64(limit)
	p.Limit, p.Observed = &l, &l
	return p.WithDocs(docsBase + "/deploys#rollbacks-that-restore-the-database").
		WithHint("wait for one to finish, or cancel one with gregale rollback switches")
}

// ErrRollbackCrossesRelease refuses an unacknowledged rollback over release
// commands that ran after its target last served (ADR-1448).
func ErrRollbackCrossesRelease(target string, crossed []RollbackCrossedRelease, restore []ReleaseRestorePoint) *Problem {
	refs := make([]string, 0, len(crossed))
	for _, c := range crossed {
		ref := c.DeploymentID
		if c.Revision > 0 {
			ref = fmt.Sprintf("v%d", c.Revision)
		}
		refs = append(refs, fmt.Sprintf("%s (%s, started %s)", ref, c.Status, c.StartedAt))
	}
	p := NewProblem(http.StatusConflict, CodeRollbackCrossesRelease,
		"Rollback crosses release commands",
		fmt.Sprintf("Release commands ran after %s last served: %s. A rollback does not undo them, so %s would run against changes it has never run with.",
			target, strings.Join(refs, "; "), target)).
		WithHint("If the target is compatible with those changes, retry with acknowledge_releases (CLI: --acknowledge-releases). To undo database changes first, restore each listed database to its restore point.").
		WithDocs(docsBase + "/deploys#releases-that-change-the-database")
	p.CrossedReleases = crossed
	p.RestorePoints = restore
	return p
}
