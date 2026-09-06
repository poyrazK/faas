// commands/cli_meta.go — Tier A8 / ADR-083.
//
// Hand-curated manifest of every top-level gregale command, used as the
// single source of truth for `gregale completion {bash|zsh|fish|powershell}`
// and `gregale man [command]`. Mirrors the dispatch switch in main.go
// (cmd/gregale/main.go::run); the manifest-drift test (see
// commands_completion_test.go::TestCompletion_ManifestDrift) walks
// main.go and asserts every `case "<name>":` arm has a matching
// cliCommand entry, and vice versa.
//
// New commands add a 4-line entry here at the same time as the
// `case "<name>":` in main.go. The code-review gate is the same one
// that fires when a new command ships without a usage block — both
// additions are required, in the same PR.
//
// Adding a closed-set enum? Add it to cliFlag.ClosedSet (a runtime
// companion string slice) and the per-shell completion backend
// picks it up automatically. Adding a new positional? Add the
// placeholder to cliCommand.Positionals and the man-page renderer
// interpolates it into the SYNOPSIS section.
//
// Field naming tracks the conventional "what the user typed"
// shape — Short is the one-line summary that surfaces in
// `gregale help` and `gregale completion <shell>`; DocSlug is
// consolidated CLI docs page; Subcommands lists the
// verbs the dispatcher recognises (the dispatcher in each
// commands_*.go file is the source of truth for verb spellings —
// the manifest mirrors them).

package main

import "github.com/onebox-faas/faas/pkg/api"

// cliCommand is one top-level gregale command.
type cliCommand struct {
	// Name is the literal the user types: "apps", "delayed-task", etc.
	Name string
	// DocSlug is the stable manifest topic passed to PrintUsage. The
	// public web app resolves command topics on its consolidated CLI page.
	DocSlug string
	// Short is the one-line summary shown in `gregale help` and the
	// per-shell completion script's description list. Should fit on
	// one terminal line (~80 chars).
	Short string
	// Subcommands enumerates the verb set the dispatcher recognises.
	// Empty for commands with no verb set (e.g. `whoami`, `version`).
	Subcommands []cliSub
	// Flags enumerates the top-level flags accepted on this command's
	// own flag set (i.e. before any subcommand dispatch). Empty if
	// the command dispatches immediately on args[0] (most multi-verb
	// commands). The `--app <slug>` etc. that follow a subcommand
	// belong on the cliSub, not here.
	Flags []cliFlag
	// Positionals documents the required positional args in order.
	// Used by the man-page renderer to fill the SYNOPSIS section.
	// Example: ["<slug>", "<wake-id>"] for `gregale wake-timeline`.
	// A leading `<slug>` marker also drives cache-backed completion
	// across all four shell backends (see hasSlugFirst).
	Positionals []string
	// ClosedSet enumerates the allowed values for the FIRST positional
	// when the command takes exactly one. Today only `plan` uses this
	// (free|hobby|pro|scale). Mirrors api.Plans so the manifest is the
	// source of truth for completion of the plan literal.
	ClosedSet []string
}

// hasSlugFirst reports whether the first positional is the <slug>
// placeholder. The completion backends use this to wire up cache-
// backed slug completion for every command that takes an app slug
// (app, invoke, metrics, slo, wake-timeline) — driven by the manifest
// rather than a hardcoded name list.
func (c cliCommand) hasSlugFirst() bool {
	return len(c.Positionals) > 0 && c.Positionals[0] == "<slug>"
}

// cliSub is one verb under a cliCommand (e.g. alerts.list, alerts.add).
type cliSub struct {
	Name  string
	Short string
	// Flags enumerates the per-subcommand flag set. Req marks the
	// required flags; ClosedSet marks the closed-enum values
	// (plan names, metric enums, etc.) — completion backends
	// expand these inline.
	Flags []cliFlag
}

// cliFlag is one CLI flag.
type cliFlag struct {
	// Name is the kebab-case form (matches flag.NewFlagSet's arg):
	// "app", "min", "require-signed", "scheduled-at".
	Name string
	// Short is the human description (mirrors flag.NewFlagSet's
	// third arg in each leaf).
	Short string
	// Req marks required flags. Completion backends do NOT offer
	// required flags as a TAB choice (the user is forced to provide
	// them); the marker exists for the man-page SYNOPSIS section
	// to render the required marker `(<name>|<placeholder>)`.
	Req bool
	// Value is the placeholder for a value-taking flag (for example,
	// "slug" or "PATH"). Empty means the flag is boolean unless Req or
	// ClosedSet says otherwise.
	Value string
	// ClosedSet enumerates the allowed literal values, when the
	// flag is a closed enum (plan, metric, comparison, window-spec,
	// etc.). When non-empty, completion offers these as the flag's
	// value; the leaf's validator accepts ONLY these strings, so
	// mirroring the server-side gate here is a hard contract.
	ClosedSet []string
}

// templateNames13 is the canonical template catalog. The historical name is
// retained because tests and completion metadata refer to this package-local
// symbol; it now contains all 15 embedded templates. Mirrors
// cmd/gregale/templates/embed.go::Names verbatim; the ClosedSet literals
// in deploy/init reference this const so goconst stops flagging the
// duplicated 13-name lists. Kept in sync with the embed FS by the
// TestClosedSetTemplatesMatchEmbedFS pin test in commands_meta_test.go.
var templateNames13 = []string{
	"hello-node",
	"hello-python",
	"hello-go",
	"cron-example",
	"function-node",
	"function-python",
	"function-go",
	"function-node24",
	"function-python313",
	"s3-uploader",
	"slack-bot",
	"rest-api-postgres",
	"cron-worker",
	"webhook-receiver",
	"ai-chat",
}

// cliCommands is the manifest. One entry per top-level command in
// main.go's run() switch. Order matches the dispatch table roughly
// (operator-vs-customer split is intentional — operator commands sit
// at the bottom of `gregale help` today, the manifest mirrors that).
//
// When you add a command to main.go, add it here too. The drift test
// catches the omission; the manifest-drift guard is the load-bearing
// sync mechanism per ADR-083 §Decision 4.
var cliCommands = []cliCommand{
	{
		Name:    "account",
		DocSlug: "account",
		Short:   "Manage the local account (account export|delete|restore|status|dpa|slo)",
		Subcommands: []cliSub{
			{Name: "export", Short: "Export account data (GDPR)"},
			{Name: "delete", Short: "Schedule account deletion"},
			{Name: "restore", Short: "Cancel a pending deletion"},
			{Name: "status", Short: "Show account status"},
			{Name: "dpa", Short: "Show DPA metadata"},
			{Name: "slo", Short: "Account-wide SLO panel"},
		},
	},
	{
		Name:    "admin",
		DocSlug: "admin",
		Short:   "Operator-only billing ops (admin credit|refund|consume-credits)",
		Subcommands: []cliSub{
			{Name: "credit", Short: "Issue a billing credit", Flags: []cliFlag{
				{Name: "reason", Short: "credit reason text", Req: true, Value: "text"},
			}},
			{Name: "refund", Short: "Refund a paid Polar invoice", Flags: []cliFlag{
				{Name: "reason", Short: "refund reason text", Req: true, Value: "text"},
				{Name: "idempotency-key", Short: "stable provider retry key", Value: "key"},
			}},
			{Name: "consume-credits", Short: "Consume credits against an invoice"},
		},
		Positionals: []string{"<uuid>", "<cents>"},
	},
	{
		Name:    "alerts",
		DocSlug: "alerts",
		Short:   "Per-app alert rules (alerts list|add|info|update|rm|rotate-secret|preset --app <slug>)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List alert rules", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			}},
			{Name: "add", Short: "Add an alert rule"},
			{Name: "info", Short: "Show one alert rule"},
			{Name: "update", Short: "Update one alert rule"},
			{Name: "rm", Short: "Delete one alert rule"},
			{Name: "rotate-secret", Short: "Rotate the alert's webhook secret"},
			// Issue #1233 / ADR-123 — alert preset catalog +
			// instantiate-from-preset. Two leaves under preset:
			// list (no flags), enable <name> --app <slug>
			// --webhook-url <url> --webhook-secret <s>.
			{Name: "preset", Short: "Alert preset catalog (preset list|enable --app <slug>)"},
		},
		Flags: []cliFlag{{Name: "app", Short: "app slug", Value: "slug"}},
	},
	{
		Name:    "audit-events",
		DocSlug: "audit-events",
		Short:   "Audit-log query (audit-events list|get <id>)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List audit events"},
			{Name: "get", Short: "Show one audit event"},
		},
		Positionals: []string{"<id>"},
	},
	{
		Name:    dispatchApps,
		DocSlug: "apps",
		Short:   "List your apps",
		Subcommands: []cliSub{
			{Name: "ls", Short: "Alias for the default list action"},
			{Name: "routes", Short: "List admitted per-route labels for one app (ADR-093)"},
			{Name: "streaming-cap", Short: "Per-app streaming classification probe (ADR-102 D6)"},
			{Name: "-q", Short: "Delete one app (positional: <slug>)"},
			{Name: "--quiet", Short: "Delete one app (positional: <slug>)"},
		},
		Flags: []cliFlag{{Name: "q", Short: "delete one app"}, {Name: "quiet", Short: "delete one app"}},
	},
	{
		Name:    appSlugFallback,
		DocSlug: "apps",
		Short:   "Get/update one app (gregale app <slug> [scale|rename <new>|--profile NAME|--ram N|…])",
		Subcommands: []cliSub{
			{Name: "scale", Short: "Set max_concurrency / resource profile / RAM / CPU"},
			{Name: "rename", Short: "Rename an app"},
			{Name: "security", Short: "Toggle require_signed on deploys"},
			{Name: "routes", Short: "List admitted per-route labels for one app (ADR-093)"},
		},
		Positionals: []string{"<slug>"},
		Flags: []cliFlag{
			{Name: "profile", Short: "set a named RAM/CPU profile", Value: "micro|small|medium|large|xlarge"},
			{Name: "ram", Short: "set RAM in MB", Value: "MB"},
			{Name: "max-concurrency", Short: "set max_concurrency", Value: "N"},
			{Name: "require-signed", Short: "toggle require_signed", ClosedSet: []string{"true", "false"}},
		},
	},
	// operator-side "backup" verb moved to gregalectl in PR-6.5
	// (sealed-cred rotation is an operator concern; see plan §Scope).
	{
		Name:    "billing",
		DocSlug: "billing",
		Short:   "Manage billing (portal, invoices, subscription, card on file)",
		// Mirrors every case in cmdBilling (commands_billing.go); the
		// manifest had listed `portal` alone for eight real verbs.
		Subcommands: []cliSub{
			{Name: "portal", Short: "Open the active billing provider's portal"},
			{Name: "retry", Short: "Retry failed payment when supported; Polar uses the portal"},
			{Name: "cancel", Short: "Cancel the subscription at period end"},
			{Name: "payment-method", Short: "Show the card on file"},
			{Name: "status", Short: "Show subscription status"},
			{Name: "price-catalog", Short: "Inspect the price catalog (admin)"},
			{Name: "reconcile", Short: "Reconcile an invoice with the provider (admin)"},
			{Name: "reconcile-paddle-overage", Short: "Reconcile Paddle overage charges (admin)"},
			{Name: "webhook-test", Short: "Send a signed test webhook (operator)"},
		},
	},
	{
		Name:    "canary",
		DocSlug: "canary",
		Short:   "Project a canary preset against recent app traffic (canary simulate <slug>)",
		Subcommands: []cliSub{
			{Name: "simulate", Short: "Estimate per-stage canary success from the last hour", Flags: []cliFlag{
				{Name: "canary-preset", Short: "canary ladder preset", Value: "PRESET", ClosedSet: []string{"slow", "balanced", "aggressive", "1-10-50-100"}},
			}},
		},
		Positionals: []string{"<slug>"},
	},
	{
		Name:    dispatchBuild,
		DocSlug: "build",
		Short:   "Build provenance + sbom (build provenance <id>|build sbom <id>)",
		Subcommands: []cliSub{
			{Name: "provenance", Short: "Show the build provenance attestation"},
			{Name: "sbom", Short: "Show the build SBOM"},
		},
	},
	{
		Name:    "connect",
		DocSlug: "connect",
		Short:   "Connect a third-party service (github | repo OWNER/NAME)",
		Subcommands: []cliSub{
			{Name: "github", Short: "Connect a GitHub account for repo deploys"},
			// Issue #961 / Mega-B PR-1: `connect repo <owner>/<name>`
			// opens the dashboard's /dashboard/apps/new?repo=... wizard
			// (PR-3 wires the server side). The CLI stays out of the
			// OAuth dance — the cookie-session dashboard is the
			// install-token trust root.
			{Name: "repo", Short: "Open the dashboard wizard to bind <owner>/<name> to a Gregale app"},
		},
	},
	{
		Name:    "cors",
		DocSlug: "cors",
		Short:   "Configure CORS for an app (allow|ls|rm|show)",
		Subcommands: []cliSub{
			{Name: "allow", Short: "Attach a CORS rule to <slug>"},
			{Name: "ls", Short: "List CORS rules bound to <slug>"},
			{Name: "rm", Short: "Delete a CORS rule by id"},
			{Name: "show", Short: "Show per-app default CORS + active rules"},
		},
	},
	{
		Name:    "crons",
		DocSlug: "crons",
		Short:   "Manage scheduled requests",
		Subcommands: []cliSub{
			{Name: "list", Short: "List cron rules"},
			{Name: "add", Short: "Add a cron rule"},
			{Name: "info", Short: "Show one cron rule"},
			{Name: "update", Short: "Update one cron rule"},
			{Name: "rm", Short: "Delete one cron rule"},
			{Name: "runs", Short: "Show execution history"},
		},
	},
	{
		Name:    "triggers",
		DocSlug: "triggers",
		Short:   "Manage unified event triggers (broker mappings + cron-linked rows)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List triggers", Flags: []cliFlag{
				{Name: "app", Short: "filter to an app slug", Value: "slug"},
				{Name: "kind", Short: "filter by trigger kind", ClosedSet: triggerKindNames},
			}},
			{Name: "get", Short: "Show one trigger"},
			{Name: "create", Short: "Create a broker trigger", Flags: []cliFlag{
				{Name: "app", Short: "app slug (required)", Req: true, Value: "slug"},
				{Name: "kind", Short: "trigger kind (required)", Req: true, Value: "kind", ClosedSet: triggerBrokerKindNames},
				{Name: "slug", Short: "trigger slug (required for non-cron kinds)", Value: "slug"},
				{Name: "config", Short: "JSON config (inline | @file | -)", Value: "JSON"},
				{Name: "enabled", Short: "enable the trigger"},
				{Name: "disabled", Short: "disable the trigger"},
				{Name: "batch-size", Short: "maximum records per dispatch batch", Value: "N"},
				{Name: "batch-window-ms", Short: "maximum batch dwell time in milliseconds", Value: "N"},
				{Name: "max-attempts", Short: "maximum delivery attempts", Value: "N"},
				{Name: "payload-max-bytes", Short: "maximum broker payload size", Value: "N"},
				{Name: "broker-poison-strategy", Short: "kafka poison strategy", Value: "commit|seek-to-offset", ClosedSet: []string{api.BrokerPoisonStrategyCommit, api.BrokerPoisonStrategySeekToOffset}},
			}},
			{Name: "update", Short: "Update one trigger", Flags: []cliFlag{
				{Name: "enabled", Short: "enable the trigger"},
				{Name: "disabled", Short: "disable the trigger"},
				{Name: "config", Short: "replace JSON config (inline | @file | -)", Value: "JSON"},
				{Name: "schedule", Short: "replace cron expression", Value: "EXPR"},
				{Name: "path", Short: "replace cron request path", Value: "PATH"},
				{Name: "batch-size", Short: "maximum records per dispatch batch", Value: "N"},
				{Name: "batch-window-ms", Short: "maximum batch dwell time in milliseconds", Value: "N"},
				{Name: "max-attempts", Short: "maximum delivery attempts", Value: "N"},
				{Name: "payload-max-bytes", Short: "maximum broker payload size", Value: "N"},
				{Name: "broker-poison-strategy", Short: "kafka poison strategy", Value: "commit|seek-to-offset", ClosedSet: []string{api.BrokerPoisonStrategyCommit, api.BrokerPoisonStrategySeekToOffset}},
			}},
			{Name: "delete", Short: "Delete one trigger", Flags: []cliFlag{
				{Name: "quiet", Short: "skip the typed confirmation (for scripts)"},
			}},
			{Name: "pause", Short: "Disable one trigger"},
			{Name: "resume", Short: "Enable one trigger"},
			{Name: "records", Short: "List recent trigger records", Flags: []cliFlag{
				{Name: "state", Short: "filter by record state", Value: "STATE", ClosedSet: triggerRecordStateNames},
			}},
			{Name: "retry", Short: "Re-drive one trigger record"},
			{Name: "drop", Short: "Drop one trigger record"},
			{Name: "dlq", Short: "List dead-letter records", Flags: []cliFlag{
				{Name: "reason", Short: "filter by dead-letter reason", Value: "REASON"},
			}},
			{Name: "metrics", Short: "Show per-state trigger metrics"},
		},
	},
	{
		Name:    "jobs",
		DocSlug: "jobs",
		Short:   "Manage jobs (run-to-completion workloads)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List jobs in this account"},
			{Name: "add", Short: "Create a new job"},
			{Name: "info", Short: "Show one job"},
			{Name: "update", Short: "Update one job"},
			{Name: "rm", Short: "Soft-delete one job"},
			{Name: "run", Short: "Dispatch a new run (fan-out N tasks)"},
			{Name: "runs", Short: "List runs for one job"},
			{Name: "cancel", Short: "Cancel a run"},
			{Name: "tasks", Short: "List tasks for one run"},
			{Name: "logs", Short: "Tail logs for one task"},
		},
	},
	{
		Name:    "workflows",
		DocSlug: "workflows",
		Short:   "Manage durable execution workflows",
		Subcommands: []cliSub{
			{Name: "list", Short: "List workflow runs for an app"},
			{Name: "run", Short: "Trigger a new workflow run"},
			{Name: "status", Short: "Show details of a workflow run"},
			{Name: "steps", Short: "List steps for a workflow run"},
			{Name: "cancel", Short: "Cancel an active workflow run"},
			{Name: "events", Short: "Send external event to a workflow run"},
		},
	},
	{
		Name:    "dashboard",
		DocSlug: "dashboard",
		Short:   "Open the account dashboard in your browser",
	},
	{
		// Error-explanations cluster (spec §6.4 amendment 1):
		// customer preflight that scans the cwd for the 8 source-side
		// failure modes the cluster's runtime detectors catch
		// post-deploy. Auth not required (local source only).
		Name:    dispatchDoctor,
		DocSlug: "doctor",
		Short:   "Preflight local source or OCI image metadata; runtime checks are skipped",
		Flags: []cliFlag{
			{Name: "image", Value: "REF", Short: "inspect the Linux/amd64 image without downloading layers"},
			{Name: "registry-user", Value: "USER", Short: "registry username; requires --registry-password-stdin"},
			{Name: "registry-password-stdin", Short: "read registry password/token from stdin; requires --image and --registry-user"},
			{Name: "strict", Short: "exit 1 on warn (default: exit 0 on warn)"},
			{Name: "json", Short: "machine output (default: human prose)"},
		},
	},
	{
		Name:    "delayed-task",
		DocSlug: "delayed-task",
		Short:   "Schedule a deferred invocation (delayed-task add|get|cancel)",
		Subcommands: []cliSub{
			{Name: "add", Short: "Schedule a deferred invocation"},
			{Name: "get", Short: "Show one delayed task"},
			{Name: "info", Short: "Alias for get"},
			{Name: "cancel", Short: "Cancel a delayed task"},
		},
	},
	{
		Name:    dispatchDeployments,
		DocSlug: "deployments",
		Short:   "List deployments (--limit N | --before C | --all)",
		Flags: []cliFlag{
			{Name: "limit", Short: "page size (1-200)", Value: "N"},
			{Name: "before", Short: "pagination cursor (RFC3339Nano)", Value: "cursor"},
			{Name: "all", Short: "walk every page"},
		},
	},
	{
		Name:    dispatchDeployment,
		DocSlug: "deployment",
		Short:   "Get or wait for one deployment (<id> | wait <id> | set-min-instances <id>)",
		Subcommands: []cliSub{
			{Name: "wait", Short: "Wait until a deployment is live", Flags: []cliFlag{
				{Name: "timeout", Short: "maximum seconds to wait", Value: "SECONDS"},
			}},
			{Name: "set-min-instances", Short: "Set the per-deployment cold-wake floor"},
		},
		Positionals: []string{"<id>"},
		Flags: []cliFlag{
			{Name: "show-scan", Short: "include the per-deploy grype scan payload"},
			{Name: "min", Short: "min_instances floor (>= 0)", Value: "N"},
		},
	},
	{
		Name:    dispatchDeploys,
		DocSlug: "deploys",
		Short:   "Deployment drill-downs (deploys show|status|cancel|reorder|clear|clear-obsolete)",
		Subcommands: []cliSub{
			// ADR-117 companion read surface. Future siblings
			// (timeline, events, artifacts) land here as new
			// cliSub entries — NOT as flags on the singular
			// `deployment` verb, which is already at three
			// flag-shaped drill-downs.
			{Name: "show", Short: "Print the closed 6-stage post-stream summary"},
			{Name: statusLiteral, Short: "Print the stage summary with terminal-status footer (live since / failed at)"},
			// ADR-117 §Production-ready follow-on, C2 — per-stage
			// retry. The verb is `retry` (NOT a `--retry` flag on
			// show/status) because the action mutates state — a
			// subcommand is the CLI convention for write-side
			// verbs. The --from flag accepts a closed-6 stage name
			// (default = the failing stage on the row, fetched
			// via the existing GET /v1/deployments/{id}/stages
			// read surface).
			{Name: "retry", Short: "Retry a failed deployment from a specific stage (--from=<stage>)"},
		},
		Positionals: []string{"<id>"},
	},
	{
		Name:    "deploy",
		DocSlug: "deploy",
		Short:   "Deploy (--path DIR | --image REF | --tarball PATH | --repo OWNER/NAME --ref REF | --github | --template NAME)",
		Flags: []cliFlag{
			{Name: "image", Short: "deploy from a container image reference", Value: "REF"},
			{Name: "tarball", Short: "deploy from a source tarball", Value: "PATH"},
			{Name: "path", Short: "deploy a selected local source directory (relative to the current directory)", Value: "DIR"},
			{Name: "worktree", Short: "deploy the selected source directory from the working tree, including local changes"},
			{Name: "repo", Short: "deploy from a GitHub repo", Value: "OWNER/NAME"},
			// Issue #739 / ADR-092: --ref pairs with --repo to
			// drive the headless source-ref deploy (CI-friendly,
			// no install-token env). Required when --repo is set.
			{Name: "ref", Short: "git ref for --repo (branch, tag, or 40-char SHA)", Value: "REF"},
			// Issue #270: --github emits a copy-paste Actions workflow
			// snippet for the Gregale deploy action. No auth, no side effects.
			// The snippet uses --name / cwd as the app slug.
			{Name: "github", Short: "emit a GitHub Actions workflow snippet for the Gregale deploy action"},
			{Name: "template", Short: "scaffold from a built-in template", Value: "NAME", ClosedSet: templateNames13},
			{Name: "dockerfile", Short: "build with the supplied Dockerfile inside --tarball"},
			{Name: "runtime", Short: "function runtime", Value: "RUNTIME", ClosedSet: []string{"node22", "python312", "go124", "go124-alpine", "node24", "python313"}},
			{Name: "handler", Short: "function handler", Value: "HANDLER"},
			{Name: "name", Short: "app name (default: selected source directory, or current directory)", Value: "SLUG"},
			{Name: "profile", Short: "named app resource profile", Value: "PROFILE", ClosedSet: []string{"micro", "small", "medium", "large", "xlarge"}},
			{Name: "function", Short: "deploy as a function; skip shape auto-detection"},
			{Name: "app", Short: "deploy as an app; skip shape auto-detection"},
			{Name: "yes", Short: "skip the apply confirmation prompt"},
			{Name: "only", Short: "workloads to apply (comma-separated; project apply path)", Value: "SLUGS"},
			// Issue #977 / ADR-116: deployment annotations surface.
			// --reason is free text (≤280 chars); --tag is closed-set
			// (see DeploymentAnnotationTags in cmd_deploy_annotations.go);
			// --deployed-by auto-resolves to `git config user.name`
			// when unset and cwd is in a git repo; --pr-number
			// threads the GitHub PR number through the JSON
			// CreateDeploymentRequest path (the source-ref path
			// threads it via the githubd bridge). The manifest is
			// the source of truth for --tag's vocabulary (goconst
			// package-wide; reusing the slice keeps completion + docs
			// in lockstep).
			//
			// Review fix CRIT-3 (issue #977 / ADR-116): --pr-number
			// was added in the cli-flag threading commit but not
			// registered here, so generated help/docs and the
			// cli_meta-driven validation surfaces missed it. The
			// Action path defaults to ${{ github.event.pull_request.number }}
			// but operators running the CLI directly with a known
			// PR number need an discoverable way to stamp it.
			{Name: "reason", Short: "free-text deploy reason (≤280 chars)", Value: "text"},
			{Name: "tag", Short: "annotation tag", Value: "TAG", ClosedSet: DeploymentAnnotationTags},
			{Name: "deployed-by", Short: "operator label (auto-resolved from git config user.name)", Value: "NAME"},
			{Name: "pr-number", Short: "GitHub PR number (positive int; 0 = absent). CI paths stamp via the GitHub Action.", Value: "N"},
			// ADR-124 follow-up #1: --exclude + --show-affected
			// were added to cmdDeployTarball in PR-#1065 but the
			// cli_meta manifest (this file's source of truth for
			// `gregale man <cmd>` + `gregale completion <shell>`)
			// missed them; operators running `gregale deploy --help`
			// had no discoverable way to learn the affected-workloads
			// preview flags. The Short text mirrors the wire-
			// contract headline (slug, mutex with --only) without
			// re-litigating the ADR-124 partition semantic — that's
			// public docs site territory.
			{Name: "exclude", Short: "omit workloads (slug, comma-separated; mutex with --only; ADR-124)", Value: "SLUGS"},
			{Name: "show-affected", Short: "render the WillDeploy + Skipped + Unaffected + Removed partition (ADR-124)"},
			// ADR-124 follow-up #3 (PR-B commit 5): write-side
			// complement to --exclude. Records excluded slugs into
			// deployment_scope_exclusions on a successful apply so
			// subsequent deploys honor the persisted set automatically.
			{Name: "persist-exclude", Short: "record --exclude slugs into deployment_scope_exclusions (apply path only; ADR-124 follow-up #3)"},
			{Name: "project-slug", Short: "kebab slug for the project (one-key provision)", Value: "SLUG"},
			{Name: "canary-preset", Short: "canary ladder preset", Value: "PRESET", ClosedSet: []string{"none", "slow", "balanced", "aggressive", "1-10-50-100", "custom"}},
			{Name: "canary-stages", Short: "custom percent@duration canary stages", Value: "STAGES"},
			{Name: "require-authn", Short: "require bearer auth on every request"},
			{Name: "no-require-authn", Short: "drop the token requirement"},
			{Name: "app-protocol", Short: "wire protocol selector", Value: "PROTOCOL", ClosedSet: []string{"http1", "http2", "grpc"}},
			{Name: "traffic-percent", Short: "deployment traffic split weight (0-100)", Value: "PERCENT"},
			{Name: "no-triggers", Short: "skip gregale.yaml trigger fan-out"},
			{Name: "wait", Short: "wait for deployment to become live (default)"},
			{Name: "no-wait", Short: "return after deployment is queued"},
			{Name: "secret-scan", Short: "scan .env files before packing", Value: "on|off", ClosedSet: []string{"on", "off"}},
			{Name: "diff", Short: "preview what would change without deploying"},
			{Name: "strict", Short: "fail on diff schema/quota/env breaks"},
			{Name: "lenient", Short: "return success even when diff has breaks"},
			{Name: "server-diff", Short: "compute deploy diff on apid"},
			{Name: "doctor-strict", Short: "run doctor before deploy and abort on errors"},
		},
	},
	{
		Name:    "domains",
		DocSlug: "domains",
		Short:   "Manage custom domains",
		Subcommands: []cliSub{
			{Name: subList, Short: "List custom domain bindings"},
			{Name: subAdd, Short: "Bind a custom domain to an app"},
			{Name: subRm, Short: "Remove a custom domain binding"},
			{Name: subDomainsVerify, Short: "Re-verify DNS + cert for a domain"},
			{Name: subDomainsShow, Short: "Show a domain's cert details"},
			{Name: subDomainsStatus, Short: "Show durable TLS status for all domains"},
			{Name: subDomainsDoctor, Short: "5-check doctor report (DNS / CNAME / TLS / CAA / IPv6)"},
		},
	},
	{
		Name:    "dev",
		DocSlug: "dev",
		Short:   "Sync the dirty working tree to a stable remote developer environment",
		Flags: []cliFlag{
			{Name: "path", Short: "source directory", Value: "DIR"},
			{Name: "name", Short: "developer-session project name", Value: "PROJECT"},
			{Name: "env-file", Short: "sync KEY=VALUE entries as developer secrets", Value: "PATH"},
			{Name: "once", Short: "deploy once and exit"},
			{Name: "stop", Short: "tear down the developer environment"},
			{Name: "no-logs", Short: "do not attach the live runtime log stream"},
		},
	},
	{
		Name:    "preview",
		DocSlug: "preview",
		Short:   "Manage preview environments (Mega-C PR-1 / issue #961 leaf 3)",
		Subcommands: []cliSub{
			{Name: "destroy", Short: "Tear down a preview app (POST /v1/preview/{slug}/destroy)"},
		},
	},
	{
		Name:    "tenant-surfaces",
		DocSlug: "tenant-surfaces",
		Short:   "Manage tenant surfaces (multi-hostname SAN bundle per app)",
		Subcommands: []cliSub{
			{Name: subList, Short: "List tenant surfaces on an app", Flags: []cliFlag{
				{Name: "app", Short: "app slug (required)", Value: "slug"},
			}},
			{Name: subAdd, Short: "Add a tenant surface (with seed hostnames)"},
			{Name: subRm, Short: "Remove a tenant surface (cascades hostnames)"},
			{Name: "hostname", Short: "Manage hostnames on a surface (add|rm)"},
		},
		Flags: []cliFlag{
			{Name: "app", Short: "app slug", Value: "slug"},
		},
	},
	{
		Name:    "edge-rules",
		DocSlug: "edge-rules",
		Short:   "Per-app edge rules (edge-rules list|create|get|update|delete --app <slug>)",
		Subcommands: []cliSub{
			{Name: subList, Short: "List edge rules", Flags: []cliFlag{
				{Name: "app", Short: "filter to a single app slug", Value: "slug"},
				{Name: "kind", Short: "filter to a single kind", ClosedSet: edgeRuleKindVocab},
			}},
			{Name: subCreate, Short: "Add an edge rule"},
			{Name: subGet, Short: "Show one edge rule"},
			{Name: subUpdate, Short: "Update one edge rule"},
			{Name: subRm, Short: "Delete one edge rule"},
		},
		Flags: []cliFlag{
			{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			{Name: "kind", Short: "rule kind", ClosedSet: edgeRuleKindVocab},
		},
	},
	{
		// Issue #976 / ADR-122 / SAFE-RELEASES-D: pre-publish
		// schema-drift gate. Pure local: reads two openapi.yaml
		// files, runs pkg/openapidiff.Compare, prints one row per
		// SchemaBreak, exits 2 iff any BREAKING row is present.
		// CI consumes the exit code.
		Name:    "openapi",
		DocSlug: "openapi",
		Short:   "Manage app OpenAPI docs + pre-publish schema-drift checks",
		Subcommands: []cliSub{
			{Name: "diff", Short: "Diff two openapi.yaml files; exit 2 on any BREAKING row"},
			{Name: "get", Short: "Fetch an app OpenAPI document (manual_import|auto)", Flags: []cliFlag{
				{Name: "source", Short: "document source", Value: "manual_import|auto", ClosedSet: []string{"manual_import", "auto"}},
			}},
			{Name: "import", Short: "Import an app OpenAPI document from a JSON file or stdin"},
			{Name: "dry-run", Short: "Preview uncovered routes without importing the document"},
			{Name: "rm", Short: "Remove the imported app OpenAPI document"},
		},
	},
	{
		Name:    "env",
		DocSlug: "env",
		Short:   "Pull/push .env <-> sealed secrets (--app <slug>)",
		Flags:   []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "slug"}},
		Subcommands: []cliSub{
			{Name: "pull", Short: "Pull sealed-secret keys to a .env skeleton (values blank)"},
			{Name: "push", Short: "Push KEY=VALUE pairs to sealed secrets"},
			{Name: "diff", Short: "Render the env-diff matrix (presence / value-equality across scopes)"},
		},
	},
	{
		Name:    "init",
		DocSlug: "init",
		Short:   "Scaffold a reference project from a built-in template (--template NAME --path DIR [--deploy])",
		Flags: []cliFlag{
			{Name: "template", Short: "template name", Req: true, Value: "NAME", ClosedSet: templateNames13},
			{Name: "path", Short: "target directory", Req: true, Value: "DIR"},
			{Name: "deploy", Short: "deploy after scaffolding"},
			{Name: "name", Short: "app slug used with --deploy", Value: "SLUG"},
			{Name: "list", Short: "list available templates"},
		},
	},
	{
		Name:        dispatchInspect,
		DocSlug:     "inspect",
		Short:       "Read-only operator surface (inspect <slug> --upstreams [--scope <scope>] [--json])",
		Positionals: []string{"<slug>"},
		// Leaf-selectors are flags on this verb, not positional
		// sub-verbs (issue #952 UX: `gregale inspect <slug>
		// --upstreams`). Future leaves (--env, --crons,
		// --instances) add another `cliFlag` entry below. The
		// completion backend and man-page renderer read this
		// Flags block to surface the right verb shape.
		Flags: []cliFlag{
			{Name: "upstreams", Short: "List data upstreams captured for this app (ADR-098 §9.A)"},
			{Name: "scope", Short: "filter by scope (forwarded as ?scope=, used with --upstreams)", Value: "scope"},
			{Name: "errors", Short: "show the latest failed deployment's persisted error explanation"},
		},
	},
	{
		Name:    "invoke",
		DocSlug: "invoke",
		Short:   "Functional smoke test (invoke [--async] <slug> [--payload J|@file|-])",
		Flags: []cliFlag{
			{Name: "async", Short: "return immediately with status_url"},
			{Name: "payload", Short: "JSON payload (inline | @file | -)", Value: "J|@file|-"},
		},
		Positionals: []string{"<slug>"},
	},
	{
		Name:    "invocations",
		DocSlug: "invocations",
		Short:   "Per-account invocation ledger (invocations list|get <id>)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List invocations"},
			{Name: "get", Short: "Show one invocation"},
		},
		Positionals: []string{"<id>"},
	},
	{
		Name:    "debug",
		DocSlug: "debug",
		Short:   "Production debugger (ADR-127 / PR-B)",
		Subcommands: []cliSub{
			{Name: "requests", Short: "Per-request telemetry (list|get|replay)"},
			{Name: "regressions", Short: "Active regression observations"},
			{Name: "compare", Short: "Per-route deployment-vs-deployment compare"},
		},
		Positionals: []string{"<slug>"},
	},
	{
		Name:    "invitations",
		DocSlug: "invitations",
		Short:   "Standalone invitation actions (invitations peek <token>|accept <token>)",
		Subcommands: []cliSub{
			{Name: "peek", Short: "Look up an invitation by token"},
			{Name: "accept", Short: "Accept an invitation"},
		},
		Positionals: []string{"<token>"},
	},
	{
		Name:    "invoices",
		DocSlug: "invoices",
		Short:   "List issued invoices",
	},
	{
		Name:    "keys",
		DocSlug: "keys",
		Short:   "Manage API keys (keys list|add|rm|rotate|grace-window)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List API keys"},
			{Name: "add", Short: "Mint a new API key"},
			{Name: "rm", Short: "Revoke an API key"},
			{Name: subRotate, Short: "Rotate an API key"},
			{Name: "grace-window", Short: "Set the rotation grace window"},
		},
	},
	{
		Name:    "login",
		DocSlug: "auth",
		Short:   "Authenticate this machine (--token for CI)",
		Flags:   []cliFlag{{Name: "token", Short: "use a pre-minted token (CI)", Value: "TOKEN"}},
	},
	{
		Name:    "logout",
		DocSlug: "auth",
		Short:   "Remove the stored token",
	},
	{
		Name:    "signup",
		DocSlug: "auth",
		Short:   "Create a new account (signup [--email-only EMAIL | --password-stdin])",
		Flags: []cliFlag{
			{Name: "email-only", Short: "send a one-time signup link to this email (no password prompt)", Value: "EMAIL"},
			{Name: "password-stdin", Short: "read password from stdin (CI; mutually exclusive with --email-only)"},
		},
	},
	{
		Name:    "logs",
		DocSlug: "logs",
		Short:   "Tail app or deployment logs (--follow)",
		Flags:   []cliFlag{{Name: "follow", Short: "stream logs until interrupted"}},
	},
	{
		Name:    "metrics",
		DocSlug: "metrics",
		Short:   "Per-app or account-wide metrics (gregale metrics <slug> [--range 5m] | --account)",
		Flags: []cliFlag{
			{Name: "range", Short: "window (5m|15m|1h|6h|24h|7d)", Value: "WINDOW", ClosedSet: []string{"5m", "15m", "1h", "6h", "24h", "7d"}},
			{Name: "account", Short: "account-wide roll-up"},
		},
		Positionals: []string{"<slug>"},
	},
	{
		Name:    "mfa",
		DocSlug: "mfa",
		Short:   "Manage account MFA (mfa enroll|confirm|verify|recover|disable)",
		Subcommands: []cliSub{
			{Name: "enroll", Short: "Begin TOTP enrolment"},
			{Name: "confirm", Short: "Confirm an enrolment code"},
			{Name: "verify", Short: "Verify a TOTP code (step-up)"},
			{Name: "recover", Short: "Use a recovery code"},
			{Name: "disable", Short: "Disable MFA"},
		},
	},
	{
		Name:    "open",
		DocSlug: "open",
		Short:   "Open the app's URL (or its dashboard page) in your browser",
		Subcommands: []cliSub{
			{Name: "docs", Short: "Open a CLI docs page (open docs [<slug>])"},
		},
	},
	{
		Name:    "orgs",
		DocSlug: "orgs",
		Short:   "Manage orgs + members (orgs ls|create|info|rm|members ...|keys ...|transfer-ownership|seat-usage|invitations ...|me)",
		Subcommands: []cliSub{
			{Name: "ls", Short: "List orgs"},
			{Name: "create", Short: "Create an org"},
			{Name: "info", Short: "Show one org"},
			{Name: "rm", Short: "Delete one org"},
			{Name: "members", Short: "Manage org members"},
			{Name: "keys", Short: "Manage org API keys"},
			{Name: "transfer-ownership", Short: "Transfer org ownership"},
			{Name: "seat-usage", Short: "Show seat usage"},
			{Name: "invitations", Short: "Manage org invitations"},
			{Name: "me", Short: "Show current org membership"},
			{Name: "update", Short: "Update org metadata"},
		},
	},
	{
		Name:        "overage-cap",
		DocSlug:     "overage-cap",
		Short:       "Set / clear the account's overage cap (--clear | <cents>)",
		Flags:       []cliFlag{{Name: "clear", Short: "remove the overage cap"}},
		Positionals: []string{"<cents>"},
	},
	{
		Name:    "park",
		DocSlug: "park-wake",
		Short:   "Park an app cold (kill all live instances)",
	},
	{
		Name:      "plan",
		DocSlug:   "plan",
		Short:     "Change plan (free|hobby|pro|scale); paid upgrades open the provider checkout",
		ClosedSet: []string{"free", "hobby", "pro", "scale"},
	},
	{
		Name:    "ps",
		DocSlug: "ps",
		Short:   "Show live instances + state for an app",
	},
	{
		Name:    "queue",
		DocSlug: "queue",
		Short:   "Inspect the wake-queue depth (queue tail|send|receive|state|peek|dead-letter|ack)",
		Subcommands: []cliSub{
			{Name: "tail", Short: "Tail the wake queue"},
			{Name: "send", Short: "Enqueue a wake request"},
			{Name: "receive", Short: "Receive a wake request"},
			{Name: statusLiteral, Short: "Show queue state"},
			{Name: "peek", Short: "Peek at the next wake"},
			{Name: "dead-letter", Short: "Inspect the dead-letter queue"},
			{Name: "ack", Short: "Ack a wake"},
		},
	},
	{
		Name:    "registry",
		DocSlug: "registry",
		Short:   "Per-app private container registry credentials (registry list|set|rm --app <slug>)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List registry credentials"},
			{Name: "set", Short: "Set a registry credential"},
			{Name: "rm", Short: "Remove a registry credential"},
		},
		Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true}},
	},
	{
		Name:    "rollback",
		DocSlug: "rollback",
		Short:   "Re-promote the previous deployment",
	},
	{
		// SAFE-RELEASES-R (issue #976 / ADR-122): the
		// operator manual-recovery escape hatch — see
		// cmd/gregale/commands_rollouts.go. The CLI
		// subcommand `gregale rollouts recover <slug>` is
		// the canonical caller; the route is mounted at
		// POST /v1/apps/{slug}/rollouts/recover (apid).
		Name:    "rollouts",
		DocSlug: "rollouts",
		Short:   "Operator manual rollout recovery (rollouts recover <slug> --action advance|promote|abort --reason <text>)",
		Subcommands: []cliSub{
			{Name: "recover", Short: "Manually advance / promote / abort a stuck rollout (operator escape hatch)"},
		},
		Positionals: []string{"<slug>"},
		Flags: []cliFlag{
			{Name: "action", Short: "recover action", ClosedSet: []string{"advance", "promote", "abort"}, Req: true},
			{Name: "reason", Short: "operator-supplied reason (logged to deployment_audit)", Value: "text"},
		},
	},
	{
		Name:    "scan",
		DocSlug: "scan",
		Short:   "Decomposition dry-run (--tarball | --path | --repo OWNER/NAME)",
		Flags: []cliFlag{
			{Name: "tarball", Short: "scan a source tarball", Value: "PATH"},
			{Name: "path", Short: "scan a local directory", Value: "DIR"},
			{Name: "repo", Short: "scan a GitHub repo", Value: "OWNER/NAME"},
			// ADR-124 follow-up #1: --exclude + --show-affected
			// ship on scan as well as deploy (the partition is the
			// preview surface, scan is the operator's first stop).
			// Same rationale as the deploy entries above: they were
			// added to cmdScan in PR-#1065 but missing from the
			// manifest that drives `gregale man scan` and the shell
			// completion tables.
			{Name: "exclude", Short: "omit workloads (slug, comma-separated; mutex with --only; ADR-124)", Value: "SLUGS"},
			{Name: "show-affected", Short: "render the WillDeploy + Unaffected tables (ADR-124)"},
			// ADR-124 follow-up #3 (PR-B commit 5): symmetric flag
			// set on scan (no-op on the scan path; the scan handler
			// ignores persist_exclude). Accepted so a single flag set
			// is reusable across the scan + apply pair.
			{Name: "persist-exclude", Short: "record --exclude slugs into deployment_scope_exclusions (apply path only; ADR-124 follow-up #3)"},
		},
	},
	{
		Name:    "secrets",
		DocSlug: "secrets",
		Short:   "Manage env secrets (secrets list|set|unset|list-all|rotate)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List sealed secrets"},
			{Name: "set", Short: "Set a sealed secret"},
			{Name: "unset", Short: "Remove a sealed secret"},
			{Name: "list-all", Short: "List every secret across apps"},
			{Name: subRotate, Short: "Re-seal one secret under the current host key"},
		},
	},
	{
		// Compatibility surface for installation-scoped secrets used by
		// legacy non-GitHub senders. Standard GitHub App webhooks use the
		// single platform App secret documented in ADR-012 §8.
		Name:    "github-webhook-secret",
		DocSlug: "github-webhook-secret",
		Short:   "Manage legacy installation-scoped webhook secrets (admin)",
		Subcommands: []cliSub{
			{Name: "set", Short: "Rotate the secret for one installation_id"},
		},
	},
	// operator-side verbs (sign-keys, node-key) moved to gregalectl
	// in PR-6.5; see cmd/gregale/constants.go for the dispatch consts.
	{
		Name:    "slo",
		DocSlug: "slo",
		Short:   "Per-app SLO panel (gregale slo <slug> [--window 24h])",
		Flags: []cliFlag{
			{Name: "window", Short: "window (1h|24h|7d)", Value: "WINDOW", ClosedSet: []string{"1h", "24h", "7d"}},
		},
		Positionals: []string{"<slug>"},
	},
	{
		Name:    statusLiteral,
		DocSlug: "status",
		Short:   "Personal SLO numbers (availability, wake p95, build success)",
	},
	{
		Name:    "tail",
		DocSlug: "tail",
		Short:   "Live tail of the unified event stream",
		// The stream is always followed; there is no --follow flag on
		// cmdTail (commands5.go) and the manifest must not invent one.
		Flags: []cliFlag{
			{Name: "app", Short: "filter to a single app slug (optional)", Value: "slug"},
			{Name: "include-stateless", Short: "also print stateless.advisory frames (default: hide)"},
		},
	},
	{
		Name:    dispatchTrustedPublishers,
		DocSlug: "trusted-publishers",
		Short:   "Per-app cosign trusted-publisher list (admin; trusted-publishers add|remove|list)",
		Subcommands: []cliSub{
			{Name: "add", Short: "Add a trusted publisher"},
			{Name: "remove", Short: "Remove a trusted publisher"},
			{Name: "list", Short: "List trusted publishers"},
		},
	},
	{
		Name:    "usage",
		DocSlug: "usage",
		Short:   "Show this month's usage (gregale usage [--month YYYY-MM]|daily [--day YYYY-MM-DD]|storage [--day YYYY-MM-DD]|summary)",
		Subcommands: []cliSub{
			{Name: "daily", Short: "Per-day breakdown"},
			{Name: "storage", Short: "Per-app storage bytes"},
			{Name: "summary", Short: "Account roll-up"},
		},
		Flags: []cliFlag{
			{Name: "month", Short: "month (YYYY-MM)", Value: "YYYY-MM"},
			{Name: "day", Short: "day (YYYY-MM-DD)", Value: "YYYY-MM-DD"},
		},
	},
	{
		Name:    "version",
		DocSlug: "version",
		Short:   "Print the CLI version",
	},
	{
		Name:    "wake-timeline",
		DocSlug: "wake-timeline",
		Short:   "Walk the per-wake event stream (wake-timeline <slug> <wake-id> [--since RFC3339] [--limit N] [--all])",
		Flags: []cliFlag{
			{Name: "since", Short: "RFC3339 timestamp", Value: "RFC3339"},
			{Name: "limit", Short: "page size (1..1000)", Value: "N"},
			{Name: "all", Short: "walk every page"},
		},
		Positionals: []string{"<slug>", "<wake-id>"},
	},
	{
		Name:    "throttle-suggestions",
		DocSlug: "throttle-suggestions",
		Short:   "Per-route throttle recommendations + dry-run preview (gregale throttle-suggestions <slug> [--range 5m] [--dry-run --candidate-rps N --candidate-burst N])",
		Flags: []cliFlag{
			{Name: "range", Short: "observation window (e.g. 5m|1h|24h)", Value: "WINDOW", ClosedSet: []string{"5m", "15m", "1h", "6h", "24h"}},
			{Name: "dry-run", Short: "enable the dry-run preview pass (requires --candidate-rps)"},
			{Name: "candidate-rps", Short: "candidate rate-limit rps for the dry-run preview", Value: "N"},
			{Name: "candidate-burst", Short: "candidate burst for the dry-run preview", Value: "N"},
		},
		Positionals: []string{"<slug>"},
	},
	{
		Name:    "mail",
		DocSlug: "mail-dry-run",
		Short:   "Mail operator dry-run (issue #246 acceptance item 6): `gregale mail dry-run [--unsubscribe-url URL]` renders every production template against a fixture account + day and writes the wire payload as JSON. The eyeball gate before flipping a box to FAAS_MAIL_TRANSPORT=resend.",
		Subcommands: []cliSub{
			{Name: "dry-run", Short: "render every mail template against a fixture; print wire JSON"},
		},
		Flags: []cliFlag{
			{Name: "unsubscribe-url", Short: "List-Unsubscribe URL (RFC 8058); empty disables the header", Value: "URL"},
		},
	},
	{
		Name:    "wake",
		DocSlug: "park-wake",
		Short:   "Wake a parked app (pulls out of snapshot)",
	},
	{
		Name:    "traffic",
		DocSlug: "traffic",
		Short:   "Manage deployment traffic split (issue #556; Pro/Scale only)",
		Subcommands: []cliSub{
			{
				Name:  "set",
				Short: "Set the traffic split for a deployment",
				Flags: []cliFlag{
					{Name: "deployment", Short: "deployment id to set the traffic split on", Req: true, Value: "ID"},
					{Name: "percent", Short: "traffic weight in [0, 100]; -1 = unset (server default 100)", Req: true, Value: "N"},
				},
			},
		},
	},
	{
		Name:    "mirror",
		DocSlug: "mirror",
		Short:   "Manage traffic mirroring (mirror list|create|info|update|rm|summary --app <slug>; issue #72 / ADR-124; Pro/Scale only)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List mirror rules", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			}},
			{Name: "create", Short: "Create a mirror rule", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "source", Short: "source deployment id (live)", Req: true, Value: "ID"},
				{Name: "mirror", Short: "mirror deployment id (live; same app)", Req: true, Value: "ID"},
				{Name: "percent", Short: "fan-out percent in [0, 100]; 100 = every request", Value: "N"},
				{Name: "include-body", Short: "include request/response bodies in the comparison ledger"},
				{Name: "redact-header", Short: "extra header name to redact (repeatable)", Value: "NAME"},
			}},
			{Name: "info", Short: "Show one mirror rule", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "id", Short: "mirror rule id", Req: true, Value: "ID"},
			}},
			{Name: "update", Short: "Patch a mirror rule (patch semantics)", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "id", Short: "mirror rule id", Req: true, Value: "ID"},
				{Name: "percent", Short: "new percent in [0, 100]", Value: "N"},
				{Name: "enable", Short: "enable the rule (mutually exclusive with --disable)"},
				{Name: "disable", Short: "disable the rule (mutually exclusive with --enable)"},
				{Name: "include-body", Short: "enable body capture (mutually exclusive with --no-include-body)"},
				{Name: "no-include-body", Short: "disable body capture"},
				{Name: "redact-header", Short: "extra header name to redact (repeatable)", Value: "NAME"},
				{Name: "clear-redact", Short: "clear the customer's redact_headers list (drop to always-stripped only)"},
			}},
			{Name: "rm", Short: "Delete a mirror rule", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "id", Short: "mirror rule id", Req: true, Value: "ID"},
			}},
			{Name: "summary", Short: "Aggregate mirror drift counts over a window", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "id", Short: "mirror rule id", Req: true, Value: "ID"},
				{Name: "window", Short: "summary window: 1h | 24h | 7d (default 1h)", Value: "WINDOW", ClosedSet: []string{"1h", "24h", "7d"}},
			}},
		},
	},
	{
		Name:    "cache",
		DocSlug: "cache",
		Short:   "Manage response cache (cache purge <slug> [--path GLOB])",
		Subcommands: []cliSub{
			{Name: "purge", Short: "Purge cached responses for an app", Flags: []cliFlag{
				{Name: "path", Short: "optional normalized request path glob", Value: "GLOB"},
			}},
		},
		Positionals: []string{"<slug>"},
	},
	{
		Name:    "webhooks",
		DocSlug: "webhooks",
		Short:   "Manage outbound webhooks (webhooks list|add|info|update|rm|deliveries|retry|rotate-secret)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List webhooks"},
			{Name: "add", Short: "Add a webhook"},
			{Name: "info", Short: "Show one webhook"},
			{Name: "update", Short: "Update one webhook"},
			{Name: "rm", Short: "Delete one webhook"},
			{Name: "deliveries", Short: "Show the delivery ledger"},
			{Name: "retry", Short: "Retry a failed delivery"},
			{Name: "rotate-secret", Short: "Rotate the webhook signing secret"},
		},
	},
	{
		Name:    "whoami",
		DocSlug: "auth",
		Short:   "Show the authenticated account",
	},

	// Tier A8 surface — the two new commands this PR lands.
	{
		Name:    "completion",
		DocSlug: "completion",
		Short:   "Print a shell completion script (bash|zsh|fish|powershell)",
		Subcommands: []cliSub{
			{Name: "bash", Short: "Print the bash completion script"},
			{Name: "zsh", Short: "Print the zsh completion script"},
			{Name: "fish", Short: "Print the fish completion script"},
			{Name: "powershell", Short: "Print the powershell completion snippet"},
		},
	},
	{
		Name:        "man",
		DocSlug:     "man",
		Short:       "Print the gregale(1) man page (or gregale-<command>(1) with one arg)",
		Positionals: []string{"<command>"},
	},
}
