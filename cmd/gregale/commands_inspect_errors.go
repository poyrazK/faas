// gregale inspect <slug> --errors — error-explanations cluster
// (spec §6.4 amendment 1) post-mortem leaf. Lifts the persisted
// failure prose (ErrorHint/ErrorWhy/ErrorFix/ErrorRelevantLogs)
// from the latest failed deployment for the app so the customer
// can debug after the fact (the stream is gone, the apid
// response is gone — only the row in the `deployments` table
// remains).
//
// Sister to commands_inspect_upstreams.go (issue #952 / ADR-098
// §9.A). The leaf first resolves the app, then scans the account's
// deployment list while applying an app-ID predicate before rendering.
//
// Wire shape: the apid's DeploymentResponse carries the 4 fields
// added by commit 6 (pkg/api/dto.go::DeploymentResponse). The
// SDK's ListDeploymentsAll cursor walks the pages; we read until
// we find status=failed AND error_code != "". The latest failed
// deployment wins (ordered by created_at DESC).
//
// Exit codes:
//
//	0  found a failed deployment, rendered the explanation
//	1  no failed deployment for the app (nothing to show)
//	2  auth error
//	3  platform/transport error
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// inspectErrorsUsage is the leaf's usage line. Kept as a const
// so the test pin (commands_inspect_errors_test.go) catches any
// drift in the wording — script consumers grep on the literal.
const inspectErrorsUsage = "usage: gregale inspect <slug> --errors [--json]"

// cmdInspectErrors lifts the latest failed deployment's
// persisted explanation prose for slug and renders it via the
// standard 3-5 line shape (Title / Detail / Hint / Why / Fix /
// RelevantLogs / DocsURL). Under --json emits the raw DTO so
// `gregale inspect <slug> --errors --json | jq` works.
//
// Auth required (the row is per-account, not public). On auth
// failure the renderAPIError path prints the RFC 7807 problem
// the same way the rest of the package does.
func cmdInspectErrors(slug string) int {
	fs := flag.NewFlagSet("inspect-errors", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	asJSON := fs.Bool("json", false, "machine output (default: human prose)")
	if err := fs.Parse([]string{}); err != nil {
		PrintUsage(osStderr, inspectErrorsUsage, "inspect")
		return 2
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return printErr("Could not load app", err)
	}
	// Walk deployments until we find a failed one with a typed
	// error_code. The cursor protocol is RFC3339Nano on
	// created_at DESC (per the apid's ListDeployments contract).
	// Most apps have ≤1 failed deployment after a successful
	// recovery; the cap of 50 (5 pages × 10/page) keeps a
	// pathological "every deploy failed" loop bounded. Past 50
	// we tell the customer to use `gregale logs <slug> --explain`
	// directly.
	dep, err := findLatestFailedDeployment(client, app.ID, 50)
	if err != nil {
		return printErr("Could not reach the API", err)
	}
	if dep == nil {
		message := fmt.Sprintf("no failed deployment for %s — try `gregale logs %s --explain` if the live build is failing", slug, slug)
		if jsonOutput || *asJSON {
			problem := api.NewProblem(http.StatusNotFound, api.CodeNotFound, "No failed deployment", message)
			_ = writeJSONProblem(*problem)
			return 1
		}
		PrintFail(osStderr, "%s", message)
		return 1
	}
	if jsonOutput || *asJSON {
		return jsonOut(writeJSON(dep))
	}
	renderInspectErrorsHuman(osStdout, slug, *dep)
	return 0
}

// findLatestFailedDeployment walks the deployments list for appID
// and returns the first row with status=failed AND error_code
// non-empty. Returns (nil, nil) when no such row exists within
// the cap. Errors propagate to the caller.
//
// Why we walk the cursor instead of a targeted query: the apid
// doesn't expose a "latest failed deployment for app" endpoint
// (no routing-tag need justifies the addition yet). The walk is
// bounded (maxPages), and the app-ID predicate prevents a failed
// deployment from another app in the same account from being shown.
func findLatestFailedDeployment(client *Client, appID string, maxPages int) (*api.DeploymentResponse, error) {
	ctx := context.Background()
	before := ""
	pages := 0
	for {
		pages++
		if pages > maxPages {
			return nil, nil
		}
		resp, err := client.ListDeployments(ctx, before, 10)
		if err != nil {
			return nil, err
		}
		for i := range resp.Items {
			d := &resp.Items[i]
			if d.AppID == appID && d.Status == "failed" && d.ErrorCode != "" {
				return d, nil
			}
		}
		if resp.NextBefore == "" {
			return nil, nil
		}
		before = resp.NextBefore
	}
}

// renderInspectErrorsHuman prints the lifted explanation in the
// standard 3-5 line shape. The render mirrors commands.go:506
// renderAPIError so a customer who has seen one cluster error
// sees this output the same way; the only difference is we lift
// the prose from the deployment row (persisted) instead of the
// API Problem (live).
func renderInspectErrorsHuman(w io.Writer, slug string, dep api.DeploymentResponse) {
	renderDeploymentFailure(w, dep)
	// Footer: when the failure happened (the apid stamps
	// created_at; we accept the empty case gracefully).
	_, _ = fmt.Fprintf(w, "  app: %s\n", slug)
	if dep.CreatedAt != "" {
		_, _ = fmt.Fprintf(w, "  failed_at: %s\n", dep.CreatedAt)
	}
}

// renderDeploymentFailure prints the persisted explanation attached to one
// failed deployment. Keeping this separate from the app-level `inspect
// --errors` footer lets `deploys status` show the same actionable guidance
// while the deployment id is still in hand.
func renderDeploymentFailure(w io.Writer, dep api.DeploymentResponse) {
	title := dep.ErrorCode
	if title == "" {
		title = "Failed deployment"
	}
	RenderTitle(w, fmt.Sprintf("%s — deployment %s", title, dep.ID))
	if dep.Error != "" {
		_, _ = fmt.Fprintf(w, "  %s\n", dep.Error)
	}
	if dep.ErrorHint != "" {
		RenderHintRow(w, dep.ErrorHint)
	}
	if dep.ErrorWhy != "" {
		RenderWhyRow(w, dep.ErrorWhy)
	}
	if dep.ErrorFix != "" {
		RenderFixRow(w, dep.ErrorFix)
	}
	if len(dep.ErrorRelevantLogs) > 0 {
		RenderRelevantLogs(w, dep.ErrorRelevantLogs)
	}
	// Always close with the per-code docs URL so the customer
	// has the same "→ docs" line the live-error renderer emits.
	if dep.ErrorCode != "" {
		RenderDocsRow(w, docsURLForCode(dep.ErrorCode))
	}
}
