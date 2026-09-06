// `gregale deploys retry <id> [--from=<stage>]` — ADR-117
// §Production-ready follow-on, C2.
//
// Subcommand of `gregale deploys` (sibling to `show` / `status`).
// Calls POST /v1/apps/{slug}/deployments/{id}/retry and prints the
// new row's ID + stage state so the customer can pipe it to
// `gregale deploys status <new-id>` to follow the live progress.
//
// `--from` is the closed-6 stage vocabulary (source_download /
// dependency_restore / image_build / security_scan /
// snapshot_prepare / readiness). Default when omitted = the
// failing stage on the source row, fetched via the existing
// `GET /v1/deployments/{id}/stages` read surface (cmdDeploysRetry
// asks the SDK for the typed view, decodes the jsonb, and
// surfaces `state.Current` for the row's last-known failing
// stage). A from_stage=source_download re-runs the entire
// pipeline (intentional — that's how a user "retry from the
// top" works).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// fromStageFlag is the canonical CLI flag for retry. Mirrors the
// `--from=<stage>` convention used by other gregale subcommands
// (e.g. `gregale logs --from=…`). Keeping the name locked here
// means the completion test only has one string to pin.
const fromStageFlag = "--from="

const deploysRetryUsage = "usage: gregale deploys retry <id> [--from=<stage>]"

// cmdDeploysRetry is the subcommand handler. Returns 0 on
// success, 1 on user-input errors, 2 on API errors (per the
// gregale exit-code convention).
func cmdDeploysRetry(args []string) int {
	if hasHelpFlag(args) {
		PrintUsage(osStdout, deploysRetryUsage, "deploys")
		return 0
	}
	if len(args) < 1 {
		// PrintUsage (not printErr) for usage errors — printErr
		// unconditionally calls err.Error() and the CLI convention
		// is to keep PrintUsage for the no-args branch (mirrors
		// cmdDeploysShow's branch at deploys_show.go:116).
		PrintUsage(os.Stderr, deploysRetryUsage, "deploys")
		return 1
	}
	ctx := context.Background()
	depID := args[0]
	if _, err := uuid.Parse(depID); err != nil {
		printErr("invalid deployment id", fmt.Errorf("expected UUID: %w", err))
		return 1
	}

	// Parse --from=<stage>. Default = "" (handler falls back to
	// the failing stage on the source row).
	var fromStage string
	for _, a := range args[1:] {
		if strings.HasPrefix(a, fromStageFlag) {
			fromStage = strings.TrimPrefix(a, fromStageFlag)
			continue
		}
		// Unknown flag — surface as a usage error rather than
		// silently ignoring it (the gregale convention; matches
		// cmdDeploysShow).
		printErr(fmt.Sprintf("unknown flag: %s", a), errors.New("unknown flag"))
		return 1
	}

	// Validate the wire-supplied from_stage against the closed-6
	// vocabulary BEFORE hitting the wire. The server re-validates
	// (defence in depth); this is the faster-path UX.
	if fromStage != "" && !state.IsStageName(state.StageName(fromStage)) {
		printErr(fmt.Sprintf("from_stage %q is not one of: %v",
			fromStage, stageNamesForCLI()), errors.New("invalid from_stage"))
		return 1
	}

	// Auth + client. Mirrors cmdDeploysShow so the auth chain
	// (token / cookie / mTLS) stays consistent across the
	// `gregale deploys *` family.
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	// Default from_stage to the failing stage on the source row.
	// Fetching /v1/deployments/{id}/stages is the cheapest way to
	// read the stage_state jsonb for the failing row.
	if fromStage == "" {
		raw, err := client.GetDeploymentStages(ctx, depID)
		if err != nil {
			printErr(fmt.Sprintf("read source deployment stages: %v", err), err)
			return 2
		}
		var ss state.StageState
		if uerr := json.Unmarshal(raw, &ss); uerr != nil {
			printErr(fmt.Sprintf("decode stage_state: %v", uerr), uerr)
			return 2
		}
		// Code-review finding #4: production sets state.Current=""
		// on the failure path (MarkDeploymentStageFailed rolls the
		// in-flight stage into history with status=failed). Reading
		// ss.Current here yields "" for the primary use case
		// (retrying a failed deploy), so we fall through to the
		// history scan first; only fall back to ss.Current when
		// the jsonb pre-dates the ADR-117 shape (no history rows).
		from := state.StageName("")
		for _, item := range ss.History {
			if item.Status == "failed" && item.Name != "" {
				from = item.Name
				break
			}
		}
		if from == "" && ss.Current != "" {
			from = ss.Current
		}
		if from == "" {
			printErr("source deployment has no current or failed stage; pass --from=<stage> explicitly", errors.New("no stage hint"))
			return 1
		}
		fromStage = string(from)
	}

	// Fire the retry. The new row's id is on the response; we
	// surface it so the customer can pipe to status/show.
	resp, err := client.RetryDeploymentFromStage(ctx, depID, fromStage)
	if err != nil {
		printErr("retry deployment failed", err)
		return 2
	}

	_, _ = fmt.Fprintf(osStdout, "Retry queued.\n")
	_, _ = fmt.Fprintf(osStdout, "  new deployment id: %s\n", resp.ID)
	_, _ = fmt.Fprintf(osStdout, "  starting from:     %s\n", fromStage)
	_, _ = fmt.Fprintf(osStdout, "  status:            %s\n", resp.Status)
	_, _ = fmt.Fprintf(osStdout, "\nFollow progress:\n  gregale deploys status %s\n", resp.ID)
	return 0
}

// stageNamesForCLI returns the closed-6 vocabulary formatted for
// the user-facing error message. Mirrors pkg/state.AllStageNames
// (the canonical list); the fmt.Sprint keeps the order stable so
// the error string is byte-identical across runs (testable).
func stageNamesForCLI() string {
	names := make([]string, 0, len(state.AllStageNames))
	for _, n := range state.AllStageNames {
		names = append(names, string(n))
	}
	return strings.Join(names, ", ")
}

// _ silences the unused-import lint when this file is built
// without the api package being referenced directly (e.g. when
// retry callers are commented out during development). The
// import is load-bearing for the SDK round-trip via
// RetryDeploymentFromStage.
var _ = api.RetryDeploymentRequest{}
