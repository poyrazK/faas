package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// errRollbackDeclined means the person did not confirm a rollback over
// release commands; requireTyped has already said so.
var errRollbackDeclined = errors.New("rollback over release commands not confirmed")

// sendRollbackOverReleases sends a rollback and handles ADR-1448's
// rollback_crosses_release: it prints the release commands the rollback
// would not undo and the restore commands for managed databases, then
// retries with acknowledgement when acknowledge is set or the person retypes
// the app slug at a terminal. Scripts and JSON callers get the refusal back.
func sendRollbackOverReleases(slug string, req api.RollbackRequest, acknowledge bool, send func(api.RollbackRequest) (api.DeploymentResponse, error)) (api.DeploymentResponse, error) {
	resp, err := send(req)
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Problem.Code != api.CodeRollbackCrossesRelease {
		return resp, err
	}
	if !jsonOutput {
		writeCrossedReleases(osStderr, apiErr.Problem)
	}
	if !acknowledge {
		if jsonOutput || nonInteractive || !stdinIsTTY() {
			return resp, err
		}
		if !requireTyped(slug) {
			return resp, errRollbackDeclined
		}
	}
	req.AcknowledgeReleases = true
	return send(req)
}

// releaseSummaryLine describes a deployment's release command run
// (ADR-1448), or "" for a deployment without one.
func releaseSummaryLine(r *api.DeploymentRelease) string {
	if r == nil {
		return ""
	}
	line := "release: " + r.Status
	if r.StartedAt != "" {
		line += ", started " + r.StartedAt
	}
	if r.FinishedAt != "" {
		line += ", finished " + r.FinishedAt
	}
	if len(r.MigrationDatabases) > 0 {
		names := make([]string, 0, len(r.MigrationDatabases))
		for _, db := range r.MigrationDatabases {
			names = append(names, db.Name)
		}
		line += "; can migrate " + strings.Join(names, ", ")
	}
	return line
}

// writeCrossedReleases explains a rollback_crosses_release refusal.
func writeCrossedReleases(w io.Writer, p api.Problem) {
	_, _ = fmt.Fprintln(w, "This rollback crosses release commands that ran after its target last served:")
	for _, r := range p.CrossedReleases {
		ref := r.DeploymentID
		if r.Revision > 0 {
			ref = fmt.Sprintf("v%d", r.Revision)
		}
		_, _ = fmt.Fprintf(w, "  %s  %s  started %s\n", ref, r.Status, r.StartedAt)
	}
	_, _ = fmt.Fprintln(w, "A rollback does not undo them; the target would run against their changes.")
	if len(p.RestorePoints) > 0 {
		_, _ = fmt.Fprintln(w, "To undo database changes first, restore each database to a new one and attach it:")
		for _, rp := range p.RestorePoints {
			_, _ = fmt.Fprintf(w, "  %s\n", rp.Command)
		}
	}
	_, _ = fmt.Fprintln(w, "Pass --acknowledge-releases to roll back anyway.")
}
