package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/onebox-faas/faas/pkg/api"
)

// commands_builds.go — the `gregale build` parent command and its
// `provenance` subcommand (ADR-038, Tier 3 / issue #197 B3.10-read
// half). The parent is the dispatch entry point; future build-
// surface subcommands (`gregale build logs`, `gregale build sbom` from
// Phase 3) land here without touching main.go's switch.
//
// build id shape mirrors deploymentIDPattern: 32-hex UUID without
// dashes. The server-side `text -> uuid` cast accepts the unhyphenated
// form; the dashboard / CLI tooling expects the compact shape so
// customers can paste a row id from the dashboard into `gregale build
// provenance <id>` without manual fixing-up.
var buildIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// dispatchBuild is the registered parent command name. Mirrors the
// dispatchDeployments / dispatchDeployment convention; main.go
// dispatches `gregale build <subcommand>` via this constant.
const dispatchBuild = "build"

// cmdBuild is the parent dispatcher. With zero arguments it prints
// usage; with `provenance` (or future subcommands) it fans to the
// matching helper. Unknown subcommands print a usage hint + return 1
// so the customer's "gregale build provanence" typo surfaces
// immediately rather than silently invoking a sibling.
func cmdBuild(args []string) int {
	parent, _ := lookupCliCommand("build")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale build <subcommand> [flags]\n  subcommands: provenance, sbom", "build")
		return 1
	}
	switch args[0] {
	case "provenance":
		return cmdBuildProvenance(args[1:])
	case "sbom":
		return cmdBuildSbom(args[1:])
	default:
		sug, _ := suggestSubcommand(args[0], parent)
		fmt.Fprintf(os.Stderr, "gregale build: unknown subcommand %q (known: provenance, sbom)\n", args[0])
		maybeSuggestSub(sug)
		return 1
	}
}

// cmdBuildSbom streams the CycloneDX SBOM for a build id (issue
// #299 / ADR-038 Phase 3). `gregale build sbom <id>` writes the raw
// SBOM JSON to stdout so the operator can pipe it straight to
// `cyclonedx-cli validate` or to a CI artifact publisher. No -j
// flag — the SBOM IS the JSON; the binary form is the only shape
// we ever serve.
//
// Errors:
//   - 401/unauthenticated → "Not logged in"
//   - 404 build_provenance_not_found → "no SBOM for this build"
//     (Phase-3 populator hasn't landed; or build predates the
//     schema column)
//   - other 4xx/5xx → server-supplied code + message.
func cmdBuildSbom(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale build sbom <id>", "build")
		return 1
	}
	id := args[0]
	if !buildIDPattern.MatchString(id) {
		PrintUsage(os.Stderr, "usage: gregale build sbom <id>   (id is 32 hex chars)", "build")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	body, err := client.GetBuildsIdSbom(context.Background(), id)
	if err != nil {
		return printErr("Could not fetch build SBOM", err)
	}
	if len(body) == 0 {
		return printErr("Could not fetch build SBOM", fmt.Errorf("no SBOM for build %s (Phase-3 populator may not have run)", id))
	}
	// Write raw bytes — preserves formatting for downstream tools
	// (jq, cyclonedx-cli validate, etc.). Avoid Fprintf's string
	// conversion since the body may contain non-UTF-8 bytes
	// (CycloneDX's package-purl strings are pure UTF-8 today, but
	// binary SBOM consumers shouldn't depend on that).
	_, _ = osStdout.Write(body)
	return 0
}

// cmdBuildProvenance renders the ADR-038 build_provenance row for
// a build id. `gregale build provenance <id>` is the customer-facing
// shape; mirrors `gregale deployment <id>` for cross-resource parity.
// With -j/--json it emits the raw BuildProvenanceResponse JSON;
// without it it prints a one-line-per-field text summary so the
// terminal-friendly output (the platform's universal default) keeps
// flow.
//
// Errors:
//   - 401/unauthenticated → "Not logged in"
//   - 404 build_provenance_not_found → propagates as the SDK's
//     *APIError with code build_provenance_not_found (the populator
//     WARN-logged path; the build row itself may exist)
//   - 404 generic → "no such build"
//   - other 4xx/5xx → server-supplied code + message.
func cmdBuildProvenance(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale build provenance <id> [-j]", "build")
		return 1
	}
	id := args[0]
	if !buildIDPattern.MatchString(id) {
		PrintUsage(os.Stderr, "usage: gregale build provenance <id>   (id is 32 hex chars)", "build")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	p, err := client.GetBuildsIdProvenance(context.Background(), id)
	if err != nil {
		return printErr("Could not fetch build provenance", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(p))
	}
	printProvenance(osStdout, p)
	return 0
}

// printProvenance writes a label-aligned text view. Kept as a
// helper so a future `gregale build list` (not in this PR) can reuse
// the rendering without re-deriving the column list. `w` is an
// io.Writer so the test harness can capture with a *bytes.Buffer
// (memory note: captureStdout race under -race; safeBuffer vs bare
// *bytes.Buffer with mutex).
//
// Empty fields still render so a pre-Phase-3 cache-hit build shows
// blank buildkit_version rather than skipping the line — auditors
// want consistency. Field order is the column order of the DB
// table with the PK + build_id first, the two timestamps last.
func printProvenance(w io.Writer, p api.BuildProvenanceResponse) {
	rows := []struct {
		label, value string
	}{
		{"id", p.ID},
		{"build_id", p.BuildID},
		{"buildkit_version", p.BuildkitVer},
		{"railpack_version", p.RailpackVer},
		{"base_digest", p.BaseDigest},
		{"source_sha256", p.SourceSHA256},
		{"source_url", p.SourceURL},
		{"commit_sha", p.CommitSHA},
		{"plan", p.Plan},
		{"runner_digest", p.RunnerDigest},
		{"builder_node_id", p.BuilderNodeID},
		{"started_at", p.StartedAt},
		{"finished_at", p.FinishedAt},
		{"sbom_storage_key", p.SBOMStorageKey},
	}
	for _, r := range rows {
		//nolint:errcheck // tabular printer writes to a typed writer; a failed Fprintf
		// at the tab stop is no different from a panic mid-row for the operator — both
		// show up as a malformed output and the CLI will exit non-zero on the parse below.
		fmt.Fprintf(w, "%-22s %s\n", r.label+":", r.value)
	}
}
