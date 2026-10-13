package main

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Names and observed timestamps only; no audit payloads or customer values.
type inspectRecentChange struct {
	Kind, ID, Status, Scope, Clock string
	At                             time.Time
}

func inspectRecentChanges(appID string, deployments []api.DeploymentResponse, operations *api.AppOperationalSummary) []inspectRecentChange {
	changes := make([]inspectRecentChange, 0, len(deployments))
	for _, deployment := range deployments {
		if deployment.AppID != appID || deployment.ID == "" {
			continue
		}
		at, _ := time.Parse(time.RFC3339Nano, deployment.CreatedAt)
		changes = append(changes, inspectRecentChange{Kind: "deployment", ID: deployment.ID, Status: deployment.Status, Scope: deployment.Scope, Clock: "submitted", At: at})
	}
	if operations != nil {
		for _, rollback := range operations.Recovery.Rollbacks {
			changes = append(changes, inspectRecentChange{Kind: "rollback", ID: rollback.ID, Status: rollback.Status, Scope: rollback.Scope, Clock: "updated", At: rollback.UpdatedAt})
		}
		for _, restart := range operations.Recovery.Restarts {
			changes = append(changes, inspectRecentChange{Kind: "restart", ID: restart.WakeID, Status: restart.Status, Clock: "requested", At: restart.RequestedAt})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if !changes[i].At.Equal(changes[j].At) {
			return changes[i].At.After(changes[j].At)
		}
		if changes[i].Kind != changes[j].Kind {
			return changes[i].Kind < changes[j].Kind
		}
		return changes[i].ID < changes[j].ID
	})
	if len(changes) > 3 {
		changes = changes[:3]
	}
	return changes
}

func renderInspectRecentChanges(w io.Writer, summary inspectSummary) {
	_, _ = fmt.Fprintln(w, "\nRecent observed changes")
	if len(summary.RecentChanges) == 0 {
		_, _ = fmt.Fprintln(w, "  No changes available from these reads; this does not establish that nothing changed.")
	}
	for _, change := range summary.RecentChanges {
		at := "time unavailable"
		if !change.At.IsZero() {
			at = change.At.UTC().Format(time.RFC3339)
		}
		_, _ = fmt.Fprintf(w, "  %s %s: %s · %s %s", change.Kind, compactInspectFailureText(change.ID), compactInspectFailureText(change.Status), change.Clock, at)
		if change.Scope != "" {
			_, _ = fmt.Fprintf(w, " · scope=%s", compactInspectFailureText(change.Scope))
		}
		_, _ = fmt.Fprintln(w)
		command := recoveryCLI()
		switch change.Kind {
		case "deployment":
			command += " deployment get " + recoveryArgument(change.ID)
		case "rollback":
			command += " rollback status " + recoveryArgument(summary.App.Slug) + " --operation " + recoveryArgument(change.ID)
		case "restart":
			command += " app " + recoveryArgument(summary.App.Slug) + " restart status --wake-id " + recoveryArgument(change.ID)
		}
		_, _ = fmt.Fprintf(w, "    %s\n", command)
	}
	_, _ = fmt.Fprintln(w, "  Up to three latest observations from deployment submissions and pending/failed recovery operations, not a complete audit history. Timing does not establish causation.")
	_, _ = fmt.Fprintf(w, "  Deployment history (POSIX shells):\n    %s deployments --app %s\n", recoveryCLI(), recoveryArgument(summary.App.Slug))
}
