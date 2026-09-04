// commands_openapi.go — `gregale openapi diff <baseline> <proposed>` and
// the app OpenAPI lifecycle commands.
//
// Issue #976 / ADR-122 / SAFE-RELEASES-D:
//
// Silent schema drift across a service-version bump is one of the
// five gaps the mega PR #1 audit identified: a customer upgrades
// their integration to v2 of Gregale, the new openapi.yaml drops a
// field the customer's adapter depends on, and the deploy flows
// through. The fix is two-pronged:
//
//   1. In-process gate — pkg/deploydiff/engine.go::detectSchemaBreak
//      plus pkg/openapidiff.Compare ship on main since #860 / #869
//      / #874 and pin the apid-side break surface.
//
//   2. Pre-publish gate (this command) — operators running a
//      customer-facing service bump can run `gregale openapi diff
//      old.yaml new.yaml` in CI BEFORE merging; the command
//      exits non-zero on any BREAKING row so a service upgrade
//      that breaks the wire contract can be caught before it
//      reaches customers.
//
// The command is intentionally a thin pass-through to
// pkg/openapidiff — the differ + classifier + loader have been
// heavily unit-tested already (pkg/openapidiff/differ_test.go
// pins 13 classification rules). This file owns the IO + the
// exit-code contract only.
//
// Why not fold this into `gregale deploy --diff-schema`: the
// deploy verb hits the apid wire and depends on auth state +
// a live-store seeded with the app's row. The new verb is
// pure-local: feed it two YAMLs, get a deterministic exit
// code. CI scripts can run it without secrets, without an
// account, and without ever calling our wire.
//
// Exit codes:
//
//   - 0   — no BREAKING rows; informational rows (if any) still
//           printed but the gate is clear.
//   - 1   — usage error (wrong arg count, missing file, etc.).
//   - 2   — BREAKING rows present. CI consumes this as "do not
//           merge the bump". Surfaces ALL breaking rows before
//           exiting so a 50-line openapi flip doesn't get a
//           one-at-a-time fix loop.
//
// Output shape: one row per SchemaBreak, path/method/status/kind
// followed by the before/after values. The same kinds classify
// as the deploy-diff engine (openapidiff.SchemaKind*) so the
// prose is uniform across the two surfaces.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/openapidiff"
)

// breakingKinds is the closed set of openapidiff.SchemaKind values
// the CLI treats as a non-zero exit. Mirrors the apid-side break
// classifications (cmd/apid/handlers_diff.go + pkg/deploydiff/engine.go)
// but is the CLI's single source of truth for what blocks a
// service bump.
//
// Property-added, required-removed, and the noise kinds
// (description-whitespace, property-reorder, $ref drift) are
// absent from this set — they're informational, not blocking.
// A pinned release-process note explains the rule:
// "every BREAKING row is a wire-shape regression that customers
// cannot paper over with a software update."
var breakingKinds = map[openapidiff.SchemaKind]struct{}{
	openapidiff.SchemaKindTypeChange:        {},
	openapidiff.SchemaKindFieldRemoved:      {},
	openapidiff.SchemaKindRequiredAdded:     {},
	openapidiff.SchemaKindNullabilityChange: {},
}

func cmdOpenapi(args []string) int {
	parent, _ := lookupCliCommand("openapi")
	if len(args) == 0 {
		PrintUsage(os.Stderr,
			"usage: gregale openapi <diff|get|import|dry-run|rm> ...",
			"openapi")
		return 1
	}
	switch args[0] {
	case "diff":
		return cmdOpenapiDiff(args[1:])
	case "get":
		return cmdOpenapiGet(args[1:])
	case "import":
		return cmdOpenapiImport(args[1:])
	case "dry-run":
		return cmdOpenapiDryRun(args[1:])
	case "rm":
		return cmdOpenapiRemove(args[1:])
	}
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
	PrintUsage(os.Stderr,
		"usage: gregale openapi <subcommand>   (subcommands: diff|get|import|dry-run|rm)",
		"openapi")
	return 1
}

// cmdOpenapiDiff is the entry point for `gregale openapi diff`.
// It loads two openapi documents via openapidiff.LoadBytes
// (the same parser pkg/openapidiff uses), runs Compare, prints
// every SchemaBreak in the unified one-row-per-break prose,
// and exits non-zero iff any BREAKING row is present.
//
// Flags:
//
//   - --json (or FAAS_JSON=1) → NDJSON envelope, one record per
//     SchemaBreak. CI scripts that want to ingest the delta into
//     a downstream tool reach for this shape; the human-readable
//     default is the per-row prose used in PR reviews.
func cmdOpenapiDiff(args []string) int {
	fs := flag.NewFlagSet("openapi diff", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()
	if len(rest) != 2 {
		PrintUsage(os.Stderr,
			"usage: gregale openapi diff <baseline.yaml> <proposed.yaml>",
			"openapi")
		return 1
	}
	baseBytes, err := os.ReadFile(rest[0])
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "could not read %s: %v\n", rest[0], err)
		return 1
	}
	propBytes, err := os.ReadFile(rest[1])
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "could not read %s: %v\n", rest[1], err)
		return 1
	}
	base, err := openapidiff.LoadBytes(baseBytes)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "could not parse baseline: %v\n", err)
		return 1
	}
	prop, err := openapidiff.LoadBytes(propBytes)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "could not parse proposed: %v\n", err)
		return 1
	}

	breaks := openapidiff.Compare(base, prop)

	if jsonOutput {
		// NDJSON envelope: one record per SchemaBreak. Tests
		// pin the field set rather than byte-stability (the
		// encoding/json ordering is the go runtime's, not
		// ours), so the consumer decodes per record and stays
		// open to new SchemaBreak fields added in the future.
		for _, b := range breaks {
			out, err := json.Marshal(b)
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
				return 1
			}
			_, _ = fmt.Fprintln(osStdout, string(out))
		}
	} else {
		// One row per break. Empty result prints nothing.
		// Width-sensitive: operators paste the output into PR
		// reviews; widths matter less than the path/method/
		// status/kind anchor.
		for _, b := range breaks {
			marker := "INFO"
			if _, ok := breakingKinds[b.Kind]; ok {
				marker = "BREAKING"
			}
			_, _ = fmt.Fprintf(osStdout, "%s %s %s %s %s\n",
				marker, b.Path, b.Method, b.Status, b.Kind)
			if b.PathInSchema != "" {
				_, _ = fmt.Fprintf(osStdout, "    at: %s\n", b.PathInSchema)
			}
			if b.Before != nil {
				_, _ = fmt.Fprintf(osStdout, "    before: %v\n", b.Before)
			}
			if b.After != nil {
				_, _ = fmt.Fprintf(osStdout, "    after:  %v\n", b.After)
			}
		}
	}

	// Exit-code contract: 2 on any BREAKING row, 0 otherwise.
	// Informational rows do NOT bump the exit — the prose still
	// prints them, but the gate is clear. Operators reading CI
	// logs can see all the deltas even when the exit is 0; the
	// non-zero exit is reserved for "wire shape regression".
	for _, b := range breaks {
		if _, ok := breakingKinds[b.Kind]; ok {
			return 2
		}
	}
	return 0
}
