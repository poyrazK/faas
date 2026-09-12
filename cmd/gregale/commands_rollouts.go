// commands_rollouts.go — `gregale rollouts recover <slug>` (SAFE-RELEASES-R,
// issue #976 / ADR-122). The operator manual-recovery escape hatch.
//
// Pattern mirrors commands_alerts.go: a small dispatcher with one
// subcommand, FlagSet parsing for the closed-set action + the
// optional reason, an authedClient round-trip via
// Client.RecoverRollout (pkg/api/client.go), and a friendly echo
// of the post-recovery deployment + audit row id.
//
// Closed-set discipline: --action ∈ {"advance", "promote", "abort"}
// is validated AGAINST api.AllowedRecoverRolloutAction BEFORE the
// network round-trip so a CLI typo costs zero latency and the
// server's 422 ErrInvalidRecoverAction is unreachable from the CLI.
// The store re-validates the same closed-set as defence-in-depth.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	rolloutsUsage        = "usage: gregale rollouts recover <slug> --action advance|promote|abort [--reason <text>]"
	rolloutsRecoverUsage = rolloutsUsage
)

func cmdRollouts(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, rolloutsUsage, "rollouts")
		return 1
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		PrintUsage(osStdout, rolloutsUsage, "rollouts")
		return 0
	}
	switch args[0] {
	case "recover":
		return cmdRolloutsRecover(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown rollouts subcommand %q\n", args[0])
	return 1
}

// cmdRolloutsRecover is the canonical manual-recovery path.
//
// Usage:
//
//	gregale rollouts recover <slug> --action advance|promote|abort [--reason <text>]
//
// Where:
//
//   - <slug> is the app's slug (positional; required).
//   - --action advance bumps canary_step by 1, requires the
//     rollout to be stuck (canary_step_started_at older than 30
//     minutes — the canned stuck-after window in
//     pkg/safedeploy.StuckAfterDuration). The CLI surfaces the
//     server's 409 ErrRolloutNotStuck with the suggestion "use
//     --action promote instead" if the operator typos this on a
//     healthy rollout.
//   - --action promote short-circuits the rollout to 100% /
//     rollout_state='complete'. No stuck-check; this is the
//     operator's "I'm sure, ship it" path.
//   - --action abort flips rollout_state='aborted' with the
//     supplied reason. Emits a deploy.rolled_back audit row.
//   - --reason is optional but recommended; the value lands in
//     the deployment_audit row's data payload so SOC 2 / GDPR
//     auditors can re-derive the operator's intent.
//
// Returns 0 on success, 1 on usage error, prints the
// post-recovery deployment + audit id on stdout (or the JSON
// shape via --json).
func cmdRolloutsRecover(args []string) int {
	if hasHelpFlag(args) {
		PrintUsage(osStdout, rolloutsRecoverUsage, "rollouts")
		return 0
	}
	fs := flag.NewFlagSet("rollouts recover", flag.ContinueOnError)
	action := fs.String("action", "", "recover action (advance|promote|abort)")
	reason := fs.String("reason", "", "operator-supplied reason (logged to deployment_audit)")
	// The public usage is `recover <slug> --action ...`, while the
	// standard flag package stops parsing at the first positional
	// argument. Peel off the documented slug first so both that
	// spelling and the conventional flags-before-positionals form
	// remain accepted.
	var slug string
	flagArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		slug = args[0]
		flagArgs = args[1:]
	}
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if slug == "" && fs.NArg() > 0 {
		slug = fs.Arg(0)
	}
	if slug == "" {
		PrintUsage(os.Stderr, rolloutsRecoverUsage, "rollouts")
		return 1
	}
	if *action == "" {
		return printErr("Missing --action", fmt.Errorf("--action is required (one of: advance, promote, abort)"))
	}
	// Closed-set check BEFORE the network round-trip. The
	// server's 422 ErrInvalidRecoverAction is unreachable from
	// the CLI; the server still re-validates as defence-in-
	// depth so a programmatic caller bypassing the CLI gets
	// the same stable shape.
	if !api.AllowedRecoverRolloutAction(*action) {
		return printErr("Invalid --action", fmt.Errorf("--action must be one of: advance, promote, abort; got %q", *action))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.RecoverRollout(context.Background(), slug, *action, *reason)
	if err != nil {
		return printErr("Recover failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	// Human-mode echo: deployment id, post-state, traffic
	// percent, and the audit row id (the operator can paste
	// the audit id into the dashboard's deployment timeline).
	_, _ = fmt.Fprintf(osStdout, "Rollout %s on app %s.\n", *action, slug)
	_, _ = fmt.Fprintf(osStdout, "  deployment:  %s\n", resp.Deployment.ID)
	if resp.Deployment.RolloutState != "" {
		_, _ = fmt.Fprintf(osStdout, "  state:       %s\n", resp.Deployment.RolloutState)
	}
	if resp.Deployment.CanaryTotalSteps > 0 {
		_, _ = fmt.Fprintf(osStdout, "  canary_step: %d / %d\n", resp.Deployment.CanaryStep, resp.Deployment.CanaryTotalSteps)
	}
	_, _ = fmt.Fprintf(osStdout, "  audit_id:    %s\n", resp.AuditID)
	return 0
}
