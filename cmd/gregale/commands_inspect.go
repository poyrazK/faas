// gregale inspect — read-only operator surface for inspecting
// per-app state (issue #952). A bare invocation renders the
// application-intelligence summary; `--upstreams` selects the
// ADR-098 §9.A captured-upstream table. Future leaves
// (--env, --crons, --instances, …) add a flag here and a
// commands_inspect_<noun>.go dispatcher file — keeps the verb
// itself a single dispatcher and the per-leaf logic isolated.
//
// Why this isn't a sub-dispatch table (a la crons / domains):
// every leaf has its own flag set + validation, and a future
// `--env` and `--crons` will share the slug but NOT the
// filters. Putting the leaf's flag set on cmdInspect itself
// lets the man-page manifest see every flag at one stop and
// keeps the per-leaf renderer in commands_inspect_<noun>.go
// narrow (≤50 lines per the handlers convention).
//
// UX shape:
//
//	$ gregale inspect <slug>
//	$ gregale inspect <slug> --upstreams
//	$ gregale inspect <slug> --upstreams --scope <scope>
//	$ gregale inspect <slug> --upstreams --json
//
// Future leaves reuse the slug positional; their flag set is
// added below the `--upstreams` arm.
package main

import (
	"flag"
	"fmt"
	"os"
)

// cmdInspect is the verb-level dispatcher for
// `gregale inspect <slug> [flags]`. A bare invocation aggregates the
// existing app, deployment, OpenAPI, upstream, and alert surfaces.
//
// Why --upstreams is a flag and not a positional subcommand:
// the issue wording is authoritative — `gregale inspect <slug>
// --upstreams` — and the verb-shape is symmetric with the
// upcoming `gregale inspect <slug> --env` / `--crons` /
// `--instances` leaves (each one is a switch on the leaf set
// the customer wants to see).
//
// inspectUsage is the single source of truth for the verb's
// usage line. The dispatcher prints it on bad-args paths, and the
// test file pins this exact wording.
const inspectUsage = "usage: gregale inspect <slug> [--upstreams] [--scope <scope>] [--errors] [--json]"

func cmdInspect(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, inspectUsage, "inspect")
		return 1
	}
	// The slug is always args[0]; flags follow. Mirrors the
	// cmdCronsUpdate shape (commands2.go) — stdlib flag.Parse
	// stops parsing flags at the first positional, so the
	// slug-shaped args[0] would otherwise eat the --upstreams
	// flag. We pass args[1:] to the FlagSet so the leaf's flags
	// are visible.
	slug := args[0]
	if !validCLISlug(slug) {
		fmt.Fprintf(os.Stderr, "invalid slug %q (3..40 chars, lowercase alnum + dash, no leading/trailing dash)\n", slug)
		return 1
	}
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	upstreams := fs.Bool("upstreams", false, "list data upstreams captured for this app (ADR-098 §9.A)")
	scope := fs.String("scope", "", "filter upstreams by scope (forwarded as ?scope=<scope>)")
	// Error-explanations cluster (spec §6.4 amendment 1): --errors
	// lifts the persisted failure prose (Error{Hint,Why,Fix,
	// RelevantLogs}) from the latest failed deployment for the app.
	// Auth required; no scope filter (errors are per-deployment).
	errorsFlag := fs.Bool("errors", false, "show the latest failed deployment's persisted error explanation (Hint/Why/Fix/RelevantLogs)")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, inspectUsage, "inspect")
		return 1
	}
	// The bare form is the application-intelligence summary. A scope
	// only has meaning for the explicit upstream table, so reject it
	// before auth/network rather than silently ignoring the filter.
	if !*upstreams && !*errorsFlag {
		if *scope != "" {
			return printErr("Invalid flags", fmt.Errorf("--scope requires --upstreams"))
		}
		return cmdInspectSummary(slug)
	}
	// Mutually exclusive: --upstreams hits /v1/apps/{slug}/upstreams;
	// --errors hits the latest deployment via the deployments list.
	// Mixing them would force two server calls and the customer's
	// intent is ambiguous.
	if *upstreams && *errorsFlag {
		return printErr("Invalid flags", fmt.Errorf("--upstreams and --errors are mutually exclusive"))
	}
	if *errorsFlag {
		return cmdInspectErrors(slug)
	}
	return cmdInspectUpstreams(slug, *scope)
}
