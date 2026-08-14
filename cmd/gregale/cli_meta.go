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
// the docs.gregale.dev/cli/<DocSlug> page; Subcommands lists the
// verbs the dispatcher recognises (the dispatcher in each
// commands_*.go file is the source of truth for verb spellings —
// the manifest mirrors them).

package main

// cliCommand is one top-level gregale command.
type cliCommand struct {
	// Name is the literal the user types: "apps", "delayed-task", etc.
	Name string
	// DocSlug is the slug appended to docs.gregale.dev/cli/<DocSlug>;
	// mirrors the second arg of every PrintUsage call (output.go:156).
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
	// ClosedSet enumerates the allowed literal values, when the
	// flag is a closed enum (plan, metric, comparison, window-spec,
	// etc.). When non-empty, completion offers these as the flag's
	// value; the leaf's validator accepts ONLY these strings, so
	// mirroring the server-side gate here is a hard contract.
	ClosedSet []string
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
		Short:   "Operator-only billing ops (admin credit --reason <text> <uuid> <cents>)",
		Subcommands: []cliSub{
			{Name: "credit", Short: "Issue a billing credit", Flags: []cliFlag{
				{Name: "reason", Short: "credit reason text", Req: true},
			}},
		},
		Positionals: []string{"<uuid>", "<cents>"},
	},
	{
		Name:    "alerts",
		DocSlug: "alerts",
		Short:   "Per-app alert rules (alerts list|add|info|update|rm|rotate-secret --app <slug>)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List alert rules", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true},
			}},
			{Name: "add", Short: "Add an alert rule"},
			{Name: "info", Short: "Show one alert rule"},
			{Name: "update", Short: "Update one alert rule"},
			{Name: "rm", Short: "Delete one alert rule"},
			{Name: "rotate-secret", Short: "Rotate the alert's webhook secret"},
		},
		Flags: []cliFlag{{Name: "app", Short: "app slug"}},
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
		Short:   "Get/update one app (gregale app <slug> [scale|rename <new>|--ram N|…])",
		Subcommands: []cliSub{
			{Name: "scale", Short: "Set max_concurrency / ram_mb"},
			{Name: "rename", Short: "Rename an app"},
			{Name: "security", Short: "Toggle require_signed on deploys"},
			{Name: "routes", Short: "List admitted per-route labels for one app (ADR-093)"},
		},
		Positionals: []string{"<slug>"},
		Flags: []cliFlag{
			{Name: "ram", Short: "set RAM in MB"},
			{Name: "max-concurrency", Short: "set max_concurrency"},
			{Name: "require-signed", Short: "toggle require_signed", ClosedSet: []string{"true", "false"}},
		},
	},
	{
		Name:    "backup",
		DocSlug: "backup",
		Short:   "Operator rclone config unseal (backup unseal-rclone)",
		Subcommands: []cliSub{
			{Name: "unseal-rclone", Short: "Unseal the rclone config"},
		},
	},
	{
		Name:    "billing",
		DocSlug: "billing",
		Short:   "Manage billing (gregale billing portal)",
		Subcommands: []cliSub{
			{Name: "portal", Short: "Open the Stripe billing portal"},
		},
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
		Short:   "Connect a third-party service (github)",
		Subcommands: []cliSub{
			{Name: "github", Short: "Connect a GitHub account for repo deploys"},
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
		Name:    "dashboard",
		DocSlug: "dashboard",
		Short:   "Open the account dashboard in your browser",
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
			{Name: "limit", Short: "page size (1-200)"},
			{Name: "before", Short: "pagination cursor (RFC3339Nano)"},
			{Name: "all", Short: "walk every page"},
		},
	},
	{
		Name:    dispatchDeployment,
		DocSlug: "deployment",
		Short:   "Get one deployment (<id> | set-min-instances <id> --min N)",
		Subcommands: []cliSub{
			{Name: "set-min-instances", Short: "Set the per-deployment cold-wake floor"},
		},
		Positionals: []string{"<id>"},
		Flags: []cliFlag{
			{Name: "show-scan", Short: "include the per-deploy grype scan payload"},
			{Name: "min", Short: "min_instances floor (>= 0)"},
		},
	},
	{
		Name:    "deploy",
		DocSlug: "deploy",
		Short:   "Deploy (--image REF | --tarball PATH | --repo OWNER/NAME --ref REF | --github | --template NAME)",
		Flags: []cliFlag{
			{Name: "image", Short: "deploy from a container image reference"},
			{Name: "tarball", Short: "deploy from a source tarball"},
			{Name: "repo", Short: "deploy from a GitHub repo"},
			// Issue #739 / ADR-092: --ref pairs with --repo to
			// drive the headless source-ref deploy (CI-friendly,
			// no install-token env). Required when --repo is set.
			{Name: "ref", Short: "git ref for --repo (branch, tag, or 40-char SHA)"},
			// Issue #270: --github emits a copy-paste Actions workflow
			// snippet for the faas-deploy-action (companion repo
			// poyrazK/faas-deploy-action). No auth, no side effects.
			// The snippet uses --name / cwd as the app slug.
			{Name: "github", Short: "emit a GitHub Actions workflow snippet for faas-deploy-action"},
			{Name: "template", Short: "scaffold from a built-in template", ClosedSet: []string{"node22-http", "python312-http"}},
		},
	},
	{
		Name:    "domains",
		DocSlug: "domains",
		Short:   "Manage custom domains",
	},
	{
		Name:    "edge-rules",
		DocSlug: "edge-rules",
		Short:   "Per-app edge rules (edge-rules list|create|get|update|delete --app <slug>)",
		Subcommands: []cliSub{
			{Name: subList, Short: "List edge rules", Flags: []cliFlag{
				{Name: "app", Short: "filter to a single app slug"},
				{Name: "kind", Short: "filter to a single kind", ClosedSet: edgeRuleKindVocab},
			}},
			{Name: subCreate, Short: "Add an edge rule"},
			{Name: subGet, Short: "Show one edge rule"},
			{Name: subUpdate, Short: "Update one edge rule"},
			{Name: subRm, Short: "Delete one edge rule"},
		},
		Flags: []cliFlag{
			{Name: "app", Short: "app slug", Req: true},
			{Name: "kind", Short: "rule kind", ClosedSet: edgeRuleKindVocab},
		},
	},
	{
		Name:    "env",
		DocSlug: "env",
		Short:   "Pull/push .env <-> sealed secrets (--app <slug>)",
		Flags:   []cliFlag{{Name: "app", Short: "app slug", Req: true}},
	},
	{
		Name:    dispatchHostAge,
		DocSlug: "host-age",
		Short:   "Operator host.age rotation (host-age init|rotate|status|prune-previous)",
		Subcommands: []cliSub{
			{Name: "init", Short: "Initialise host.age"},
			{Name: subRotate, Short: "Rotate host.age"},
			{Name: "status", Short: "Show host.age status"},
			{Name: "prune-previous", Short: "Prune the previous host.age key"},
		},
	},
	{
		Name:    "init",
		DocSlug: "init",
		Short:   "Scaffold a reference project from a built-in template (--template NAME --path DIR [--deploy])",
		Flags: []cliFlag{
			{Name: "template", Short: "template name", Req: true, ClosedSet: []string{"node22-http", "python312-http"}},
			{Name: "path", Short: "target directory", Req: true},
			{Name: "deploy", Short: "deploy after scaffolding"},
		},
	},
	{
		Name:    "invoke",
		DocSlug: "invoke",
		Short:   "Functional smoke test (invoke [--async] <slug> [--payload J|@file|-])",
		Flags: []cliFlag{
			{Name: "async", Short: "return immediately with status_url"},
			{Name: "payload", Short: "JSON payload (inline | @file | -)"},
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
		Name:    "jobs",
		DocSlug: "jobs",
		Short:   "Manage run-to-completion jobs (jobs list|create|info|update|rm|run|runs|cancel)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List jobs"},
			{Name: "create", Short: "Create a job"},
			{Name: "info", Short: "Show one job"},
			{Name: "update", Short: "Update one job"},
			{Name: "rm", Short: "Delete one job"},
			{Name: "run", Short: "Enqueue a job run"},
			{Name: "runs", Short: "Show one job's run history"},
			{Name: "cancel", Short: "Cancel an in-flight run"},
		},
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
		Flags:   []cliFlag{{Name: "token", Short: "use a pre-minted token (CI)"}},
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
			{Name: "email-only", Short: "send a one-time signup link to this email (no password prompt)"},
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
			{Name: "range", Short: "window (5m|15m|1h|6h|24h|7d)", ClosedSet: []string{"5m", "15m", "1h", "6h", "24h", "7d"}},
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
		Name:    "manifest",
		DocSlug: "manifest",
		Short:   "Operator split-box deployment manifest (manifest validate --file PATH; issue #911 / ADR-110)",
		Subcommands: []cliSub{
			{
				Name:  "validate",
				Short: "Validate a manifest YAML file (canonical path: pkg/manifest.Validate)",
				Flags: []cliFlag{{Name: "file", Short: "path to the manifest YAML file (required)"}},
			},
		},
	},
	{
		Name:    "park",
		DocSlug: "park-wake",
		Short:   "Park an app cold (kill all live instances)",
	},
	{
		Name:    dispatchPKI,
		DocSlug: "pki",
		Short:   "Operator local-dev PKI bootstrap (pki init|status|rotate)",
		Subcommands: []cliSub{
			{Name: "init", Short: "Initialise the local PKI"},
			{Name: statusLiteral, Short: "Show PKI status"},
			{Name: subRotate, Short: "Rotate the PKI"},
		},
	},
	{
		Name:      "plan",
		DocSlug:   "plan",
		Short:     "Change plan (free|hobby|pro|scale)",
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
		Name:    "scan",
		DocSlug: "scan",
		Short:   "Decomposition dry-run (--tarball | --path | --repo OWNER/NAME)",
		Flags: []cliFlag{
			{Name: "tarball", Short: "scan a source tarball"},
			{Name: "path", Short: "scan a local directory"},
			{Name: "repo", Short: "scan a GitHub repo"},
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
		// PR-D / ADR-012 §7 amendment. Per-tenant GitHub App
		// webhook secret rotation (admin-scoped). Distinct from
		// `secrets` because the trust boundary is the GitHub
		// App install, not the Faas app — the resolver in
		// pkg/githubd/webhook_secret.go reads this row first
		// before falling back to the platform secret.
		Name:    "github-webhook-secret",
		DocSlug: "github-webhook-secret",
		Short:   "Manage per-tenant GitHub App webhook secrets (admin)",
		Subcommands: []cliSub{
			{Name: "set", Short: "Rotate the secret for one installation_id"},
		},
	},
	{
		Name:    dispatchSignKeys,
		DocSlug: "sign-keys",
		Short:   "Provision the cosign sign keypair (operator; --sign-key / --verify-key)",
		Subcommands: []cliSub{
			{Name: "init", Short: "Initialise the cosign keypair"},
			{Name: subRotate, Short: "Rotate the cosign keypair"},
			{Name: statusLiteral, Short: "Show keypair status"},
		},
		Flags: []cliFlag{
			{Name: "sign-key", Short: "path to the sign key"},
			{Name: "verify-key", Short: "path to the verify key"},
		},
	},
	{
		Name:    dispatchNodeKey,
		DocSlug: "node-key",
		Short:   "Provision the per-node CapacityReport signing keypair (operator; ADR-053)",
		Subcommands: []cliSub{
			{Name: subNodeInit, Short: "Initialise the node signing keypair"},
			{Name: subNodeRotate, Short: "Rotate the node signing keypair"},
			{Name: subNodeStatus, Short: "Show node keypair status"},
		},
		Flags: []cliFlag{
			{Name: "node-key", Short: "path to the node signing private key"},
			{Name: "node-key-pub", Short: "path to the node signing public key"},
		},
	},
	{
		Name:    "slo",
		DocSlug: "slo",
		Short:   "Per-app SLO panel (gregale slo <slug> [--window 24h])",
		Flags: []cliFlag{
			{Name: "window", Short: "window (1h|24h|7d)", ClosedSet: []string{"1h", "24h", "7d"}},
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
		Short:   "Live tail of the unified event stream (--follow)",
		Flags:   []cliFlag{{Name: "follow", Short: "stream until interrupted"}},
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
			{Name: "month", Short: "month (YYYY-MM)"},
			{Name: "day", Short: "day (YYYY-MM-DD)"},
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
			{Name: "since", Short: "RFC3339 timestamp"},
			{Name: "limit", Short: "page size (1..1000)"},
			{Name: "all", Short: "walk every page"},
		},
		Positionals: []string{"<slug>", "<wake-id>"},
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
					{Name: "deployment", Short: "deployment id to set the traffic split on", Req: true},
					{Name: "percent", Short: "traffic weight in [0, 100]; -1 = unset (server default 100)", Req: true},
				},
			},
		},
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
