package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// checkConnectedAuthorization never updates or adopts a receipt. Both
// observations must match the recorded resources around the pinned replay.
func checkConnectedAuthorization(ctx context.Context, client dataAPIConnectRemote, receipt dataAPIConnectReceipt, replay func(context.Context, string, string) error) string {
	if receipt.Stage != "verified" || dataAPIConnectSnapshot(ctx, client, &receipt, false) != nil {
		return "stale_or_unavailable"
	}
	app, err := client.GetApp(ctx, receipt.App)
	if err != nil || app.ID != receipt.AppID || app.Slug != receipt.App {
		return "stale_or_unavailable"
	}
	endpoint, err := dataAPIReadinessURL(app)
	if err != nil {
		return "stale_or_unavailable"
	}
	if err := replay(ctx, endpoint, receipt.Fingerprint); err != nil {
		return "failed_or_unavailable"
	}
	if dataAPIConnectSnapshot(ctx, client, &receipt, false) != nil {
		return "stale_or_unavailable"
	}
	app, err = client.GetApp(ctx, receipt.App)
	if err != nil || app.ID != receipt.AppID || app.Slug != receipt.App {
		return "stale_or_unavailable"
	}
	after, err := dataAPIReadinessURL(app)
	if err != nil || after != endpoint {
		return "stale_or_unavailable"
	}
	return "passed"
}

func verifyDataAPIConnectedAuthorization(ctx context.Context, client dataAPIConnectRemote, receipt dataAPIConnectReceipt, config *dataAPIHostedReplayConfig) int {
	status := checkConnectedAuthorization(ctx, client, receipt, func(ctx context.Context, endpoint, fingerprint string) error {
		return runDataAPIHostedReplay(ctx, config, endpoint, fingerprint, false, false, nil)
	})
	report := map[string]any{
		"event": "connect_authorization_verification", "status": status,
		"checked_at": time.Now().UTC().Format(time.RFC3339), "app": receipt.App,
		"app_id": receipt.AppID, "database_id": receipt.DatabaseID,
		"binding_id": receipt.BindingID, "deployment_id": receipt.DeploymentID,
		"schema_fingerprint": receipt.Fingerprint, "collection_sha256": receipt.CollectionHash,
		"previous_evidence": "historical", "coverage": "supplied_scenarios_only",
	}
	if jsonOutput {
		if err := writeNDJSON([]map[string]any{report}); err != nil {
			return printErr("Cannot write verification report", err)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Authorization verification: %s at %s; deployment %s; schema %s.\n", status, report["checked_at"], receipt.DeploymentID, receipt.Fingerprint)
		_, _ = fmt.Fprintln(osStdout, "The result covers only the supplied scenarios at the time they ran; it does not establish that policies or membership are unchanged.")
	}
	if status != "passed" {
		if !jsonOutput {
			return printErr("Connection verification incomplete", errors.New("review the replay results and recorded resources before retrying"))
		}
		return 1
	}
	return 0
}
