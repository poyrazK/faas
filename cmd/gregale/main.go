// Command gregale is the customer-facing CLI and the primary interface to the
// platform (docs/faas_ux_spec.md §3). Everything the platform does is
// possible from here.
//
// Exit codes follow docs/faas_ux_spec.md §3.2: 0 ok, 1 user error, 2 auth,
// 3 platform/infra. See also the brand-residue sweep that landed in the
// same PR as the rename — every string in this file should say `gregale`,
// not `faas`, and any new dispatcher arm must add a matching entry to the
// usage block below so `gregale help` lists it.
package main

import (
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/wire"
)

// docsURL is the canonical link printed at the bottom of the usage string.
// Computed (not a const) so the tripwire that bans DOMAIN-shaped literals
// in source keeps working; the only host that surfaces in the binary is
// wire.DocsHost.
var docsURL = "https://" + wire.DocsHost

var usage = `gregale — deploy apps and functions that scale to zero.

Usage:
  gregale <command> [flags]

Commands:
  account      Manage the local account (account export|delete|restore|status|dpa|slo)
  admin        Operator-only billing ops (admin credit --reason <text> <uuid> <cents>)
  alerts       Per-app alert rules (alerts list|add|info|update|rm|rotate-secret --app <slug>)
  audit-events Audit-log query (audit-events list|get <id>)
  apps         List your apps
  apps ls      Alias for 'gregale apps'
  apps routes  List admitted per-route labels for one app (ADR-093)
  apps streaming-cap  Per-app streaming classification probe (ADR-102 D6)
  apps -q      Delete an app
  app          Get/update one app (gregale app <slug> [scale|rename <new>|--ram N|…])
  backup       Operator rclone config unseal (backup unseal-rclone)
  billing      Manage billing (gregale billing portal)
  build        Build provenance + sbom (build provenance <id>|build sbom <id>)
  connect      Connect a third-party service (github)
  crons        Manage scheduled requests
  completion   Print shell completion script (bash|zsh|fish|powershell)
  dashboard    Open the account dashboard in your browser
  delayed-task Schedule a deferred invocation (delayed-task add|get|cancel)
  deployments  List deployments (--limit N | --before C | --all)
  deployment   Get one deployment (<id> | set-min-instances <id> --min N)
  deploy       Deploy (--image REF | --tarball PATH | --repo OWNER/NAME | --template NAME)
  domains      Manage custom domains
  edge-rules   Per-app edge rules (route|rewrite|redirect|headers|cors|jwt|ip; ADR-089)
  env          Pull/push .env <-> sealed secrets (--app <slug>)
  host-age     Operator host.age rotation (host-age init|rotate|status|prune-previous)
  init         Scaffold a reference project from a built-in template (--template NAME --path DIR [--deploy])
  invoke       Functional smoke test (invoke [--async] <slug> [--payload J|@file|-])
  invocations  Per-account invocation ledger (invocations list|get <id> [--replay])
  invitations  Standalone invitation actions (invitations peek <token>|accept <token>)
  invoices     List issued invoices
  jobs         Manage run-to-completion jobs (jobs list|create|info|update|rm|run|runs|cancel)
  keys         Manage API keys (keys list|add|rm|rotate|grace-window)
  login        Authenticate this machine (--token for CI)
  logout       Remove the stored token
  manifest     Operator split-box deployment manifest (manifest validate --file PATH; issue #911 / ADR-110)
  signup       Create a new account (signup [--email-only EMAIL])
  man          Print the gregale(1) man page (or gregale-<command>(1) with one arg)
  logs         Tail app or deployment logs (--follow); logs tail <slug> is an alias that always follows
  metrics      Per-app or account-wide metrics (gregale metrics <slug> [--range 5m] | --account)
  mfa          Manage account MFA (mfa enroll|confirm|verify|recover|disable)
  open         Open the app's URL (or its dashboard page) in your browser
  orgs         Manage orgs + members (orgs ls|create|info|rm|members ...|keys ...|transfer-ownership|seat-usage|invitations ...|me)
  overage-cap  Set / clear the account's overage cap (--clear | <cents>)
  park         Park an app cold (kill all live instances)
  pki          Operator local-dev PKI bootstrap (pki init|status|rotate)
  plan         Change plan (free|hobby|pro|scale)
  ps           Show live instances + state for an app
  queue        Inspect the wake-queue depth (queue tail|send|receive|state|peek|dead-letter|ack)
  registry     Per-app private container registry credentials (registry list|set|rm --app <slug>)
  rollback     Re-promote the previous deployment
  scan         Decomposition dry-run (--tarball | --path | --repo OWNER/NAME)
  secrets      Manage env secrets (secrets list|set|unset|list-all)
  sign-keys    Provision the cosign sign keypair (operator; --sign-key / --verify-key)
  slo          Per-app SLO panel (gregale slo <slug> [--window 24h])
  status       Personal SLO numbers (availability, wake p95, build success)
  tail         Live tail of the unified event stream (--follow)
  traffic      Manage deployment traffic split (issue #556; Pro/Scale only)
  trusted-publishers  Per-app cosign trusted-publisher list (admin; trusted-publishers add|remove|list)
  usage        Show this month's usage (gregale usage [--month YYYY-MM]|daily [--day YYYY-MM-DD]|storage [--day YYYY-MM-DD]|summary)
  version      Print the CLI version
  wake-timeline Walk the per-wake event stream (wake-timeline <slug> <wake-id> [--since RFC3339] [--limit N] [--all])
  wake         Wake a parked app (pulls out of snapshot)
  webhooks     Manage outbound webhooks (webhooks list|add|info|update|rm|deliveries|retry|rotate-secret)
  whoami       Show the authenticated account

Run 'gregale <command> --help' for command details.

Global flags:
  --json         Machine-readable output on every command. Slices emit
                 NDJSON (one JSON object per line, jq -c '.'); scalars
                 emit indented JSON; errors print raw RFC 7807 to stderr.
                 Equivalent env: FAAS_JSON=1. Negate with --json=false.
Docs: ` + docsURL + `
`

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

func run(args []string) int {
	// Issue #64 D1: every command accepts --json (top-level). Strip
	// it before dispatch and set jsonOutput so per-command printers
	// switch to NDJSON/indented JSON. FAAS_JSON=1 env also works.
	args = applyJSONFlag(args)
	if len(args) == 0 {
		fmt.Print(usage)
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
		fmt.Print(usage)
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
			return cmdAppsRm(args[2:])
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
	case dispatchBuild:
		// `gregale build provenance <id>` — ADR-038 / Tier 3 / issue
		// #197 B3.10-read half. The parent dispatch is in
		// commands_builds.go::cmdBuild; future build-surface
		// subcommands (`logs`, `sbom`) land there without
		// touching this switch.
		return cmdBuild(args[1:])
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
	case "park":
		return cmdPark(args[1:])
	case "wake":
		return cmdWake(args[1:])
	case "traffic":
		return cmdTraffic(args[1:])
	case "domains":
		return cmdDomains(args[1:])
	case "edge-rules":
		// PR 2 of Edge Rules rollout: customer CLI wrapper around the
		// /v1/apps/{slug}/edge-rules CRUD surface (PR 1 #799). Sub-
		// commands live in commands_edge_rules.go; the dispatcher
		// itself is cmdEdgeRules. --json round-trips through the
		// pkg/api SDK methods (ListEdgeRules / CreateEdgeRule / etc.).
		return cmdEdgeRules(args[1:])
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
	case "jobs":
		// ADR-099 PR-E: jobs cluster. The wire surface ships in PR-D
		// (handlers_jobs.go, pkg/api/client.go). This dispatcher routes
		// to cmdJobs in commands_jobs.go — eight verbs share the
		// crons dispatcher shape (list|create|info|update|rm|run|runs|
		// cancel). The cli_meta.go entry drives bash/zsh/fish/
		// powershell completion; the manifest-drift test asserts both
		// arms stay in sync.
		return cmdJobs(args[1:])
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
	case dispatchSignKeys:
		return cmdSignKeys(args[1:])
	case dispatchNodeKey:
		// ADR-053 — operator-side provisioning for the per-node
		// CapacityReport signing keypair. Mirrors sign-keys shape
		// (init|rotate|status) but writes /etc/faas/secrets/vmmd/
		// {node.key (0400 root:root), node.pub (0444)} and prints
		// the key_id (SHA-256 hex of the SPKI) at init time so an
		// operator can confirm the same value schedd will register.
		return cmdNodeKey(args[1:])
	case dispatchTrustedPublishers:
		// Issue #472 / ADR-054 — operator CLI for the per-app
		// cosign trusted-publisher list. Admin API key required;
		// every leaf calls authedClient() and hits apid. The
		// sibling operator surface `sign-keys` (above) hits the
		// local fs, never apid.
		return cmdTrustedPublishers(args[1:])
	case dispatchHostAge:
		// Operator-side host.age rotation (issue #316 / ADR-057).
		// Same operator-only surface as sign-keys / pki: every
		// leaf is a local fs operation against /etc/faas/secrets/.
		// Sibling — never reuse the `keys` namespace (that's the
		// customer API-key manager in commands2.go::cmdKeys which
		// hits apid via authedClient()).
		return cmdHostAge(args[1:])
	case dispatchBackup:
		return cmdBackup(args[1:])
	case dispatchPKI:
		// Operator-side local-dev PKI bootstrap (ADR-052). Issues
		// /etc/faas/tls/{ca,<daemon>/} material for multi-box mTLS.
		// Distinct from sign-keys because the trust root is the CA,
		// not the per-box cosign keypair.
		return cmdPKI(args[1:])
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
	case "manifest":
		// Issue #911 / ADR-110: operator-side manifest loader.
		// `gregale manifest validate --file=PATH` runs the
		// canonical validator (pkg/manifest/Validate); PR-2 adds
		// `manifest render`; the install path lives under
		// `gregale release install` (PR-3), not `manifest install`.
		// The dispatcher is cmdManifestDispatch in commands_manifest.go.
		return cmdManifestDispatch(args[1:])
	case "release":
		// Issue #911 / ADR-110: cluster-shipped release bundle
		// (PR-3). `gregale release bundle` materialises the
		// daemon-binary bundle and INSERTs into release_bundles;
		// `gregale release install` flips the local
		// /opt/faas/current symlink + stamps applied_at. The
		// dispatcher is cmdReleaseDispatch in commands_release.go.
		return cmdReleaseDispatch(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregale: unknown command %q\nRun 'gregale help' for usage.\n", args[0])
		return 1
	}
}
