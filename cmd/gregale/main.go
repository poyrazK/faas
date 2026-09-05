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
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/wire"
)

// docsURL is the canonical link printed at the bottom of the usage string.
var docsURL = docsSiteURL

func topLevelUsage() string {
	var b strings.Builder
	b.WriteString("gregale — deploy apps and functions that scale to zero.\n\n")
	b.WriteString("Usage:\n  gregale <command> [flags]\n\nCommands:\n")
	for _, command := range cliCommands {
		fmt.Fprintf(&b, "  %-22s %s\n", command.Name, command.Short)
	}
	b.WriteString("  help                   Show this help message\n")
	b.WriteString("\nRun 'gregale <command> --help' for command details.\n\n")
	b.WriteString("Global flags:\n")
	b.WriteString("  --json                 Machine-readable output where supported. Slices emit\n")
	b.WriteString("                         NDJSON; scalars emit indented JSON; errors print\n")
	b.WriteString("                         RFC 7807 to stderr. Equivalent env: FAAS_JSON=1.\n")
	b.WriteString("                         Interactive-only commands retain human prompts.\n")
	fmt.Fprintf(&b, "Docs: %s\n", docsURL)
	return b.String()
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
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
	// the man page header (`.TH GREGALE(1) "version"`). Wired once
	// at process boot from wire.Version so the man page reflects the
	// binary the user is running, not a hardcoded literal.
	gregaleVersion = wire.Version
}

func run(args []string) (status int) {
	helpRequested := hasHelpFlag(args)
	defer func() {
		// The standard flag package returns flag.ErrHelp, which legacy
		// command handlers historically mapped to exit 1. Help is a
		// successful request at the process boundary.
		if helpRequested && status != 0 {
			status = 0
		}
	}()

	// Issue #64 D1: every command accepts --json (top-level). Strip
	// it before dispatch and set jsonOutput so per-command printers
	// switch to NDJSON/indented JSON. FAAS_JSON=1 env also works.
	args = applyJSONFlag(args)
	if len(args) == 0 {
		fmt.Print(topLevelUsage())
		return 0
	}
	switch args[0] {
	case "version", "--version", "-v":
		// `gregale version --help` prints usage + docs link; bare
		// `gregale version foo` still prints the version string (POSIX
		// convention — git does the same).
		if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
			PrintUsage(os.Stderr, "usage: gregale version", "version")
			return 0
		}
		fmt.Printf("gregale %s\n", wire.Version)
		return 0
	case "help", "--help", "-h":
		fmt.Print(topLevelUsage())
		return 0
	case "completion":
		// Tier A8 / ADR-083. Routes to one of bash|zsh|fish|powershell
		// via cmdCompletion; the dispatcher is in completion.go.
		return cmdCompletion(args[1:])
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
	case "deploy":
		return cmdDeployTarball(args[1:])
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
	case "init":
		return cmdInit(args[1:])
	case "connect":
		return cmdConnect(args[1:])
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
			return cmdApps()
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
			// Outer guard verified args[1] == "routes" but did not
			// bounds-check args[2]. `len(args) < 3` falls through
			// to the default cmdApps() path so `gregale apps
			// routes` (no slug) doesn't panic; the leaf's
			// PrintUsage exits 1 with the usage hint.
			if len(args) < 3 {
				return cmdApps()
			}
			return cmdAppsRoutes(args[2], args[3:])
		}
		// `gregale apps streaming-cap <slug>` — ADR-102 D6 operator
		// entry point. Same shape as the routes arm above: 3-token
		// form (`apps streaming-cap <slug>`), placed BEFORE the
		// `-q`/`--quiet` delete fall-through so a slug-shaped token
		// never hits the delete path. Mirrors the routes CodeQL
		// off-by-one guard (`len(args) < 3` falls through).
		if len(args) > 1 && args[1] == subStreamingCap {
			if len(args) < 3 {
				return cmdApps()
			}
			return cmdAppsStreamingCap(args[2], args[3:])
		}
		// `gregale apps -q <slug>` is the delete path.
		if len(args) > 1 && (args[1] == "-q" || args[1] == "--quiet") {
			// Preserve the quiet flag for cmdAppsRm. Dropping it here
			// made the documented `gregale apps -q <slug>` command
			// unexpectedly enter the typed-confirmation path.
			return cmdAppsRm(args[1:])
		}
		return cmdApps()
	case dispatchDeployments:
		// `gregale deployments [--limit N|--before C|--all]` — list.
		// Place before appSlugFallback so the singular never shadows it.
		return cmdDeployments(args[1:])
	case dispatchDeployment:
		// `gregale deployment <id>` — get one. Must come before appSlugFallback
		// so the singular is never misread as an app slug.
		return cmdDeployment(args[1:])
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
	case "traffic":
		return cmdTraffic(args[1:])
	case "mirror":
		return cmdMirror(args[1:])
	case "domains":
		return cmdDomains(args[1:])
	case "tenant-surfaces":
		return cmdTenantSurfaces(args[1:])
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
		return githubWebhookSecretSet(args[1:])
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
	case "invocations":
		// Tier C: per-account invocation ledger (issue #394 follow-up).
		// Mirrors `audit-events` for dispatcher shape.
		return cmdInvocations(args[1:])
	case "invoices":
		return cmdInvoices(args[1:])
	case "jobs":
		// Issue #1184 Workstream A: run-to-completion jobs
		// (list|add|info|update|rm|run|runs|cancel|tasks|logs).
		// Mirrors `crons` for dispatcher shape. Implementation
		// lives in commands_jobs.go (cmdJobs).
		return cmdJobs(args[1:])
	case "workflows":
		// ADR-081: durable execution workflows (list|run|status|steps|cancel|events).
		return cmdWorkflows(args[1:])
	case "debug":
		// ADR-127 PR-B: production debugger (regression banner,
		// compare panel, replay stub). Mirrors `invocations` for
		// dispatcher shape.
		return cmdDebug(args[1:])
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
		fmt.Fprintf(os.Stderr, "gregale: unknown command %q\nRun 'gregale help' for usage.\n", args[0])
		return 1
	}
}
