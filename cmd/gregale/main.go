// Command gregale is the customer-facing CLI and the primary interface to the
// platform (docs/faas_ux_spec.md §3). Everything the platform does is
// possible from here.
//
// Exit codes follow docs/faas_ux_spec.md §3.2: 0 ok, 1 user error, 2 auth,
// 3 platform/infra. See also the brand-residue sweep that landed in the
// same PR as the rename — every string in this file should say `gregale`,
// not `faas`. Top-level help is rendered from cli_meta.go, so a new
// dispatcher arm must add a matching manifest entry.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/wire"
)

// docsURL is the canonical link printed at the bottom of the usage string.
var docsURL = docsSiteURL

func topLevelUsage(showAdvanced bool) string {
	var b strings.Builder
	b.WriteString("gregale — deploy apps and functions that scale to zero.\n\n")
	b.WriteString("Usage:\n  gregale <command> [flags]\n\n")
	if showAdvanced {
		b.WriteString("Customer commands:\n")
		writeGroupedCommands(&b, customerCliCommands())
		b.WriteString("\nAdvanced/operator compatibility:\n")
		for _, command := range advancedCliCommands() {
			fmt.Fprintf(&b, "  %-22s %s\n", command.Name, command.Short)
		}
		b.WriteString("  help                   Show this help message\n")
	} else {
		b.WriteString("Get started:\n")
		for _, name := range []string{"start", "login", "init", "deploy", "dev", "apps", "logs", "inspect", "doctor", "status", "openapi"} {
			command, _ := lookupCliCommand(name)
			fmt.Fprintf(&b, "  %-22s %s\n", command.Name, command.Short)
		}
		b.WriteString("  help                   Show command help\n")
		b.WriteString("\nExamples:\n")
		b.WriteString("  gregale start\n  gregale deploy --plan\n  gregale deploy --path ./api\n")
		b.WriteString("\nRun 'gregale help --all' for every command, or 'gregale help deploy' for a topic.\n")
	}
	b.WriteString("Search by task: gregale help --search \"reduce costs\"\n")
	b.WriteString("\nRun 'gregale <command> --help' for command details.\n\n")
	b.WriteString("Global flags:\n")
	b.WriteString("  --json                 Machine-readable output where supported. Slices emit\n")
	b.WriteString("                         NDJSON; scalars emit indented JSON; errors print\n")
	b.WriteString("                         RFC 7807 to stderr. Equivalent env: FAAS_JSON=1.\n")
	b.WriteString("                         Interactive-only commands retain human prompts.\n")
	b.WriteString("  --non-interactive      Disable prompts and browser launches (before the command).\n")
	b.WriteString("  --profile NAME         Select a connection profile (before the command).\n")
	fmt.Fprintf(&b, "Docs: %s\n", docsURL)
	return b.String()
}

// writeGroupedCommands keeps the root help useful as the manifest grows. The
// manifest remains ordered for completion/man output, while customer discovery
// is grouped around the API-hosting workflow.
func writeGroupedCommands(b *strings.Builder, commands []cliCommand) {
	groups := []string{"Core", "API", "Data", "Delivery", "Observe", "Advanced"}
	byGroup := make(map[string][]cliCommand, len(groups))
	for _, command := range commands {
		group := cliHelpGroup(command)
		byGroup[group] = append(byGroup[group], command)
	}
	for _, group := range groups {
		groupCommands := byGroup[group]
		if len(groupCommands) == 0 {
			continue
		}
		fmt.Fprintf(b, "\n%s:\n", group)
		for _, command := range groupCommands {
			fmt.Fprintf(b, "  %-22s %s\n", command.Name, command.Short)
		}
	}
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func init() {
	// Tier A8 / ADR-083: gregaleVersion is the value substituted into
	// the man page header (`.TH GREGALE 1 "date" "gregale" "version"`). Wired once
	// at process boot from wire.Version so the man page reflects the
	// binary the user is running, not a hardcoded literal.
	gregaleVersion = wire.Version
}

func run(args []string) (status int) {
	previousJSON, previousUsageHelp, previousPath := jsonOutput, jsonUsageHelp, invokedCommandPath
	defer func() {
		jsonOutput = previousJSON
		jsonUsageHelp = previousUsageHelp
		invokedCommandPath = previousPath
	}()
	previousAutomation := nonInteractive
	defer func() { nonInteractive = previousAutomation }()
	nonInteractive = false
	var automationErr error
	args, automationErr = extractAutomationFlag(args)
	previousProfile := profileOverride
	defer func() { profileOverride = previousProfile }()
	profileOverride = ""
	var profileErr error
	args, profileErr = extractConnectionProfile(args)
	invalidJSON := invalidJSONFlagValue(args)
	// Issue #64 D1: every command accepts --json (top-level). Strip
	// it before dispatch and set jsonOutput so per-command printers
	// switch to NDJSON/indented JSON. FAAS_JSON=1 env also works.
	args = applyJSONFlag(args)
	if invalidJSON != "" {
		PrintUsage(os.Stderr, "invalid --json value "+invalidJSON+"; use true or false", "cli")
		return 1
	}
	if automationErr != nil {
		return printErr("Invalid automation option", automationErr)
	}
	if profileErr != nil {
		return printErr("Invalid connection profile", profileErr)
	}
	jsonUsageHelp = hasHelpFlag(args)
	invokedCommandPath = publicCommandPath(args)
	if len(args) == 0 {
		fmt.Print(topLevelUsage(false))
		return 0
	}
	// Help is always local and comes from the same manifest as completion and
	// the generated reference. This also handles flags preceding --help.
	if len(args) > 1 && args[0] != "help" && hasHelpFlag(args[1:]) {
		if command, ok := lookupCliCommand(args[0]); ok {
			if printManifestHelp(osStdout, command, args[1:]) {
				return 0
			}
		}
	}
	// Task discovery is local even when the selected connection is unavailable.
	if args[0] == "help" && hasHelpSearchFlag(args[1:]) {
		return cmdHelpSearch(args[1:])
	}
	if err := validateSelectedProfile(); err != nil {
		return printErr("Invalid connection profile", err)
	}
	switch args[0] {
	case "profile":
		return cmdProfile(args[1:])
	case "version", "--version", "-v":
		// `gregale version --help` prints usage + docs link; bare
		// `gregale version foo` still prints the version string (POSIX
		// convention — git does the same).
		if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
			PrintUsage(os.Stderr, "usage: gregale version", "version")
			return 0
		}
		if jsonOutput {
			// production-us hunt #4: --json version printed "gregale dev".
			return jsonOut(writeJSON(map[string]string{
				"version": wire.Version, "git_sha": wire.GitSHA, "build_time": wire.BuildTime,
			}))
		}
		fmt.Printf("gregale %s\n", wire.Version)
		return 0
	case "help", "--help", "-h":
		if len(args) == 1 || (len(args) == 2 && (args[1] == "--all" || hasHelpFlag(args[1:]))) {
			fmt.Print(topLevelUsage(len(args) == 2 && args[1] == "--all"))
			return 0
		}
		if command, ok := lookupCliCommand(args[1]); ok {
			if printManifestHelp(osStdout, command, args[2:]) {
				return 0
			}
		}
		message := "unknown help topic: " + strings.Join(args[1:], " ")
		if len(args) == 2 {
			if suggestion, ok := suggestCommand(args[1]); ok {
				message += fmt.Sprintf("; did you mean 'gregale help %s'?", suggestion)
			}
		}
		PrintUsage(os.Stderr, message, "cli")
		return 1
	case "completion":
		// Tier A8 / ADR-083. Routes to one of bash|zsh|fish|powershell
		// via cmdCompletion; the dispatcher is in completion.go.
		return cmdCompletion(args[1:])
	case "config":
		return cmdConfig(args[1:])
	case "link":
		return cmdLink(args[1:])
	case "unlink":
		return cmdUnlink(args[1:])
	case "context":
		return cmdContext(args[1:])
	case "capabilities":
		return cmdCapabilities(args[1:])
	case "man":
		// Tier A8 / ADR-083. No arg → gregale(1); one arg →
		// gregale-<command>(1). Dispatcher is in man.go.
		return cmdMan(args[1:])
	case "login":
		return cmdLogin(args[1:])
	case dispatchSignup:
		return cmdSignup(args[1:])
	case "logout":
		if len(args) > 1 {
			PrintUsage(os.Stderr, "usage: gregale logout", "auth")
			return 1
		}
		return cmdLogout()
	case "whoami":
		if len(args) > 1 {
			PrintUsage(os.Stderr, "usage: gregale whoami", "auth")
			return 1
		}
		return cmdWhoami()
	case "add":
		return cmdAdd(args[1:])
	case "bucket":
		return cmdBucket(args[1:])
	case "bindings":
		return cmdBindings(args[1:])
	case "deploy":
		return cmdDeployTarball(args[1:])
	case "start":
		return cmdStart(args[1:])
	case "diff":
		return environmentDiff(args[1:])
	case "dev":
		return cmdDev(args[1:])
	case "canary":
		return cmdCanary(args[1:])
	case "preview":
		// Mega-C PR-1 / issue #961 leaf 3: `gregale preview
		// destroy <slug>`. Currently a single sub-command;
		// future sub-commands (list, inspect) extend the
		// switch in cmdPreview itself rather than living as
		// siblings in main.go.
		return cmdPreview(args[1:])
	case "scan":
		// Phase 3 (repo decomposition) — dry-run entry point. Prints
		// the plan as a table or --json, never writes. The
		// transactional apply path lives in cmdDeployTarball when
		// --yes/--json/--only/--project-slug are set.
		return cmdScan(args[1:])
	case "projects":
		return cmdProjects(args[1:])
	case "init":
		return cmdInit(args[1:])
	case "mcp":
		return cmdMCP(args[1:])
	case "connect":
		return cmdConnect(args[1:])
	case "github":
		return cmdGithub(args[1:])
	case "open":
		return cmdOpen(args[1:])
	case dispatchDoctor:
		// Error-explanations cluster (spec §6.4 amendment 1):
		// customer preflight that scans the local cwd for the
		// 8 source-side failure modes. Routes to commands_doctor.go.
		return cmdDoctor(args[1:])
	case dispatchApps:
		// `gregale apps ls` is an alias for the default list action.
		if len(args) > 1 && args[1] == "ls" {
			if len(args) != 2 {
				PrintUsage(os.Stderr, "usage: gregale apps [ls]", "apps")
				return 1
			}
			return cmdApps()
		}
		if len(args) > 1 && args[1] == subRestore {
			return cmdAppsRestore(args[2:])
		}
		// `gregale apps routes <slug>` — ADR-093 Tier B item #2
		// operator entry point. Must come before the default
		// fall-through so a slug-shaped token ("routes") is never
		// misread as the delete path. The delete path requires
		// `-q`/`--quiet`; the routes path takes the slug as args[2]
		// (a 3-token form, distinct from the 2-token delete form).
		if len(args) > 1 && args[1] == "routes" {
			// CR-B1: CodeQL off-by-one (alerts #208 + #209) flagged
			// the unguarded `args[2]` / `args[3:]` access below.
			// Outer guard verifies args[1] == "routes" but does not
			// bounds-check args[2]. Forwarding an empty slug to the leaf
			// gives a usage error instead of listing every app.
			if len(args) < 3 {
				return cmdAppsRoutes("", nil)
			}
			return cmdAppsRoutes(args[2], args[3:])
		}
		// `gregale apps tcp <slug> [list|add|enable|disable|rm]` —
		// app-owned raw TCP listener management. Keep this before the
		// delete fall-through so the subcommand is never parsed as a slug.
		if len(args) > 1 && args[1] == subTCPListeners {
			if len(args) < 3 {
				return cmdAppsTCP("", nil)
			}
			return cmdAppsTCP(args[2], args[3:])
		}
		if len(args) > 1 && args[1] == subUDPListeners {
			if len(args) < 3 {
				return cmdAppsUDP("", nil)
			}
			return cmdAppsUDP(args[2], args[3:])
		}
		// `gregale apps streaming-cap <slug>` — ADR-102 D6 operator
		// entry point. Same shape as the routes arm above: 3-token
		// form (`apps streaming-cap <slug>`), placed BEFORE the
		// `-q`/`--quiet` delete fall-through so a slug-shaped token
		// never hits the delete path. Mirrors the routes CodeQL
		// off-by-one guard (`len(args) < 3` falls through).
		if len(args) > 1 && args[1] == subStreamingCap {
			if len(args) < 3 {
				return cmdAppsStreamingCap("", nil)
			}
			return cmdAppsStreamingCap(args[2], args[3:])
		}
		// `gregale apps -q <slug>` is the delete path.
		if len(args) > 1 && (strings.SplitN(args[1], "=", 2)[0] == "-q" || strings.SplitN(args[1], "=", 2)[0] == "--quiet" || strings.SplitN(args[1], "=", 2)[0] == "--yes" || strings.SplitN(args[1], "=", 2)[0] == "--dry-run") {
			// Preserve the quiet flag for cmdAppsRm. Dropping it here
			// made the documented `gregale apps -q <slug>` command
			// unexpectedly enter the typed-confirmation path.
			return cmdAppsRm(args[1:])
		}
		if len(args) > 1 {
			PrintUsage(os.Stderr, "usage: gregale apps [ls|restore <slug>|routes <slug>|tcp <slug>|udp <slug>|streaming-cap <slug>|-q|--quiet|--yes <slug>]", "apps")
			return 1
		}
		return cmdApps()
	case dispatchDeployments:
		// `gregale deployments [--app SLUG] [--limit N|--before C|--all]` — list.
		// Place before appSlugFallback so the singular never shadows it.
		return cmdDeployments(args[1:])
	case dispatchDeployment:
		// `gregale deployment <id>` — get one. Must come before appSlugFallback
		// so the singular is never misread as an app slug.
		return cmdDeployment(args[1:])
	case "data-api":
		return cmdDataAPI(args[1:])
	case dispatchPostgres:
		return cmdPostgres(args[1:])
	case "realtime":
		return cmdRealtime(args[1:])
	case dispatchDeploys:
		// ADR-117 companion read surface (post-stream stage
		// summary). Routes to cmdDeploys in deploys_show.go,
		// which dispatches `deploys show <id>` to the new
		// GET /v1/deployments/{id}/stages endpoint. Distinct
		// from the singular `deployment` (flag-shaped drill-downs)
		// and plural `deployments` (paginated list) on purpose:
		// the noun-form `deploys` is the read-only cluster verb
		// for future siblings (timeline, events, artifacts).
		return cmdDeploys(args[1:])
	case dispatchBuild:
		// `gregale build provenance <id>` — ADR-038 / Tier 3 / issue
		// #197 B3.10-read half. The parent dispatch is in
		// commands_builds.go::cmdBuild; future build-surface
		// subcommands (`logs`, `sbom`) land there without
		// touching this switch.
		return cmdBuild(args[1:])
	case dispatchInspect:
		// Issue #952 — `gregale inspect <slug> --upstreams`
		// (ADR-098 §9.A cluster follow-up). Read-only operator
		// surface for diagnosing why schedd places a given app
		// where it does. The verb-level dispatcher lives in
		// commands_inspect.go; future leaves (--env, --crons)
		// add their own flag to cmdInspect and a sibling
		// commands_inspect_<noun>.go file.
		return cmdInspect(args[1:])
	case appSlugFallback:
		// Routes to cmdAppDispatch which knows the new scale/rename
		// subcommand form and falls back to the legacy flag-form
		// (commands2.go::cmdApp) for backwards compat.
		return cmdAppDispatch(args[1:])
	case "ps":
		return cmdPS(args[1:])
	case statusLiteral:
		return cmdStatus(args[1:])
	case "env":
		return cmdEnv(args[1:])
	case "plan":
		return cmdPlan(args[1:])
	case "dashboard":
		return cmdDashboard(args[1:])
	case "rollback":
		return cmdRollback(args[1:])
	case "rollouts":
		// SAFE-RELEASES-R (issue #976 / ADR-122) — operator
		// manual-recovery escape hatch. See
		// cmd/gregale/commands_rollouts.go.
		return cmdRollouts(args[1:])
	case "park":
		return cmdPark(args[1:])
	case "wake":
		return cmdWake(args[1:])
	case "test":
		return cmdTest(args[1:])
	case "chaos":
		return cmdChaos(args[1:])
	case "traffic":
		return cmdTraffic(args[1:])
	case "mirror":
		return cmdMirror(args[1:])
	case "log-drains":
		return cmdLogDrains(args[1:])
	case "cache":
		return cmdCache(args[1:])
	case dispatchUploadCache:
		return cmdUploadCache(args[1:])
	case "domains":
		return cmdDomains(args[1:])
	case "tenant-surfaces":
		return cmdTenantSurfaces(args[1:])
	case "flags":
		return cmdFlags(args[1:])
	case "platform-tenants":
		return cmdPlatformTenants(args[1:])
	case "edge-rules":
		// PR 2 of Edge Rules rollout: customer CLI wrapper around the
		// /v1/apps/{slug}/edge-rules CRUD surface (PR 1 #799). Sub-
		// commands live in commands_edge_rules.go; the dispatcher
		// itself is cmdEdgeRules. --json round-trips through the
		// pkg/api SDK methods (ListEdgeRules / CreateEdgeRule / etc.).
		return cmdEdgeRules(args[1:])
	case "openapi":
		// Issue #976 / ADR-122 / SAFE-RELEASES-D: pre-publish
		// schema-drift gate. Single subcommand `diff`
		// (commands_openapi.go) compares two openapi.yaml files
		// using the same pkg/openapidiff.Compare the apid
		// deploy-diff engine uses — exits non-zero on BREAKING
		// rows so CI can pin a contract across a service bump.
		return cmdOpenapi(args[1:])
	case "routes":
		return cmdRoutes(args[1:])
	case "cors":
		// CORS improvements D5: thin shim over the typed SDK
		// helper CreateCORSEdgeRule. Sub-commands live in
		// commands_cors.go; the dispatcher is cmdCors. Not a
		// parallel wire surface - customers who need the full
		// edge-rule power (priority, enable/disable, multi-host)
		// still go through `gregale edge-rules create --kind cors`.
		return cmdCors(args[1:])
	case "crons":
		return cmdCrons(args[1:])
	case "triggers":
		return cmdTriggers(args[1:])
	case "workers":
		return cmdWorkers(args[1:])
	case "delayed-task":
		// Tier D: scheduled-at deferred invocations (issue #557 /
		// ADR-072 sibling). Mirrors crons for dispatcher shape
		// (add|get|cancel); cmdDelayedTask lives in
		// commands_delayed_task.go.
		return cmdDelayedTask(args[1:])
	case "registry":
		// Tier D: per-app private container registry credentials
		// (issue #461 / ADR-062). Mirrors alerts for the
		// list|set|rm dispatcher shape; cmdRegistry lives in
		// commands_registry.go.
		return cmdRegistry(args[1:])
	case "webhooks":
		// Issue #476 / ADR-076 — outbound webhook subscriptions
		// and delivery ledger. Mirrors the crons surface (list /
		// add / update / rm + deliveries / retry). Routes through
		// authedClient() the same way crons does; the dispatcher
		// itself lives in schedd (pkg/webhook/dispatcher.go).
		return cmdWebhooks(args[1:])
	case "keys":
		return cmdKeys(args[1:])
	case dispatchTrustedPublishers:
		// Issue #472 / ADR-054 — operator CLI for the per-app
		// cosign trusted-publisher list. Admin API key required;
		// every leaf calls authedClient() and hits apid. The
		// operator-only surfaces (sign-keys, node-key, pki,
		// host-age, manifest, release, backup) moved to
		// `gregalectl` in PR-6.5 — this is the only operator verb
		// that stayed in `gregale` because it's a customer/admin
		// API surface.
		return cmdTrustedPublishers(args[1:])
	case "secrets":
		return cmdSecrets(args[1:])
	case "github-webhook-secret":
		// PR-D / ADR-012 §7 amendment. Distinct top-level
		// command; dispatches to a single verb (set) for the
		// per-tenant webhook secret rotation.
		return cmdGithubWebhookSecret(args[1:])
	case "account":
		return cmdAccount(args[1:])
	case "alerts":
		// Tier C: per-app alert rules (list|add|info|update|rm|
		// rotate-secret). Mirrors `webhooks` for dispatcher shape;
		// MFA-required writes, server-validated closed-set enums.
		return cmdAlerts(args[1:])
	case "usage":
		// cmdUsage dispatches: bare `gregale usage` → per-app rows;
		// `gregale usage daily [--day X]` → per-day breakdown;
		// `gregale usage storage [--day X]` → per-app storage bytes;
		// `gregale usage summary [--month X]` → account roll-up.
		// Unknown positionals are rejected by the dispatcher.
		return cmdUsage(args[1:])
	case "wake-timeline":
		// Tier D: per-wake event stream (issue #517 PR-C / ADR-064).
		// Mirrors cmdAuditEventsGet for the positional shape;
		// cmdWakeTimeline lives in commands_wake_timeline.go.
		return cmdWakeTimeline(args[1:])
	case "invoke":
		// Tier C: functional smoke test. POST /v1/apps/{slug}/invoke
		// (sync drain through the gateway) or /invoke/async (returns
		// the status_url). Same handler the dashboard's "Test" button
		// uses; auth + MFA + deploy:write scope.
		return cmdInvoke(args[1:])
	case "run":
		// ADR-171: execute untrusted source in a fresh, networkless
		// disposable microVM and tear it down after the terminal result.
		return cmdRun(args[1:])
	case "runs":
		// ADR-171 lifecycle reads/cancellation for disposable runs.
		return cmdRuns(args[1:])
	case "invocations":
		// Tier C: per-account invocation ledger (issue #394 follow-up).
		// Mirrors `audit-events` for dispatcher shape.
		return cmdInvocations(args[1:])
	case "customer-operations":
		return cmdCustomerOperations(args[1:])
	case "operations":
		// Durable exclusive operations: reconcile coordination policies and
		// trigger bindings, submit work, inspect ownership, or cancel it.
		return cmdOperations(args[1:])
	case "invoices":
		return cmdInvoices(args[1:])
	case "jobs":
		// Issue #1184 Workstream A: run-to-completion jobs
		// (list|add|info|update|rm|run|runs|cancel|tasks|logs).
		// Mirrors `crons` for dispatcher shape. Implementation
		// lives in commands_jobs.go (cmdJobs).
		return cmdJobs(args[1:])
	case "workflows":
		// ADR-081: durable execution workflows (list|run|status|steps|resume|resumes|cancel|events).
		return cmdWorkflows(args[1:])
	case "automations":
		// Manage declarative automation definitions: validate, save a draft, publish.
		return cmdAutomations(args[1:])
	case "commit":
		return cmdCommit(args[1:])
	case "events":
		// EPIC #1278: publish a tenant-scoped CloudEvents envelope into
		// the internal matcher and async fan-out path.
		return cmdEvents(args[1:])
	case "send":
		return cmdSend(args[1:])
	case "deliver":
		return cmdDeliver(args[1:])
	case "issues":
		return cmdIssues(args[1:])
	case "debug":
		// ADR-127: production debugger (request evidence, regression
		// watch, deployment compare, safe replay, and incident bundles).
		// Mirrors `invocations` for dispatcher shape.
		return cmdDebug(args[1:])
	case "trace":
		// Account-scoped W3C trace lookup composed from redacted debugger
		// evidence for each app.
		return cmdTrace(args[1:])
	case "billing":
		// Issue #253: dashboard's "Open Stripe billing portal"
		// button has a CLI twin. Subcommands live in
		// commands_billing.go.
		return cmdBilling(args[1:])
	case "admin":
		return cmdAdmin(args[1:])
	case "logs":
		return cmdLogs(args[1:])
	case "tail":
		return cmdTail(args[1:])
	case "audit-events":
		// Wave 0 PR-C / ADR-047: customer/operator CLI for the
		// /v1/audit-events surface. Default scope = caller's own
		// account; --kind-prefix filters (stateless.advisory is
		// the Wave 0 use case); --include-anonymous surfaces the
		// rare subject=NULL defensive rows. Singular `get <id>`
		// closes the Tier B audit gap (operator post-mortem).
		return cmdAuditEvents(args[1:])
	case "metrics":
		// Move 1 PR-A: CLI twin for GET /v1/apps/{slug}/metrics.
		// Same data shape the dashboard panel renders, in the
		// terminal where the rest of the debugging happens.
		// Tier C: --account flips to GET /v1/account/metrics
		// (account-wide aggregate).
		return cmdMetrics(args[1:])
	case "analytics":
		// Customer-facing historical request analytics with bounded
		// route/country/referrer/client/status groupings.
		return cmdAnalytics(args[1:])
	case "throttle-suggestions":
		// Phase 4 D2: CLI twin for GET /v1/apps/{slug}/throttle-suggestions.
		// Mirrors the read-only recommender + dry-run preview.
		// --dry-run + --candidate-rps + --candidate-burst is the
		// guard-rail for the customer's own probe value (not
		// auto-apply). See ADR-104 amendment 5.
		return cmdThrottleSuggestions(args[1:])
	case "slo":
		// Move 2 PR-A: CLI twin for GET /v1/apps/{slug}/slo
		// (issue #696 / ADR-082). Closed-set windowed SLO
		// panel (1h | 24h | 7d) — distinct from `metrics` which
		// is the 5m dashboard panel.
		return cmdSLO(args[1:])
	case "queue":
		// Tier C extension: tail + send|receive|state|peek|
		// dead-letter|ack. Dispatcher lives in commands5.go.
		return cmdQueueDispatch(args[1:])
	case "dlq":
		// Issue #1278: unified app-scoped dead-letter operator surface.
		return cmdDLQ(args[1:])
	case "mfa":
		// IAM-2 / issue #186: MFA enrollment + step-up + recovery.
		// Routes through authedClient(); the dispatcher itself lives
		// in commands_mfa.go.
		return cmdMfa(args[1:])
	case "orgs":
		// IAM-6 / ADR-061 / issue #190: org CRUD + members +
		// invitations + ownership transfer. Sub-dispatchers live in
		// commands_orgs.go (`orgs members ...`, `orgs invitations ...`).
		return cmdOrgs(args[1:])
	case "invitations":
		// Standalone invitation entry points (no slug context).
		// `invitations peek <token>` is unauth-friendly (the server
		// validates the token itself); `invitations accept <token>`
		// requires an authenticated session + 5-min step-up.
		return cmdInvitations(args[1:])
	case "overage-cap":
		// Tier B audit gap: per-account overage cap (€0.01/GB-h
		// above the plan's included GB-h). schedd refuses new wakes
		// once the cap is hit.
		return cmdOverageCap(args[1:])
	case "mail":
		// Issue #246 acceptance item 6: operator dry-run for the
		// outbound mail pipeline. `gregale mail dry-run` renders
		// every production template against a fixture account
		// + day and prints the wire payload as JSON so an
		// operator can eyeball subject/body/headers before
		// flipping the box to FAAS_MAIL_TRANSPORT=resend.
		return cmdMail(args[1:])
	default:
		if suggestion, ok := suggestCommand(args[0]); ok {
			return printErr("Unknown command", fmt.Errorf("gregale: unknown command %q; did you mean 'gregale %s'?", args[0], suggestion))
		}
		return printErr("Unknown command", fmt.Errorf("gregale: unknown command %q; run 'gregale help' for usage", args[0]))
	}
}

// printManifestHelp resolves verb paths without invoking a command parser or
// making a network request. For --help, unrelated flags and positionals are
// ignored so `deploy --plan --help` still shows deploy help.
func printManifestHelp(w io.Writer, command cliCommand, args []string) bool {
	var selected []cliSub
	positionalCount := 0
	nestedPositionalCounts := map[int]int{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--help" || arg == "-h" {
			break
		}
		if arg == "--" {
			break
		}
		if strings.HasPrefix(arg, "-") {
			if !strings.Contains(arg, "=") {
				flag := manifestFlag(command, selected, strings.TrimLeft(arg, "-"))
				if flag != nil && !flag.Bool && (flag.Value != "" || flag.Req || len(flag.ClosedSet) > 0) && i+1 < len(args) {
					i++
				}
			}
			continue
		}
		var choices []cliSub
		if len(selected) == 0 {
			choices = command.Subcommands
		} else {
			choices = selected[len(selected)-1].Subcommands
		}
		// Slug-first command families reserve their first positional before
		// considering verbs. A slug such as "network" is valid data, even
		// when it happens to match one of the command's verbs.
		if command.SubcommandsAfterPositionals && len(selected) == 0 && positionalCount < len(command.Positionals) {
			positionalCount++
			continue
		}
		if len(selected) > 0 {
			depth := len(selected) - 1
			parent := selected[depth]
			if parent.SubcommandsAfterPositionals && nestedPositionalCounts[depth] < len(parent.Positionals) {
				nestedPositionalCounts[depth]++
				continue
			}
		}
		if sub, ok := findCliSubcommand(choices, arg); ok {
			selected = append(selected, sub)
			nestedPositionalCounts[len(selected)-1] = 0
		} else if len(choices) > 0 {
			return false
		}
	}
	switch len(selected) {
	case 0:
		printLocalCommandHelp(w, command)
	case 1:
		helpPath := command.Name + " " + selected[0].Name
		if helpPath == "app scale" {
			PrintUsage(w, appScaleUsage, command.DocSlug)
			return true
		}
		if helpPath == "rollouts recover" {
			PrintUsage(w, rolloutsRecoverUsage, command.DocSlug)
			return true
		}
		if helpPath == "rollouts status" {
			PrintUsage(w, rolloutsStatusUsage, command.DocSlug)
			return true
		}
		if helpPath == "deploys retry" {
			PrintUsage(w, deploysRetryUsage, command.DocSlug)
			return true
		}
		printLocalSubcommandHelp(w, command, selected[0])
	case 2:
		printLocalLeafHelp(w, command, selected[0], selected[1])
	default:
		return false
	}
	return true
}

func manifestFlag(command cliCommand, selected []cliSub, name string) *cliFlag {
	flags := command.Flags
	if len(selected) > 0 {
		flags = selected[len(selected)-1].Flags
	}
	for i := range flags {
		if flags[i].Name == name {
			return &flags[i]
		}
	}
	return nil
}

func printLocalCommandHelp(w io.Writer, command cliCommand) {
	// Release-management commands use verb-first syntax with the slug on
	// the leaf. The generic manifest renderer cannot express that shape
	// (it would print `rollouts <slug> <command>`), so keep these two
	// public help paths aligned with their actual dispatchers.
	switch command.Name {
	case "rollback":
		_, _ = fmt.Fprintf(w, "%s\n\nUsage:\n  %s\n", command.Short, strings.TrimPrefix(rollbackUsage, "usage: "))
		_, _ = fmt.Fprintf(w, "  %s\n", strings.TrimPrefix(rollbackStatusUsage, "usage: "))
		if len(command.Examples) > 0 {
			_, _ = fmt.Fprintln(w, "\nExamples:")
			printCLIExamples(w, command.Examples)
		}
		_, _ = fmt.Fprintf(w, "\nDocs: %s\n", docsURLForTopic(command.DocSlug))
		return
	case "rollouts":
		PrintUsage(w, rolloutsUsage, command.DocSlug)
		return
	}
	usage := "gregale " + command.Name
	if len(command.Subcommands) > 0 && !command.SubcommandsAfterPositionals {
		usage += " <" + command.subcommandChoice() + ">"
	}
	for _, positional := range command.Positionals {
		usage += " " + positional
	}
	if len(command.Subcommands) > 0 && command.SubcommandsAfterPositionals {
		usage += " <" + command.subcommandChoice() + ">"
	}
	if len(command.Flags) > 0 {
		usage += " [flags]"
	}
	_, _ = fmt.Fprintf(w, "%s\n\nUsage:\n  %s\n", command.Short, usage)
	if len(command.Subcommands) > 0 {
		_, _ = fmt.Fprintln(w, "\nCommands:")
		for _, sub := range command.Subcommands {
			_, _ = fmt.Fprintf(w, "  %-18s %s\n", sub.Name, sub.Short)
		}
	}
	if len(command.Flags) > 0 {
		_, _ = fmt.Fprintln(w, "\nFlags:")
		for _, flag := range command.Flags {
			_, _ = fmt.Fprintf(w, "  --%-16s %s\n", flag.Name, flag.Short)
		}
	}
	if command.Name == "deploy" {
		_, _ = fmt.Fprintln(w, "\nSource defaults to committed HEAD when origin exists; otherwise it uses local files.")
		_, _ = fmt.Fprintln(w, "Use --source=head to require a commit, --source=worktree to include local changes, or --path DIR for a subtree.")
	}
	if len(command.Examples) > 0 {
		_, _ = fmt.Fprintln(w, "\nExamples:")
		printCLIExamples(w, command.Examples)
	}
	_, _ = fmt.Fprintf(w, "\nDocs: %s\n", docsURLForTopic(command.DocSlug))
}

func printLocalSubcommandHelp(w io.Writer, command cliCommand, sub cliSub) {
	usage := localHelpSubcommandPath(command, []string{sub.Name})
	if len(sub.Subcommands) > 0 {
		usage += " <" + sub.subcommandChoice() + ">"
	}
	if sub.SubcommandsAfterPositionals {
		usage = localHelpArguments(usage, nil, sub.Flags, false)
	} else {
		usage = localHelpArguments(usage, sub.Positionals, sub.Flags, sub.FlagsAfterPositionals)
	}
	_, _ = fmt.Fprintf(w, "%s\n\nUsage:\n  %s\n", sub.Short, usage)
	if len(sub.Subcommands) > 0 {
		_, _ = fmt.Fprintln(w, "\nCommands:")
		for _, child := range sub.Subcommands {
			_, _ = fmt.Fprintf(w, "  %-18s %s\n", child.Name, child.Short)
		}
	}
	if len(sub.Flags) > 0 {
		_, _ = fmt.Fprintln(w, "\nFlags:")
		for _, flag := range sub.Flags {
			printLocalHelpFlag(w, flag)
		}
	}
	if len(sub.Examples) > 0 {
		_, _ = fmt.Fprintln(w, "\nExamples:")
		printCLIExamples(w, sub.Examples)
	}
	_, _ = fmt.Fprintf(w, "\nDocs: %s\n", docsURLForTopic(command.DocSlug))
}

func printLocalLeafHelp(w io.Writer, command cliCommand, parent, leaf cliSub) {
	usage := localHelpSubcommandPath(command, []string{parent.Name, leaf.Name})
	usage = localHelpArguments(usage, leaf.Positionals, leaf.Flags, leaf.FlagsAfterPositionals)
	_, _ = fmt.Fprintf(w, "%s\n\nUsage:\n  %s\n", leaf.Short, usage)
	if len(leaf.Flags) > 0 {
		_, _ = fmt.Fprintln(w, "\nFlags:")
		for _, flag := range leaf.Flags {
			printLocalHelpFlag(w, flag)
		}
	}
	if len(leaf.Examples) > 0 {
		_, _ = fmt.Fprintln(w, "\nExamples:")
		printCLIExamples(w, leaf.Examples)
	}
	_, _ = fmt.Fprintf(w, "\nDocs: %s\n", docsURLForTopic(command.DocSlug))
}

func localHelpCommandPath(command cliCommand) string {
	path := "gregale " + command.Name
	if command.SubcommandsAfterPositionals {
		for _, positional := range command.Positionals {
			path += " " + positional
		}
	}
	return path
}

func localHelpSubcommandPath(command cliCommand, names []string) string {
	path := localHelpCommandPath(command)
	choices := command.Subcommands
	for _, name := range names {
		sub, ok := findCliSubcommand(choices, name)
		path += " " + name
		if !ok {
			choices = nil
			continue
		}
		if sub.SubcommandsAfterPositionals {
			for _, positional := range sub.Positionals {
				path += " " + positional
			}
		}
		choices = sub.Subcommands
	}
	return path
}

func localHelpArguments(path string, positionals []string, flags []cliFlag, flagsAfterPositionals bool) string {
	hasOptional := false
	appendRequiredFlags := func() {
		for _, flag := range flags {
			if flag.Req {
				path += " " + mdFlagSyntax(flag)
			} else {
				hasOptional = true
			}
		}
	}
	if !flagsAfterPositionals {
		appendRequiredFlags()
	}
	for _, positional := range positionals {
		path += " " + positional
	}
	if flagsAfterPositionals {
		appendRequiredFlags()
	}
	if hasOptional {
		path += " [flags]"
	}
	return path
}

func printLocalHelpFlag(w io.Writer, flag cliFlag) {
	required := ""
	if flag.Req {
		required = " (required)"
	}
	_, _ = fmt.Fprintf(w, "  %-24s %s%s\n", mdFlagLabel(flag), flag.Short, required)
}

func printCLIExamples(w io.Writer, examples []string) {
	for _, example := range examples {
		_, _ = fmt.Fprintf(w, "  %s\n", example)
	}
}

func findCliSubcommand(subcommands []cliSub, name string) (cliSub, bool) {
	for _, sub := range subcommands {
		if sub.Name == name {
			return sub, true
		}
	}
	return cliSub{}, false
}
