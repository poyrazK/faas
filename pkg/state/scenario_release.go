// adr: 20261020150000
package state

import (
	"errors"
	"slices"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
)

// IsScenarioCommandAdmissionRefused reports a durable refusal by the contained
// command admission fence: its trigger's check violation, or MemStore's
// ErrAppTaskInvalid. A retry cannot change that outcome.
func IsScenarioCommandAdmissionRefused(err error) bool {
	if errors.Is(err, ErrAppTaskInvalid) {
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

// ReleaseTaskRunsDeploymentRelease reports whether task executes exactly the
// deployment's pinned release command: verbatim, or, for an app enrolled in a
// contained rehearsal, as the contained command envelope admission pinned to
// that deployment (ADR-20261020150000). It does not judge task status.
func ReleaseTaskRunsDeploymentRelease(task AppTask, dep Deployment) bool {
	if task.Kind != AppTaskKindRelease || task.DeploymentID != dep.ID || task.AppID != dep.AppID {
		return false
	}
	if task.CommandShell == dep.ReleaseCommandShell && slices.Equal(task.Command, dep.ReleaseCommand) {
		return true
	}
	return !task.CommandShell && rehearsalcommand.IsContainedReleaseFor(task.Command, dep.ID, dep.ReleaseCommand, dep.ReleaseCommandShell)
}
