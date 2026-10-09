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

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
)

// cliAudience controls how a command is presented in the customer binary.
// The complete manifest remains authoritative for dispatch, man pages, and
// compatibility checks; audience only affects the default discovery surface.
type cliAudience uint8

const (
	cliAudienceCustomer cliAudience = iota
	cliAudienceOperator
	cliAudienceCompatibility
)

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
	// Examples are runnable command lines shown by local help, man pages,
	// and the generated Markdown reference for common customer tasks.
	Examples []string
	// Subcommands enumerates the verb set the dispatcher recognises.
	// Empty for commands with no verb set (e.g. `whoami`, `version`).
	Subcommands []cliSub
	// SubcommandsAfterPositionals marks command families whose syntax puts
	// the leading positional before the verb (for example, `app <slug>
	// scale`). Most commands use the conventional verb-first shape, such
	// as `cache purge <slug>`, so the zero value remains false.
	SubcommandsAfterPositionals bool
	// Flags enumerates the top-level flags accepted on this command's
	// own flag set (i.e. before any subcommand dispatch). Empty if
	// the command dispatches immediately on args[0] (most multi-verb
	// commands). The `--app <slug>` etc. that follow a subcommand
	// belong on the cliSub, not here.
	Flags []cliFlag
	// Positionals documents the required positional args in order.
	// Used by the man-page renderer to fill the SYNOPSIS section.
	// Example: ["<slug>", "<wake-id>"] for `gregale wake-timeline`.
	// App and slug positional markers drive cache-backed completion in the
	// generated shell backends.
	Positionals []string
	// CompletionPositions declares cache-backed positional values for nested
	// command paths whose arguments do not follow the top-level slug convention.
	CompletionPositions []cliCompletionPosition
	// ClosedSet enumerates the allowed values for the FIRST positional
	// when the command takes exactly one. Today only `plan` uses this
	// (free|hobby|pro|scale). Mirrors api.Plans so the manifest is the
	// source of truth for completion of the plan literal.
	ClosedSet []string
	// Audience controls whether the command is shown in the default customer
	// help/completion surface. Non-customer entries remain callable so existing
	// scripts do not break, and remain in the manifest for man pages and drift
	// tests.
	Audience cliAudience
}

// customerCliCommands returns the commands intended for normal application
// developers. Keep the full cliCommands manifest intact: hidden operator and
// legacy entries still need completion/man/dispatch coverage.
func customerCliCommands() []cliCommand {
	commands := make([]cliCommand, 0, len(cliCommands))
	for _, command := range cliCommands {
		if command.Audience == cliAudienceCustomer {
			commands = append(commands, command)
		}
	}
	return commands
}

func advancedCliCommands() []cliCommand {
	commands := make([]cliCommand, 0, len(cliCommands))
	for _, command := range cliCommands {
		if command.Audience != cliAudienceCustomer {
			commands = append(commands, command)
		}
	}
	return commands
}

// cliHelpGroup maps the customer command vocabulary to the workflow sections
// used by root help. Operator and compatibility entries intentionally fall
// through to Advanced so `gregale help --all` keeps them discoverable without
// making them part of the normal customer path.
func cliHelpGroup(command cliCommand) string {
	if command.Audience != cliAudienceCustomer {
		return "Advanced"
	}
	switch command.Name {
	case "account", "billing", "capabilities", "context", "dashboard", "doctor", "invitations", "invoices", "keys", "link", "login", "logout", "mfa", "open", "orgs", "overage-cap", "plan", "signup", "unlink", "upload-cache", "usage", "version", "completion", "man", "whoami":
		return "Core"
	case "apps", "app", "build", "connect", "cors", "deploy", "deployment", "deployments", "deploys", "dev", "domains", "edge-rules", "env", "github", "init", "invoke", "mcp", "openapi", "preview", "projects", "registry", "rollback", "routes", "scan", "secrets", "start", "tenant-surfaces", "platform-tenants", "trusted-publishers":
		return "API"
	case "add", "automations", "bindings", "bucket", "crons", "delayed-task", "events", "send", "deliver", "invocations", "jobs", "operations", "customer-operations", "run", "runs", "triggers", "webhooks", "workflows", "cache", "postgres":
		return "Data"
	case "canary", "mirror", "park", "ps", "queue", "dlq", "traffic", "wake", "wake-timeline", "workers":
		return "Delivery"
	case "alerts", "analytics", "audit-events", "debug", "inspect", "log-drains", "logs", "metrics", "realtime", "slo", "status", "tail", "throttle-suggestions", "trace":
		return "Observe"
	default:
		return "Core"
	}
}

// hasSlugFirst reports whether the first positional is an app or slug
// placeholder. It is used to place subcommands that follow a positional.
func (c cliCommand) hasSlugFirst() bool {
	if len(c.Positionals) == 0 {
		return false
	}
	return isAppSlugPositional(c.Positionals[0])
}

func (c cliCommand) subcommandChoice() string {
	names := make([]string, 0, len(c.Subcommands))
	for _, sub := range c.Subcommands {
		names = append(names, sub.Name)
	}
	return strings.Join(names, "|")
}

// completionSubcommandWord gives the argument position of the first
// subcommand, accounting for command families that put a positional first
// (`app <slug> scale`).
func (c cliCommand) completionSubcommandWord() int {
	if c.SubcommandsAfterPositionals && c.hasSlugFirst() {
		return 3
	}
	return 2
}

// cliSub is one verb under a cliCommand (e.g. alerts.list, alerts.add).
type cliSub struct {
	Name  string
	Short string
	// Aliases are accepted spellings for this subcommand. They are used
	// when expanding manifest completion paths.
	Aliases []string
	// Examples are runnable command lines shown with this subcommand's help
	// and in the generated man and Markdown references.
	Examples []string
	// Positionals are documented in the leaf synopsis for verbs whose
	// argument contract is narrower than the parent command's.
	Positionals []string
	// Flags enumerates the per-subcommand flag set. Req marks the
	// required flags; ClosedSet marks the closed-enum values
	// (plan names, metric enums, etc.) — completion backends
	// expand these inline.
	Flags []cliFlag
	// Subcommands contains one additional command level for verbs such
	// as `deployments alias list`. Most command families remain flat.
	Subcommands []cliSub
	// SubcommandsAfterPositionals marks nested command families whose
	// syntax takes positional arguments between the parent verb and its
	// child verb, such as `apps tcp <slug> add`.
	SubcommandsAfterPositionals bool
	// FlagsAfterPositionals marks leaf commands that take a positional
	// argument before parsing their flags, such as `tcp <slug> tls NAME --tls-mode`.
	FlagsAfterPositionals bool
}

type cliCompletionRole uint8

const (
	cliCompletionProjectSlug cliCompletionRole = iota + 1
	cliCompletionEnvironmentSlug
	cliCompletionBuildID
	cliCompletionDeploymentID
)

// cliCompletionPosition describes a dynamic positional completion under a
// cliCommand. Position is the one-based argument number after Path. Environment
// positions name the earlier project argument used to scope suggestions.
// Choices are static values to mix into the same position.
type cliCompletionPosition struct {
	Path            []string
	Position        int
	Role            cliCompletionRole
	ProjectPosition int
	Choices         []string
	Description     string
}

func (p cliCompletionPosition) completionDescription() string {
	if p.Description != "" {
		return p.Description
	}
	switch p.Role {
	case cliCompletionEnvironmentSlug:
		return "project environment"
	case cliCompletionBuildID:
		return "build ID"
	case cliCompletionDeploymentID:
		return "deployment ID"
	default:
		return "project slug"
	}
}

func (c cliCommand) expandedCompletionPositions() []cliCompletionPosition {
	var expanded []cliCompletionPosition
	for _, position := range c.CompletionPositions {
		for _, path := range c.completionPathVariants(position.Path) {
			copy := position
			copy.Path = path
			expanded = append(expanded, copy)
		}
	}
	return expanded
}

func (c cliCommand) completionPathVariants(path []string) [][]string {
	variants := [][]string{{}}
	children := c.Subcommands
	for _, token := range path {
		spellings := []string{token}
		var next []cliSub
		for _, sub := range children {
			if sub.Name == token {
				spellings = append(spellings, sub.Aliases...)
				next = sub.Subcommands
				break
			}
		}
		grown := make([][]string, 0, len(variants)*len(spellings))
		for _, variant := range variants {
			for _, spelling := range spellings {
				candidate := append(append([]string(nil), variant...), spelling)
				grown = append(grown, candidate)
			}
		}
		variants = grown
		children = next
	}
	return variants
}

func (s cliSub) subcommandChoice() string {
	names := make([]string, 0, len(s.Subcommands))
	for _, sub := range s.Subcommands {
		names = append(names, sub.Name)
	}
	return strings.Join(names, "|")
}

func (s cliSub) completionSpellings() []string {
	spellings := []string{s.Name}
	for _, alias := range s.Aliases {
		if alias == "" || containsCompletionString(spellings, alias) {
			continue
		}
		spellings = append(spellings, alias)
	}
	return spellings
}

// cliFlag is one CLI flag.
type cliFlag struct {
	// Name is the kebab-case form (matches flag.NewFlagSet's arg):
	// "app", "min", "require-signed", "scheduled-at".
	Name string
	// ShortName is an optional single-character spelling, without its
	// leading dash. When it matches Name, the flag is short-only (for
	// example, Name "o" and ShortName "o" renders as -o). When it
	// differs, both -<ShortName> and --<Name> are available.
	ShortName string
	// Short is the human description (mirrors flag.NewFlagSet's
	// third arg in each leaf).
	Short string
	// Req marks required flags. Completion backends do NOT offer
	// required flags as a TAB choice (the user is forced to provide
	// them); the marker exists for the man-page SYNOPSIS section
	// to render the required marker `(<name>|<placeholder>)`.
	Req bool
	// Bool marks a switch that takes no value, including when it is
	// required. Use it for explicit confirmation flags such as --yes.
	Bool bool
	// Value is the placeholder for a value-taking flag (for example,
	// "slug" or "PATH"). Empty means the flag is boolean unless Req or
	// ClosedSet says otherwise.
	Value string
	// Repeatable marks flags that may be supplied multiple times; synopsis
	// renderers append an ellipsis to make that contract visible.
	Repeatable bool
	// ClosedSet enumerates the allowed literal values, when the
	// flag is a closed enum (plan, metric, comparison, window-spec,
	// etc.). When non-empty, completion offers these as the flag's
	// value; the leaf's validator accepts ONLY these strings, so
	// mirroring the server-side gate here is a hard contract.
	ClosedSet []string
}

// cliFlagSpellings returns the accepted spellings used in generated help and
// completion. A short-only flag is rendered once with its conventional single
// dash; a long flag with a short alias exposes both forms.
func cliFlagSpellings(f cliFlag) []string {
	if f.ShortName == "" {
		return []string{"--" + f.Name}
	}
	short := "-" + f.ShortName
	if f.Name == f.ShortName {
		return []string{short}
	}
	return []string{"--" + f.Name, short}
}

func cliFlagPrimarySpelling(f cliFlag) string {
	if f.ShortName != "" && f.Name == f.ShortName {
		return "-" + f.ShortName
	}
	return "--" + f.Name
}

func appScaleCLIFlags() []cliFlag {
	return []cliFlag{
		{Name: "environment", Short: "edit desired workload settings in a project environment", Value: "SLUG"},
		{Name: "profile", Short: "named RAM/CPU profile", Value: "PROFILE", ClosedSet: []string{"micro", "small", "medium", "large", "xlarge"}},
		{Name: "ram", Short: "RAM in MB", Value: "MB"},
		{Name: "cpu-millicores", Short: "sustained CPU allowance", Value: "250|500|1000", ClosedSet: []string{"250", "500", "1000"}},
		{Name: "max-concurrency", Short: "maximum concurrent requests", Value: "N"},
		{Name: "concurrency-overflow", Short: "saturated concurrency behavior", Value: "POLICY", ClosedSet: []string{"queue", "drop"}},
		{Name: "max-queue-depth", Short: "maximum queued requests at warm saturation", Value: "N"},
		{Name: "max-queue-wait", Short: "maximum warm-saturation wait", Value: "DURATION"},
		{Name: "max-queue-wait-ms", Short: "maximum queued concurrency wait", Value: "MS"},
		{Name: "wake-max-queue-depth", Short: "per-app cold-wake waiter cap", Value: "N"},
		{Name: "wake-max-queue-wait-seconds", Short: "per-app cold-wake wait budget", Value: "SECONDS"},
		{Name: "idle", Short: "idle timeout", Value: "SECONDS"},
		{Name: "request-timeout", Short: "per-app request timeout", Value: "SECONDS"},
		{Name: "min", Short: "minimum warm instances", Value: "N"},
		{Name: "autoscale-target-rps", Short: "per-instance RPS scale-up target", Value: "N"},
		{Name: "autoscale-target-cpu-pct", Short: "per-instance CPU scale-up target", Value: "1..100"},
		{Name: "warm-snapshot", Short: "enable warm-snapshot tier", Bool: true},
		{Name: "no-warm-snapshot", Short: "disable warm-snapshot tier", Bool: true},
		{Name: "warm-snapshot-min-requests", Short: "warm-snapshot minimum request gate", Value: "N"},
		{Name: "warm-snapshot-min-ms", Short: "warm-snapshot ready-time gate", Value: "MS"},
		{Name: "warm-pool-size", Short: "paused warm-pool size", Value: "N"},
		{Name: "require-authn", Short: "require a bearer token on each request", Bool: true},
		{Name: "no-require-authn", Short: "remove the bearer-token requirement", Bool: true},
		{Name: "head-wakes", Short: "wake a parked app for HEAD requests", Bool: true, ClosedSet: []string{"true", "false"}},
		{Name: "crawler-policy", Short: "monitor/crawler wake policy", Value: "POLICY", ClosedSet: []string{"wake", "cached", "block"}},
		{Name: "health-path", Short: "monitor-facing health path", Value: "PATH"},
		{Name: "health-path-wakes", Short: "allow health probes to wake the app", Bool: true},
		{Name: "no-health-path-wakes", Short: "answer health probes without waking", Bool: true},
		{Name: "app-protocol", Short: "wire-protocol selector", Value: "PROTOCOL", ClosedSet: []string{"http1", "http2", "grpc"}},
	}
}

func projectsCLICompletionPositions() []cliCompletionPosition {
	var positions []cliCompletionPosition
	add := func(path string, position int, role cliCompletionRole, projectPosition int, choices ...string) {
		completionPosition := cliCompletionPosition{
			Path:            strings.Fields(path),
			Position:        position,
			Role:            role,
			ProjectPosition: projectPosition,
			Choices:         choices,
		}
		if len(choices) > 0 {
			completionPosition.Description = "config option or project"
		}
		positions = append(positions, completionPosition)
	}
	project := func(path string) { add(path, 1, cliCompletionProjectSlug, 0) }
	environment := func(path string) { add(path, 2, cliCompletionEnvironmentSlug, 1) }

	for _, path := range []string{"info", "update", "rm"} {
		project(path)
	}

	for _, path := range []string{
		"environments list", "environments create", "environments preflight",
		"environments diff", "environments preview", "environments promote",
		"environments status", "environments rollback",
	} {
		project(path)
	}
	for _, path := range []string{
		"environments protect", "environments unprotect", "environments inspect",
		"environments release-sets", "environments releases", "environments qualify",
		"environments history",
		"environments config set", "environments config apply",
		"environments queues get", "environments queues set",
		"environments routes set", "environments policies set",
		"environments gitops status", "environments gitops bind",
		"environments gitops rebind", "environments gitops unbind",
		"environments gitops review", "environments gitops approve",
		"environments gitops adoption-preview", "environments gitops adopt",
		"environments gitops controls", "environments gitops override",
		"environments gitops remove-override",
	} {
		project(path)
		environment(path)
	}
	add("environments config", 1, cliCompletionProjectSlug, 0, "set", "apply")
	environment("environments config")
	return positions
}

// templateNames13 is the canonical template catalog. The historical name is
// retained because tests and completion metadata refer to this package-local
// symbol; it now contains all 19 embedded templates. Mirrors
// cmd/gregale/templates/embed.go::Names verbatim; the ClosedSet literals
// in deploy/init reference this const so goconst stops flagging the
// duplicated 13-name lists. Kept in sync with the embed FS by the
// TestClosedSetTemplatesMatchEmbedFS pin test in commands_meta_test.go.
var (
	// orgSlugFlag is the --org flag every org-scoped verb requires.
	orgSlugFlag = cliFlag{Name: "org", Short: "organization slug", Value: "SLUG", Req: true}
	// orgMemberRoles are the roles a member can be invited to or given.
	orgMemberRoles = []string{"admin", "developer", "viewer", "billing"}
	// issueAppFlags is the --app flag every issues verb requires.
	issueAppFlags = []cliFlag{{Name: "app", Short: "application slug", Value: "SLUG", Req: true}}
	// platformTenantIDFlag is the --id flag platform-tenants verbs address a customer by.
	platformTenantIDFlag = cliFlag{Name: "id", Short: "platform tenant UUID", Value: "UUID", Req: true}
)

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
	"event-worker",
	"queue-worker",
	"s3-uploader",
	"slack-bot",
	"rest-api-postgres",
	"cron-worker",
	"webhook-receiver",
	"ai-chat",
	"secret-reload-node",
	"customer-platform",
	"mcp-node",
	"data-api",
}

// cliCommands is the manifest. One entry per top-level command in
// main.go's run() switch. Order matches the dispatch table roughly. Audience
// metadata drives the default customer help/completion projection without
// removing compatibility entries from this manifest.
//
// When you add a command to main.go, add it here too. The drift test
// catches the omission; the manifest-drift guard is the load-bearing
// sync mechanism per ADR-083 §Decision 4.
var cliCommands = []cliCommand{
	mcpCLICommand(),
	{
		Name: "start", DocSlug: "deploy", Short: "Get your first app live with a few guided prompts",
		Examples: []string{"gregale start"},
	},
	{
		Name:    "account",
		DocSlug: "account",
		Short:   "Manage the local account (account export|delete|restore|status|dpa|slo)",
		Subcommands: []cliSub{
			{Name: "export", Short: "Export account data (GDPR)", Flags: []cliFlag{
				{Name: "o", ShortName: "o", Short: "output file", Value: "PATH"},
				{Name: "no-secrets", Short: "exclude the sealed-secret ciphertext slice", Bool: true},
			}},
			{Name: "delete", Short: "Schedule account deletion", Flags: []cliFlag{
				{Name: "q", ShortName: "q", Short: "skip the confirmation prompt", Bool: true},
				{Name: "quiet", Short: "confirm account deletion without prompting", Bool: true},
				{Name: "yes", Short: "confirm account deletion without prompting", Bool: true},
			}},
			{Name: "restore", Short: "Cancel a pending deletion"},
			{Name: "status", Short: "Show account status"},
			{Name: "dpa", Short: "Show DPA metadata", Flags: []cliFlag{
				{Name: "o", ShortName: "o", Short: "write DPA metadata to a file", Value: "PATH"},
			}},
			{Name: "slo", Short: "Account-wide SLO panel"},
		},
	},
	{
		Name:    "add",
		DocSlug: "add",
		Short:   "Provision and bind managed resources to an app",
		Subcommands: []cliSub{
			{Name: "postgres", Short: "Provision or attach PostgreSQL and inject DATABASE_URL", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "APP"},
				{Name: "env", Short: "environment scope (defaults to linked project environment)", Value: "SCOPE"},
				{Name: "scope", Short: "environment scope (alias for --env)", Value: "SCOPE"},
				{Name: "database", Short: "existing database ID or name", Value: "REF"},
				{Name: "region", Short: "provider-neutral region when creating", Value: "REGION"},
				{Name: "postgres-major", Short: "PostgreSQL major version", Value: "N"},
				{Name: "class", Short: "service class", Value: "CLASS", ClosedSet: []string{"development", "burstable", "production"}},
				{Name: "availability", Short: "availability mode", Value: "MODE", ClosedSet: []string{"single_zone", "high_availability"}},
				{Name: "scale-to-zero", Short: "suspend compute when idle"},
				{Name: "environment-key", Short: "connection environment variable", Value: "KEY"},
				{Name: "access", Short: "credential access", Value: "MODE", ClosedSet: []string{"read_write", "read_only", "migration", "data_api"}},
				{Name: "wait-timeout", Short: "readiness timeout", Value: "DURATION"},
			}},
			{Name: "bucket", Short: "Provision or attach object storage and inject sealed S3 settings", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "APP"},
				{Name: "env", Short: "environment scope (defaults to linked project environment)", Value: "SCOPE"},
				{Name: "scope", Short: "environment scope (alias for --env)", Value: "SCOPE"},
				{Name: "region", Short: "object-storage region", Value: "REGION"},
				{Name: "public", Short: "serve objects publicly from the app host"},
				{Name: "serve-at", Short: "public mount path", Value: "PATH"},
				{Name: "permission", Short: "compute binding permission", Value: "MODE", ClosedSet: []string{"read", "write", "read_write"}},
				{Name: "label", Short: "bucket-scoped compute credential label", Value: "LABEL"},
				{Name: "prefix", Short: "injected storage secret prefix", Value: "PREFIX"},
				{Name: "wait-timeout", Short: "readiness timeout", Value: "DURATION"},
			}},
		},
	},
	{
		Name: "bucket", DocSlug: "object-storage", Short: "Manage object encryption, Object Lock, copy sources, tags, versioning, lifecycle rules, receipts and capacity",
		Subcommands: []cliSub{
			{Name: "uploads", Short: "Inspect owned multipart upload sessions and parts", Subcommands: []cliSub{
				{Name: "list", Short: "List multipart sessions", Positionals: []string{"<app>", "<bucket-id>"}, Flags: []cliFlag{{Name: "limit", Value: "N", Short: "page size (1..1000)"}, {Name: "cursor", Value: "ID", Short: "next page cursor"}}},
				{Name: "status", Short: "Inspect a multipart session returned by an upload", Positionals: []string{"<app>", "<bucket-id>", "<upload-id>"}},
				{Name: "parts", Short: "List uploaded multipart parts", Positionals: []string{"<app>", "<bucket-id>", "<upload-id>"}, Flags: []cliFlag{{Name: "limit", Value: "N", Short: "page size (1..1000)"}, {Name: "part-number-marker", Value: "N", Short: "last part from the previous page"}}},
			}},
			{Name: "upload", Short: "Upload a file with resumable multipart transfers", Positionals: []string{"<app>", "<bucket-id>", "<key>", "<file>"}, Flags: []cliFlag{{Name: "content-type", Value: "TYPE", Short: "object MIME type"}, {Name: "resume", Value: "UPLOAD-ID", Short: "resume a multipart upload from its local checkpoint"}, {Name: "timeout", Value: "DURATION", Short: "transfer deadline (default 30m)"}}},
			{Name: "download", Short: "Download an object to a file after a complete transfer", Positionals: []string{"<app>", "<bucket-id>", "<key>", "<file>"}, Flags: []cliFlag{{Name: "version-id", Value: "VERSION", Short: "download this owned immutable version"}, {Name: "force", Short: "replace destination after a complete transfer"}, {Name: "timeout", Value: "DURATION", Short: "transfer deadline (default 30m)"}}},
			{Name: "versions", Short: "Browse retained object versions and delete markers", Subcommands: []cliSub{
				{Name: "list", Short: "List a page of owned public versions", Positionals: []string{"<app>", "<bucket-id>"}, Flags: []cliFlag{
					{Name: "prefix", Value: "PREFIX", Short: "key prefix"}, {Name: "delimiter", Value: "DELIMITER", Short: "group matching keys"}, {Name: "limit", Value: "N", Short: "maximum items and prefixes (1-1000)"}, {Name: "key-marker", Value: "KEY", Short: "continuation key from the previous page"}, {Name: "version-id-marker", Value: "VERSION", Short: "public continuation version from the previous page"},
				}},
			}},
			{Name: "copy-sources", Short: "Manage copy-only owned source grants", Subcommands: []cliSub{
				{Name: "list", Short: "List source grants for a destination credential", Positionals: []string{"<app>", "<bucket-id>", "<credential-id>"}},
				{Name: "grant", Short: "Allow copying an owned source bucket or prefix", Positionals: []string{"<app>", "<bucket-id>", "<credential-id>", "<source-bucket-id>", "[prefix]"}},
				{Name: "revoke", Short: "Prevent new copy dispatch from a source", Positionals: []string{"<app>", "<bucket-id>", "<credential-id>", "<source-bucket-id>"}},
			}}, {Name: "encryption-keys", Short: "List owned encryption capabilities and key references", Positionals: []string{"<app>", "<bucket-id>"}},
			{Name: "encryption", Short: "Inspect or configure verified bucket encryption defaults", Subcommands: []cliSub{
				{Name: "status", Short: "Show durable encryption progress", Positionals: []string{"<app>", "<bucket-id>"}},
				{Name: "clear", Short: "Remove the default for new writes", Positionals: []string{"<app>", "<bucket-id>"}},
				{Name: "AES256", Short: "Set provider AES256 encryption", Positionals: []string{"<app>", "<bucket-id>"}},
				{Name: "aws:kms", Short: "Set owned KMS encryption", Positionals: []string{"<app>", "<bucket-id>", "<owned-key-ref>", "[bucket-key-enabled]"}},
				{Name: "aws:kms:dsse", Short: "Set owned dual-layer KMS encryption", Positionals: []string{"<app>", "<bucket-id>", "<owned-key-ref>"}},
			}}, {Name: "object-lock", Short: "Inspect permanent Object Lock and configure retention defaults", Subcommands: []cliSub{
				{Name: "status", Short: "Show durable Object Lock progress", Positionals: []string{"<app>", "<bucket-id>"}},
				{Name: "capabilities", Short: "Show enrolled bucket lock capabilities", Positionals: []string{"<app>", "<bucket-id>"}},
				{Name: "enable", Short: "Permanently enable Object Lock without defaults", Positionals: []string{"<app>", "<bucket-id>"}},
				{Name: "clear-default", Short: "Clear future defaults while keeping Object Lock enabled", Positionals: []string{"<app>", "<bucket-id>"}},
				{Name: "GOVERNANCE", Short: "Set governance defaults", Positionals: []string{"<app>", "<bucket-id>"}, Flags: objectLockCLIFlags()},
				{Name: "COMPLIANCE", Short: "Set compliance defaults", Positionals: []string{"<app>", "<bucket-id>"}, Flags: objectLockCLIFlags()},
			}}, {Name: "protection", Short: "Manage exact version retention and legal holds", Subcommands: []cliSub{
				{Name: "status", Short: "Inspect a durable protection operation", Positionals: []string{"<app>", "<bucket-id>", "<operation-id>"}},
				{Name: "retention", Short: "Read, set or clear fixed retention", Positionals: []string{"<app>", "<bucket-id>", "<key>", "<version-id>", "[clear operation-id | GOVERNANCE|COMPLIANCE retain-until operation-id]"}},
				{Name: "legal-hold", Short: "Read or change an independent legal hold", Positionals: []string{"<app>", "<bucket-id>", "<key>", "<version-id>", "[ON|OFF operation-id]"}},
				{Name: "event-hold", Short: "Set or release event retention for an exact version", Positionals: []string{"<app>", "<bucket-id>", "<key>", "<version-id>", "<GOVERNANCE|COMPLIANCE>", "<ON|OFF>", "[days|years duration]", "[--retain-until timestamp]", "<operation-id>"}},
			}}, {Name: "reconcile", Short: "Start, inspect or cancel a fenced capacity inventory", Subcommands: []cliSub{
				{Name: "start", Short: "Pause writes and request capacity reconciliation", Positionals: []string{"<app>", "<bucket-id>"}},
				{Name: "status", Short: "Show reconciliation progress and reclaimed capacity", Positionals: []string{"<app>", "<bucket-id>", "<job-id>"}},
				{Name: "cancel", Short: "Cancel reconciliation and resume writes", Positionals: []string{"<app>", "<bucket-id>", "<job-id>"}},
			}}, {Name: "writes", Short: "Inspect tracked writes and await recovery", Subcommands: []cliSub{
				{Name: "list", Short: "List pending writes or recent completed and failed receipts", Positionals: []string{"<app>", "<bucket-id>"}, Flags: []cliFlag{
					{Name: "status", Short: "pending (default), completed, failed or all", Value: "STATUS"},
					{Name: "limit", Short: "page size (default 50, maximum 100)", Value: "N"},
					{Name: "cursor", Short: "next page cursor", Value: "TOKEN"},
				}},
				{Name: "status", Short: "Read a tracked write receipt", Positionals: []string{"<app>", "<bucket-id>", "<receipt-id>"}},
				{Name: "wait", Short: "Poll until completed or failed; pending timeout retains the receipt", Positionals: []string{"<app>", "<bucket-id>", "<receipt-id>"}, Flags: []cliFlag{
					{Name: "timeout", Short: "maximum wait (default 5m)", Value: "DURATION"},
					{Name: "poll-interval", Short: "time between reads (default 5s, minimum 1s)", Value: "DURATION"},
				}},
			}}, {Name: "tags", Short: "Read, replace or clear tags on current or selected data", Subcommands: []cliSub{
				{Name: "get", Short: "Read object tags", Positionals: []string{"<app>", "<bucket-id>", "<key>", "[version-id|null]"}},
				{Name: "set", Short: "Replace the complete tag set", Positionals: []string{"<app>", "<bucket-id>", "<key>", "<URL-encoded-tags>", "[version-id|null]"}},
				{Name: "clear", Short: "Remove all object tags", Positionals: []string{"<app>", "<bucket-id>", "<key>", "[version-id|null]"}},
			}}, {Name: "deletions", Short: "Create or inspect durable object deletions", Subcommands: []cliSub{
				{Name: "start", Short: "Delete current data or an owned version with a retry identity", Positionals: []string{"<app>", "<bucket-id>", "<key>", "<request-id>", "[version-id|null]"}},
				{Name: "status", Short: "Show a persisted deletion receipt", Positionals: []string{"<app>", "<bucket-id>", "<request-id>"}},
			}}, {Name: "version-delete", Short: "Permanently delete an owned immutable version or marker", Positionals: []string{"<app>", "<bucket-id>", "<key>", "<version-id>"}},
			{Name: "notifications", Short: "Manage bucket event notifications", Subcommands: []cliSub{
				{Name: "get", Short: "Read notification rules", Positionals: []string{"<app>", "<bucket-id>"}},
				{Name: "set", Short: "Replace rules from a JSON file or stdin", Positionals: []string{"<app>", "<bucket-id>", "<JSON-file|->"}},
				{Name: "clear", Short: "Remove notification rules", Positionals: []string{"<app>", "<bucket-id>"}},
			}}, {Name: "lifecycle", Short: "Manage lifecycle rules and discovery progress", Subcommands: []cliSub{
				{Name: "get", Short: "Read the complete lifecycle policy", Positionals: []string{"<app>", "<bucket-id>"}},
				{Name: "set", Short: "Replace rules from a JSON file or stdin", Positionals: []string{"<app>", "<bucket-id>", "<JSON-file|->"}},
				{Name: "clear", Short: "Remove rules; admitted cleanup continues", Positionals: []string{"<app>", "<bucket-id>"}},
				{Name: "scan", Short: "Start or resume due discovery", Positionals: []string{"<app>", "<bucket-id>"}},
				{Name: "status", Short: "Read discovery progress", Positionals: []string{"<app>", "<bucket-id>", "<scan-id>"}},
			}},
			{Name: "versioning", Short: "Inspect or configure bucket versioning", Subcommands: []cliSub{
				{Name: "status", Short: "Show durable versioning progress", Positionals: []string{"<app>", "<bucket-id>"}},
				{Name: "enable", Short: "Enable retained versions", Positionals: []string{"<app>", "<bucket-id>"}},
				{Name: "suspend", Short: "Suspend versioning while retaining older versions", Positionals: []string{"<app>", "<bucket-id>"}},
			}}},
	},
	{
		Name:        "bindings",
		DocSlug:     "bindings",
		Short:       "Inspect app bindings, verification, runtime freshness, and rotation progress",
		Positionals: []string{"<app>"},
		Flags: []cliFlag{
			{Name: "require-complete", Short: "fail if binding metadata, verification, runtime freshness or refresh progress is incomplete"},
			{Name: "scope", Value: "SCOPE", Short: "filter resource bindings by environment scope; app-wide bindings remain included"},
		},
		Subcommands: []cliSub{
			{Name: "release-policy", Short: "Require fresh binding evidence for traffic increases in a scope", Subcommands: []cliSub{
				{Name: "get", Short: "Read the stored release policy", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "scope", Value: "SCOPE", Short: "deployment scope (default default)"}}},
				{Name: "set", Short: "Replace the release policy using its current revision", Positionals: []string{"<app>"}, Flags: []cliFlag{
					{Name: "scope", Value: "SCOPE", Short: "deployment scope (default default)"},
					{Name: "mode", Value: "off|enforce", Short: "disable or enable enforcement"},
					{Name: "require-verification", Short: "alias for --mode enforce"},
					{Name: "max-age", Value: "DURATION", Short: "maximum verification age (default 10m; 1s to 24h)"},
					{Name: "require-application-ack", Short: "require current application acknowledgements"},
					{Name: "expected-revision", Value: "N", Req: true, Short: "current policy revision; use 0 initially"},
					{Name: "reason", Value: "TEXT", Short: "update reason; required when disabling enforcement"},
				}, Examples: []string{"gregale bindings release-policy set public-api --scope production --require-verification --max-age 10m --expected-revision 0"}},
			}},
			{Name: "probe-policy", Short: "Configure or remove an outbound integration probe", Positionals: []string{"<integration-id>"}, Flags: []cliFlag{{Name: "path", Value: "PATH", Short: "provider path declared safe to probe"}, {Name: "method", Value: "METHOD", Short: "GET or HEAD (default GET)"}, {Name: "expect-status", Value: "STATUS", Short: "expected successful response status (default 200)"}, {Name: "delete", Short: "remove probe configuration"}}, Examples: []string{"gregale bindings probe-policy INTEGRATION_ID --path /health --method GET --expect-status 200"}},
			{
				Name:        "check",
				Short:       "Evaluate recorded binding evidence and runtime freshness for CI",
				Positionals: []string{"<app>"},
				Flags: []cliFlag{
					{Name: "scope", Value: "SCOPE", Short: "require the selected deployment to use this scope (default its current scope)"},
					{Name: "max-verification-age", Value: "DURATION", Short: "maximum age of passed probe evidence (default 10m)"},
					{Name: "deployment", Value: "ID|vN", Short: "exact live deployment whose evidence must pass, including zero-traffic candidates"},
					{Name: "allow-unsupported", Short: "waive connectivity coverage for active queue and outbound bindings"},
					{Name: "require-application-ack", Short: "require current PostgreSQL/object-storage application acknowledgements"},
					{Name: "wait", Short: "poll read-only inventory while probes, refreshes or application acknowledgements are pending"},
					{Name: "timeout", Value: "DURATION", Short: "maximum preflight wait (default 5m)"},
					{Name: "poll-interval", Value: "DURATION", Short: "inventory polling interval with --wait (default 1s)"},
				}, Examples: []string{"gregale bindings check my-api --max-verification-age 10m --json", "gregale bindings check my-api --scope production", "gregale bindings check my-api --deployment v12 --max-verification-age 10m --json"},
			},
			{
				Name:  "object-storage",
				Short: "Manage app-to-bucket compute bindings",
				Subcommands: []cliSub{
					{Name: "list", Short: "List safe binding metadata", Positionals: []string{"<app>", "<bucket>"}, Examples: []string{"gregale bindings object-storage list my-api assets"}},
					{Name: "rotate", Short: "Rotate a binding credential and optionally wait for retirement", Positionals: []string{"<app>", "<bucket>", "<binding-id>"}, Flags: []cliFlag{
						{Name: "wait", Short: "wait for the previous credential to retire"},
						{Name: "wait-timeout", Short: "maximum time to wait for rotation (default 5m)", Value: "DURATION"},
						{Name: "poll-interval", Short: "status polling interval while waiting (default 1s)", Value: "DURATION"},
					}, Examples: []string{"gregale bindings object-storage rotate my-api assets BINDING_ID --wait"}},
					{Name: "revoke", Short: "Revoke a binding credential", Positionals: []string{"<app>", "<bucket>", "<binding-id>"}, Examples: []string{"gregale bindings object-storage revoke my-api assets BINDING_ID"}},
				},
			},
			{
				Name:        "verify",
				Short:       "Check a private service route or test a managed PostgreSQL or object-storage binding",
				Positionals: []string{"<app>", "[<service>]"},
				Flags: []cliFlag{
					{Name: "all", Short: "verify services, managed PostgreSQL, object storage and configured outbound bindings"},
					{Name: "postgres", Short: "verify one managed PostgreSQL binding by environment key", Value: "ENVIRONMENT_KEY"},
					{Name: "outbound", Short: "verify a configured outbound integration by UUID", Value: "INTEGRATION_ID"},
					{Name: "object-storage", Short: "verify one object-storage binding by environment prefix (read access only)", Value: "PREFIX"},
					{Name: "deployment", Value: "ID|vN", Short: "exact live deployment to verify, including zero-traffic candidates"},
					{Name: "poll-interval", Short: "status polling interval while the canary runs", Value: "D"},
					{Name: "wait-timeout", Short: "maximum time to wait for the canary task", Value: "D"},
				}, Examples: []string{"gregale bindings verify my-api billing", "gregale bindings verify my-api --all", "gregale bindings verify my-api --deployment v12 --all", "gregale bindings verify my-api --postgres DATABASE_URL", "gregale bindings verify my-api --object-storage GREGALE_S3_ASSETS"},
			},
			{
				Name:        "smoke",
				Short:       "Invoke a path on one exact live target deployment over the private HTTPS binding",
				Positionals: []string{"<app>", "<service>"},
				Flags: []cliFlag{
					{Name: "target-deployment", Short: "exact live target deployment UUID to invoke (or use --deployment)", Value: "ID"},
					{Name: "deployment", Short: "alias for --target-deployment", Value: "ID"},
					{Name: "caller-deployment", Short: "exact live caller deployment, including zero-traffic candidates", Value: "ID|vN"},
					{Name: "path", Short: "absolute path on the target service", Req: true, Value: "PATH"},
					{Name: "expect-status", Short: "require this exact HTTP status; default accepts any 2xx response", Value: "CODE"},
					{Name: "poll-interval", Short: "status polling interval while the smoke task runs", Value: "D"},
					{Name: "wait-timeout", Short: "maximum time to wait for the smoke task", Value: "D"},
				}, Examples: []string{"gregale bindings smoke public-api billing --caller-deployment v12 --target-deployment TARGET_UUID --path /ready --expect-status 200"},
			},
		},
	},
	{
		Name:    "capabilities",
		DocSlug: "capabilities",
		Short:   "Show feature maturity and plan availability",
	},
	{
		Name:     "admin",
		DocSlug:  "admin",
		Short:    "Operator-only ops (admin credit|refund|consume-credits|abuse-hold|egress-flows)",
		Audience: cliAudienceOperator,
		Subcommands: []cliSub{
			{Name: "credit", Short: "Issue a billing credit", Flags: []cliFlag{
				{Name: "reason", Short: "credit reason text", Req: true, Value: "text"},
			}},
			{Name: "refund", Short: "Refund a paid Polar invoice", Flags: []cliFlag{
				{Name: "reason", Short: "refund reason text", Req: true, Value: "text"},
				{Name: "idempotency-key", Short: "stable provider retry key", Value: "key"},
			}},
			{Name: "consume-credits", Short: "Consume credits against an invoice", Positionals: []string{"<invoice-id>"}, Flags: []cliFlag{{Name: "idempotency-key", Short: "stable retry key", Value: "K"}}},
			{Name: "abuse-hold", Short: "Place or release an account abuse hold (place|release)", Flags: []cliFlag{
				{Name: "note", Short: "audit note", Req: true, Value: "text"},
			}},
			{Name: "egress-flows", Short: "Search the egress flow log (which tenant connected where)", Flags: []cliFlag{
				{Name: "remote", Short: "remote IP address or CIDR", Value: "ip|cidr"},
				{Name: "account", Short: "account id", Value: "id"},
				{Name: "from", Short: "window start (RFC 3339)", Value: "time"},
				{Name: "to", Short: "window end (RFC 3339)", Value: "time"},
				{Name: "limit", Short: "maximum rows", Value: "n"},
			}},
		},
		Positionals: []string{"<uuid>", "<cents>"},
	},
	{
		Name:    "alerts",
		DocSlug: "alerts",
		Short:   "Per-app alert rules (alerts list|add|info|update|rm|rotate-secret|preset|actions --app <slug>)",
		Subcommands: []cliSub{
			{Name: "actions", Short: "Read or wait for automatic rollback status, deployment evidence and service handoffs", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "fire", Short: "one production alert delivery UUID", Value: "UUID"},
				{Name: "wait", Short: "wait for the selected fire to complete"},
				{Name: "timeout", Short: "wait deadline (default 10m)", Value: "duration"},
				{Name: "poll-interval", Short: "poll interval (default 2s)", Value: "duration"},
			}},
			{Name: "list", Short: "List alert rules", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			}},
			{Name: "add", Short: "Add an alert rule", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "name", Short: "rule name (3..120 chars)", Req: true, Value: "NAME"},
				{Name: "metric", Short: "metric, e.g. error_rate_pct or latency_p95_ms", Value: "METRIC"},
				{Name: "comparison", Short: "gt|gte|lt|lte", Value: "OP"},
				{Name: "threshold", Short: "threshold value", Value: "N"},
				{Name: "window-spec", Short: "5m|15m|1h|6h|24h|7d|15d", Value: "WINDOW"},
				{Name: "event-subscription-id", Short: "Subscription UUID for event consumer metrics", Value: "UUID"},
				{Name: "failure-source", Short: "any|cron|queue|delayed_task|async_invoke|inbound_webhook", Value: "SOURCE"},
				{Name: "webhook-url", Short: "https webhook URL", Req: true, Value: "URL"},
				{Name: flagNameAction, Short: "alert action", Value: "ACTION", ClosedSet: api.AllowedAlertRuleActions},
				{Name: "post-deploy-rollback-window", Short: "completed-release rollback window (0 off; up to 1h)", Value: "duration"},
				{Name: "webhook-secret-stdin", Short: "read the webhook signing secret from stdin (this or --webhook-secret is required)"},
				{Name: "webhook-secret", Short: "webhook signing secret (prefer --webhook-secret-stdin)", Value: "VALUE"},
			}, Examples: []string{`printf '%s\n' "$WEBHOOK_SECRET" | gregale alerts add --app my-api --name p95-latency --metric latency_p95_ms --comparison gt --threshold 800 --window-spec 15m --webhook-url https://hooks.example.com/gregale --webhook-secret-stdin`}},
			{Name: "info", Short: "Show one alert rule and its last delivery", Positionals: []string{"<alert-id>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			}},
			{Name: "deliveries", Short: "List a rule's webhook deliveries, newest first", Positionals: []string{"<alert-id>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "limit", Short: "max deliveries (1..100, default 20)", Value: "N"},
				{Name: "include-test", Short: "include test deliveries"},
			}},
			{Name: "update", Short: "Update one alert rule", Positionals: []string{"<alert-id>"}, Flags: []cliFlag{
				{Name: flagNameAction, Short: "alert action", Value: "ACTION", ClosedSet: api.AllowedAlertRuleActions},
				{Name: "post-deploy-rollback-window", Short: "completed-release rollback window (0 off; up to 1h)", Value: "duration"},
				{Name: "webhook-secret-stdin", Short: "read the replacement webhook secret from stdin"},
			}},
			{Name: "rm", Short: "Delete one alert rule", Positionals: []string{"<alert-id>"}},
			{Name: "rotate-secret", Short: "Rotate the alert's webhook secret", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "from-stdin", Short: "read the replacement secret from stdin"},
			}},
			// Issue #1233 / ADR-123 — alert preset catalog +
			// instantiate-from-preset. Two leaves under preset:
			// list (no flags), enable <name> --app <slug>
			// --webhook-url <url> --webhook-secret <s>.
			alertPresetCLISubcommand(),
		},
		Flags: []cliFlag{{Name: "app", Short: "app slug", Value: "slug"}},
	},
	{
		Name:    "audit-events",
		DocSlug: "audit-events",
		Short:   "Audit-log query (audit-events list|get <id>)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List audit events", Flags: []cliFlag{
				{Name: "kind-prefix", Short: "filter by kind prefix", Value: "PREFIX"},
				{Name: "app-id", Short: "filter by app UUID", Value: "UUID"},
				{Name: "since", Short: "RFC3339 lower bound", Value: "RFC3339"},
				{Name: "limit", Short: "maximum rows (1..100; default 50)", Value: "N"},
				{Name: "include-anonymous", Short: "include rows without a subject"},
				{Name: "verbose", Short: "expand stateless advisory rows"},
			}},
			{Name: "get", Short: "Show one audit event", Positionals: []string{"<id>"}},
		},
		Positionals: []string{"[<id>]"},
	},
	{
		Name: "commit", DocSlug: "commit", Short: "Manage transactional PostgreSQL outbox sources (internal)",
		Subcommands: []cliSub{
			{Name: "add", Short: "Register a fixed app destination", Flags: []cliFlag{{Name: "name", Short: "account source name", Req: true, Value: "NAME"}, {Name: "operation-policy", Short: "managed Operations queue policy", Req: true, Value: "NAME"}, {Name: "contract-version", Short: "immutable source contract version (default 1)", Value: "VERSION", ClosedSet: []string{"1", "2"}}, {Name: "allow-tenant-selection", Short: "grant version 2 account-owner customer selection"}}},
			{Name: "connection", Short: "Seal database credentials from a file", Flags: []cliFlag{{Name: "file", Short: "connection URL file", Req: true, Value: "PATH"}}},
			{Name: "pause", Short: "Pause new acceptance"},
			{Name: "resume", Short: "Resume new acceptance"},
			{Name: "info", Short: "Inspect source health and pending/blocked work"},
			{Name: "doctor", Short: "Read-only source diagnostics and optional local database checks", Flags: []cliFlag{{Name: "file", Short: "local TLS PostgreSQL credential file; never uploaded", Value: "PATH"}}},
			{Name: "inspect", Short: "Inspect event acceptance, retained execution and blocked observations"},
			{Name: "wait", Short: "Wait without cancelling durable work on timeout", Flags: []cliFlag{{Name: "until", Short: "accepted or completed", Value: "STATE"}, {Name: "timeout", Short: "maximum wait (default 2m)", Value: "D"}, {Name: "interval", Short: "poll interval (default 1s)", Value: "D"}}},
			{Name: "blocked", Short: "Inspect the bounded blocked-event snapshot"},
			{Name: "replay", Short: "Request durable replay after correcting an unaccepted event"},
			{Name: "receipt", Short: "Recover a source event acceptance receipt"},
			{Name: "operation", Short: "Inspect durable execution status"},
		},
	},
	{
		Name:    "events",
		DocSlug: "events",
		Short:   "Preview routing, publish events, inspect deliveries, and backfill retained events",
		Subcommands: []cliSub{
			{Name: "preview", Short: "Preview account-wide event routing without publishing", Flags: []cliFlag{
				{Name: "id", Short: "event id to use when filters inspect the CloudEvents id", Value: "ID"},
				{Name: "source", Short: "event source (or first positional argument)", Value: "SOURCE"},
				{Name: "type", Short: "event type (or second positional argument)", Value: "TYPE"},
				{Name: "data", Short: "JSON event data (inline | @file | -)", Req: true, Value: "J|@file|-"},
				{Name: "time", Short: "event time (RFC3339; defaults to server time)", Value: "RFC3339"},
			}},
			{Name: "replay-preview", Short: "Preview retained events for one current subscription without creating deliveries", Positionals: []string{"<app>"}, Flags: []cliFlag{
				{Name: "subscription-id", Short: "target ordinary application subscription UUID", Req: true, Value: "UUID"},
				{Name: "from", Short: "inclusive acceptance timestamp", Req: true, Value: "RFC3339"},
				{Name: "until", Short: "exclusive acceptance timestamp", Req: true, Value: "RFC3339"},
				{Name: "after", Short: "opaque continuation cursor; keep target and range unchanged", Value: "CURSOR"},
				{Name: "limit", Short: "envelopes examined per page (1..100; default 50)", Value: "N"},
			}},
			{Name: "workflow-replay-preview", Short: "Preview captured workflow event recipients and admission status without starting runs", Positionals: []string{"<app>"}, Flags: []cliFlag{
				{Name: "workflow-name", Short: "workflow name from captured event recipient snapshots", Req: true, Value: "NAME"},
				{Name: "from", Short: "inclusive acceptance timestamp", Req: true, Value: "RFC3339"},
				{Name: "until", Short: "exclusive acceptance timestamp", Req: true, Value: "RFC3339"},
				{Name: "after", Short: "opaque continuation cursor; keep app, workflow and range unchanged", Value: "CURSOR"},
				{Name: "limit", Short: "envelopes examined per page (1..100; default 50)", Value: "N"},
			}},
			{Name: "backfill", Short: "Create a durable bounded delivery job for matching retained events", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "allow-expired", Bool: true, Short: "explicitly bypass captured delivery age"},
				{Name: "subscription-id", Short: "target ordinary application subscription UUID", Req: true, Value: "UUID"},
				{Name: "from", Short: "inclusive platform acceptance timestamp", Req: true, Value: "RFC3339"},
				{Name: "until", Short: "exclusive platform acceptance timestamp", Req: true, Value: "RFC3339"},
				{Name: "yes", Short: "confirm that matching historical events may invoke this consumer", Req: true, Bool: true},
			}},
			{Name: "backfill-status", Short: "Read durable event backfill progress", Positionals: []string{"<job-id>"}},
			{Name: "backfill-items", Short: "Inspect paginated per-envelope outcomes for a backfill job", Positionals: []string{"<job-id>"}, Flags: []cliFlag{
				{Name: "state", Short: "filter by a routing outcome state", Value: "STATE"},
				{Name: "limit", Short: "items per page (1..100; default 50)", Value: "N"},
				{Name: "after", Short: "opaque continuation cursor; keep job and state unchanged", Value: "CURSOR"},
			}},
			{Name: "backfill-retry", Short: "Retry a bounded batch of failed backfill deliveries", Positionals: []string{"<job-id>"}, Flags: []cliFlag{
				{Name: "limit", Short: "failed routing recipients to requeue (1..100; default 100)", Value: "N"},
				{Name: "yes", Short: "confirm requeueing failed event deliveries", Req: true, Bool: true},
			}},
			{Name: "publish", Short: "Publish one event (SOURCE TYPE can be positional; ID is generated by default)", Flags: []cliFlag{
				{Name: "id", Short: "stable event id (generated when omitted)", Value: "ID"},
				{Name: "source", Short: "event source (or first positional argument)", Value: "SOURCE"},
				{Name: "type", Short: "event type (or second positional argument)", Value: "TYPE"},
				{Name: "data", Short: "JSON event data (inline | @file | -)", Req: true, Value: "J|@file|-"},
				{Name: "time", Short: "event time (RFC3339; defaults to server time)", Value: "RFC3339"},
			}},

			{Name: "subscription-pause", Short: "Pause new routing admissions for one event consumer", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "yes", Short: "confirm pausing subscription delivery", Req: true, Bool: true}}},
			{Name: "subscription-resume", Short: "Resume one consumer with paced backlog draining", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "rate", Short: "admissions per second (0..100; default 10; 0 removes pacing)", Value: "N"}, {Name: "yes", Short: "confirm resuming subscription delivery", Req: true, Bool: true}}},
			{Name: "schema-rollout-preview", Short: "Preview schema version coverage and validate payload samples", Positionals: []string{"<source>", "<type>"}, Flags: []cliFlag{{Name: "version", Value: "VERSION", Short: "proposed or registered version"}, {Name: "schema", Value: "JSON|@FILE|-", Short: "proposed schema; omitted uses registry"}, {Name: "samples", Value: "JSON|@FILE|-", Short: "array of event data samples"}, {Name: "from", Value: "RFC3339", Short: "retained acceptance start"}, {Name: "until", Value: "RFC3339", Short: "exclusive retained acceptance end"}, {Name: "retained-limit", Value: "N", Short: "1..100 matching payloads; default 100"}}},
			{Name: "subscription-versions-status", Short: "Inspect schema version selection", Positionals: []string{"<app>", "<subscription-id>"}},
			{Name: "subscription-versions-set", Short: "Select schema versions for future events", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "versions", Value: "v1,v2", Short: "up to 16 unique version identifiers"}, {Name: "yes", Short: "confirm selection"}}},
			{Name: "subscription-versions-reset", Short: "Accept all schema versions for future events", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "yes", Short: "confirm reset"}}},
			{Name: "subscription-retry-status", Short: "Inspect retry policy for future event routing", Positionals: []string{"<app>", "<subscription-id>"}},
			{Name: "subscription-retry-reset", Short: "Restore legacy retry defaults for future events", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "yes", Short: "confirm retry policy reset"}}},
			{Name: "subscription-circuit-status", Short: "Show consumer circuit breaker state", Positionals: []string{"<app>", "<subscription-id>"}},
			{Name: "subscription-circuit-set", Short: "Enable automatic consumer routing circuit breaker", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "failure-threshold-pct", Value: "N", Short: "failure percentage (default 50)"}, {Name: "min-samples", Value: "N", Short: "minimum outcomes (default 20)"}, {Name: "window-seconds", Value: "N", Short: "observation window (default 300)"}, {Name: "cooldown-seconds", Value: "N", Short: "cooldown (default 60)"}, {Name: "probe-successes", Value: "N", Short: "required successes (default 3)"}, {Name: "recovery-max-rate", Value: "N", Short: "maximum routes per second (default 10)"}, {Name: "recovery-seconds", Value: "N", Short: "recovery duration (default 60)"}, {Name: "yes", Short: "confirm change", Bool: true, Req: true}}},
			{Name: "subscription-circuit-disable", Short: "Disable automatic consumer routing circuit breaker", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "yes", Short: "confirm change", Bool: true, Req: true}}},
			{Name: "subscription-circuit-reset", Short: "Close enabled circuit breaker and reset observation window", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "yes", Short: "confirm change", Bool: true, Req: true}}},
			{Name: "subscription-retry-set", Short: "Configure bounded routing retries for future events", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "max-delivery-age", Value: "DURATION", Short: "wall-clock age from acceptance (up to 720h; 0 disables expiry)"}, {Name: "max-attempts", Value: "N", Short: "1..100"}, {Name: "initial-backoff", Value: "DURATION", Short: "initial delay"}, {Name: "max-backoff", Value: "DURATION", Short: "maximum delay (up to 1h)"}, {Name: "max-retry-duration", Value: "DURATION", Short: "attempt-time plus scheduled-delay budget (up to 7d; 0 disables)"}, {Name: "jitter", Short: "spread retries (default true)"}, {Name: "yes", Short: "confirm retry policy change"}}},
			{Name: "subscription-execution-health", Short: "Inspect retained consumer executions and handler outcomes", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "window", Value: "5m|15m|1h|6h|24h", Short: "attempt and completion window (default 5m)"}}},
			{Name: "subscription-health", Short: "Inspect consumer backlog, routing rates, latency, and pause duration", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "window", Short: "5m|15m|1h|6h|24h", Value: "WINDOW"}}},
			{Name: "subscription-status", Short: "Inspect delivery pause, pacing, and oldest waiting event", Positionals: []string{"<app>", "<subscription-id>"}},
			{Name: "recovery-preview", Short: "Preview a bounded selection of failed event consumers", Positionals: []string{"<app>"}, Flags: []cliFlag{
				{Name: "mode", Short: "routing (default) or execution recovery", Value: "MODE"},
				{Name: "outcome", Short: "execution outcome: failed or dead_letter", Value: "OUTCOME"},
				{Name: "subscription-id", Short: "filter by captured consumer identifier", Value: "ID"},
				{Name: "event-source", Short: "filter by exact event source", Value: "SOURCE"},
				{Name: "event-type", Short: "filter by exact event type", Value: "TYPE"},
				{Name: "failure-code", Short: "filter by failure classification", Value: "CODE"},
				{Name: "min-age", Short: "minimum failure age in whole seconds", Value: "DURATION"},
				{Name: "include-non-retryable", Short: "include failures classified as non-retryable", Bool: true},
				{Name: "rate", Short: "maximum retries per second (1..100; default 10)", Value: "N"},
			}},
			{Name: "recovery-create", Short: "Create a durable bulk recovery job", Positionals: []string{"<app>"}, Flags: []cliFlag{
				{Name: "mode", Short: "routing (default) or execution recovery", Value: "MODE"},
				{Name: "outcome", Short: "execution outcome: failed or dead_letter", Value: "OUTCOME"},
				{Name: "subscription-id", Short: "filter by captured consumer identifier", Value: "ID"},
				{Name: "event-source", Short: "filter by exact event source", Value: "SOURCE"},
				{Name: "event-type", Short: "filter by exact event type", Value: "TYPE"},
				{Name: "failure-code", Short: "filter by failure classification", Value: "CODE"},
				{Name: "min-age", Short: "minimum failure age in whole seconds", Value: "DURATION"},
				{Name: "include-non-retryable", Short: "include failures classified as non-retryable", Bool: true},
				{Name: "rate", Short: "maximum retries per second (1..100; default 10)", Value: "N"},
				{Name: "reason", Short: "optional operator reason (at most 512 bytes)", Value: "TEXT"}, {Name: "yes", Short: "confirm creating a recovery job", Bool: true, Req: true}}},
			{Name: "recovery-preflight", Short: "Assess frozen recovery eligibility and optimistic timing", Positionals: []string{"<job-id>"}},
			{Name: "recovery-health", Short: "Inspect active recovery progress and expiry risk", Positionals: []string{"<app>"}},
			{Name: "recovery-history", Short: "Inspect recovery control audit history", Positionals: []string{"<job-id>"}, Flags: []cliFlag{{Name: "after", Short: "last entry ID from the previous page", Value: "ID"}, {Name: "limit", Short: "entries per page (1..100; default 100)", Value: "N"}}},
			{Name: "recovery-list", Short: "Discover retained recovery jobs", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "state", Short: "filter by recovery state", Value: "STATE"}, {Name: "mode", Short: "routing or execution", Value: "MODE"}, {Name: "subscription-id", Short: "selected or captured subscription", Value: "ID"}, {Name: "created-after", Short: "exclusive creation lower bound (RFC3339)", Value: "TIME"}, {Name: "created-before", Short: "exclusive creation upper bound (RFC3339)", Value: "TIME"}, {Name: "cursor", Short: "next cursor from the previous page", Value: "CURSOR"}, {Name: "limit", Short: "jobs per page (1..50; default 50)", Value: "N"}}},
			{Name: "recovery-status", Short: "Inspect recovery admission progress and execution outcomes", Positionals: []string{"<job-id>"}},
			{Name: "recovery-items", Short: "Inspect recovery items, replay identities, and execution outcomes", Positionals: []string{"<job-id>"}, Flags: []cliFlag{{Name: "after", Short: "last item position from the previous page", Value: "POSITION"}, {Name: "limit", Short: "items per page (1..100; default 100)", Value: "N"}}},
			{Name: "recovery-pause", Short: "Pause further recovery admissions", Positionals: []string{"<job-id>"}, Flags: []cliFlag{{Name: "reason", Short: "optional operator reason (at most 512 bytes)", Value: "TEXT"}, {Name: "yes", Short: "confirm pause", Bool: true, Req: true}}},
			{Name: "recovery-resume", Short: "Resume the same frozen recovery selection", Positionals: []string{"<job-id>"}, Flags: []cliFlag{{Name: "reason", Short: "optional operator reason (at most 512 bytes)", Value: "TEXT"}, {Name: "yes", Short: "confirm resume", Bool: true, Req: true}}},
			{Name: "recovery-rate", Short: "Change recovery admission rate without resetting its budget", Positionals: []string{"<job-id>"}, Flags: []cliFlag{{Name: "rate", Short: "items per second (1..100)", Value: "N", Req: true}, {Name: "reason", Short: "optional operator reason (at most 512 bytes)", Value: "TEXT"}, {Name: "yes", Short: "confirm rate change", Bool: true, Req: true}}},
			{Name: "recovery-cancel", Short: "Cancel remaining bulk recovery retries", Positionals: []string{"<job-id>"}, Flags: []cliFlag{{Name: "reason", Short: "optional operator reason (at most 512 bytes)", Value: "TEXT"}, {Name: "yes", Short: "confirm cancelling remaining retries", Bool: true, Req: true}}},
			{Name: "backlog", Short: "Discover waiting event consumers and recipient counts", Flags: []cliFlag{
				{Name: "app", Short: "filter by owned app slug", Value: "APP"},
				{Name: "subscription-id", Short: "filter by captured or backfilled recipient identifier", Value: "ID"},
				{Name: "consumer-kind", Short: "application or workflow", Value: "KIND"},
				{Name: "origin", Short: "acceptance or backfill", Value: "ORIGIN"},
				{Name: "state", Short: "pending or processing", Value: "STATE"},
				{Name: "waiting-reason", Short: "filter by waiting reason, e.g. ordering_blocked", Value: "REASON"},
				{Name: "capacity-scope", Short: "consumer, app or account", Value: "SCOPE"},
				{Name: "min-age", Short: "minimum acceptance age in whole seconds (e.g. 10m)", Value: "DURATION"},
				{Name: "after", Short: "opaque recipient continuation cursor", Value: "CURSOR"},
				{Name: "consumers-after", Short: "opaque consumer continuation cursor", Value: "CURSOR"},
				{Name: "limit", Short: "recipients per page (1..200, default 100)", Value: "N"},
				{Name: "consumer-limit", Short: "consumers per page (1..200, default 100)", Value: "N"},
			}},
			{Name: "inspect", Short: "Inspect event routing, execution and replay recovery", Flags: []cliFlag{
				{Name: "source", Short: "published event source", Req: true, Value: "SOURCE"},
				{Name: "id", Short: "published event id", Req: true, Value: "ID"},
				{Name: "subscription", Short: "list retained handler replays for one captured recipient", Value: "SUB"},
				{Name: "after", Short: "opaque next_after cursor for recipients or replays", Value: "CURSOR"},
				{Name: "limit", Short: "max recipients or replays (1..200, default 100)", Value: "N"},
			}},
			{Name: "recover", Short: "Recover one event consumer using its current receipt action", Flags: []cliFlag{
				{Name: "source", Short: "published event source", Req: true, Value: "SOURCE"},
				{Name: "id", Short: "published event id", Req: true, Value: "ID"},
				{Name: "subscription", Short: "captured recipient identifier", Req: true, Value: "SUB"},
				{Name: "dry-run", Short: "show recovery availability and action without replaying", Bool: true},
			}},
			{Name: "attempts", Short: "Inspect retained handler attempts, including retries and replay", Flags: []cliFlag{
				{Name: "source", Short: "published event source", Req: true, Value: "SOURCE"},
				{Name: "id", Short: "published event id", Req: true, Value: "ID"},
				{Name: "subscription", Short: "captured recipient identifier", Req: true, Value: "SUB"},
				{Name: "after", Short: "opaque next_after attempt cursor", Value: "CURSOR"},
				{Name: "limit", Short: "max attempts (1..200, default 100)", Value: "N"},
			}},
			{Name: "subscriptions", Short: "List manifest subscriptions and keyed ordering status", Positionals: []string{"<app>"}},
			{Name: "deliveries", Short: "Inspect event deliveries, replays, and pre-invocation fanout failures", Positionals: []string{"<app>"}, Flags: []cliFlag{
				{Name: "event-source", Short: "narrow event filter to one published source; requires --event-id", Value: "SOURCE"},
				{Name: "event-id", Short: "filter by published event id", Value: "ID"},
				{Name: "state", Short: "filter by delivery state; failed includes recipient fanout failures", Value: "STATE"},
				{Name: "before", Short: "pagination cursor", Value: "CURSOR"},
				{Name: "fanout-before", Short: "pre-invocation failure pagination cursor", Value: "CURSOR"},
				{Name: "limit", Short: "page size per stream (1..200, default 20)", Value: "N"},
				{Name: "all", Short: "walk both streams with independent cursors"},
			}},
			{Name: "fanout-history", Short: "Inspect immutable routing outcomes and replay history for one event", Positionals: []string{"<app>"}, Flags: []cliFlag{
				{Name: "event-source", Short: "published event source", Req: true, Value: "SOURCE"},
				{Name: "event-id", Short: "published event id", Req: true, Value: "ID"},
				{Name: "subscription-id", Short: "narrow history to one recipient", Value: "ID"},
				{Name: "before", Short: "alias for --cursor", Value: "CURSOR"},
				{Name: "cursor", Short: "opaque continuation cursor", Value: "CURSOR"},
				{Name: "all", Short: "walk every page using --limit and --cursor"},
				{Name: "limit", Short: "max history rows (1..200)", Value: "N"},
			}},
			{Name: "replay", Short: "Retry one terminal pre-invocation recipient failure using its event identity and subscription ID from events deliveries", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "allow-expired", Bool: true, Short: "explicitly bypass captured delivery age"},
				{Name: "event-id", Short: "published event id", Req: true, Value: "ID"},
				{Name: "event-source", Short: "published event source", Req: true, Value: "SOURCE"},
				{Name: "subscription-id", Short: "failed subscription id", Req: true, Value: "ID"},
			}},
			{Name: "replay-retryable", Short: "Retry a bounded batch of terminal failures classified as retryable; pass --event-source and --event-id together to filter", Positionals: []string{"<app>"}, Flags: []cliFlag{
				{Name: "event-source", Short: "limit replay to one published event source", Value: "SOURCE"},
				{Name: "event-id", Short: "limit replay to one published event", Value: "ID"},
				{Name: "limit", Short: "max recipients to requeue (1..100)", Value: "N"},
				{Name: "yes", Short: "confirm requeueing retryable event recipients", Req: true, Bool: true},
			}},
		},
	},
	{
		Name:        "send",
		DocSlug:     "send",
		Short:       "Reliably send work to another Gregale application",
		Positionals: []string{"<target-app>"},
		Flags: []cliFlag{
			{Name: "type", Short: "event type", Req: true, Value: "TYPE"},
			{Name: "data", Short: "JSON event data (inline | @file | -)", Req: true, Value: "J|@file|-"},
			{Name: "id", Short: "stable event id", Value: "ID"},
			{Name: "source", Short: "event source", Value: "SOURCE"},
			{Name: "time", Short: "event time", Value: "RFC3339"},
			{Name: "queue-name", Short: "target logical queue name", Value: "QUEUE"},
			{Name: "environment", Short: "registered project environment with an enabled queue binding", Value: "ENV"},
			{Name: "work-policy", Short: "named work policy for an unnamed queue", Value: "NAME"},
			{Name: "work-key", Short: "JSON scalar identifying related work", Value: "JSON"},
			{Name: "work-fairness-key", Short: "JSON scalar shared by related work keys", Value: "JSON"},
			{Name: "idempotency-key", Short: "stable key for retrying an uncertain send", Value: "KEY"},
		},
	},
	{
		Name:        "deliver",
		DocSlug:     "deliver",
		Short:       "Reliably deliver an event to a registered webhook",
		Positionals: []string{"<source-app>", "<webhook-id|url>"},
		Flags: []cliFlag{
			{Name: "type", Short: "event type", Req: true, Value: "TYPE"},
			{Name: "data", Short: "JSON event data (inline | @file | -)", Req: true, Value: "J|@file|-"},
			{Name: "idempotency-key", Short: "stable key for retrying an uncertain delivery", Value: "KEY"},
		},
	},
	{
		Name:     dispatchApps,
		DocSlug:  "apps",
		Short:    "List your apps",
		Examples: []string{"gregale apps", "gregale apps --json", "gregale apps -q my-api"},
		Subcommands: []cliSub{
			{Name: "ls", Short: "Alias for the default list action"},
			{Name: "restore", Short: "Restore an app during its deletion grace window", Positionals: []string{"<slug>"}},
			{Name: "routes", Short: "List admitted per-route labels for one app", Positionals: []string{"<slug>"}},
			appTCPListenerCLISubcommand(true),
			appUDPListenerCLISubcommand(true),
			{Name: "streaming-cap", Short: "Show app streaming classification", Positionals: []string{"<slug>"}},
		},
		Flags: []cliFlag{{Name: "dry-run", Short: "preview app deletion without changing resources", Bool: true}, {Name: "quiet", ShortName: "q", Short: "delete one app without prompting"}, {Name: "yes", Short: "confirm app deletion without prompting", Bool: true}},
	},
	{
		Name:    appSlugFallback,
		DocSlug: "apps",
		Short:   "Get/update one app or run a deployment-attached command",
		Examples: []string{
			"gregale app my-api --maintenance",
			"gregale app my-api --environment staging --ram 512",
			"gregale app my-api --no-maintenance --streaming-enabled --websocket-enabled --route-metrics",
			"gregale app my-api --consumer-auth-mode required --json",
		},
		SubcommandsAfterPositionals: true,
		Subcommands: []cliSub{
			{Name: subHealth, Short: "Explain default-scope serving health and missing evidence"},
			{Name: "scale", Short: "Preview, save, apply or update app resource and runtime settings", Examples: []string{
				"gregale app my-api scale --plan --ram 512 --out scale-change.json",
				"gregale app my-api scale --apply scale-change.json --confirm",
			}, Flags: append([]cliFlag{
				{Name: "plan", Short: "show changes and supported plan effects without applying them"},
				{Name: "out", Short: "write a reusable plan JSON to a new file (requires --plan)", Value: "PATH"},
				{Name: "apply", Short: "apply a saved scale plan JSON file", Value: "PATH"},
				{Name: "confirm", Short: "confirm applying the saved plan (requires --apply)"},
			}, appScaleCLIFlags()...)},
			{Name: "costs", Short: "Show this app's attributed usage costs and source coverage", Flags: []cliFlag{
				{Name: "month", Short: "UTC usage month (defaults to current)", Value: "YYYY-MM"},
				{Name: "json", Short: "Print the machine-readable app cost report"},
			}, Examples: []string{"gregale app my-api costs", "gregale app my-api costs --month 2026-10 --json"}},
			{Name: "rename", Short: "Rename an app"},
			{Name: "restart", Short: "Request a snapshot restart, or track a fresh runtime-configuration restart", Flags: []cliFlag{
				{Name: "fresh", Short: "cold-boot replacements with current runtime configuration"},
				{Name: "wait", Short: "wait for processing; requires --fresh"},
				{Name: "timeout", Short: "client deadline (default 10m)", Value: "DURATION"},
				{Name: "poll-interval", Short: "status polling interval (default 2s)", Value: "DURATION"},
				{Name: "json", Short: "print the accepted ID or last observed restart receipt"},
			}, Subcommands: []cliSub{{Name: "status", Short: "Follow an accepted fresh restart without submitting another request", Flags: []cliFlag{
				{Name: "wake-id", Short: "accepted fresh restart UUID", Value: "UUID", Req: true},
				{Name: "wait", Short: "wait for processing completion, separately from application health"},
				{Name: "timeout", Short: "client deadline (default 10m)", Value: "DURATION"},
				{Name: "poll-interval", Short: "status polling interval (default 2s)", Value: "DURATION"},
				{Name: "json", Short: "print the last observed restart receipt"},
			}}}},
			{Name: subExec, Short: "Run a one-off command against the live deployment", Flags: []cliFlag{
				{Name: "shell", Short: "interpret one command string through the app shell"},
				{Name: "detach", Short: "return after the task is queued"},
				{Name: "timeout-seconds", Short: "server-side command timeout", Value: "N"},
				{Name: "max-output-bytes", Short: "combined stdout/stderr tail cap", Value: "N"},
				{Name: "operation-policy", Short: "route through a managed exclusive-operation policy", Value: "NAME"},
				{Name: "operation-key", Short: "JSON scalar business coordination key", Value: "JSON"},
				{Name: "equivalence-key", Short: "equivalent request identity for join_existing policies", Value: "KEY"},
				{Name: "idempotency-key", Short: "stable retry identity for this submission", Value: "KEY"},
				{Name: "poll-interval", Short: "status polling interval while attached", Value: "D"},
				{Name: "wait-timeout", Short: "maximum attached wait", Value: "D"},
			}},
			{Name: "security", Short: "Show posture or configure deploy enforcement", Flags: []cliFlag{
				{Name: "posture", Short: "show the read-only security posture"},
				{Name: "require-signed", Short: "require signed images on deploy", Value: "true|false", ClosedSet: []string{"true", "false"}},
				{Name: "security-policy", Short: "deploy posture policy", Value: "off|warn|enforce", ClosedSet: []string{"off", "warn", "enforce"}},
			}},
			appEgressCLISubcommand(subEgressAllowlist, "Inspect or update the outbound CIDR allowlist", "<cidr>"),
			appEgressCLISubcommand(subEgressPorts, "Inspect or update the extra outbound TCP ports (Pro/Scale)", "<port>"),
			appNetworkCLISubcommand(),
			{Name: subStaticEgressIP, Short: "Inspect or pin a static outbound address (Pro/Scale)", Subcommands: []cliSub{
				{Name: "show", Short: "Show the pinned address and plan eligibility"},
				{Name: "set", Short: "Pin a static outbound address", Positionals: []string{"<ip>"}},
				{Name: "clear", Short: "Clear the pinned outbound address"},
			}},
			{Name: "routes", Short: "List admitted per-route labels for one app"},
			appTCPListenerCLISubcommand(false),
			appUDPListenerCLISubcommand(false),
		},
		Positionals: []string{"<slug>"},
		Flags: []cliFlag{
			{Name: "concurrency", Short: "print only the per-VM concurrency bound for the app's plan", Bool: true},
			{Name: "environment", Short: "read or edit desired workload settings in a project environment", Value: "SLUG"},
			{Name: "visibility", Short: "set public edge exposure", Value: "public|internal", ClosedSet: []string{"public", "internal"}},
			{Name: "profile", Short: "set a named RAM/CPU profile", Value: "micro|small|medium|large|xlarge", ClosedSet: []string{"micro", "small", "medium", "large", "xlarge"}},
			{Name: "ram", Short: "set RAM in MB", Value: "MB"},
			{Name: "cpu-millicores", Short: "set sustained CPU allowance", Value: "250|500|1000", ClosedSet: []string{"250", "500", "1000"}},
			{Name: "max-concurrency", Short: "set max_concurrency", Value: "N"},
			{Name: "concurrency-overflow", Short: "set saturated concurrency behavior", ClosedSet: []string{"queue", "drop"}},
			{Name: "max-queue-depth", Short: "set maximum warm-saturation waiters", Value: "N"},
			{Name: "max-queue-wait", Short: "set maximum warm-saturation wait as a duration", Value: "DURATION"},
			{Name: "max-queue-wait-ms", Short: "set maximum queued concurrency wait", Value: "N"},
			{Name: "wake-max-queue-depth", Short: "set per-app cold-wake waiter cap", Value: "N"},
			{Name: "wake-max-queue-wait-seconds", Short: "set per-app cold-wake wait budget", Value: "N"},
			{Name: "idle", Short: "set idle timeout in seconds", Value: "SEC"},
			{Name: "request-timeout", Short: "set per-app request timeout in seconds", Value: "SEC"},
			{Name: "require-signed", Short: "toggle require_signed", ClosedSet: []string{"true", "false"}},
			{Name: "security-policy", Short: "deploy posture policy", ClosedSet: []string{"off", "warn", "enforce"}},
			{Name: "basic-user", Short: "basic-auth username (required with --public-auth=basic)", Value: "USER"},
			{Name: "basic-pass", Short: "basic-auth password (required with --public-auth=basic)", Value: "PASS"},
			{Name: "min", Short: "set minimum warm instances (Pro/Scale only)", Value: "N"},
			{Name: "autoscale-target-rps", Short: "set per-instance RPS scale-up target; 0 disables", Value: "N"},
			{Name: "autoscale-target-cpu-pct", Short: "set per-instance CPU scale-up target; 0 disables", Value: "1..100"},
			{Name: "warm-snapshot", Short: "enable the warm-snapshot tier"},
			{Name: "no-warm-snapshot", Short: "disable the warm-snapshot tier"},
			{Name: "warm-snapshot-min-requests", Short: "set the warm-snapshot request threshold", Value: "N"},
			{Name: "warm-snapshot-min-ms", Short: "set the warm-snapshot ready-time threshold", Value: "MS"},
			{Name: "warm-pool-size", Short: "set the paused warm-pool size", Value: "N"},
			{Name: "eviction-priority", Short: "set the app eviction tier", Value: "best_effort|reserved", ClosedSet: []string{"best_effort", "reserved"}},
			{Name: "require-authn", Short: "require a Gregale bearer token on every request (Pro/Scale only)"},
			{Name: "no-require-authn", Short: "disable the per-deployment token requirement"},
			{Name: "platform-tenant-required", Short: "require verified customer identity on app traffic (Hobby and above)"},
			{Name: "no-platform-tenant-required", Short: "allow app traffic without verified customer identity"},
			{Name: "maintenance", Short: "put every request into 503 maintenance mode"},
			{Name: "no-maintenance", Short: "resume normal request handling"},
			{Name: "streaming-enabled", Short: "enable streamed responses (plan eligibility is checked by the API)"},
			{Name: "no-streaming-enabled", Short: "use buffered responses"},
			{Name: "websocket-enabled", Short: "allow WebSocket upgrade forwarding (plan eligibility is checked by the API)"},
			{Name: "no-websocket", Short: "disable WebSocket upgrade forwarding"},
			{Name: "route-metrics", Short: "enable per-route gateway metrics (plan eligibility is checked by the API)"},
			{Name: "no-route-metrics", Short: "disable per-route gateway metrics"},
			{Name: "consumer-auth-mode", Short: "end-customer API-key policy: optional|required", Value: "optional|required", ClosedSet: []string{api.ConsumerAuthModeOptional, api.ConsumerAuthModeRequired}},
			{Name: "only-declared-routes", Short: "reject undeclared paths before waking the app (OpenAPI or explicit route list)"},
			{Name: "no-only-declared-routes", Short: "disable the declared-route pre-wake gate"},
			{Name: "head-wakes", Short: "wake a parked app for HEAD /", Bool: true, ClosedSet: []string{"true", "false"}},
			{Name: "crawler-policy", Short: "monitor/crawler wake policy", Value: "wake|cached|block", ClosedSet: []string{"wake", "cached", "block"}},
			{Name: "pre-auth", Short: "per-source pre-auth limit mode; enforce first prints the 24h readiness check", Value: "off|observe|enforce", ClosedSet: []string{"off", "observe", "enforce"}},
			{Name: "pre-auth-rps", Short: "pre-auth requests per second per source", Value: "N"},
			{Name: "pre-auth-burst", Short: "pre-auth burst per source", Value: "N"},
			{Name: "health-path", Short: "set the monitor-facing health path", Value: "PATH"},
			{Name: "health-path-wakes", Short: "allow health probes to wake the app"},
			{Name: "no-health-path-wakes", Short: "answer health probes without waking the app"},
			{Name: "app-protocol", Short: "set the wire-protocol selector", Value: "http1|http2|grpc", ClosedSet: []string{"http1", "http2", "grpc"}},
			{Name: "public-auth", Short: "set public URL authentication; internal_only admits Gregale internal services, ip_allowlist is Pro+", Value: "open|bearer|basic|ip_allowlist|internal_only", ClosedSet: []string{"open", "bearer", "basic", "ip_allowlist", "internal_only"}},
			{Name: "ip-allowlist", Short: "allow a CIDR through the public URL; repeat for multiple ranges; requires --public-auth ip_allowlist", Value: "CIDR", Repeatable: true},
			{Name: "overflow-node", Short: "set or clear the preferred overflow compute node", Value: "NAME"},
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
			{Name: "portal", Short: "Open the active billing provider's portal", Flags: []cliFlag{
				{Name: "print", Short: "print the portal URL without opening a browser", Bool: true},
				{Name: "no-open", Short: "alias of --print", Bool: true},
			}},
			{Name: "retry", Short: "Retry failed payment when supported; Polar uses the portal"},
			{Name: "cancel", Short: "Cancel the subscription at period end"},
			{Name: "payment-method", Short: "Show the card on file", Flags: []cliFlag{
				{Name: "print", Short: "print the card summary and portal URL without opening a browser", Bool: true},
				{Name: "no-open", Short: "alias of --print", Bool: true},
			}},
			{Name: "status", Short: "Show subscription status"},
			{Name: "costs", Short: "Explain retained usage costs and source coverage", Flags: []cliFlag{{Name: "month", Short: "UTC usage month (defaults to current)", Value: "YYYY-MM"}, {Name: "json", Short: "Print the machine-readable cost report"}}, Examples: []string{"gregale billing costs --month 2026-10 --json"}},
			{Name: "forecast", Short: "Show usage cost forecasts and their availability", Flags: []cliFlag{{Name: "month", Short: "UTC usage month (defaults to current)", Value: "YYYY-MM"}, {Name: "json", Short: "Print the machine-readable forecast"}}, Examples: []string{"gregale billing forecast --json"}},
			{Name: "budget-preview", Short: "Preview a budget's cost and workload consequences without writes", Flags: []cliFlag{{Name: "file", Short: "Budget spec JSON file", Value: "PATH"}, {Name: "json", Short: "Print the machine-readable preview"}}, Examples: []string{"gregale billing budget-preview --file budget.json --json"}},
			{Name: "budgets", Short: "Manage revisioned budget drafts; activation is gated", Subcommands: []cliSub{
				{Name: "list", Short: "List account budget drafts", Flags: []cliFlag{{Name: "json", Short: "Print JSON"}}},
				{Name: "get", Short: "Read a budget, including a deletion tombstone", Positionals: []string{"ID"}, Flags: []cliFlag{{Name: "json", Short: "Print JSON"}}},
				{Name: "create", Short: "Save a budget draft", Flags: []cliFlag{{Name: "file", Short: "Budget spec JSON file (required)", Value: "PATH"}, {Name: "key", Short: "Stable operation key for retries", Value: "KEY"}, {Name: "json", Short: "Print JSON"}}, Examples: []string{"gregale billing budgets create --file budget.json --key previews-october --json"}},
				{Name: "update", Short: "Replace a draft at its expected revision", Positionals: []string{"ID"}, Flags: []cliFlag{{Name: "file", Short: "Budget spec JSON file (required)", Value: "PATH"}, {Name: "expected-revision", Short: "Current revision (required)", Value: "N"}, {Name: "key", Short: "Stable operation key for retries", Value: "KEY"}, {Name: "json", Short: "Print JSON"}}},
				{Name: "delete", Short: "Tombstone a policy and retain its audit", Positionals: []string{"ID"}, Flags: []cliFlag{{Name: "expected-revision", Short: "Current revision (required)", Value: "N"}, {Name: "key", Short: "Stable operation key for retries", Value: "KEY"}, {Name: "json", Short: "Print JSON"}}},
				{Name: "history", Short: "Page through immutable policy revisions", Positionals: []string{"ID"}, Flags: []cliFlag{{Name: "after-revision", Short: "Continue after this revision", Value: "N"}, {Name: "limit", Short: "Page size (1..100)", Value: "N"}, {Name: "json", Short: "Print JSON"}}},
			}},
			{Name: "refresh-invoice", Short: "Refresh provider facts for an existing invoice", Positionals: []string{"ID"}, Examples: []string{"gregale billing refresh-invoice INVOICE_ID"}},
			{Name: "backfill-invoices", Short: "Import one page of missing provider invoices", Examples: []string{"gregale billing backfill-invoices", "gregale billing backfill-invoices --cursor TOKEN"}},
			{Name: "export", Short: "Export a partial FOCUS 1.4 invoice projection", Flags: []cliFlag{
				{Name: "month", Short: "invoice period-end month (required)", Value: "YYYY-MM"},
				{Name: "format", Short: "export encoding (default zip with CSV and metadata)", Value: "FORMAT", ClosedSet: []string{"zip", "csv", "metadata"}},
				{Name: "out", Short: "new output file (required for zip); - writes stdout", Value: "PATH"},
			}},
		},
		Examples: []string{"gregale billing export --month 2026-09 --out invoices.zip", "gregale billing export --month 2026-09 --format csv --out invoices.csv"},
	},
	{
		Name:    "canary",
		DocSlug: "canary",
		Short:   "Inspect profiling gates, advance canary stages, or simulate a preset",
		Subcommands: []cliSub{
			{Name: "gate", Short: "Read the current profiling gate and route evidence", Positionals: []string{"<deployment-id>"}},
			{Name: "advance", Short: "Advance one observed stage with an optional audited profiling override", Positionals: []string{"<deployment-id>"}, Flags: []cliFlag{
				{Name: "expected-step", Short: "observed current canary step", Value: "N"},
				{Name: "profile-policy-revision", Short: "current policy revision for an explicit override", Value: "REV"},
				{Name: "profile-override-reason", Short: "audited reason for overriding only the profiling gate", Value: "TEXT"},
			}},
			{Name: "simulate", Short: "Estimate per-stage canary success from the last hour", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "canary-preset", Short: "canary ladder preset", Value: "PRESET", ClosedSet: []string{"slow", "balanced", "aggressive", "1-10-50-100"}},
			}},
		},
		Positionals: []string{"<slug>"},
	},
	{
		Name:                dispatchBuild,
		DocSlug:             "build",
		Short:               "Inspect builds (build status|list|provenance|sbom)",
		CompletionPositions: buildCLICompletionPositions(),
		Subcommands: []cliSub{
			{Name: statusLiteral, Short: "Show the current status of one build", Positionals: []string{"<id>"}},
			{Name: "list", Short: "List builds and discover build IDs", Flags: []cliFlag{
				{Name: "app", Short: "filter to one app", Value: "SLUG"},
				{Name: "status", Short: "filter by lifecycle status", Value: "STATUS", ClosedSet: []string{"queued", "running", "succeeded", "failed", "cancelled"}},
				{Name: "limit", Short: "page size (1..200)", Value: "N"},
				{Name: "before", Short: "alias for --cursor", Value: "CURSOR"},
				{Name: "cursor", Short: "opaque cursor from a prior page", Value: "CURSOR"},
				{Name: "all", Short: "walk every page using --limit and --cursor"},
			}},
			{Name: "provenance", Short: "Show the build provenance attestation", Positionals: []string{"<id>"}},
			{Name: "sbom", Short: "Show the build SBOM", Positionals: []string{"<id>"}},
		},
	},
	{
		Name:    "connect",
		DocSlug: "connect",
		Short:   "Connect a third-party service (github | repo OWNER/NAME)",
		Subcommands: []cliSub{
			{Name: "github", Short: "Connect a GitHub account for repo deploys", Examples: []string{"gregale connect github"}},
			// Issue #961 / Mega-B PR-1: `connect repo <owner>/<name>`
			// opens the dashboard's /dashboard/apps/new?repo=... wizard
			// (PR-3 wires the server side). The CLI stays out of the
			// OAuth dance — the cookie-session dashboard is the
			// install-token trust root.
			{Name: "repo", Short: "Open the dashboard wizard to bind <owner>/<name> to a Gregale app", Examples: []string{"gregale connect repo acme/my-api"}, Positionals: []string{"<owner>/<name>"}},
		},
	},
	{
		Name:    "github",
		DocSlug: "github",
		Short:   "Manage an app's GitHub installation and repository binding",
		Subcommands: []cliSub{
			{Name: "status", Short: "Show the GitHub connection health for <slug>", Examples: []string{"gregale github status my-api"}, Positionals: []string{"<slug>"}},
			{Name: "sync", Short: "Reconcile repository access with GitHub", Examples: []string{"gregale github sync my-api"}, Positionals: []string{"<slug>"}},
			{Name: "repos", Short: "List repositories visible to the connected GitHub installation for <slug>", Examples: []string{"gregale github repos my-api"}, Positionals: []string{"<slug>"}},
			{Name: "bind", Short: "Bind <slug> to a visible GitHub repository", Examples: []string{"gregale github bind my-api --repo acme/my-api --branch main"}, Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "installation-id", Short: "GitHub App installation id (auto-resolved when omitted)", Value: "ID"},
				{Name: "repo", Short: "GitHub repository OWNER/NAME", Value: "OWNER/NAME", Req: true},
				{Name: "branch", Short: "production branch", Value: "BRANCH"},
				{Name: "deploy-branches", Short: "branch=scope mappings", Value: "MAPPINGS"},
			}},
			{Name: "setup", Short: "Bind GitHub, configure previews, and write an Actions workflow", Examples: []string{"gregale github setup my-api --repo acme/my-api --dry-run", "gregale github setup my-api --repo acme/my-api --preview --preview-ttl-hours 72"}, Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "repo", Short: "GitHub repository OWNER/NAME (required for a dry run)", Value: "OWNER/NAME"},
				{Name: "production-branch", Short: "production branch (default: current binding or main)", Value: "BRANCH"},
				{Name: "deploy-branches", Short: "comma-separated branch=environment mappings (default or registered environment)", Value: "MAPPINGS"},
				{Name: "pinned-sha", Short: "pin the generated deploy Action to this full 40-character commit SHA (default: immutable SHA embedded in the CLI release)", Value: "SHA"},
				{Name: "pin-action", Short: "resolve the current v0 deploy Action tag to its commit SHA"},
				{Name: "enable-action-updates", Short: "add a weekly GitHub Actions Dependabot updater"},
				{Name: "workflow", Short: "workflow path relative to repository root", Value: "PATH"},
				{Name: "preview", Short: "enable pull-request previews"},
				{Name: "no-preview", Short: "disable pull-request previews"},
				{Name: "preview-ttl-hours", Short: "preview lease in hours (1-720)", Value: "HOURS"},
				{Name: "preview-service-policy", Short: "preview-to-production service calls: deny|allow_marked", Value: "POLICY", ClosedSet: []string{"deny", "allow_marked"}},
				{Name: "root-dir", Short: "repository-relative source root for the root workload", Value: "DIR"},
				{Name: "ignore", Short: "comma-separated ignored change paths", Value: "PATHS"},
				{Name: "rollout", Short: "production rollout mode: standard|safe (safe requires Pro/Scale)", Value: "MODE", ClosedSet: []string{"standard", "safe"}},
				{Name: "dry-run", Short: "show generated files without writing or changing remote state"},
				{Name: "force", Short: "overwrite an existing workflow file"},
			}},
			{Name: "disconnect", Short: "Remove the app's GitHub repository binding", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "yes", Short: "confirm removing the repository binding"},
			}},
		},
		Positionals: []string{"<slug>"},
	},
	{
		Name:    "cors",
		DocSlug: "cors",
		Short:   "Configure CORS for an app (allow|ls|rm|show)",
		Subcommands: []cliSub{
			{Name: "allow", Short: "Attach a CORS rule to <slug>", Positionals: []string{"<slug>", "<origin>", "[<origin>...]"}, Flags: []cliFlag{
				{Name: "method", Short: "allowed method (repeat)", Value: "VERB"},
				{Name: "credentials", Short: "enable Access-Control-Allow-Credentials"},
				{Name: "max-age", Short: "Access-Control-Max-Age in seconds (default 600)", Value: "N"},
				{Name: "host", Short: "match host (default: the app's first verified custom domain)", Value: "HOST"},
			}, Examples: []string{"gregale cors allow my-api https://app.example.com --method GET --method POST"}},
			{Name: "ls", Short: "List CORS rules bound to <slug> (defaults to linked context)", Positionals: []string{"[<slug>]"}},
			{Name: "rm", Short: "Delete a CORS rule by id", Positionals: []string{"[<slug>]", "<rule-id>"}},
			{Name: "show", Short: "Show per-app default CORS + active rules (defaults to linked context)", Positionals: []string{"[<slug>]"}},
		},
	},
	{
		Name:    "crons",
		DocSlug: "crons",
		Short:   "Manage scheduled HTTP requests and deployment commands",
		Subcommands: []cliSub{
			{Name: "list", Short: "List cron rules", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "slug"}}},
			{Name: "add", Short: "Schedule an HTTP request or deployment command", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "schedule", Short: "five-field cron expression", Req: true, Value: "EXPR"},
				{Name: "path", Short: "HTTP request path (mutually exclusive with --command)", Value: "PATH"},
				{Name: "command", Short: "executable for a deployment command cron", Value: "EXEC"},
				{Name: "arg", Short: "append one command argument (repeatable)", Value: "ARG"},
				{Name: "shell", Short: "run --command as one shell string"},
				{Name: "timeout-seconds", Short: "command timeout (default 600 seconds)", Value: "N"},
				{Name: "max-output-bytes", Short: "captured output limit (default 1048576 bytes)", Value: "N"},
				{Name: "timezone", Short: "IANA timezone (default UTC)", Value: "TZ"},
				{Name: "skip-if-running", Short: "skip fires while the previous run is active"},
				{Name: "retry-max", Short: "additional command attempts after failure or timeout"},
				{Name: "retry-backoff-seconds", Short: "base retry delay; doubles per attempt"},
				{Name: "schedule-policy", Short: "versioned schedule policy JSON", Value: "JSON"},
				{Name: "failure-rules", Short: "versioned failure and outcome-code rules JSON", Value: "JSON"},
			}},
			{Name: "info", Short: "Show one cron rule", Positionals: []string{"<id>"}},
			{Name: "update", Short: "Update one cron rule", Positionals: []string{"<id>"}, Flags: []cliFlag{
				{Name: "schedule", Short: "new five-field cron expression", Value: "EXPR"},
				{Name: "path", Short: "HTTP request path", Value: "PATH"},
				{Name: "timezone", Short: "IANA timezone", Value: "TZ"},
				{Name: "enable", Short: "enable the cron"},
				{Name: "disable", Short: "disable the cron"},
				{Name: "skip-if-running", Short: "skip fires while a previous run is active"},
				{Name: "allow-overlap", Short: "allow scheduled fires to overlap"},
				{Name: "retry-max", Short: "additional command attempts after failure or timeout"},
				{Name: "retry-backoff-seconds", Short: "base retry delay; doubles per attempt", Value: "N"},
				{Name: "schedule-policy", Short: "replace versioned schedule policy JSON", Value: "JSON"},
				{Name: "failure-rules", Short: "replace versioned failure and outcome-code rules JSON", Value: "JSON"},
			}},
			{Name: "rm", Short: "Delete one cron rule", Positionals: []string{"<id>"}},
			{Name: "run", Short: "Fire one cron immediately", Positionals: []string{"<cron-id>"}},
			{Name: "fire-now", Short: "Show the status of a manual fire request", Positionals: []string{"<request-id>"}},
			{Name: "runs", Short: "Show execution history", Positionals: []string{"<id>"}, Flags: []cliFlag{
				{Name: "before", Short: "pagination cursor for older runs", Value: "CURSOR"},
				{Name: "limit", Short: "max runs to show (1..100)", Value: "N"},
				{Name: "run", Short: "show details and captured output for one command run", Value: "TASK-ID"},
			}},
			{Name: "occurrences", Short: "Inspect scheduled occurrence decisions", Positionals: []string{"<id>"}, Flags: []cliFlag{
				{Name: "before", Short: "alias for --cursor", Value: "ID"},
				{Name: "cursor", Short: "opaque continuation cursor", Value: "ID"},
				{Name: "all", Short: "walk every page using --limit and --cursor"},
				{Name: "limit", Short: "max occurrence decisions (1..200)", Value: "N"},
			}},
			{Name: "cancel", Short: "Request cancellation of one command-cron run", Positionals: []string{"<cron-id>", "<run-id>"}},
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
			{Name: "get", Short: "Show one trigger", Positionals: []string{"<id>"}},
			{Name: "create", Short: "Create a broker trigger", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "kind", Short: "trigger kind", Req: true, Value: "kind", ClosedSet: triggerBrokerKindNames},
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
			{Name: "update", Short: "Update one trigger", Positionals: []string{"<id>"}, Flags: []cliFlag{
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
			{Name: "delete", Short: "Delete one trigger", Positionals: []string{"<id>"}, Flags: []cliFlag{
				{Name: "quiet", Short: "skip the typed confirmation (for scripts)"},
				{Name: "yes", Short: "confirm trigger deletion without prompting", Bool: true},
			}},
			{Name: "pause", Short: "Disable one trigger", Positionals: []string{"<id>"}},
			{Name: "resume", Short: "Enable one trigger", Positionals: []string{"<id>"}},
			{Name: "records", Short: "List recent trigger records", Positionals: []string{"<id>"}, Flags: []cliFlag{
				{Name: "state", Short: "filter by record state", Value: "STATE", ClosedSet: triggerRecordStateNames},
			}},
			{Name: "retry", Short: "Re-drive one trigger record", Positionals: []string{"<trigger-id>", "<record-id>"}},
			{Name: "drop", Short: "Drop one trigger record", Positionals: []string{"<trigger-id>", "<record-id>"}},
			{Name: "dlq", Short: "List dead-letter records", Positionals: []string{"<id>"}, Flags: []cliFlag{
				{Name: "reason", Short: "filter by dead-letter reason", Value: "REASON"},
			}},
			{Name: "metrics", Short: "Show per-state trigger metrics", Positionals: []string{"<id>"}},
		},
	},
	{
		Name:    "workers",
		DocSlug: "workers",
		Short:   "Inspect and manage background worker pools",
		Subcommands: []cliSub{
			{Name: "list", Short: "List background worker pools"},
			{Name: "status", Short: "Show real-time status and autoscaling for a worker pool", Positionals: []string{"[<app>]"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug (optional; defaults to linked context)", Value: "slug"},
			}},
			{Name: "logs", Short: "Tail logs for a background worker pool", Positionals: []string{"[<app>]"}, Flags: []cliFlag{
				{Name: "follow", Short: "follow new log lines"},
				{Name: "grep", Short: "filter log lines by substring", Value: "SUBSTR"},
				{Name: "since", Short: "filter log lines after timestamp", Value: "RFC3339"},
				{Name: "level", Short: "filter log lines by level (info|warn|error)", Value: "LEVEL"},
			}},
			{Name: "scale", Short: "Adjust scaling bounds and graceful drain for a worker pool", Positionals: []string{"<app>"}, Flags: []cliFlag{
				{Name: "min", Short: "min worker replicas (0 = scale-to-zero)", Value: "N"},
				{Name: "max", Short: "max worker replicas", Value: "N"},
				{Name: "target", Short: "target backlog per worker", Value: "N"},
				{Name: "metric", Short: "autoscaling metric (queue_lag | queue_depth | custom)", Value: "METRIC"},
				{Name: "name", Short: "custom metric name (required with --metric custom)", Value: "CUSTOM_METRIC"},
				{Name: "drain-timeout", Short: "shutdown grace duration (e.g. 90s, 2m)", Value: "DURATION"},
				{Name: "stop-signal", Short: "stop signal (e.g. SIGTERM, SIGINT, SIGQUIT)", Value: "SIG"},
			}},
		},
		Positionals: []string{"[<slug>]"},
	},
	{
		Name:    "jobs",
		DocSlug: "jobs",
		Short:   "Manage jobs (run-to-completion workloads)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List jobs in this account", Flags: []cliFlag{
				{Name: "limit", Short: "page size (1..200, default 50)", Value: "N"},
				{Name: "offset", Short: "starting offset (>= 0)", Value: "N"},
				{Name: "all", Short: "walk every page using --limit and --offset"},
			}},
			{Name: "add", Short: "Create a new job", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "image", Value: "REF", Short: "OCI image", Req: true},
				{Name: "command", Value: "ARGV", Short: "comma-separated entrypoint (e.g. /bin/sh,-c,echo hi)"},
				{Name: "ram", Value: "MB", Short: "billable memory in MB (0 = plan default)"},
				{Name: "timeout", Value: "SECONDS", Short: "per-task wall-clock deadline (0 = plan default)"},
				{Name: "parallelism", Value: "N", Short: "max concurrent tasks across a run (0 = plan default)"},
				{Name: "retries", Value: "N", Short: "per-task max retries (0 = plan default)"},
				{Name: "schedule", Value: "EXPR", Short: "recurring five-field cron schedule"},
				{Name: "timezone", Value: "TZ", Short: "IANA timezone for the recurring schedule"},
				{Name: "schedule-policy", Value: "JSON", Short: "versioned recurring schedule policy JSON"},
				{Name: "failure-rules", Value: "JSON", Short: "versioned exit-code and outcome retry rules JSON"},
			}},
			{Name: "info", Short: "Show one job", Positionals: []string{"<name>"}},
			{Name: "update", Short: "Update one job", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "image", Value: "REF", Short: "new OCI image"},
				{Name: "command", Value: "ARGV", Short: "new comma-separated entrypoint"},
				{Name: "ram", Value: "MB", Short: "new RAM (MB)"},
				{Name: "timeout", Value: "SECONDS", Short: "new per-task timeout"},
				{Name: "parallelism", Value: "N", Short: "new max parallel tasks"},
				{Name: "retries", Value: "N", Short: "new per-task max retries"},
				{Name: "pause", Short: "halt future dispatches (status=paused)"},
				{Name: "resume", Short: "resume dispatches (status=active)"},
				{Name: "schedule", Value: "EXPR", Short: "replace recurring cron schedule"},
				{Name: "timezone", Value: "TZ", Short: "replace schedule IANA timezone"},
				{Name: "unschedule", Short: "remove recurring schedule"},
				{Name: "schedule-policy", Value: "JSON", Short: "replace versioned recurring schedule policy JSON"},
				{Name: "failure-rules", Value: "JSON", Short: "replace versioned exit-code and outcome retry rules JSON"},
			}},
			{Name: "rm", Short: "Soft-delete one job", Positionals: []string{"<name>"}},
			{Name: "run", Short: "Dispatch a new run (fan-out N tasks)", Positionals: []string{"<job-name>"}, Flags: []cliFlag{
				{Name: "tasks", Value: "N", Short: "number of tasks to fan out (or use --input)"},
				{Name: "retries", Value: "N", Short: "override retry max for this run"},
				{Name: "timeout", Value: "SECONDS", Short: "override task timeout for this run"},
				{Name: "input", Value: "ID=REF", Short: "repeatable input binding"},
				{Name: "input-manifest-uri", Value: "URI", Short: "account-readable input manifest object"},
				{Name: "input-manifest-sha256", Value: "DIGEST", Short: "SHA-256 of exact manifest bytes"},
				{Name: "parallelism", Value: "N", Short: "maximum concurrent tasks"},
				{Name: "flexible", Short: "use spare capacity within a start window"},
				{Name: "eligible-at", Value: "RFC3339", Short: "earliest task start"},
				{Name: "latest-start-at", Value: "RFC3339", Short: "latest task start"},
				{Name: "fail-fast", Short: "cancel unstarted tasks after permanent failure"},
				{Name: "failure-rules", Value: "JSON", Short: "override versioned exit-code and outcome retry rules for this run"},
			}},
			{Name: "runs", Short: "List runs for one job", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "limit", Short: "page size (1..200, default 50)", Value: "N"},
				{Name: "offset", Short: "starting offset (>= 0)", Value: "N"},
				{Name: "all", Short: "walk every page using --limit and --offset"},
			}},
			{Name: "occurrences", Short: "Inspect recurring schedule decisions", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "before", Short: "alias for --cursor", Value: "ID"},
				{Name: "cursor", Short: "opaque continuation cursor", Value: "ID"},
				{Name: "all", Short: "walk every page using --limit and --cursor"},
				{Name: "limit", Short: "max occurrence decisions (1..200)", Value: "N"},
			}},
			{Name: "cancel", Short: "Cancel a run", Positionals: []string{"<name>", "<run-id>"}},
			{Name: "tasks", Short: "List tasks for one run", Positionals: []string{"<name>", "<run-id>"}},
			{Name: "attempts", Short: "List retained attempts for one task", Positionals: []string{"<name>", "<run-id>", "<task-index>"}},
			{Name: "retry", Short: "Retry one failed task", Positionals: []string{"<name>", "<run-id>", "<task-index>"}},
			{Name: "replay-failed", Short: "Replay unsuccessful tasks in a linked run", Positionals: []string{"<name>", "<run-id>"}},
			{Name: "artifact-url", Short: "Verify a managed result and get a signed URL", Positionals: []string{"<name>", "<run-id>", "<task-index>", "<artifact-name>"}},
			{Name: "logs", Short: "Tail logs for one task", Positionals: []string{"<name>", "<run-id>", "<task-index>"}, Flags: []cliFlag{
				{Name: "max-bytes", Short: "maximum log payload size (1..1048576)", Value: "N"},
			}},
			{Name: "registry", Short: "Manage private registry credentials for one job", Subcommands: []cliSub{
				{Name: "list", Short: "List registry credentials for the job", Positionals: []string{"<job>"}},
				{Name: "set", Short: "Store a registry credential for the job", Positionals: []string{"<job>"}, Flags: []cliFlag{
					{Name: "registry", Short: "registry host", Value: "HOST", Req: true},
					{Name: "user", Short: "registry user", Value: "USER", Req: true},
					{Name: "password-stdin", Short: "read the password from stdin"},
					{Name: "password", Short: "registry password (prefer --password-stdin)", Value: "PASSWORD"},
				}},
				{Name: "rm", Short: "Remove a registry credential from the job", Positionals: []string{"<job>"}, Flags: []cliFlag{
					{Name: "registry", Short: "registry host", Value: "HOST", Req: true},
				}},
			}},
		},
	},
	{
		Name:    "automations",
		DocSlug: "automations",
		Short:   "Build, monitor and control customer-built automations",
		Examples: []string{
			"gregale automations list --app billing",
			"gregale automations get --app billing --name paid-invoice",
			"gregale automations health --app billing --name paid-invoice",
			"gregale automations pause --app billing --name paid-invoice --expected-version 8",
			"gregale automations resume --app billing --name paid-invoice --expected-version 9",
			"gregale automations revisions list --app billing --name paid-invoice",
			"gregale automations revisions show --app billing --name paid-invoice --revision 42",
			"gregale automations validate --app billing --file automation.yaml",
			"gregale automations simulate --app billing --file automation.yaml --input-file sample.json",
			"gregale automations apply --app billing --file automation.yaml --expected-version 0",
			"gregale automations publish --app billing --name paid-invoice --expected-version 1",
			"gregale automations restore --app billing --name paid-invoice --revision 42 --expected-version 47",
			"gregale automations delete --app billing --name paid-invoice --expected-version 48 --yes",
		},
		Subcommands: []cliSub{
			{Name: "list", Short: "List automation versions and ownership for an app", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}}},
			{Name: "get", Short: "Inspect automation state or export a definition", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "definition-out", Short: "export the selected definition as JSON to a new file", Value: "PATH"}, {Name: "published", Short: "export the published definition instead of the draft"}}},
			{Name: "health", Short: "Show bounded run reliability and failed-step metrics", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "created-after", Short: "inclusive RFC3339 window start (max 30 days)", Value: "RFC3339"}, {Name: "created-before", Short: "inclusive RFC3339 window end", Value: "RFC3339"}}},
			{Name: "pause", Short: "Stop future scheduled and event-triggered admissions", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "expected-version", Short: "current automation version", Req: true, Value: "N"}}},
			{Name: "resume", Short: "Resume automatic scheduled and event-triggered admissions", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "expected-version", Short: "current automation version", Req: true, Value: "N"}}},
			{Name: "revisions", Short: "Inspect immutable published snapshots", Subcommands: []cliSub{
				{Name: "list", Short: "List published revisions for an automation", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "limit", Short: "page size (1..100, default 50)", Value: "N"}, {Name: "offset", Short: "number of revisions to skip", Value: "N"}}},
				{Name: "show", Short: "Inspect a revision or export its definition", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "revision", Short: "published revision number", Req: true, Value: "N"}, {Name: "definition-out", Short: "export the definition as JSON to a new file", Value: "PATH"}}},
			}},
			{Name: "restore", Short: "Restore a published revision as a draft", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "revision", Short: "published revision number to restore", Req: true, Value: "N"}, {Name: "expected-version", Short: "current version; use 0 if deleted", Req: true, Value: "N"}}},
			{Name: "delete", Short: "Delete an automation using its current version", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "expected-version", Short: "current automation version", Req: true, Value: "N"}, {Name: "yes", Short: "required explicit confirmation of automation deletion", Req: true, Bool: true}, {Name: "restore-manifest", Short: "allow the current YAML definition to own this automation again"}}},
			{Name: "validate", Short: "Validate an automation definition without saving it", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "file", Short: "YAML or JSON definition file", Req: true, Value: "PATH"}}},
			{Name: "simulate", Short: "Trace an automation using sample input and mocked outputs, without running steps", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "file", Short: "YAML or JSON definition file", Req: true, Value: "PATH"}, {Name: "input-file", Short: "sample workflow input JSON file", Value: "PATH"}, {Name: "mock-outputs-file", Short: "JSON object of action outputs keyed by step name", Value: "PATH"}, {Name: "mock-item-outputs-file", Short: "JSON object of for_each output arrays keyed by step name", Value: "PATH"}, {Name: "mock-attempts-file", Short: "JSON object of ordered attempt outcomes keyed by step name", Value: "PATH"}, {Name: "require-complete", Short: "fail if mocks leave steps unresolved"}}},
			{Name: "apply", Short: "Save an automation definition as a draft", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "file", Short: "YAML or JSON definition file", Req: true, Value: "PATH"}, {Name: "expected-version", Short: "current version; use 0 for a new draft", Req: true, Value: "N"}}},
			{Name: "publish", Short: "Publish the current automation draft", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "expected-version", Short: "current version of the draft", Req: true, Value: "N"}, {Name: "take-over-manifest", Short: "explicitly take over YAML ownership"}}},
		},
	},
	{
		Name:    "workflows",
		DocSlug: "workflows",
		Short:   "Manage durable execution workflows",
		Subcommands: []cliSub{
			{Name: "list", Short: "List workflow runs for an app", Examples: []string{"gregale workflows list --app billing --workflow-name paid-invoice --status failed", "gregale workflows list --app billing --created-after 2026-10-01T00:00:00Z --created-before 2026-10-05T23:59:59Z"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "SLUG"},
				{Name: "limit", Short: "page size (1..100)", Value: "N"},
				{Name: "offset", Short: "page offset", Value: "N"},
				{Name: "status", Short: "filter by workflow run status", Value: "STATUS"},
				{Name: "workflow-name", Short: "filter by exact workflow name", Value: "NAME"},
				{Name: "created-after", Short: "inclusive RFC3339 creation-time start", Value: "RFC3339"},
				{Name: "created-before", Short: "inclusive RFC3339 creation-time end", Value: "RFC3339"},
			}},
			{Name: "schedules", Short: "Inspect recurring workflow schedules and preview their next fires", Flags: []cliFlag{{Name: "app", Short: "application slug", Req: true, Value: "SLUG"}}, Subcommands: []cliSub{
				{Name: "preview", Short: "Simulate upcoming fires and the next catch-up decision", Examples: []string{"gregale workflows schedules preview --app billing --workflow nightly", "gregale workflows schedules preview --app billing --workflow nightly --at 2027-03-28T00:00:00Z --count 8"}, Flags: []cliFlag{
					{Name: "app", Short: "app slug", Req: true, Value: "SLUG"},
					{Name: "workflow", Short: "schedule workflow name", Req: true, Value: "NAME"},
					{Name: "at", Short: "hypothetical evaluator time", Value: "RFC3339"},
					{Name: "since", Short: "simulated previous evaluation time", Value: "RFC3339"},
					{Name: "count", Short: "upcoming fires to return (1..20, default 5)", Value: "N"},
				}},
			}},
			{Name: "schedule-history", Short: "Inspect recurring workflow admission history", Flags: []cliFlag{
				{Name: "app", Short: "application slug", Req: true, Value: "SLUG"},
				{Name: "platform-tenant-id", Short: "filter by tenant UUID", Value: "UUID"},
				{Name: "cursor", Short: "next cursor from the previous page", Value: "UUID"},
				{Name: "limit", Short: "maximum occurrences (1-200)", Value: "N"},
			}, Subcommands: []cliSub{
				{Name: "replay-preview", Short: "Check selected skipped occurrences against current definitions, overlap, and quota", Examples: []string{"gregale workflows schedule-history replay-preview --app billing --occurrence-id <uuid>"}, Flags: []cliFlag{
					{Name: "app", Short: "app slug", Req: true, Value: "SLUG"},
					{Name: "occurrence-id", Short: "skipped occurrence UUID (repeat up to 20 times)", Req: true, Value: "UUID"},
				}},
				{Name: "replay", Short: "Replay eligible skipped occurrences using the current workflow and normal quota", Examples: []string{"gregale workflows schedule-history replay --app billing --occurrence-id <uuid>"}, Flags: []cliFlag{
					{Name: "app", Short: "app slug", Req: true, Value: "SLUG"},
					{Name: "occurrence-id", Short: "skipped occurrence UUID (repeat up to 20 times)", Req: true, Value: "UUID"},
				}},
			}},
			{Name: "run", Short: "Trigger a new workflow run", Positionals: []string{"<workflow-name>"}, Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "slug"}, {Name: "input", Short: "JSON input payload (default {})", Value: "JSON"}, {Name: "idempotency-key", Short: "stable key for retrying an uncertain run start", Value: "KEY"}}},
			{Name: "status", Short: "Show details of a workflow run", Positionals: []string{"<run_id>"}},
			{Name: "diagnose", Short: "Inspect queue reasons and preview recovery without changing the run", Positionals: []string{"<run_id>"}, Examples: []string{"gregale workflows diagnose <run_id>", "gregale --json workflows diagnose <run_id>"}},
			{Name: "steps", Short: "List steps for a workflow run", Positionals: []string{"<run_id>"}},
			{Name: "attempts", Short: "List retry attempts and managed effect delivery status for a workflow step", Positionals: []string{"<run_id>", "<step_name>"}},
			{Name: "retry", Short: "Retry one safely resumable failed HTTP step", Positionals: []string{"<run_id>", "<step_name>"}},
			{Name: "resume", Short: "Resume eligible failed actions in a workflow run", Positionals: []string{"<run_id>"}, Flags: []cliFlag{{Name: "expected-resume-count", Short: "current resume_count shown by workflows status", Req: true, Value: "N"}, {Name: "idempotency-key", Short: "stable key for retrying the same resume request", Value: "KEY"}}},
			{Name: "resumes", Short: "List continuation history for a workflow run", Positionals: []string{"<run_id>"}},
			{Name: "cancel", Short: "Cancel an active workflow run", Positionals: []string{"<run_id>"}},
			{Name: "cancel-queued-preview", Short: "Preview selected runs that are still pending and have never started", Examples: []string{"gregale workflows cancel-queued-preview --app billing --run-id <uuid> --run-id <uuid>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "SLUG"},
				{Name: "workflow-name", Short: "require an exact workflow name", Value: "NAME"},
				{Name: "run-id", Short: "selected run UUID (repeat up to 20 times)", Req: true, Value: "UUID"},
			}},
			{Name: "cancel-queued", Short: "Cancel selected runs that remain pending and have never started", Examples: []string{"gregale workflows cancel-queued --app billing --run-id <uuid> --yes"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "SLUG"},
				{Name: "workflow-name", Short: "require an exact workflow name", Value: "NAME"},
				{Name: "run-id", Short: "selected run UUID (repeat up to 20 times)", Req: true, Value: "UUID"},
				{Name: "yes", Short: "confirm cancellation of eligible selected runs", Req: true, Bool: true},
			}},
			{Name: "events", Short: "Send external event to a workflow run", Positionals: []string{"<run_id>", "<event_name>"}, Flags: []cliFlag{{Name: "payload", Short: "JSON event payload (default {})", Value: "JSON"}}},
		},
	},
	{
		Name:    "dashboard",
		DocSlug: "dashboard",
		Short:   "Open the account dashboard in your browser",
		Flags:   []cliFlag{{Name: "stateless", Short: "open the stateless-advisory landing page instead of the account page", Bool: true}},
	},
	{
		// Error-explanations cluster (spec §6.4 amendment 1):
		// customer preflight that scans the cwd for the 8 source-side
		// failure modes the cluster's runtime detectors catch
		// post-deploy. Auth not required (local source only).
		Name:     dispatchDoctor,
		DocSlug:  "doctor",
		Short:    "Preflight local source or OCI image metadata; runtime checks are skipped",
		Examples: []string{"gregale doctor", "gregale doctor --strict"},
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
		Short:   "Schedule and inspect deferred invocations",
		Subcommands: []cliSub{
			{Name: "add", Short: "Schedule a deferred invocation", Positionals: []string{"<duration>"}, Flags: []cliFlag{
				{Name: "app", Value: "SLUG", Short: "app slug", Req: true},
				{Name: "scheduled-at", Value: "RFC3339", Short: "absolute dispatch time; exclusive with --delay"},
				{Name: "delay", Value: "DURATION", Short: "relative delay such as 30m; exclusive with --scheduled-at"},
				{Name: "payload", Value: "JSON|@FILE|-", Short: "JSON request payload"},
				{Name: "method", Value: "METHOD", Short: "HTTP method (default POST)"},
				{Name: "path", Value: "PATH", Short: "app path (default /)"},
				{Name: "work-policy", Value: "NAME", Short: "named app work policy"},
				{Name: "work-key", Value: "JSON", Short: "JSON scalar identifying related work"},
				{Name: "work-fairness-key", Value: "JSON", Short: "JSON scalar shared by related work keys"},
				{Name: "header", Value: "NAME:VALUE", Short: "request header (repeatable)"},
				{Name: "max-attempts", Value: "N", Short: "maximum delivery attempts"},
				{Name: "retry-base-seconds", Value: "N", Short: "base retry delay in seconds"},
				{Name: "retry-max-seconds", Value: "N", Short: "maximum retry delay in seconds"},
				{Name: "retry-jitter-seconds", Value: "N", Short: "retry jitter fraction (0..1)"},
				{Name: "retention", Value: "DURATION", Short: "terminal result retention"},
				{Name: "on-success-webhook", Value: "ID", Short: "success webhook subscription"},
				{Name: "on-failure-webhook", Value: "ID", Short: "failure webhook subscription"},
				{Name: "idempotency-key", Value: "KEY", Short: "stable create retry key"},
			}},
			{Name: "list", Short: "List delayed tasks for an app", Flags: []cliFlag{
				{Name: "app", Value: "SLUG", Short: "app slug", Req: true},
				{Name: "limit", Value: "N", Short: "page size (1-200)"},
				{Name: "before", Value: "ID", Short: "alias for --cursor"},
				{Name: "cursor", Value: "CURSOR", Short: "opaque continuation cursor"},
				{Name: "all", Short: "walk every page"},
			}},
			{Name: "get", Short: "Show one delayed task", Positionals: []string{"<id>"}},
			{Name: "info", Short: "Alias for get", Positionals: []string{"<id>"}},
			{Name: "cancel", Short: "Cancel a delayed task", Positionals: []string{"<id>"}},
		},
	},
	{
		Name:    dispatchDeployments,
		DocSlug: "deployments",
		Short:   "List deployments or manage stable named URLs for immutable revisions",
		Examples: []string{
			"gregale deployments --app my-api --limit 10",
			"gregale deployments --app my-api --wide",
		},
		Subcommands: []cliSub{{
			Name:  "alias",
			Short: "Manage stable named URLs for immutable deployments",
			Subcommands: []cliSub{
				{Name: "list", Short: "List deployment aliases for an app", Flags: []cliFlag{
					{Name: "app", Short: "app slug; defaults to the linked project", Value: "SLUG"},
				}},
				{Name: "set", Short: "Point an alias at an exact deployment revision", Flags: []cliFlag{
					{Name: "app", Short: "app slug; defaults to the linked project", Value: "SLUG"},
					{Name: "name", Short: "lowercase DNS-label alias name", Req: true, Value: "NAME"},
					{Name: "deployment", Short: "deployment ID or app revision (vN)", Req: true, Value: "ID|vN"},
				}},
				{Name: "delete", Short: "Remove an alias without deleting its deployment", Flags: []cliFlag{
					{Name: "app", Short: "app slug; defaults to the linked project", Value: "SLUG"},
					{Name: "name", Short: "lowercase DNS-label alias name", Req: true, Value: "NAME"},
				}},
			},
		}},
		Flags: []cliFlag{
			{Name: "app", Short: "app slug (app-scoped deployment history)", Value: "slug"},
			{Name: "limit", Short: "page size (1-200)", Value: "N"},
			{Name: "before", Short: "alias for --cursor", Value: "CURSOR"},
			{Name: "cursor", Short: "opaque cursor from a prior page", Value: "CURSOR"},
			{Name: "all", Short: "walk every page"},
			{Name: "wide", Short: "include annotation columns (by / pr / tag / reason)"},
		},
	},
	{
		Name:    dispatchDeployment,
		DocSlug: "deployment",
		Short:   "Inspect a deployment, wait for its rollout, advance a canary, or set its minimum instances",
		CompletionPositions: []cliCompletionPosition{
			{Position: 1, Role: cliCompletionDeploymentID},
			{Path: []string{"advance"}, Position: 1, Role: cliCompletionDeploymentID},
			{Path: []string{"summary"}, Position: 1, Role: cliCompletionDeploymentID},
			{Path: []string{"wait"}, Position: 1, Role: cliCompletionDeploymentID},
			{Path: []string{"set-min-instances"}, Position: 1, Role: cliCompletionDeploymentID},
		},
		Examples: []string{
			"gregale deployment summary v42 --app my-api",
			"gregale deployment wait v42 --app my-api",
		},
		Subcommands: []cliSub{
			{Name: "runtime", Positionals: []string{"<ID|vN>"}, Short: "Inspect runtime identity or preview a published runtime change", Examples: []string{"gregale deployment runtime v42 --app my-function", "gregale deployment runtime v42 --app my-function --target RELEASE_ID --json"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "app slug for a vN revision"}, {Name: "target", Value: "RELEASE_ID", Short: "published runtime release to preview without applying"}}},
			{Name: "advance", Positionals: []string{"<ID|vN>"}, Short: "Advance a canary by one stage with route enforcement", Examples: []string{"gregale deployment advance DEPLOYMENT_UUID --expected-step 1", "gregale deployment advance v42 --app my-api --expected-step 1"}, Flags: []cliFlag{{Name: "expected-step", Value: "N", Short: "observed current canary step (see deployment summary)", Req: true}, {Name: "app", Value: "SLUG", Short: "app slug, to resolve a vN revision"}}},
			{Name: "summary", Short: "Show the release diff and rollback target", Examples: []string{"gregale deployment summary v42 --app my-api", "gregale deployment summary v42 --app my-api --json"}, Positionals: []string{"<id|vN>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "SLUG"},
			}},
			{Name: "wait", Short: "Wait until a deployment is live (or safe rollout completes)", Examples: []string{"gregale deployment wait 00000000000000000000000000000001", "gregale deployment wait 00000000000000000000000000000001 --rollout --progress"}, Positionals: []string{"<id|vN>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug for a vN revision outside a linked project", Value: "SLUG"},
				{Name: "rollout", Short: "wait for safe rollout to reach 100% traffic"},
				{Name: "progress", Short: "print rollout transitions while waiting (human output only)"},
				{Name: "timeout", Short: "maximum wait (seconds, or a duration such as 10m)", Value: "SECONDS|DURATION"},
			}},
			{Name: "set-min-instances", Short: "Set the per-deployment cold-wake floor", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "min", Short: "minimum warm instances for this deployment", Req: true, Value: "N"}}},
		},
		Positionals: []string{"<id|vN>"},
		Flags: []cliFlag{
			{Name: "app", Short: "app slug; only needed to resolve a vN revision outside a linked project", Value: "SLUG"},
			{Name: "show-scan", Short: "include the per-deploy grype scan payload"},
			{Name: "show-secret-scan", Short: "include the per-deploy image-layer secret-scan payload"},
			{Name: "min", Short: "min_instances floor (>= 0)", Value: "N"},
		},
	},
	{
		Name:    dispatchDeploys,
		DocSlug: "deploys",
		Short:   "Deployment drill-downs (deploys show|status|cancel|reorder|clear|clear-obsolete|retry)",
		CompletionPositions: []cliCompletionPosition{
			{Position: 1, Role: cliCompletionDeploymentID},
			{Path: []string{"show"}, Position: 1, Role: cliCompletionDeploymentID},
			{Path: []string{"status"}, Position: 1, Role: cliCompletionDeploymentID},
			{Path: []string{"cancel"}, Position: 1, Role: cliCompletionDeploymentID},
			{Path: []string{"reorder"}, Position: 1, Role: cliCompletionDeploymentID},
			{Path: []string{"clear"}, Position: 1, Role: cliCompletionDeploymentID},
			{Path: []string{"retry"}, Position: 1, Role: cliCompletionDeploymentID},
		},
		Examples: []string{
			"gregale deploys status 00000000000000000000000000000001",
			"gregale deploys show v42 --app my-api --status",
		},
		Subcommands: []cliSub{
			// ADR-117 companion read surface and ADR-124 deployment
			// operations. Keep this list in lock-step with cmdDeploys.
			{Name: "show", Short: "Print the closed 6-stage post-stream summary", Examples: []string{"gregale deploys show 00000000000000000000000000000001", "gregale deploys show v42 --app my-api --status"}, Positionals: []string{"<id|vN>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug; only needed to resolve a vN revision outside a linked project", Value: "SLUG"},
				{Name: "status", Short: "include terminal status and timing"},
				{Name: "url", Short: "print only the deployment preview URL"},
			}},
			{Name: statusLiteral, Short: "Print stages, terminal status, and failure guidance", Examples: []string{"gregale deploys status 00000000000000000000000000000001", "gregale deploys status v42 --app my-api --json"}, Positionals: []string{"<id|vN>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug; only needed to resolve a vN revision outside a linked project", Value: "SLUG"},
			}},
			{Name: "cancel", Short: "Cancel one pending deployment", Positionals: []string{"<id>"}},
			{Name: "reorder", Short: "Change one pending deployment's queue priority", Positionals: []string{"<id>"}},
			{Name: "clear", Short: "Hide one deployment from the list", Positionals: []string{"<id|vN>"}, Flags: []cliFlag{{Name: "app", Short: "app slug for revision resolution", Value: "SLUG"}, {Name: "dry-run", Short: "preview cleanup without changing resources", Bool: true}, {Name: "force", Short: "confirm cleanup without prompting", Bool: true}}},
			{Name: "clear-obsolete", Short: "Hide obsolete deployments older than a cutoff", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "slug"}, {Name: "older-than", Short: "cutoff age (default 168h)", Value: "D"}, {Name: "dry-run", Short: "preview age/status candidates without changing resources"}, {Name: "force", Short: "skip the confirmation"}}},
			// ADR-117 §Production-ready follow-on, C2 — per-stage
			// retry. The verb is `retry` (NOT a `--retry` flag on
			// show/status) because the action mutates state — a
			// subcommand is the CLI convention for write-side
			// verbs. The --from flag accepts a closed-6 stage name
			// (default = the failing stage on the row, fetched
			// via the existing GET /v1/deployments/{id}/stages
			// read surface).
			{Name: "retry", Short: "Retry a failed deployment from a specific stage (--from=<stage>)", Positionals: []string{"<id>"}, Flags: []cliFlag{
				{Name: "from", Short: "retry from this stage (defaults to the failing stage)", Value: "STAGE", ClosedSet: stageNamesForCLIValues()},
			}},
		},
		Positionals: []string{"<id|vN>"},
		Flags: []cliFlag{
			{Name: "app", Short: "app slug; only needed to resolve a vN revision outside a linked project", Value: "SLUG"},
		},
	},
	{
		Name:     "deploy",
		DocSlug:  "deploy",
		Short:    "Deploy an app, function, or project",
		Examples: []string{"gregale deploy --plan", "gregale deploy --source=head --name my-api", "gregale deploy --path packages/api --source=worktree"},
		Flags: []cliFlag{
			{Name: "image", Short: "deploy from a container image reference", Value: "REF"},
			{Name: "tarball", Short: "deploy from a source tarball", Value: "PATH"},
			{Name: "path", Short: "deploy a selected local source directory (relative to the current directory)", Value: "DIR"},
			{Name: "source", Short: "local source policy (default: auto)", Value: "auto|head|worktree", ClosedSet: []string{"auto", "head", "worktree"}},
			{Name: "worktree", Short: "deploy the selected source directory from the working tree, including local changes"},
			{Name: "repo", Short: "deploy from a GitHub repo", Value: "OWNER/NAME"},
			{Name: "repository", Short: "GitHub owner/name to bind to a project", Value: "OWNER/NAME"},
			{Name: "install-id", Short: "GitHub installation id for a project binding", Value: "N"},
			{Name: "production-branch", Short: "production branch for a project binding", Value: "BRANCH"},
			// Issue #739 / ADR-092: --ref pairs with --repo to
			// drive the headless source-ref deploy (CI-friendly,
			// no install-token env). Required when --repo is set.
			{Name: "ref", Short: "git ref for --repo (branch, tag, or 40-char SHA)", Value: "REF"},
			{Name: "source-branch", Short: "reject promotion if the branch for a pinned --ref moves", Value: "BRANCH"},
			// Issue #270: --github emits a copy-paste Actions workflow
			// snippet; --pin-action can resolve the public Action tag.
			// The snippet uses --name / cwd as the app slug.
			{Name: "github", Short: "emit a GitHub Actions workflow snippet for the Gregale deploy action"},
			{Name: "pinned-sha", Short: "with --github only, pin the generated Action to this full 40-character commit SHA", Value: "SHA"},
			{Name: "pin-action", Short: "with --github only, resolve the current v0 Action tag to its commit SHA"},
			{Name: "template", Short: "scaffold from a built-in template", Value: "NAME", ClosedSet: templateNames13},
			{Name: "dockerfile", Short: "build with the supplied Dockerfile inside --tarball"},
			{Name: "runtime", Short: "function runtime", Value: "RUNTIME", ClosedSet: []string{"node22", "python312", "go124", "go124-alpine", "node24", "python313"}},
			{Name: "handler", Short: "function handler", Value: "HANDLER"},
			{Name: "name", Short: "app name (default: selected source directory, or current directory)", Value: "SLUG"},
			{Name: "profile", Short: "named app resource profile", Value: "PROFILE", ClosedSet: []string{"micro", "small", "medium", "large", "xlarge"}},
			{Name: "vcpu", Short: "assert the plan guest vCPU shape (omit to use the plan default)", Value: "N"},
			{Name: "execution-mode", Short: "app lifecycle mode", Value: "request|service|worker|job"},
			{Name: "restart-policy", Short: "app restart policy", Value: "no|on-failure|always|unless-stopped"},
			{Name: "startup-deadline-s", Short: "maximum startup seconds (0 uses plan default)", Value: "SECONDS"},
			{Name: "max-retries", Short: "maximum restart attempts (0 uses plan default)", Value: "N"},
			{Name: "function", Short: "deploy as a function; skip shape auto-detection"},
			{Name: "app", Short: "deploy as an app; skip shape auto-detection"},
			{Name: "yes", Short: "skip the apply confirmation prompt"},
			{Name: "only", Short: "workloads to apply; retain unselected project workloads (comma-separated)", Value: "SLUGS"},
			{Name: "project", Short: "deploy all detected workloads as one project (slug defaults from --name or source)"},
			{Name: "environment", Short: "deploy to a registered project environment (defaults to linked context)", Value: "SLUG"},
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
			{Name: "tag", Short: "annotation tag (" + strings.Join(DeploymentAnnotationTags, "|") + ")", Value: "TAG", ClosedSet: DeploymentAnnotationTags},
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
			{Name: "exclude", Short: "omit workloads (comma-separated slugs; cannot combine with --only)", Value: "SLUGS"},
			{Name: "show-affected", Short: "show workloads that deploy, stay unchanged, or are removed"},
			// ADR-124 follow-up #3 (PR-B commit 5): write-side
			// complement to --exclude. Records excluded slugs into
			// deployment_scope_exclusions on a successful apply so
			// subsequent deploys honor the persisted set automatically.
			{Name: "persist-exclude", Short: "save --exclude slugs for future project deploys"},
			{Name: "project-slug", Short: "kebab slug for the project (one-key provision)", Value: "SLUG"},
			{Name: "canary-preset", Short: "canary ladder preset", Value: "PRESET", ClosedSet: []string{"none", "slow", "balanced", "aggressive", "1-10-50-100", "custom"}},
			{Name: "canary-stages", Short: "custom percent@duration canary stages (requires --canary-preset custom)", Value: "STAGES"},
			{Name: "safe", Short: "deploy with the balanced health-gated rollout and first-wake 5xx rollback"},
			{Name: "require-authn", Short: "require bearer auth on every request"},
			{Name: "no-require-authn", Short: "drop the token requirement"},
			{Name: "platform-tenant-required", Short: "require verified customer identity on selected apps (Hobby and above)"},
			{Name: "no-platform-tenant-required", Short: "allow selected apps to receive traffic without customer identity"},
			{Name: "app-protocol", Short: "wire protocol selector", Value: "PROTOCOL", ClosedSet: []string{"http1", "http2", "grpc"}},
			{Name: "traffic-percent", Short: "deployment traffic split weight (0-100)", Value: "PERCENT"},
			{Name: "no-traffic", Short: "stage with 0% production traffic and print the preview URL"},
			{Name: "rollback-on-5xx", Short: "roll back after repeated first-wake 5xx responses"},
			{Name: "healthcheck-path", Short: "startup HTTP readiness path", Value: "PATH"},
			{Name: "healthcheck-grpc", Short: "use standard gRPC health for startup readiness"},
			{Name: "healthcheck-grpc-service", Short: "service name for --healthcheck-grpc; empty checks overall health", Value: "SERVICE"},
			{Name: "disable-startup-cpu-boost", Short: "disable temporary CPU boost during VM startup"},
			{Name: "no-triggers", Short: "skip gregale.yaml trigger and async-route changes"},
			{Name: "wait", Short: "wait for deployment to become live (default)"},
			{Name: "no-wait", Short: "return after deployment is queued"},
			{Name: "create-only", Short: "create or reserve the app without uploading a deployment"},
			{Name: "timeout", Short: "maximum wait seconds for deploy (default 1200)", Value: "SECONDS"},
			{Name: "idempotency-key", Short: "stable logical retry key for this deployment", Value: "KEY"},
			{Name: "secrets-file", Short: "seal KEY=VALUE pairs before the first deployment", Value: "PATH"},
			{Name: "secret-scan", Short: "scan .env files before packing", Value: "on|off", ClosedSet: []string{"on", "off"}},
			{Name: "diff", Short: "preview what would change without deploying"},
			{Name: "dry-run", Short: "run deploy preflight without uploading or changing remote state"},
			{Name: "plan", Short: "show local stateless app defaults without login or remote changes"},
			{Name: "strict", Short: "fail on diff schema/quota/env breaks"},
			{Name: "lenient", Short: "return success even when diff has breaks"},
			{Name: "server-diff", Short: "compute deploy diff on apid"},
			{Name: "doctor-strict", Short: "run doctor before deploy and abort on errors"},
			{Name: "no-doctor", Short: "skip the automatic local doctor preflight"},
		},
	},
	{
		Name:    "domains",
		DocSlug: "domains",
		Short:   "Manage custom domains",
		Subcommands: []cliSub{
			{Name: subList, Short: "List custom domain bindings"},
			{Name: subAdd, Short: "Bind a custom domain to an app or project environment", Positionals: []string{"[<domain>]"}, Flags: []cliFlag{
				{Name: "domain", Short: "domain to attach (or the first argument)", Value: "DOMAIN"},
				{Name: "app", Short: "app slug to attach to", Req: true, Value: "SLUG"},
				{Name: "environment", Short: "project environment to route this domain to", Value: "SLUG"},
			}},
			{Name: subRm, Short: "Remove a custom domain binding", Positionals: []string{"<domain>"}},
			{Name: subDomainsSetDefault, Short: "Set a verified domain as the app default", Positionals: []string{"<domain>"}},
			{Name: subDomainsVerify, Short: "Check DNS and certificate verification status; exits nonzero while pending", Positionals: []string{"<domain>"}},
			{Name: subDomainsShow, Short: "Show a domain's cert details", Positionals: []string{"<domain>"}},
			{Name: subDomainsStatus, Short: "Show durable TLS status for all domains"},
			{Name: subDomainsDoctor, Short: "5-check readiness report; exits nonzero when unhealthy, including in JSON mode", Positionals: []string{"<domain>"}},
		},
	},
	{
		Name:     "dev",
		DocSlug:  "dev",
		Short:    "Sync local changes to a developer environment",
		Examples: []string{"gregale dev --once", "gregale dev --path ./api --once"},
		Flags: []cliFlag{
			{Name: "path", Short: "source directory", Value: "DIR"},
			{Name: "name", Short: "developer-session project name", Value: "PROJECT"},
			{Name: "env-file", Short: "sync KEY=VALUE entries as developer secrets", Value: "PATH"},
			{Name: "service-override-file", Short: "sync validated service URLs as developer secrets", Value: "PATH"},
			{Name: "once", Short: "deploy once and exit"},
			{Name: "stop", Short: "tear down the developer environment"},
			{Name: "no-logs", Short: "do not attach the live runtime log stream"},
			{Name: "open", Short: "open the developer environment URL after the first live sync"},
		},
		Subcommands: []cliSub{
			{Name: "status", Short: "show developer-environment quota usage"},
			{Name: "bridge", Short: "run an HTTP service locally in a remote development environment", Flags: []cliFlag{
				{Name: "environment", Short: "named development environment", Value: "ENV"},
				{Name: "local-port", Short: "local HTTP service port", Value: "PORT"},
				{Name: "dependencies", Short: "comma-separated remote dependency apps", Value: "APPS"},
				{Name: "entrypoint", Short: "remote frontend for the session URL", Value: "APP"},
				{Name: "inspect", Short: "inspect recent requests in a local browser"},
				{Name: "replay-webhook", Short: "copy one provider-verified webhook receipt locally", Value: "INVOCATION"},
				{Name: "ready-path", Short: "check a local HTTP readiness path before attaching", Value: "PATH"},
				{Name: "bind-env", Short: "map ENV_KEY=dependency to its local proxy URL; repeatable", Value: "BINDING"},
			}, Subcommands: []cliSub{
				{Name: "list", Short: "list active account development bridges"},
				{Name: "status", Short: "inspect a session and recent request activity", Positionals: []string{"SESSION"}},
				{Name: "revoke", Short: "revoke a session and disconnect its laptop", Positionals: []string{"SESSION"}},
				{Name: "doctor", Short: "check admission, relay and local listener using a temporary session", Positionals: []string{"APP"}, Flags: []cliFlag{
					{Name: "environment", Short: "named development environment", Value: "ENV"},
					{Name: "local-port", Short: "local HTTP service port", Value: "PORT"},
					{Name: "entrypoint", Short: "remote frontend", Value: "APP"},
				}},
			}},
			{Name: "history", Short: "show edit-to-live timings and SLO guidance", Flags: []cliFlag{
				{Name: "path", Short: "source directory", Value: "DIR"},
				{Name: "name", Short: "developer-session project name", Value: "PROJECT"},
				{Name: "limit", Short: "number of recent syncs to show", Value: "N"},
			}},
			{Name: "setup", Short: "preflight a project and prepare the first developer environment", Flags: []cliFlag{
				{Name: "path", Short: "source directory", Value: "DIR"},
				{Name: "name", Short: "developer-session project name", Value: "PROJECT"},
				{Name: "env-file", Short: "validate and sync developer secrets", Value: "PATH"},
				{Name: "service-override-file", Short: "validate and sync service URLs", Value: "PATH"},
				{Name: "start", Short: "start after preflight"},
				{Name: "once", Short: "sync once and exit"},
				{Name: "no-logs", Short: "do not attach runtime logs"},
				{Name: "open", Short: "open the verified URL"},
				{Name: "postgres", Short: "provision an isolated PostgreSQL database"},
				{Name: "postgres-region", Short: "choose managed database placement", Value: "REGION"},
			}},
		},
	},
	{
		Name:        "diff",
		DocSlug:     "diff",
		Short:       "Compare two named environments in the linked project",
		Positionals: []string{"<from-environment>", "<to-environment>"},
		Flags: []cliFlag{
			{Name: "project", Short: "project slug (defaults to linked project)", Value: "SLUG"},
		},
	},
	{
		Name:     "test",
		DocSlug:  "test",
		Short:    "Run scenario suites, lifecycle profiles, and bounded local HTTP load tests",
		Examples: []string{"gregale test init --from openapi.yaml --project my-api", "gregale test import --from collection.json --project my-api", "gregale test --validate", "gregale test --suite smoke --engine local --fail-fast --junit test-results.xml", "gregale test --suite smoke --engine local --report test-results.json --junit test-results.xml --html test-results.html", "gregale test compare baseline.json current.json --budget test-budget.yaml --html comparison.html", "gregale test --suite regression --validate", "gregale test --scenario customer-export --preflight", "gregale test --scenario customer-export --profile restored --repeat 3 --max-workload-minutes 135 --report test-results.json --junit test-results.xml", "gregale test --scenario api-smoke --engine local", "gregale test --scenario api-smoke --engine local --load --baseline baseline.json --report current.json --junit current.xml", "gregale test --scenario customer-export --engine local --base-url http://localhost:3000 --data cases.json", "gregale test --scenario api-smoke --engine local --base-url http://localhost:3000 --load --vus 5 --duration 30s --pacing 100ms --progress", "gregale test --scenario api-smoke --engine local --load --rate 20 --duration 30s --vus 10 --progress", "gregale test --scenario customer-export --engine simulated"},
		Subcommands: []cliSub{{Name: "init", Short: "Create public GET smoke checks from a local OpenAPI document", Examples: []string{"gregale test init --from openapi.yaml --project my-api --source ."}, Flags: []cliFlag{
			{Name: "from", Short: "local OpenAPI 3.0 or 3.1 document", Value: "PATH", Req: true},
			{Name: "project", Short: "Gregale project slug", Value: "SLUG", Req: true},
			{Name: "source", Short: "application source directory", Value: "DIR"},
			{Name: "scenario", Short: "scenario name", Value: "NAME"},
			{Name: "output", Short: "new manifest path", Value: "PATH"},
		}}, {Name: "compare", Short: "Compare saved JSON run reports, enforce budgets, and write summaries", Positionals: []string{"<before.json>", "<after.json>"}, Examples: []string{"gregale test compare baseline.json current.json", "gregale test compare baseline.json current.json --budget test-budget.yaml --markdown comparison.md", "gregale test compare baseline.json current.json --budget test-budget.yaml --github-summary"}, Flags: []cliFlag{
			{Name: "budget", Short: "apply comparison budgets from a YAML file; fail if a check fails or is inconclusive", Value: "PATH"},
			{Name: "html", Short: "write a standalone HTML comparison report", Value: "PATH"},
			{Name: "markdown", Short: "write a Markdown comparison summary", Value: "PATH"},
			{Name: "github-summary", Short: "append a Markdown summary to GITHUB_STEP_SUMMARY"},
		}}, {Name: "ci", Short: "Set up GitHub Actions for scenario tests", Subcommands: []cliSub{{Name: "init", Short: "Generate GitHub Actions for local, simulated, or real-VM scenario tests", Examples: []string{"gregale test ci init --manifest gregale-test.yaml --suite smoke", "gregale test ci init --manifest gregale-test.yaml --suite smoke --baseline-max-age-days 14", "gregale test ci init --manifest tests/scenario-acceptance/gregale-test.yaml --suite real-vm --engine real-vm --profiles warm,cold,restored --max-workload-minutes 135"}, Flags: []cliFlag{
			{Name: "manifest", Short: "scenario manifest path", Value: "PATH"},
			{Name: "suite", Short: "suite from the manifest (inferred when exactly one exists)", Value: "NAME"},
			{Name: "engine", Short: "override suite engine for CI", Value: "ENGINE", ClosedSet: []string{"local", "simulated", "real-vm"}},
			{Name: "load", Short: "include local HTTP load execution"},
			{Name: "repeat", Short: "runs per scenario (default 1; default 3 with --load)", Value: "N"},
			{Name: "profiles", Short: "real-VM lifecycle profiles (all or comma-separated)", Value: "LIST"},
			{Name: "max-workload-minutes", Short: "required real-VM workload-minute limit per profile job", Value: "N"},
			{Name: "environment", Short: "GitHub environment containing real-VM credentials", Value: "NAME"},
			{Name: "branch", Short: "baseline branch for local and simulated workflows", Value: "NAME"},
			{Name: "baseline-max-age-days", Short: "maximum baseline artifact age (default 30; 0 disables age check)", Value: "N"},
			{Name: "workflow", Short: "new GitHub Actions workflow path", Value: "PATH"},
			{Name: "budget", Short: "comparison budget path for local and simulated workflows", Value: "PATH"},
		}}}}, {Name: "import", Short: "Create draft native requests from a local Postman Collection v2.1 export", Examples: []string{"gregale test import --from collection.json --project my-api", "gregale test import --from collection.json --project my-api --requests-only --status 202"}, Flags: []cliFlag{
			{Name: "from", Short: "local Postman Collection v2.1 JSON export", Value: "PATH", Req: true},
			{Name: "project", Short: "Gregale project slug", Value: "SLUG", Req: true},
			{Name: "source", Short: "command working directory", Value: "DIR"},
			{Name: "scenario", Short: "scenario name (default api-collection)", Value: "NAME"},
			{Name: "output", Short: "new manifest path", Value: "PATH"},
			{Name: "requests-only", Short: "explicitly omit Postman scripts; add their assertions and setup as native steps"},
			{Name: "status", Short: "fallback expected status without a unique saved response (default 200)", Value: "CODE"},
		}}},
		Flags: []cliFlag{
			{Name: "scenario", Short: "scenario declared in gregale-test.yaml", Value: "NAME"},
			{Name: "suite", Short: "named suite; run members sequentially in declaration order", Value: "NAME"},
			{Name: "fail-fast", Short: "stop after the first failed run and cleanup; report remaining runs as skipped"},
			{Name: "validate", Short: "validate local scenario sources without a platform login"},
			{Name: "preflight", Short: "check account entitlements and developer app capacity"},
			{Name: "engine", Short: "execution engine (default real-vm)", Value: "ENGINE", ClosedSet: []string{"real-vm", "local", "simulated"}},
			{Name: "base-url", Short: "HTTP loopback origin (optional with local.command)", Value: "URL"},
			{Name: "data", Short: "JSON or CSV case data for the local engine", Value: "PATH"},
			{Name: "load", Short: "repeat native HTTP journeys concurrently with the local engine"},
			{Name: "vus", Short: "concurrent users or arrival-rate concurrency cap (1..50, default 1)", Value: "N"},
			{Name: "rate", Short: "target journeys per second with --load and duration (1..1000)", Value: "N"},
			{Name: "iterations", Short: "total journeys for --load (1..10000, default 100)", Value: "N"},
			{Name: "duration", Short: "schedule journeys for this duration with --load (1s..5m)", Value: "DURATION"},
			{Name: "pacing", Short: "pause between each user's load journeys (0s..1m)", Value: "DURATION"},
			{Name: "progress", Short: "print live load progress to stderr"},
			{Name: "baseline", Short: "compare local load with a saved successful JSON report; apply regression budgets", Value: "PATH"},
			{Name: "profile", Short: "required lifecycle (default all)", Value: "PROFILE", ClosedSet: []string{"warm", "cold", "restored", "all"}},
			{Name: "repeat", Short: "runs per lifecycle profile or local case (1..20)", Value: "N"},
			{Name: "max-workload-minutes", Short: "abort if the estimated VM workload-minute ceiling exceeds N", Value: "N"},
			{Name: "manifest", Short: "scenario manifest path", Value: "PATH"},
			{Name: "report", Short: "write a JSON report", Value: "PATH"},
			{Name: "junit", Short: "write a JUnit XML report", Value: "PATH"},
			{Name: "html", Short: "write a standalone HTML report", Value: "PATH"},
		},
	},
	{
		Name:    "chaos",
		DocSlug: "chaos",
		Short:   "Inject bounded faults into isolated real-VM scenario tests",
		Subcommands: []cliSub{{Name: "inject", Short: "Run one scenario profile with a scoped service fault", Examples: []string{
			"gregale chaos inject --scenario customer-export --target inventory --error 503 --percent 10 --duration 5m",
			"gregale chaos inject --scenario customer-export --target payment --latency 1500ms --percent 20 --from worker --profile restored",
		}, Flags: []cliFlag{
			{Name: "scenario", Short: "scenario declared in gregale-test.yaml", Req: true, Value: "NAME"},
			{Name: "manifest", Short: "scenario manifest path", Value: "PATH"},
			{Name: "target", Short: "scenario service workload to affect", Req: true, Value: "SERVICE"},
			{Name: "from", Short: "only affect calls from this workload", Value: "SERVICE"},
			{Name: "latency", Short: "add this delay to selected requests, such as 1500ms", Value: "DURATION"},
			{Name: "error", Short: "return this synthetic HTTP 5xx status", Value: "CODE"},
			{Name: "percent", Short: "fraction of matching requests affected (1..100)", Value: "N"},
			{Name: "duration", Short: "maximum fault lease duration (1s..5m)", Value: "DURATION"},
			{Name: "profile", Short: "real-VM lifecycle profile", Value: "PROFILE", ClosedSet: []string{"warm", "cold", "restored"}},
			{Name: "seed", Short: "deterministic fault-selection seed", Value: "N"},
		}}},
	},
	{
		Name:    "preview",
		DocSlug: "preview",
		Short:   "Manage preview environments for pull requests",
		Subcommands: []cliSub{
			{Name: "create", Short: "Create and deploy a pull-request preview from a GitHub ref", Examples: []string{"gregale preview create --app my-api --repo acme/my-api --ref feature/cache --pr-number 42 --open", "gregale preview create --app my-api --repo acme/my-api --ref feature/cache --pr-number 42 --no-wait"}, Flags: []cliFlag{
				{Name: "app", Short: "parent app slug (defaults to the linked app)", Value: "slug"},
				{Name: "repo", Short: "GitHub repository OWNER/NAME", Req: true, Value: "OWNER/NAME"},
				{Name: "ref", Short: "branch, tag, or commit SHA", Req: true, Value: "REF"},
				{Name: "pr-number", Short: "pull-request number", Req: true, Value: "N"},
				{Name: "ttl-hours", Short: "preview lease in hours (default 168)", Value: "HOURS"},
				{Name: "wait", Short: "wait for the deployment to become live (default)"},
				{Name: "no-wait", Short: "return after the deployment is queued"},
				{Name: "timeout", Short: "deployment wait timeout in seconds", Value: "SECONDS"},
				{Name: "idempotency-key", Short: "stable retry key", Value: "KEY"},
				{Name: "open", Short: "open the preview URL after a successful create"},
			}},
			{Name: "list", Short: "List pull-request and developer previews (defaults to the linked app)", Examples: []string{"gregale preview list --app my-api", "gregale preview list"}, Flags: []cliFlag{
				{Name: "app", Short: "parent app slug", Value: "slug"},
			}},
			{Name: "show", Short: "Inspect a preview and its latest deployment", Examples: []string{"gregale preview show pr-42-my-api"}, Positionals: []string{"<preview-slug>"}},
			{Name: "report", Short: "Review deployment route changes, gateway rule drift, and available test/traffic evidence", Positionals: []string{"<preview-slug>"}, Examples: []string{"gregale preview report pr-42-my-api", "gregale preview report pr-42-my-api --format markdown --fail-on-breaking", "gregale preview report pr-42-my-api --test-report results.json --json", "gregale preview report pr-42-my-api --source-impact impact.json --test-report results.json --format markdown", "gregale preview report pr-42-my-api --fail-on-request-breaking --format markdown", "gregale preview report pr-42-my-api --fail-on-policy-drift --format markdown"}, Flags: []cliFlag{
				{Name: "format", Short: "report format: text or markdown (or use --json)", Value: "FORMAT"},
				{Name: "since", Short: "traffic lookback duration (default 24h)", Value: "DURATION"},
				{Name: "customer-details", Short: "include observed consumer and tenant IDs in the report"},
				{Name: "baseline-deployment", Short: "explicit parent deployment ID", Value: "ID"},
				{Name: "test-report", Short: "JSON receipts from gregale test", Value: "PATH"},
				{Name: "source-impact", Short: "route impact report from gregale routes impact", Value: "PATH"},
				{Name: "requirements", Short: "versioned route requirements YAML or JSON file", Value: "PATH"},
				{Name: "fail-on-breaking", Short: "exit 1 for known response-contract breaks"},
				{Name: "fail-on-request-breaking", Short: "exit 1 for known request-contract restrictions"},
				{Name: "fail-on-security-regression", Short: "exit 1 for known reductions in declared authentication requirements"},
				{Name: "fail-on-policy-drift", Short: "exit 1 for changed or incomplete route rule policy comparison"},
				{Name: "fail-on-incomplete", Short: "exit 1 when evidence is missing or needs review"},
				{Name: "fail-on-requirements", Short: "exit 1 for violated or unknown route requirements"},
			}},
			{Name: "review", Short: "Review route risk across multiple app previews in one release", Positionals: []string{"<preview-slug>..."}, Examples: []string{"gregale preview review pr-42-api pr-42-worker --format markdown", "gregale preview review pr-42-api pr-42-worker --source-impact pr-42-api=api-impact.json --source-impact pr-42-worker=worker-impact.json --json", "gregale preview review pr-42-api pr-42-worker --test-report pr-42-api=api-tests.json --fail-on-breaking --fail-on-incomplete"}, Flags: []cliFlag{
				{Name: "format", Short: "report format: text or markdown (or use --json)", Value: "FORMAT"},
				{Name: "since", Short: "traffic lookback duration (default 24h)", Value: "DURATION"},
				{Name: "customer-details", Short: "include observed consumer and tenant IDs in each app report"},
				{Name: "baseline-deployment", Short: "explicit parent deployment as PREVIEW=ID; repeat per preview", Value: "PREVIEW=ID"},
				{Name: "test-report", Short: "test receipts as PREVIEW=PATH; repeat per preview", Value: "PREVIEW=PATH"},
				{Name: "source-impact", Short: "route impact report as PREVIEW=PATH; repeat per preview", Value: "PREVIEW=PATH"},
				{Name: "requirements", Short: "route requirements as PREVIEW=PATH; repeat per preview", Value: "PREVIEW=PATH"},
				{Name: "fail-on-breaking", Short: "exit 1 if any app has known response-contract breaks"},
				{Name: "fail-on-request-breaking", Short: "exit 1 if any app has known request restrictions"},
				{Name: "fail-on-security-regression", Short: "exit 1 if any app reduces declared authentication requirements"},
				{Name: "fail-on-policy-drift", Short: "exit 1 if any app has changed or incomplete route policy comparison"},
				{Name: "fail-on-incomplete", Short: "exit 1 if any app report is unavailable or needs review"},
				{Name: "fail-on-requirements", Short: "exit 1 unless every app has satisfied route requirements"},
			}},
			{Name: "customers", Short: "Build customer impact rosters and track route migrations", Examples: []string{"gregale preview customers --report route-report.json --by consumer --format markdown", "gregale preview customers --report release-review.json --by tenant --format csv", "gregale preview customers track --roster customer-roster.json --mapping route-successors.json --deployment checkout=00000000-0000-4000-8000-000000000001"}, Flags: []cliFlag{
				{Name: "report", Short: "preview report or multi-app release review JSON with --customer-details", Value: "PATH", Req: true},
				{Name: "by", Short: "group by consumer (app scoped) or tenant (account scoped; default consumer)", Value: "consumer|tenant", ClosedSet: []string{"consumer", "tenant"}},
				{Name: "format", Short: "text, Markdown, or CSV output (default text; --json emits machine-readable JSON)", Value: "FORMAT", ClosedSet: []string{"text", "markdown", "csv"}},
				{Name: "out", Short: "write a machine-readable roster to a new JSON file", Value: "PATH"},
			}, Subcommands: []cliSub{{Name: "track", Short: "Compare a saved cohort with current route-customer telemetry", Examples: []string{"gregale preview customers track --roster customer-roster.json --mapping route-successors.json --deployment checkout=00000000-0000-4000-8000-000000000001 --since 14d --format markdown"}, Flags: []cliFlag{
				{Name: "roster", Short: "version 1 customer roster JSON produced by preview customers", Value: "PATH", Req: true},
				{Name: "mapping", Short: "version 1 explicit old-to-successor route mapping JSON", Value: "PATH", Req: true},
				{Name: "deployment", Short: "immutable current deployment as APP=ID; repeat for each app", Value: "APP=ID", Req: true, Repeatable: true},
				{Name: "since", Short: "post-release observation window (duration or RFC3339 timestamp; default 14d)", Value: "DURATION"},
				{Name: "format", Short: "text, Markdown, or CSV output (default text; --json emits machine-readable JSON)", Value: "FORMAT", ClosedSet: []string{"text", "markdown", "csv"}},
				{Name: "out", Short: "write the full machine-readable tracker to a new JSON file", Value: "PATH"},
			}}, {Name: "progress", Short: "Measure sustained old-route traffic and customer migration progress across saved tracker windows", Examples: []string{"gregale preview customers progress --snapshot migration-week-1.json --snapshot migration-week-2.json --grace-period 30d --format markdown"}, Flags: []cliFlag{
				{Name: "snapshot", Short: "saved route customer tracker JSON; repeat for each observation window", Value: "PATH", Req: true, Repeatable: true},
				{Name: "grace-period", Short: "minimum continuous zero-traffic period before owner review (default 30d)", Value: "DURATION"},
				{Name: "min-windows", Short: "minimum distinct complete observation windows (default 2)", Value: "COUNT"},
				{Name: "max-staleness", Short: "maximum age of the latest telemetry watermark (default 72h)", Value: "DURATION"},
				{Name: "format", Short: "text or Markdown output (default text; --json emits machine-readable JSON)", Value: "FORMAT", ClosedSet: []string{"text", "markdown"}},
				{Name: "out", Short: "write the full machine-readable progress report to a new JSON file", Value: "PATH"},
				{Name: "fail-on-incomplete", Short: "exit 1 when evidence is incomplete"},
				{Name: "fail-on-not-ready", Short: "exit 1 unless every route is ready for owner review"},
			}}, {Name: "migration", Short: "Join contract compatibility with customer cutover evidence", Subcommands: []cliSub{{Name: "review", Short: "Review contract compatibility and customer-by-customer route migration progress", Examples: []string{"gregale preview customers migration review --contract-review migration-review.json --snapshot migration-week-1.json --snapshot migration-week-2.json --grace-period 30d --format markdown", "gregale preview customers migration review --contract-review migration-review.json --snapshot migration-week-1.json --snapshot migration-week-2.json --format csv"}, Flags: []cliFlag{
				{Name: "contract-review", Short: "version 1 JSON from gregale routes migration review", Value: "PATH", Req: true},
				{Name: "snapshot", Short: "saved route customer tracker JSON; repeat for each observation window", Value: "PATH", Req: true, Repeatable: true},
				{Name: "grace-period", Short: "minimum continuous zero-traffic period before owner review (default 30d)", Value: "DURATION"},
				{Name: "min-windows", Short: "minimum distinct complete observation windows (default 2)", Value: "COUNT"},
				{Name: "max-staleness", Short: "maximum age of the latest telemetry watermark (default 72h)", Value: "DURATION"},
				{Name: "format", Short: "text, Markdown, or prioritized CSV action queue (default text; --json emits machine-readable JSON)", Value: "FORMAT", ClosedSet: []string{"text", "markdown", "csv"}},
				{Name: "out", Short: "write the full machine-readable cutover review to a new JSON file", Value: "PATH"},
				{Name: "fail-on-breaking", Short: "exit 1 when any mapped successor has a declared breaking change"},
				{Name: "fail-on-incomplete", Short: "exit 1 when contract or telemetry evidence is incomplete"},
				{Name: "fail-on-not-ready", Short: "exit 1 unless every route is ready for owner review"},
			}}, {Name: "diff", Short: "Compare customer migration evidence between two cutover reviews", Examples: []string{"gregale preview customers migration diff --before migration-last-week.json --after migration-today.json --fail-on-regression --format markdown"}, Flags: []cliFlag{
				{Name: "before", Short: "previous version 1 customer migration cutover review JSON", Value: "PATH", Req: true},
				{Name: "after", Short: "current version 1 customer migration cutover review JSON", Value: "PATH", Req: true},
				{Name: "format", Short: "text, Markdown, or CSV output (default text; --json emits machine-readable JSON)", Value: "FORMAT", ClosedSet: []string{"text", "markdown", "csv"}},
				{Name: "out", Short: "write the machine-readable migration diff to a new JSON file", Value: "PATH"},
				{Name: "fail-on-regression", Short: "exit 1 when confirmed customer migration regressions are found"},
			}}}}}},
			{Name: "wait", Short: "Wait for a preview deployment to become ready", Examples: []string{"gregale preview wait pr-42-my-api --progress --open"}, Positionals: []string{"<preview-slug>"}, Flags: []cliFlag{
				{Name: "progress", Short: "print deployment transitions while waiting"},
				{Name: "open", Short: "open the preview URL after it becomes ready"},
				{Name: "timeout", Short: "maximum wait (seconds, or a duration such as 10m)", Value: "SECONDS|DURATION"},
			}},
			{Name: "destroy", Short: "Tear down a preview app", Examples: []string{"gregale preview destroy pr-42-my-api"}, Positionals: []string{"<preview-slug>"}},
		},
	},
	{
		Name:    "tenant-surfaces",
		DocSlug: "tenant-surfaces",
		Short:   "Manage tenant surfaces (multi-hostname SAN bundle per app)",
		// The API remains behind FAAS_TENANT_SURFACES_ENABLED in production.
		// Keep the compatibility entry callable for prepared clusters, but do
		// not advertise it as a generally available customer feature.
		Audience: cliAudienceCompatibility,
		Subcommands: []cliSub{
			{Name: subList, Short: "List tenant surfaces on an app", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			}},
			{Name: subAdd, Short: "Add a tenant surface (with seed hostnames)", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "slug"}, {Name: "name", Short: "surface name", Req: true, Value: "name"}, {Name: "hostname", Short: "seed hostname (repeat)", Value: "h", Repeatable: true}}},
			{Name: subRm, Short: "Remove a tenant surface (cascades hostnames)", Positionals: []string{"<surface-id>"}, Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "slug"}}},
			{Name: "hostname", Short: "Manage hostnames on a surface", Subcommands: []cliSub{
				{Name: "add", Short: "Attach a hostname to a surface", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "surface", Short: "surface ID", Req: true, Value: "ID"}, {Name: "hostname", Short: "hostname to attach", Req: true, Value: "HOST"}}},
				{Name: "rm", Short: "Remove a hostname from a surface", Positionals: []string{"<hostname>"}, Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "surface", Short: "surface ID", Req: true, Value: "ID"}}},
			}},
		},
		Flags: []cliFlag{
			{Name: "app", Short: "app slug", Value: "slug"},
		},
	},
	{
		Name: "flags", DocSlug: "flags", Short: "Release application behavior to selected customers",
		Flags: []cliFlag{{Name: "project", Short: "project slug", Value: "slug", Req: true}, {Name: "environment", Short: "named environment (default production)", Value: "slug"}},
		Subcommands: []cliSub{
			{Name: "get", Short: "Read current flag configuration"},
			{Name: "apply", Short: "Publish a versioned configuration", Flags: []cliFlag{{Name: "file", Short: "JSON update bundle", Value: "path", Req: true}}},
			{Name: "history", Short: "List immutable configuration versions", Flags: []cliFlag{{Name: "before-version", Short: "page before this version", Value: "number"}}},
			{Name: "inspect", Short: "Explain a customer's decision", Flags: []cliFlag{{Name: "key", Short: "flag key", Value: "key", Req: true}, {Name: "customer-id", Short: "customer UUID", Value: "UUID"}, {Name: "subject-id", Short: "authenticated application subject (requires --customer-id)", Value: "ID"}, {Name: "fallback-variant", Short: "named-variant fallback", Value: "NAME"}, {Name: "version", Short: "historical configuration version", Value: "number"}}},
			{Name: "rollback", Short: "Publish an earlier configuration", Flags: []cliFlag{{Name: "version", Short: "version to restore", Value: "number", Req: true}, {Name: "expected-version", Short: "current version", Value: "number", Req: true}}},
			{Name: "requests", Short: "Inspect request evidence by flag value", Flags: []cliFlag{{Name: "key", Short: "flag key", Value: "key", Req: true}, {Name: "customer-id", Short: "customer UUID", Value: "UUID"}, {Name: "value", Short: "true or false", Value: "bool"}, {Name: "variant", Short: "filter by named variant", Value: "NAME"}, {Name: "used", Short: "true or false exposure", Value: "bool"}, {Name: "since", Short: "lookback (default 24h)", Value: "duration"}, {Name: "cursor", Short: "next-page cursor", Value: "cursor"}}},
			{Name: "outcomes", Short: "Inspect flag decision outcomes", Flags: []cliFlag{{Name: "key", Short: "flag key", Value: "key", Req: true}, {Name: "customer-id", Short: "customer UUID", Value: "UUID"}, {Name: "rule-id", Short: "filter to a targeting rule", Value: "ID"}, {Name: "config-version", Short: "filter to one configuration version", Value: "number"}, {Name: "since", Short: "lookback (default 24h)", Value: "duration"}}},
			{Name: "promote", Short: "Promote a targeting rule rollout", Flags: []cliFlag{{Name: "key", Short: "flag key", Value: "key", Req: true}, {Name: "rule-id", Short: "targeting rule ID", Value: "ID", Req: true}, {Name: "expected-version", Short: "current configuration version", Value: "number", Req: true}}},
		},
	},
	{
		Name:    "platform-tenants",
		DocSlug: "platform-tenants",
		Short:   "Manage one customer across app consumers and tenant hostnames",
		Subcommands: []cliSub{
			{Name: "list", Short: "List platform customers", Flags: []cliFlag{
				{Name: "limit", Short: "page size (1..100)", Value: "N"},
				{Name: "offset", Short: "page offset", Value: "N"},
			}},
			{Name: "add", Short: "Register a customer by external reference", Flags: []cliFlag{
				{Name: "external-ref", Short: "stable customer reference", Value: "REF", Req: true},
				{Name: "name", Short: "customer display name", Value: "TEXT", Req: true},
			}},
			{Name: "apply", Short: "Preview or apply an onboarding bundle", Flags: []cliFlag{
				{Name: "file", Short: "JSON onboarding bundle", Value: "path"},
				{Name: "dry-run", Short: "Preview without changes"},
			}},
			{Name: "credentials-list", Short: "List metadata for a customer's cross-app keys", Flags: []cliFlag{
				{Name: "id", Short: "platform tenant UUID", Value: "UUID", Req: true},
				{Name: "limit", Short: "page size (1..100)", Value: "number"},
				{Name: "offset", Short: "page offset", Value: "number"},
			}},
			{Name: "credentials-apply", Short: "Preview or apply hash-only key issuance and rotation", Flags: []cliFlag{
				{Name: "id", Short: "platform tenant UUID", Value: "UUID", Req: true},
				{Name: "file", Short: "hash-only credential bundle", Value: "path", Req: true},
				{Name: "dry-run", Short: "preview without changes"},
			}},
			{Name: "info", Short: "Show linked consumers and surfaces", Flags: []cliFlag{platformTenantIDFlag}},
			{Name: "activation", Short: "Show or wait for customer hostname activation", Flags: []cliFlag{
				{Name: "id", Short: "platform tenant UUID", Value: "UUID", Req: true},
				{Name: "wait", Short: "poll until all surfaces are ready"},
				{Name: "timeout", Short: "maximum wait (default 10m)", Value: "duration"},
			}},
			{Name: "link-consumer", Short: "Attach an existing app consumer", Flags: []cliFlag{
				platformTenantIDFlag,
				{Name: "consumer-id", Short: "existing app consumer UUID", Value: "UUID", Req: true},
			}},
			{Name: "link-surface", Short: "Attach an existing tenant surface", Flags: []cliFlag{
				platformTenantIDFlag,
				{Name: "surface-id", Short: "existing tenant surface UUID", Value: "UUID", Req: true},
			}},
			{Name: "usage", Short: "Show cross-app raw usage", Flags: []cliFlag{
				platformTenantIDFlag,
				{Name: "since", Short: "usage window start (RFC3339)", Value: "RFC3339"},
				{Name: "until", Short: "usage window end (RFC3339)", Value: "RFC3339"},
			}},
			{Name: "activity", Short: "Show recent request activity across linked apps", Flags: []cliFlag{
				platformTenantIDFlag,
				{Name: "since", Short: "lookback (e.g. 24h)", Value: "DURATION"},
				{Name: "app-id", Short: "filter to one linked app UUID", Value: "UUID"},
				{Name: "status", Short: "filter to one HTTP status (100..599)", Value: "N"},
				{Name: "cursor", Short: "opaque next-page cursor", Value: "CURSOR"},
				{Name: "limit", Short: "page size (1..200)", Value: "N"},
			}},
			{Name: "suspend", Short: "Stop linked credentials and hostnames", Flags: []cliFlag{platformTenantIDFlag}},
			{Name: "resume", Short: "Restore linked credentials and hostnames", Flags: []cliFlag{platformTenantIDFlag}},
		},
	},
	{
		Name:    "edge-rules",
		DocSlug: "edge-rules",
		Short:   "Per-app edge rules (edge-rules list|summary|trace|create|get|update --app <slug>; edge-rules rm <id>)",
		Subcommands: []cliSub{
			{Name: subList, Short: "List edge rules", Flags: []cliFlag{
				{Name: "app", Short: "filter to a single app slug", Value: "slug"},
				{Name: "kind", Short: "filter to a single kind", ClosedSet: edgeRuleKindVocab},
			}},
			{Name: "summary", Short: "Show what the edge rejected for an app: pre-auth blocks, validation failures, and gate rejections by status", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Value: "slug", Req: true},
				{Name: "range", Short: "time window (default 1h)", Value: "WINDOW", ClosedSet: appmetrics.Ranges()},
			}},
			{Name: "trace", Short: "Simulate composed edge-rule outcomes and budget, throttle, retry, circuit-breaker, and async-route policy; --config loads reusable JSON scenarios (see edge-rule-trace docs)", Flags: []cliFlag{
				{Name: "config", Short: "load a versioned JSON scenario (headers array; body or body_base64); - reads stdin and is exclusive with request flags", Value: "file|-"},
				{Name: "app", Short: "app slug (required unless --config is used)", Value: "slug"},
				{Name: "url", Short: "absolute HTTP(S) request URL (required unless --config is used)", Value: "URL"},
				{Name: "method", Short: "request method (default GET)", Value: "method"},
				{Name: "client-ip", Short: "simulated client IP for kind=ip rules", Value: "IP"},
				{Name: "country", Short: "simulated ISO alpha-2 country for kind=geo rules", Value: "CC"},
				{Name: "header", Short: "simulated request header; repeat for multiple values", Value: "Name:Value"},
				{Name: "body-file", Short: "request body file or - for stdin (max 1 MiB; contents are withheld)", Value: "path|-"},
			}},
			{Name: subCreate, Short: "Add an edge rule", Examples: []string{
				"gregale edge-rules create --app my-api --kind throttle --match-host my-api.gregale.dev --match-path /search --throttle-requests-per-second 5 --throttle-burst 10",
				"gregale edge-rules create --app my-api --kind redirect --match-host my-api.gregale.dev --match-path /old --redirect-status 308 --redirect-to https://my-api.gregale.dev/new",
				"gregale edge-rules create --app my-api --kind cache --match-host my-api.gregale.dev --match-path /catalog --cache-max-age-seconds 60",
				"gregale edge-rules create --app my-api --kind budget --match-host my-api.gregale.dev --match-path /reports --budget-ms 20000",
				"gregale edge-rules create --app my-api --kind validate --match-host api.example.com --validate-schema @schema.json --validate-content-type application/json --validate-mode block",
				"cat schema.json | gregale edge-rules create --app my-api --kind validate --match-host api.example.com --validate-schema -",
				`gregale edge-rules create --app my-api --kind validate --match-host api.example.com --match-path '/users/?*' --match-method GET --validate-path-template '/users/{id}' --validate-path-schema '{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}' --validate-query-schema @query.json`,
			}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Value: "slug", Req: true},
				{Name: "kind", Short: "rule kind", Value: "KIND", Req: true, ClosedSet: edgeRuleKindVocab},
				{Name: "match-host", Short: "host to match", Value: "HOST", Req: true},
				{Name: "match-path", Short: "path to match (default /)", Value: "PATH"},
				{Name: "match-method", Short: "HTTP method to match (repeat for multiple)", Value: "METHOD"},
				{Name: "match-header", Short: "exact request header selector (repeat)", Value: "Name=Value"},
				{Name: "priority", Short: "match priority; lower wins (default 100)", Value: "N"},
				{Name: "enabled", Short: "whether the rule is enabled (default true)", Bool: true, ClosedSet: []string{"true", "false"}},
				{Name: "throttle-requests-per-second", Short: "kind=throttle: refill rate in requests per second", Value: "RPS"},
				{Name: "throttle-burst", Short: "kind=throttle: token-bucket burst", Value: "N"},
				{Name: "throttle-key-by", Short: "kind=throttle: bucket key (none|api_key|consumer_id|jwt_subject|jwt_claim|country|ip)", Value: "KEY"},
				{Name: "redirect-status", Short: "kind=redirect: 301|302|307|308", Value: "CODE"},
				{Name: "redirect-to", Short: "kind=redirect: Location URL", Value: "URL"},
				{Name: "rewrite-from", Short: "kind=rewrite: from path", Value: "PATH"},
				{Name: "rewrite-to", Short: "kind=rewrite: to path", Value: "PATH"},
				{Name: "route-target-slug", Short: "kind=route: target app slug", Value: "slug"},
				{Name: "cache-max-age-seconds", Short: "kind=cache: fresh window (default 60; max 3600)", Value: "N"},
				{Name: "cache-stale-while-revalidate-seconds", Short: "kind=cache: serve stale during a background refresh (max 300)", Value: "N"},
				{Name: "budget-ms", Short: "kind=budget: per-request wall-clock budget in ms (max 30000)", Value: "MS"},
				{Name: "retry-max-attempts", Short: "kind=retry: total attempts including the original (default 2; max 3)", Value: "N"},
				{Name: "circuit-failure-threshold", Short: "kind=circuit_breaker: failure ratio that opens the app's instance-health breaker (default 0.5); the highest-priority rule tunes every instance, selectors do not partition it", Value: "RATIO"},
				{Name: "circuit-open-seconds", Short: "kind=circuit_breaker: first open interval (default 5)", Value: "N"},
				{Name: "respond-status", Short: "kind=respond: response status (default 200)", Value: "CODE"},
				{Name: "respond-body", Short: "kind=respond: JSON response body (max 64 KiB)", Value: "JSON"},
				{Name: "ip-allow", Short: "kind=ip: allow CIDR (repeat)", Value: "CIDR"},
				{Name: "ip-deny", Short: "kind=ip: deny CIDR (repeat)", Value: "CIDR"},
				{Name: "geo-allow", Short: "kind=geo: allow ISO country code (repeat)", Value: "CC"},
				{Name: "geo-deny", Short: "kind=geo: deny ISO country code (repeat)", Value: "CC"},
				{Name: "jwt-issuer", Short: "kind=jwt: token issuer", Value: "ISSUER"},
				{Name: "jwt-jwks-url", Short: "kind=jwt: JWKS URL (https)", Value: "URL"},
				{Name: "on-success-webhook", Short: "success webhook subscription; repeat when updating async policy", Value: "ID"},
				{Name: "on-failure-webhook", Short: "failure webhook subscription; repeat when updating async policy", Value: "ID"},
				{Name: "async-max-attempts", Short: "total attempts (0 = plan default; capped by plan)", Value: "N"},
				{Name: "async-retry-base-seconds", Short: "exponential retry base delay", Value: "N"},
				{Name: "async-retry-max-seconds", Short: "maximum exponential retry delay", Value: "N"},
				{Name: "async-retry-jitter-seconds", Short: "retry jitter fraction (0..1)", Value: "N"},
				{Name: "async-max-age-seconds", Short: "invocation lifetime from acceptance (0 = plan default; capped by plan)", Value: "N"},
				{Name: "waf-paranoia-level", Short: "kind=waf: OWASP CRS paranoia level 1 or 2 (default 1)", Value: "N"},
				{Name: "waf-anomaly-threshold", Short: "kind=waf: anomaly score that counts as a detection, 1..100 (default 5)", Value: "N"},
				{Name: "waf-exclude-rules", Short: "kind=waf: comma-separated CRS rule IDs left out of scoring", Value: "IDS"},
				{Name: "waf-inspect-body-bytes", Short: "kind=waf: request body bytes to inspect, 1..65536 (default 8192)", Value: "BYTES"},
				{Name: "validate-schema", Short: "JSON Schema (inline JSON, @file, or - for stdin; max 64 KiB)", Value: "JSON|@FILE|-"},
				{Name: "validate-mode", Short: "invalid-request behavior (default block)", Value: "MODE", ClosedSet: []string{api.ValidateModeBlock, api.ValidateModeObserve, api.ValidateModeWarn}},
				{Name: "validate-content-type", Short: "accepted application media type (repeat; e.g. application/json)", Value: "TYPE"},
				{Name: "validate-max-body-bytes", Short: "optional body cap in bytes (0 = plan default)", Value: "N"},
				{Name: "validate-apply-while-streaming", Short: "also validate streaming requests"},
				{Name: "validate-reject-unknown-fields", Short: "reject fields not declared by the schema"},
				{Name: "validate-path-template", Short: "OpenAPI path template for the path schema (match-path must use ?* for each placeholder)", Value: "TEMPLATE"},
				{Name: "validate-path-schema", Short: "object JSON Schema for path parameters (inline JSON, @file, or -)", Value: "JSON|@FILE|-"},
				{Name: "validate-query-schema", Short: "object JSON Schema for query parameters (inline JSON, @file, or -)", Value: "JSON|@FILE|-"},
				{Name: "validate-headers-schema", Short: "object JSON Schema for request headers, lowercase names (inline JSON, @file, or -)", Value: "JSON|@FILE|-"},
				{Name: "cors-allow-credentials", Short: "kind=cors: allow credentials"},
				{Name: "cors-max-age-seconds", Short: "kind=cors: preflight max age", Value: "SECONDS"},
				{Name: "jwt-platform-tenant-external-ref-claim", Short: "kind=jwt: claim containing the platform tenant external reference", Value: "CLAIM"},
				{Name: "limit-max-body-bytes", Short: "kind=limit: buffered body cap (required; 1..25 MiB)", Value: "BYTES"},
				{Name: "limit-max-body-bytes-streaming", Short: "kind=limit: streaming body cap (0 inherits buffered cap)", Value: "BYTES"},
				{Name: "throttle-jwt-claim", Short: "kind=throttle: JWT claim when key-by is jwt_claim", Value: "CLAIM"},
				{Name: "throttle-max-keys-per-rule", Short: "kind=throttle: maximum distinct consumer buckets", Value: "N"},
				{Name: "throttle-missing-key-policy", Short: "kind=throttle: behavior when identity is missing", Value: "POLICY", ClosedSet: []string{"shared", "reject"}},
				{Name: "cache-stale-if-error-seconds", Short: "kind=cache: serve stale on origin failure (max 300)", Value: "SECONDS"},
				{Name: "budget-allow-override-header", Short: "kind=budget: header allowed to override the budget", Value: "HEADER"},
				{Name: "retry-allow-non-idempotent", Short: "kind=retry: allow POST/PATCH replay when Idempotency-Key is honored"},
				{Name: "retry-min-remaining-ms", Short: "kind=retry: remaining request budget required before replay", Value: "MS"},
				{Name: "retry-backoff-ms", Short: "kind=retry: delay before replay", Value: "MS"},
				{Name: "retry-budget-percent", Short: "kind=retry: retry budget as percent of originals", Value: "PERCENT"},
				{Name: "retry-budget-min-retries", Short: "kind=retry: minimum retries allowed per window", Value: "N"},
				{Name: "circuit-min-requests", Short: "kind=circuit_breaker: observations before consulting the failure ratio", Value: "N"},
				{Name: "circuit-window-seconds", Short: "kind=circuit_breaker: rolling failure window", Value: "SECONDS"},
				{Name: "circuit-max-open-seconds", Short: "kind=circuit_breaker: maximum open interval", Value: "SECONDS"},
				{Name: "maintenance-retry-after-seconds", Short: "kind=maintenance: Retry-After hint", Value: "SECONDS"},
				{Name: "maintenance-message", Short: "kind=maintenance: operator message", Value: "TEXT"},
			}},
			{Name: subGet, Short: "Show one edge rule", Positionals: []string{"<id>"}},
			{Name: subUpdate, Short: "Update one edge rule", Positionals: []string{"<id>"}, Examples: []string{
				"gregale edge-rules update RULE_ID --kind validate --validate-schema @schema.json --validate-mode block",
				"gregale edge-rules update RULE_ID --kind validate --validate-mode observe",
			}, Flags: []cliFlag{
				{Name: "match-host", Short: "new host to match", Value: "HOST"},
				{Name: "match-path", Short: "new path to match", Value: "PATH"},
				{Name: "match-method", Short: "replacement HTTP method (repeatable)", Value: "METHOD", Repeatable: true},
				{Name: "match-header", Short: "replacement exact request header selector (repeatable)", Value: "NAME=VALUE", Repeatable: true},
				{Name: "clear-match-headers", Short: "remove all request header selectors"},
				{Name: "priority", Short: "new match priority", Value: "N"},
				{Name: "enable", Short: "enable the rule"},
				{Name: "disable", Short: "disable the rule"},
				{Name: "kind", Short: "new action kind (required when changing action flags)", Value: "KIND", ClosedSet: edgeRuleKindVocab},
				{Name: "route-target-slug", Short: "kind=route: target app slug", Value: "SLUG"},
				{Name: "rewrite-from", Short: "kind=rewrite: source path", Value: "PATH"},
				{Name: "rewrite-to", Short: "kind=rewrite: destination path", Value: "PATH"},
				{Name: "redirect-status", Short: "kind=redirect: response status", Value: "CODE"},
				{Name: "redirect-to", Short: "kind=redirect: Location URL", Value: "URL"},
				{Name: "redirect-header", Short: "kind=redirect: extra response header (repeatable)", Value: "NAME:VALUE", Repeatable: true},
				{Name: "headers-request-add", Short: "kind=headers: request header to add (repeatable)", Value: "NAME:VALUE", Repeatable: true},
				{Name: "headers-request-set", Short: "kind=headers: request header to set (repeatable)", Value: "NAME:VALUE", Repeatable: true},
				{Name: "headers-request-remove", Short: "kind=headers: request header to remove (repeatable)", Value: "NAME", Repeatable: true},
				{Name: "headers-response-add", Short: "kind=headers: response header to add (repeatable)", Value: "NAME:VALUE", Repeatable: true},
				{Name: "headers-response-set", Short: "kind=headers: response header to set (repeatable)", Value: "NAME:VALUE", Repeatable: true},
				{Name: "headers-response-remove", Short: "kind=headers: response header to remove (repeatable)", Value: "NAME", Repeatable: true},
				{Name: "cors-allow-origin", Short: "kind=cors: allowed origin (repeatable)", Value: "ORIGIN", Repeatable: true},
				{Name: "cors-allow-method", Short: "kind=cors: allowed method (repeatable)", Value: "METHOD", Repeatable: true},
				{Name: "cors-allow-header", Short: "kind=cors: allowed header (repeatable)", Value: "HEADER", Repeatable: true},
				{Name: "cors-expose-header", Short: "kind=cors: exposed header (repeatable)", Value: "HEADER", Repeatable: true},
				{Name: "cors-allow-credentials", Short: "kind=cors: allow credentials"},
				{Name: "cors-max-age-seconds", Short: "kind=cors: preflight max age", Value: "SECONDS"},
				{Name: "jwt-issuer", Short: "kind=jwt: token issuer", Value: "ISSUER"},
				{Name: "jwt-jwks-url", Short: "kind=jwt: JWKS URL", Value: "URL"},
				{Name: "jwt-audience", Short: "kind=jwt: required audience (repeatable)", Value: "AUDIENCE", Repeatable: true},
				{Name: "jwt-algorithm", Short: "kind=jwt: allowed signing algorithm (repeatable)", Value: "ALG", Repeatable: true},
				{Name: "jwt-required-claim", Short: "kind=jwt: required claim (repeatable)", Value: "NAME=VALUE", Repeatable: true},
				{Name: "jwt-platform-tenant-external-ref-claim", Short: "kind=jwt: claim containing the platform tenant external reference", Value: "CLAIM"},
				{Name: "ip-allow", Short: "kind=ip: allowed CIDR (repeatable)", Value: "CIDR", Repeatable: true},
				{Name: "ip-deny", Short: "kind=ip: denied CIDR (repeatable)", Value: "CIDR", Repeatable: true},
				{Name: "geo-allow", Short: "kind=geo: allowed country code (repeatable)", Value: "CC", Repeatable: true},
				{Name: "geo-deny", Short: "kind=geo: denied country code (repeatable)", Value: "CC", Repeatable: true},
				{Name: "limit-max-body-bytes", Short: "kind=limit: buffered body cap", Value: "BYTES"},
				{Name: "limit-max-body-bytes-streaming", Short: "kind=limit: streaming body cap (0 inherits buffered cap)", Value: "BYTES"},
				{Name: "throttle-requests-per-second", Short: "kind=throttle: refill rate", Value: "RPS"},
				{Name: "throttle-burst", Short: "kind=throttle: token-bucket burst", Value: "N"},
				{Name: "throttle-key-by", Short: "kind=throttle: bucket key", Value: "KEY", ClosedSet: []string{"none", "api_key", "consumer_id", "jwt_subject", "jwt_claim", "country"}},
				{Name: "throttle-jwt-claim", Short: "kind=throttle: JWT claim when key-by is jwt_claim", Value: "CLAIM"},
				{Name: "throttle-max-keys-per-rule", Short: "kind=throttle: maximum distinct consumer buckets", Value: "N"},
				{Name: "throttle-missing-key-policy", Short: "kind=throttle: behavior when identity is missing", Value: "POLICY", ClosedSet: []string{"shared", "reject"}},
				{Name: "cache-max-age-seconds", Short: "kind=cache: fresh window", Value: "SECONDS"},
				{Name: "cache-stale-while-revalidate-seconds", Short: "kind=cache: stale-while-revalidate window", Value: "SECONDS"},
				{Name: "cache-stale-if-error-seconds", Short: "kind=cache: serve stale on origin failure", Value: "SECONDS"},
				{Name: "cache-vary-on", Short: "kind=cache: header to vary on (repeatable)", Value: "HEADER", Repeatable: true},
				{Name: "cache-methods", Short: "kind=cache: cacheable method (repeatable)", Value: "METHOD", Repeatable: true},
				{Name: "budget-ms", Short: "kind=budget: per-request wall-clock budget", Value: "MS"},
				{Name: "budget-allow-override-header", Short: "kind=budget: header allowed to override the budget", Value: "HEADER"},
				{Name: "retry-max-attempts", Short: "kind=retry: total attempts including the original", Value: "N"},
				{Name: "retry-allow-non-idempotent", Short: "kind=retry: allow POST/PATCH replay when Idempotency-Key is honored"},
				{Name: "retry-min-remaining-ms", Short: "kind=retry: remaining budget required before replay", Value: "MS"},
				{Name: "retry-backoff-ms", Short: "kind=retry: delay before replay", Value: "MS"},
				{Name: "retry-budget-percent", Short: "kind=retry: retry budget as percent of originals", Value: "PERCENT"},
				{Name: "retry-budget-min-retries", Short: "kind=retry: minimum retries allowed per window", Value: "N"},
				{Name: "circuit-failure-threshold", Short: "kind=circuit_breaker: failure ratio that opens the breaker", Value: "RATIO"},
				{Name: "circuit-min-requests", Short: "kind=circuit_breaker: observations before consulting the ratio", Value: "N"},
				{Name: "circuit-window-seconds", Short: "kind=circuit_breaker: rolling failure window", Value: "SECONDS"},
				{Name: "circuit-open-seconds", Short: "kind=circuit_breaker: first open interval", Value: "SECONDS"},
				{Name: "circuit-max-open-seconds", Short: "kind=circuit_breaker: maximum open interval", Value: "SECONDS"},
				{Name: "maintenance-retry-after-seconds", Short: "kind=maintenance: Retry-After hint", Value: "SECONDS"},
				{Name: "maintenance-message", Short: "kind=maintenance: operator message", Value: "TEXT"},
				{Name: "respond-status", Short: "kind=respond: response status", Value: "CODE"},
				{Name: "respond-body", Short: "kind=respond: JSON response body", Value: "JSON"},
				{Name: "on-success-webhook", Short: "success webhook subscription", Value: "ID"},
				{Name: "on-failure-webhook", Short: "failure webhook subscription", Value: "ID"},
				{Name: "async-max-attempts", Short: "total attempts (0 = plan default; capped by plan)", Value: "N"},
				{Name: "async-retry-base-seconds", Short: "exponential retry base delay", Value: "N"},
				{Name: "async-retry-max-seconds", Short: "maximum exponential retry delay", Value: "N"},
				{Name: "async-retry-jitter-seconds", Short: "retry jitter fraction (0..1)", Value: "N"},
				{Name: "async-max-age-seconds", Short: "invocation lifetime from acceptance (0 = plan default; capped by plan)", Value: "N"},
				{Name: "waf-paranoia-level", Short: "kind=waf: OWASP CRS paranoia level 1 or 2 (default 1)", Value: "N"},
				{Name: "waf-anomaly-threshold", Short: "kind=waf: anomaly score that counts as a detection, 1..100 (default 5)", Value: "N"},
				{Name: "waf-exclude-rules", Short: "kind=waf: comma-separated CRS rule IDs left out of scoring", Value: "IDS"},
				{Name: "waf-inspect-body-bytes", Short: "kind=waf: request body bytes to inspect, 1..65536 (default 8192)", Value: "BYTES"},
				{Name: "validate-schema", Short: "replacement body schema; an action update replaces the body schema and parameter schemas together (inline JSON, @file, or -; max 64 KiB)", Value: "JSON|@FILE|-"},
				{Name: "validate-mode", Short: "invalid-request behavior", Value: "MODE", ClosedSet: []string{api.ValidateModeBlock, api.ValidateModeObserve, api.ValidateModeWarn}},
				{Name: "validate-content-type", Short: "accepted application media type (repeat; e.g. application/json)", Value: "TYPE"},
				{Name: "validate-max-body-bytes", Short: "body cap in bytes (0 = plan default)", Value: "N"},
				{Name: "validate-apply-while-streaming", Short: "also validate streaming requests"},
				{Name: "validate-reject-unknown-fields", Short: "reject fields not declared by the schema"},
				{Name: "validate-path-template", Short: "OpenAPI path template for the path schema (match-path must use ?* for each placeholder)", Value: "TEMPLATE"},
				{Name: "validate-path-schema", Short: "object JSON Schema for path parameters (inline JSON, @file, or -)", Value: "JSON|@FILE|-"},
				{Name: "validate-query-schema", Short: "object JSON Schema for query parameters (inline JSON, @file, or -)", Value: "JSON|@FILE|-"},
				{Name: "validate-headers-schema", Short: "object JSON Schema for request headers, lowercase names (inline JSON, @file, or -)", Value: "JSON|@FILE|-"},
			}},
			{Name: subRm, Short: "Delete one edge rule", Positionals: []string{"<id>"}},
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
			{Name: "diff", Short: "Diff two openapi.yaml files; exit 2 on any BREAKING row", Positionals: []string{"<baseline.yaml>", "<proposed.yaml>"}},
			{Name: "get", Short: "Fetch an app OpenAPI document (manual_import|auto; slug defaults to linked context)", Positionals: []string{"[<slug>]"}, Flags: []cliFlag{
				{Name: "source", Short: "document source", Value: "manual_import|auto", ClosedSet: []string{"manual_import", "auto"}},
			}},
			{Name: "import", Short: "Import an app OpenAPI document from a JSON file or stdin", Positionals: []string{"<slug>", "<file|->"}},
			{Name: "dry-run", Short: "Preview uncovered routes without importing the document (slug defaults to linked context)", Positionals: []string{"[<slug>]", "<file|->"}},
			{Name: "preview", Short: "Preview routes, edge policies, and the read-only OpenAPI contract diff (slug defaults to linked context)", Positionals: []string{"[<slug>]"}, Flags: []cliFlag{
				{Name: "scope", Short: "deployment scope to compare (defaults to linked environment, otherwise prod)", Value: "scope"},
				{Name: "fail-on-unavailable", Short: "fail when the contract-diff backend is unavailable"},
			}},
			{Name: "apply", Short: "Plan or apply generated validation edge rules", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "confirm", Short: "apply the reviewed plan"},
				{Name: "preview-sha256", Short: "approval hash from the plan", Value: "SHA256"},
				{Name: "match-host", Short: "hostname for generated rules", Value: "HOST"},
			}},
			{Name: "rm", Short: "Remove the imported app OpenAPI document", Positionals: []string{"<slug>"}},
		},
	},
	{
		Name: "routes", DocSlug: "cli", Short: "Analyze route changes, migrations, lifecycle and production policies",
		Positionals: []string{"[<slug>]"},
		Subcommands: []cliSub{{Name: "requirements", Short: "Save or read versioned route requirements for an app", Subcommands: []cliSub{
			{Name: "set", Positionals: []string{"<slug>"}, Short: "Save version 2 route intent after comparing the current revision", Examples: []string{"gregale routes requirements set my-api --requirements gregale-routes.yaml --expected-revision 0"}, Flags: []cliFlag{
				{Name: "requirements", Short: "version 2 requirements YAML or JSON file", Value: "PATH", Req: true},
				{Name: "expected-revision", Short: "current saved revision; use 0 for the first save", Value: "N", Req: true},
			}},
			{Name: "get", Positionals: []string{"<slug>"}, Short: "Read current route intent and optionally export requirements for planning", Flags: []cliFlag{
				{Name: "out", Short: "export normalized requirements JSON to a new file", Value: "PATH"},
			}},
		}}, {Name: "monitor", Short: "Monitor absolute route budgets after production promotion", Subcommands: []cliSub{
			{Name: "get", Positionals: []string{"<slug>"}, Short: "Read production route budgets and revision"},
			{Name: "preview", Positionals: []string{"<slug>"}, Short: "Evaluate proposed budgets against recent production traffic without saving them", Examples: []string{"gregale routes monitor preview my-api --routes production-routes.json --customer-group-by tenant"}, Flags: []cliFlag{
				{Name: "routes", Value: "PATH", Short: "JSON array of proposed exact route budgets", Req: true},
				{Name: "customer-group-by", Value: "DIMENSION", Short: "optionally evaluate budgets per tenant or consumer", ClosedSet: []string{"tenant", "consumer"}},
				{Name: "customer-details", Short: "include observed tenant or consumer IDs (when enabled)"},
				{Name: "fail-on-unhealthy", Short: "exit nonzero unless all proposed budgets are healthy"},
			}},
			{Name: "set", Positionals: []string{"<slug>"}, Short: "Save advisory production route budgets", Flags: []cliFlag{
				{Name: "mode", Value: "MODE", Short: "enabled or disabled", Req: true, ClosedSet: []string{"enabled", "disabled"}},
				{Name: "routes", Value: "PATH", Short: "JSON array of exact method/path labels with max_5xx_rate_bps and/or max_p95_ms", Req: true},
				{Name: "expected-revision", Value: "N", Short: "current monitor revision; 0 initially", Req: true},
			}},
			{Name: "report", Positionals: []string{"<slug>"}, Short: "Read observed health for the fully serving production deployment", Flags: []cliFlag{{Name: "fail-on-unhealthy", Short: "exit nonzero unless every selected budget is healthy"}}},
			{Name: "incidents", Positionals: []string{"<slug>"}, Short: "List retained production route incidents", Flags: []cliFlag{
				{Name: "limit", Value: "N", Short: "page size (default 5; maximum 10)"},
				{Name: "before", Value: "ID", Short: "page before a retained incident UUID"},
			}},
			{Name: "explain", Positionals: []string{"<slug>"}, Short: "Inspect a saved incident and optionally correlate affected customers and changed route owners", Examples: []string{"gregale routes monitor explain api --incident INCIDENT_UUID --source-impact auto"}, Flags: []cliFlag{
				{Name: "incident", Value: "ID", Short: "saved incident UUID", Req: true},
				{Name: "out", Value: "PATH", Short: "save incident evidence JSON to a new file"},
				{Name: "source-impact", Value: "PATH|auto", Short: "correlate source, aggregate customer impact, and candidate-commit CODEOWNERS from the matching local repository"},
			}},
		}}, {Name: "lifecycle", Short: "Review deployed routes for carefully evidenced retirement candidates", Subcommands: []cliSub{
			{Name: "review", Positionals: []string{"<slug>"}, Short: "Compare captured routes, observed usage, source and requirements", Examples: []string{"gregale routes lifecycle review api --deployment DEPLOYMENT_UUID --since 14d --source-impact impact.json --out lifecycle-review.json"}, Flags: []cliFlag{
				{Name: "deployment", Value: "ID", Short: "immutable deployed contract UUID", Req: true},
				{Name: "since", Value: "WINDOW", Short: "route-usage window (default 14d; plan retention may clamp it)"},
				{Name: "source-impact", Value: "PATH", Short: "complete source impact report whose candidate revision matches the deployment commit"},
				{Name: "out", Value: "PATH", Short: "save full JSON review to a new file"},
				{Name: "fail-on-incomplete", Short: "exit nonzero when any route remains inconclusive"},
			}},
		}}, {Name: "migration", Short: "Check mapped route successors against immutable deployment contracts", Subcommands: []cliSub{
			{Name: "review", Short: "Compare method, path parameters, request, response and security contracts", Examples: []string{"gregale routes migration review --mapping route-successors.json --from-deployment checkout=OLD_DEPLOYMENT --to-deployment checkout=NEW_DEPLOYMENT --format markdown --out migration-review.json"}, Flags: []cliFlag{
				{Name: "mapping", Short: "version 1 explicit old-to-successor route mapping JSON", Value: "PATH", Req: true},
				{Name: "from-deployment", Short: "immutable baseline deployment as APP=ID; repeat for each app", Value: "APP=ID", Req: true, Repeatable: true},
				{Name: "to-deployment", Short: "immutable successor deployment as APP=ID; repeat for each app", Value: "APP=ID", Req: true, Repeatable: true},
				{Name: "format", Short: "text or Markdown output (default text; --json emits machine-readable JSON)", Value: "FORMAT", ClosedSet: []string{"text", "markdown"}},
				{Name: "out", Short: "save the full JSON review to a new file", Value: "PATH"},
				{Name: "fail-on-breaking", Short: "exit nonzero when any successor has a declared breaking change"},
				{Name: "fail-on-incomplete", Short: "exit nonzero when any route lacks complete contract evidence"},
			}},
		}}, {Name: "health", Short: "Compare critical route errors and optional p95 latency to gate canary progression", Subcommands: []cliSub{
			{Name: "get", Positionals: []string{"<slug>"}, Short: "Read selected routes, mode and revision"},
			{Name: "profile-history", Positionals: []string{"<slug>"}, Short: "Read retained CPU profile assessments for canary stages", Flags: []cliFlag{
				{Name: "deployment", Value: "ID", Short: "canary deployment UUID", Req: true},
				{Name: "limit", Value: "N", Short: "history page size (default 5; maximum 10)"},
				{Name: "before", Value: "CURSOR", Short: "opaque cursor from the prior page"},
			}},
			{Name: "set", Positionals: []string{"<slug>"}, Short: "Save exact normalized telemetry route selectors", Flags: []cliFlag{
				{Name: "routes", Value: "PATH", Short: "JSON array of method/path selectors with optional latency checks and advisory watch_statuses", Req: true},
				{Name: "mode", Value: "MODE", Short: "report (enforcement unavailable in preview)", Req: true, ClosedSet: []string{"report", "enforce"}},
				{Name: "on-regression", Value: "ACTION", Short: "hold (default) or automatically abort on confirmed route 5xx regression", ClosedSet: []string{"hold", "abort"}},
				{Name: "expected-revision", Value: "N", Short: "current revision; 0 initially", Req: true},
			}},
			{Name: "suggest", Positionals: []string{"<slug>"}, Short: "Rank observed routes by customer reach and traffic; emit reviewable canary selectors", Flags: []cliFlag{
				{Name: "deployment", Value: "ID", Short: "immutable baseline deployment UUID; must match --preview-report when supplied", Req: true},
				{Name: "since", Value: "WINDOW", Short: "observed usage window (default 168h; accepts 7d or RFC3339)"},
				{Name: "customer-group-by", Value: "DIMENSION", Short: "rank by distinct tenant (default) or consumer count", ClosedSet: []string{"tenant", "consumer"}},
				{Name: "limit", Value: "N", Short: "number of route selectors (default 10; maximum 20)"},
				{Name: "preview-report", Value: "PATH", Short: "restrict suggestions to source-affected routes in a bound preview report for this app and deployment"},
				{Name: "out", Value: "PATH", Short: "save a JSON selector array ready for routes health set"},
			}},
			{Name: "review", Positionals: []string{"<slug>"}, Short: "Join source-affected routes to configured canary coverage and candidate health evidence", Flags: []cliFlag{
				{Name: "deployment", Value: "ID", Short: "candidate deployment UUID currently receiving canary traffic", Req: true},
				{Name: "preview-report", Value: "PATH", Short: "bound preview report containing source-affected routes", Req: true},
				{Name: "fail-on-incomplete", Short: "exit nonzero unless source impact is complete and all affected route health is ready"},
			}},
			{Name: "review-release", Short: "Gate every app in an aggregate preview review on source-affected canary route health", Examples: []string{"gregale routes health review-release --release-report release-review.json --fail-on-incomplete --json"}, Flags: []cliFlag{
				{Name: "release-report", Value: "PATH", Short: "aggregate JSON report from gregale preview review", Req: true},
				{Name: "fail-on-incomplete", Short: "exit nonzero unless every app has complete, healthy affected-route coverage"},
			}},
			{Name: "report", Positionals: []string{"<slug>"}, Short: "Read candidate/stable counts, selected p95 checks and route verdicts", Flags: []cliFlag{
				{Name: "deployment", Value: "ID", Short: "candidate deployment UUID", Req: true},
				{Name: "fail-on-unhealthy", Short: "exit nonzero unless every selected route is healthy"},
				{Name: "customers", Short: "include advisory customer health comparisons"},
				{Name: "customer-group-by", Value: "DIMENSION", Short: "tenant (default) or consumer; requires --customers", ClosedSet: []string{"tenant", "consumer"}},
				{Name: "customer-details", Short: "include customer IDs; requires --customers"},
			}},
			{Name: "investigate", Positionals: []string{"<slug>"}, Short: "Investigate route errors or latency with bounded retained evidence", Flags: []cliFlag{
				{Name: "deployment", Value: "ID", Short: "candidate deployment UUID", Req: true},
				{Name: "route", Value: "LABEL", Short: "exact configured METHOD /path telemetry label", Req: true},
				{Name: "source-impact", Value: "PATH", Short: "correlate a local route impact report with both deployment revisions"},
				{Name: "signal", Value: "SIGNAL", Short: "errors (default) or latency; requires a configured latency check", ClosedSet: []string{"errors", "latency"}},
				{Name: "status", Value: "CODE", Short: "watched 4xx code; 0 (default) selects all 5xx"},
				{Name: "customer-id", Value: "ID", Short: "recorded customer UUID; explicitly includes this ID"},
				{Name: "customer-group-by", Value: "DIMENSION", Short: "tenant (default) or consumer; requires --customer-id", ClosedSet: []string{"tenant", "consumer"}},
				{Name: "out", Value: "PATH", Short: "save the investigation JSON to a new file"},
			}},
			{Name: "correlate", Positionals: []string{"<slug>"}, Short: "Find dependency slowdowns shared by multiple configured latency routes", Examples: []string{"gregale routes health correlate api --deployment CANDIDATE_UUID --json"}, Flags: []cliFlag{
				{Name: "deployment", Value: "ID", Short: "candidate deployment UUID", Req: true},
				{Name: "limit", Value: "N", Short: "shared dependency groups to show (default 10; maximum 20)"},
				{Name: "out", Value: "PATH", Short: "save correlation JSON to a new file"},
			}},
			{Name: "explain", Positionals: []string{"<slug>"}, Short: "Explain saved canary health decisions and their evidence timeline", Flags: []cliFlag{
				{Name: "deployment", Value: "ID", Short: "candidate deployment UUID", Req: true},
				{Name: "decision", Value: "ID", Short: "read one saved decision UUID"},
				{Name: "limit", Value: "N", Short: "timeline page size (default 5; maximum 10)"},
				{Name: "before", Value: "ID", Short: "page before a retained decision UUID"},
			}},
		}}, {Name: "gate", Short: "Read or change the canary route enforcement mode", Subcommands: []cliSub{
			{Name: "get", Positionals: []string{"<slug>"}, Short: "Read the current gate mode and revision", Examples: []string{"gregale routes gate get my-api --json"}},
			{Name: "set", Positionals: []string{"<slug>"}, Short: "Change report or enforce mode using the current gate revision", Examples: []string{"gregale routes gate set my-api --mode enforce --expected-revision 0"}, Flags: []cliFlag{
				{Name: "mode", Value: "MODE", Short: "report (enforcement unavailable in preview)", Req: true, ClosedSet: []string{"report", "enforce"}},
				{Name: "expected-revision", Value: "N", Short: "current gate revision; 0 initially", Req: true},
			}},
		}}, {Name: "results", Positionals: []string{"<slug>"}, Short: "Read the latest automatic check with current freshness", Examples: []string{"gregale routes results my-api --deployment DEPLOYMENT_ID --wait --fail-on-requirements --json", "gregale routes results my-api --deployment DEPLOYMENT_ID --refresh --wait"}, Flags: []cliFlag{
			{Name: "changes", Short: "show finding changes against prior known evidence"},
			{Name: "deployment", Short: "app-owned deployment UUID", Value: "ID", Req: true},
			{Name: "expected-revision", Short: "require this current saved intent revision", Value: "N"},
			{Name: "refresh", Short: "queue a new check of current configuration"},
			{Name: "wait", Short: "wait for pending work to complete"},
			{Name: "timeout", Short: "maximum wait duration (default 2m; at most 10m)", Value: "DURATION"},
			{Name: "out", Short: "export the result to a new JSON file", Value: "PATH"},
			{Name: "fail-on-requirements", Short: "require completed, current and satisfied evidence"},
		}}, {Name: "check", Positionals: []string{"<slug>"}, Short: "Check saved requirements against a captured deployment and current app policy", Examples: []string{"gregale routes check my-api --deployment DEPLOYMENT_ID --expected-revision 1 --fail-on-requirements --json"}, Flags: []cliFlag{
			{Name: "deployment", Short: "app-owned captured deployment UUID", Value: "ID", Req: true},
			{Name: "expected-revision", Short: "fail if the saved revision differs", Value: "N"},
			{Name: "format", Short: "human or markdown", Value: "FORMAT"},
			{Name: "out", Short: "save the JSON check to a new file", Value: "PATH"},
			{Name: "fail-on-requirements", Short: "exit 1 for violations or incomplete inventory evidence"},
		}}, {Name: "plan", Positionals: []string{"<slug>"}, Short: "Plan throttle and budget patches with concrete or captured-family coverage", Examples: []string{
			"gregale routes plan my-api --requirements gregale-routes.yaml --out route-plan.json",
			"gregale routes plan my-api --saved --deployment DEPLOYMENT_ID --expected-revision 1 --out repair.json",
			"gregale routes plan pr-42-api --requirements gregale-routes.yaml --throttle-burst 20 --fail-on-unresolved --json",
		}, Flags: []cliFlag{
			{Name: "requirements", Short: "versioned route requirements YAML or JSON; mutually exclusive with --saved", Value: "PATH"},
			{Name: "saved", Short: "use the app's saved requirements and bind their revision"},
			{Name: "expected-revision", Short: "require this saved requirements revision; requires --saved", Value: "N"},
			{Name: "out", Short: "save JSON plan to a new owner-readable file", Value: "PATH"},
			{Name: "throttle-burst", Short: "burst for new throttles without an existing policy", Value: "N"},
			{Name: "deployment", Short: "captured deployment UUID required for version 2 groups", Value: "ID"},
			{Name: "consolidate-budgets", Short: "combine compatible budgets within declared group prefixes, including uncaptured paths"},
			{Name: "fail-on-unresolved", Short: "exit 1 when requirements remain unresolved after proposed changes"},
		}}, {Name: "apply", Positionals: []string{"<slug>"}, Short: "Atomically apply a reviewed server plan and recover its durable receipt", Examples: []string{
			"gregale routes apply my-api --plan route-plan.json --confirm",
		}, Flags: []cliFlag{
			{Name: "plan", Short: "reviewed version 2 or 3 server plan JSON file", Value: "PATH", Req: true},
			{Name: "confirm", Short: "confirm application of every reviewed change", Req: true},
			{Name: "idempotency-key", Short: "stable retry key, defaults to plan SHA-256", Value: "KEY"},
		}}, {Name: "impact", Positionals: []string{"[<slug>]"}, Short: "Explain FastAPI, Go HTTP, Express, or Hono route impact between Git revisions", Examples: []string{
			"gregale routes impact my-api --base origin/main --path . --entrypoint main:app",
			"gregale routes impact --base HEAD~1 --head HEAD --format markdown",
			"gregale routes impact --base origin/main --framework go-nethttp --path services/api --json",
			"gregale routes impact --base origin/main --framework node-http --path services/node-api --json",
			"gregale routes impact --base origin/main --fail-on-impact --fail-on-incomplete --json",
		}, Flags: []cliFlag{
			{Name: "base", Short: "baseline Git revision", Value: "REF", Req: true},
			{Name: "head", Short: "candidate Git revision (defaults to working tree)", Value: "REF"},
			{Name: "path", Short: "application source directory inside the repository", Value: "DIR"},
			{Name: "framework", Short: "source framework: auto, fastapi, go-nethttp, or node-http", Value: "FRAMEWORK", ClosedSet: []string{"auto", "fastapi", "go-nethttp", "node-http"}},
			{Name: "entrypoint", Short: "FastAPI module:variable (inferred when exactly one exists)", Value: "MODULE:VARIABLE"},
			{Name: "format", Short: "report format", Value: "text|markdown", ClosedSet: []string{"text", "markdown"}},
			{Name: "out", Short: "save JSON report to a new owner-readable file", Value: "PATH"},
			{Name: "fail-on-impact", Short: "exit 1 when routes were added, removed, or may be affected"},
			{Name: "fail-on-incomplete", Short: "exit 1 when static analysis is incomplete"},
		}}, {Name: "contract", Short: "Check statically discovered source routes against a local OpenAPI contract", Subcommands: []cliSub{{
			Name: "check", Positionals: []string{"[<slug>]"}, Short: "Find source-only and contract-only routes with conservative parameter matching",
			Examples: []string{
				"gregale routes contract check my-api --openapi openapi.yaml --base origin/main --fail-on-drift --json",
				"gregale routes contract check --openapi openapi.yaml --base HEAD --path services/api --framework go-nethttp --format markdown",
			}, Flags: []cliFlag{
				{Name: "openapi", Short: "local OpenAPI 3.0 or 3.1 document", Value: "FILE", Req: true},
				{Name: "base", Short: "baseline Git revision", Value: "REF", Req: true},
				{Name: "head", Short: "candidate Git revision (defaults to working tree)", Value: "REF"},
				{Name: "path", Short: "application source directory inside the repository", Value: "DIR"},
				{Name: "framework", Short: "source framework: auto, fastapi, go-nethttp, or node-http", Value: "FRAMEWORK", ClosedSet: []string{"auto", "fastapi", "go-nethttp", "node-http"}},
				{Name: "entrypoint", Short: "FastAPI module:variable (inferred when exactly one exists)", Value: "MODULE:VARIABLE"},
				{Name: "format", Short: "report format", Value: "text|markdown", ClosedSet: []string{"text", "markdown"}},
				{Name: "out", Short: "save JSON report to a new owner-readable file", Value: "PATH"},
				{Name: "fail-on-drift", Short: "exit 1 for confirmed source-only or contract-only routes"},
				{Name: "fail-on-incomplete", Short: "exit 1 when route or OpenAPI analysis is inconclusive"},
			},
		}}}},
	},
	{
		Name:    "env",
		DocSlug: "env",
		Short:   "Clone project environments or manage app runtime env/secrets",
		Flags:   []cliFlag{{Name: "app", Short: "app slug (defaults to linked context)", Value: "slug"}},
		Subcommands: []cliSub{
			{Name: "create", Short: "Clone a project environment with isolated managed data by default; full-copy admission currently returns environment_full_clone_unavailable with named blockers", Positionals: []string{"<stage>"}, Examples: []string{"gregale env create staging --from production --full --wait"}, Flags: []cliFlag{
				{Name: "from", Short: "source environment", Value: "ENV", Req: true},
				{Name: "project", Short: "project slug (defaults to linked project)", Value: "SLUG"},
				{Name: "protected", Short: "protect the new environment"},
				{Name: "share-resources", Short: "use source managed data with fresh target credentials instead of isolating it"},
				{Name: "full", Short: "require complete configuration, workloads, policies and isolated data coverage; never fall back to a partial clone"},
				{Name: "wait", Short: "wait for a full clone operation to finish (requires --full)"},
				{Name: "timeout", Value: "SECONDS", Short: "maximum local wait time; the durable server operation continues after timeout"},
			}},
			{Name: "clone-status", Short: "Read durable clone progress before or after the target exists; timeout exits 3 with a resume command, failed or compensated operations exit 1", Positionals: []string{"<operation-id>"}, Examples: []string{"gregale env clone-status <operation-id> --project shop --wait"}, Flags: []cliFlag{
				{Name: "project", Value: "SLUG", Short: "project slug (defaults to linked project)"},
				{Name: "wait", Short: "wait for the same durable clone operation to finish"},
				{Name: "timeout", Value: "SECONDS", Short: "maximum local wait time"},
			}},
			{Name: "pull", Short: "Pull sealed-secret keys to a .env skeleton (values blank)", Examples: []string{"gregale env pull --app my-api", "gregale env pull --app my-api --scope staging"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug (defaults to linked context)", Value: "slug"},
				{Name: "scope", Short: "env scope (defaults to linked project environment)", Value: "SCOPE"},
				{Name: "o", ShortName: "o", Short: "output file (default .env)", Value: "PATH"},
			}},
			{Name: "push", Short: "Push KEY=VALUE pairs to sealed secrets (use --restart to apply now)", Examples: []string{"printf 'LOG_LEVEL=info\\n' | gregale env push --app my-api --from-stdin", "gregale env push --app my-api --restart"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug (defaults to linked context)", Value: "slug"},
				{Name: "scope", Short: "env scope (defaults to linked project environment)", Value: "SCOPE"},
				{Name: "f", ShortName: "f", Short: "input file (default .env)", Value: "PATH"},
				{Name: "from-stdin", Short: "read KEY=VALUE pairs from stdin"},
				{Name: "restart", Short: "restart app after applying changes (otherwise changes apply on next cold wake)"},
				{Name: "secret-scan", Short: "scan pairs before pushing", Value: "MODE", ClosedSet: []string{"on", "off", "strict", "source-tree"}},
			}},
			{Name: "diff", Short: "Render the env-diff matrix (presence / value-equality across scopes)", Examples: []string{"gregale env diff --app my-api", "gregale env diff --app my-api --json"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug (defaults to linked context)", Value: "slug"},
			}},
		},
	},
	{
		Name:     "init",
		DocSlug:  "init",
		Short:    "Scaffold a project from a built-in template",
		Examples: []string{"gregale init --list", "gregale init --template hello-node --path ./my-api"},
		Flags: []cliFlag{
			{Name: "template", Short: "template name", Req: true, Value: "NAME", ClosedSet: templateNames13},
			{Name: "path", Short: "target directory", Req: true, Value: "DIR"},
			{Name: "deploy", Short: "deploy after scaffolding"},
			{Name: "name", Short: "app slug used with --deploy", Value: "SLUG"},
			{Name: "secrets-file", Short: "seal KEY=VALUE pairs before the first deployment (requires --deploy)", Value: "PATH"},
			{Name: "list", Short: "list available templates"},
		},
	},
	{
		Name:        dispatchInspect,
		DocSlug:     "inspect",
		Short:       "Explain an app from its runtime, deployment, API, data, scaling, and release signals (slug defaults to linked context)",
		Examples:    []string{"gregale inspect my-api", "gregale inspect my-api --watch", "gregale inspect my-api --watch --interval 5s --timeout 10m --json", "gregale inspect my-api --upstreams"},
		Positionals: []string{"[<slug>]"},
		// Leaf-selectors are flags on this verb, not positional
		// sub-verbs (issue #952 UX: `gregale inspect <slug>
		// --upstreams`). The bare form renders the application-
		// intelligence summary. Future leaves (--env, --crons,
		// --instances) add another `cliFlag` entry below. The
		// completion backend and man-page renderer read this
		// Flags block to surface the right verb shape.
		Flags: []cliFlag{
			{Name: "upstreams", Short: "List data upstreams captured for this app"},
			{Name: "scope", Short: "filter by scope (defaults to linked project environment; used with --upstreams)", Value: "scope"},
			{Name: "errors", Short: "show the latest failed deployment's persisted error explanation"},
			{Name: "watch", Short: "watch summary changes using read-only requests; incompatible with --upstreams and --errors"},
			{Name: "interval", Short: "time between watch reads (default 5s; 1s..1h); requires --watch", Value: "DURATION"},
			{Name: "timeout", Short: "watch duration (default 0: until Ctrl-C); requires --watch", Value: "DURATION"},
			{Name: "json", Short: "print summary JSON, or JSON Lines events with --watch"},
		},
	},
	{
		Name:    "invoke",
		DocSlug: "invoke",
		Short:   "Functional smoke test (invoke [--async] <slug> [--payload J|@file|-]; slug defaults to linked context)",
		Flags: []cliFlag{
			{Name: "async", Short: "return immediately with status_url"},
			{Name: "payload", Short: "JSON payload (inline | @file | -)", Value: "J|@file|-"},
			{Name: "on-success-webhook", Short: "app webhook id for completed invocation callbacks", Value: "ID"},
			{Name: "on-failure-webhook", Short: "app webhook id for failed or dead-lettered callbacks", Value: "ID"},
			{Name: "work-policy", Short: "named app work policy for async invocation", Value: "NAME"},
			{Name: "work-key", Short: "JSON scalar application key for async invocation", Value: "JSON"},
			{Name: "work-fairness-key", Short: "JSON scalar fairness group for async invocation", Value: "JSON"},
		},
		Positionals: []string{"[<slug>]"},
	},
	{
		Name:    "run",
		DocSlug: "run",
		Short:   "Run untrusted code in an isolated disposable microVM",
		Flags: []cliFlag{
			{Name: "workflow-id", Short: "caller-generated workflow grouping id", Value: "ID"},
			{Name: "step-label", Short: "short label for this step within --workflow-id", Value: "LABEL"},
			{Name: "profile", Short: "preinstalled dependencies (data requires python313)", Value: "P", ClosedSet: []string{"standard", "python-data-v1"}},
			{Name: "runtime", Short: "runtime (node22|node24|python312|python313)", Value: "R", ClosedSet: []string{"node22", "node24", "python312", "python313"}},
			{Name: "source", Short: "inline source code", Value: "CODE"},
			{Name: "file", Short: "source file (regular file only)", Value: "PATH"},
			{Name: "input", Short: "JSON input (inline | @file | -)", Value: "J|@file|-"},
			{Name: "timeout-ms", Short: "execution timeout", Value: "N"},
			{Name: "memory-mb", Short: "memory limit", Value: "N"},
			{Name: "cpu-millicores", Short: "CPU limit", Value: "N"},
			{Name: "ephemeral-disk-mb", Short: "ephemeral scratch size", Value: "N"},
			{Name: "max-output-bytes", Short: "combined output cap", Value: "N"},
			{Name: "output-file", Short: "output file below context.output_dir to export (repeatable)", Value: "PATH"},
			{Name: "output-dir", Short: "save artifacts locally; implies --wait", Value: "DIR"},
			{Name: "wait", Short: "wait for terminal result"},
			{Name: "watch", Short: "stream live output while waiting"},
			{Name: "poll-interval", Short: "status polling interval with --wait", Value: "D"},
			{Name: "wait-timeout", Short: "maximum client wait duration", Value: "D"},
		},
	},
	{
		Name:    "runs",
		DocSlug: "runs",
		Short:   "Inspect or cancel isolated disposable runs",
		Subcommands: []cliSub{
			{Name: "list", Short: "List runs", Flags: []cliFlag{
				{Name: "limit", Short: "maximum number of runs (1..200)", Value: "N"},
				{Name: "offset", Short: "number of matching runs to skip", Value: "N"},
				{Name: "status", Short: "filter by lifecycle status", Value: "STATUS", ClosedSet: []string{"queued", "restoring", "running", "succeeded", "failed", "timed_out", "out_of_memory", "cancelled"}},
				{Name: "workflow-id", Short: "filter by caller-generated workflow id", Value: "ID"},
			}},
			{Name: "workflow", Short: "Show workflow status or manage an agent-owned Runs plan", Positionals: []string{"<workflow-id>"}, Subcommands: []cliSub{
				{Name: "run", Short: "Run, resume, or preview a disposable Runs plan", Flags: []cliFlag{
					{Name: "manifest", Short: "JSON workflow plan file", Req: true, Value: "PLAN.json"},
					{Name: "managed", Short: "continue a bounded Run DAG on the control plane after this client exits"},
					{Name: "dry-run", Short: "validate and preview without creating Runs"},
					{Name: "poll-interval", Short: "status polling interval", Value: "D"},
					{Name: "wait-timeout", Short: "maximum client wait duration", Value: "D"},
				}, Examples: []string{"gregale runs workflow run --manifest incident.json --json"}},
			}},
			{Name: "artifacts", Short: "Save output artifacts from a successful run", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "output-dir", Short: "local destination (required)", Value: "DIR"}}},
			{Name: "get", Short: "Show one run", Positionals: []string{"<id>"}},
			{Name: "status", Short: "Show one run (alias for get)", Positionals: []string{"<id>"}},
			{Name: "cancel", Short: "Cancel one run", Positionals: []string{"<id>"}},
		},
		Positionals: []string{"<id>"},
	},
	{
		Name:    "invocations",
		DocSlug: "invocations",
		Short:   "Per-account invocation ledger (invocations list|get|wait <id>)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List invocations", Flags: []cliFlag{
				{Name: "limit", Short: "page size (1-100, default 50)", Value: "N"},
				{Name: "cursor", Short: "opaque cursor from a prior page", Value: "CURSOR"},
				{Name: "before", Short: "alias for --cursor", Value: "CURSOR"},
				{Name: "all", Short: "walk every page using --limit and --cursor"},
			}},
			{Name: "get", Short: "Show or recover one invocation", Positionals: []string{"<id>"}, Flags: []cliFlag{
				{Name: "replay", Short: "re-issue failed unkeyed work"},
				{Name: "replay-keyed", Short: "recover failed keyed work in its captured policy lane"},
			}},
			{Name: "wait", Short: "Wait for one invocation to finish (exit 124 on timeout, 130 on Ctrl-C)", Positionals: []string{"<id>"}, Flags: []cliFlag{
				{Name: "timeout", Value: "D", Short: "stop waiting without canceling the invocation (0 waits indefinitely)"},
				{Name: "interval", Value: "D", Short: "time between status checks (default 1s)"},
			}},
		},
		Positionals: []string{"<id>"},
	},
	{Name: "issues", DocSlug: "issues", Short: "Group failures and track ownership and release-aware resolution", Examples: []string{"gregale issues list --app my-api", "gregale issues list --app my-api --assignee me", "gregale issues list --app my-api --assignee unassigned", "gregale issues list --app my-api --sort impact", "gregale issues impact-alert --app my-api --min-customers 5", "gregale issues ownership-rules --app my-api", "gregale issues ownership-rules --app my-api --rules-file issue-routing.json", "gregale issues get ISSUE_ID --app my-api", "gregale issues resolve ISSUE_ID --app my-api --deployment DEPLOYMENT_ID"}, Subcommands: []cliSub{
		{Name: "list", Short: "List grouped issues", Flags: issueAppFlags}, {Name: "get", Short: "Read evidence and release history", Positionals: []string{"<issue-id>"}, Flags: issueAppFlags}, {Name: "assign", Short: "Assign an issue to an account", Positionals: []string{"<issue-id>"}, Flags: append(append([]cliFlag{}, issueAppFlags...), cliFlag{Name: "assignee", Short: "owner account UUID (empty unassigns)", Value: "UUID"})}, {Name: "resolve", Short: "Resolve in a deployment", Positionals: []string{"<issue-id>"}, Flags: append(append([]cliFlag{}, issueAppFlags...), cliFlag{Name: "deployment", Short: "deployment UUID that fixed the issue", Req: true, Value: "UUID"})}, {Name: "reopen", Short: "Reopen an issue", Positionals: []string{"<issue-id>"}, Flags: issueAppFlags}, {Name: "ignore", Short: "Ignore until a timestamp", Positionals: []string{"<issue-id>"}, Flags: append(append([]cliFlag{}, issueAppFlags...), cliFlag{Name: "until", Short: "ignore until (RFC3339)", Req: true, Value: "RFC3339"})}, {Name: "impact-alert", Short: "Read or configure customer-impact alert threshold", Flags: issueAppFlags}, {Name: "ownership-rules", Short: "Read or replace automatic assignment rules", Flags: append(append([]cliFlag{}, issueAppFlags...), cliFlag{Name: "rules-file", Short: "JSON policy file to replace rules; use - for stdin", Value: "PATH"})}, {Name: "tokens", Short: "List ingest credentials", Flags: issueAppFlags}, {Name: "create-token", Short: "Create a deployment-bound ingest credential", Flags: append(append([]cliFlag{}, issueAppFlags...), cliFlag{Name: "deployment", Short: "deployment UUID the credential is bound to", Req: true, Value: "UUID"})}, {Name: "revoke-token", Short: "Revoke an ingest credential", Positionals: []string{"<token-id>"}, Flags: issueAppFlags},
	}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "application slug"}, {Name: "deployment", Value: "UUID", Short: "fixed or token-bound deployment"}, {Name: "state", Value: "STATE", Short: "filter issue state"}, {Name: "environment", Value: "ENV", Short: "environment filter"}, {Name: "cursor", Value: "CURSOR", Short: "issue-list or occurrence cursor"}, {Name: "release-cursor", Value: "CURSOR", Short: "release history cursor"}, {Name: "activity-cursor", Value: "CURSOR", Short: "activity history cursor"}, {Name: "assignee", Value: "OWNER", Short: "list filter me, unassigned, or account UUID; assignment owner UUID"}, {Name: "sort", Value: "ORDER", Short: "list order: recent or impact by verified customers in 24h"}, {Name: "min-customers", Value: "N", Short: "issue list threshold, or impact-alert policy threshold (0 disables)"}, {Name: "since", Value: "RFC3339", Short: "impact window start"}, {Name: "until", Value: "RFC3339", Short: "ignore until"}, {Name: "name", Value: "NAME", Short: "credential name"}, {Name: "expires-in", Value: "D", Short: "credential lifetime"}, {Name: "rules-file", Value: "PATH", Short: "ownership-rules JSON policy file to replace rules; use - for stdin"}}},

	{
		Name: "customer-operations", DocSlug: "customer-operations", Short: "Inspect customer work, verify downloads and reconcile outcomes",
		Examples:    []string{"gregale customer-operations list --app exports --scope production", "gregale customer-operations watch <id> --app exports --timeout 5m --json"},
		Subcommands: customerOperationCLIManifest(),
	},
	{
		Name:    "operations",
		DocSlug: "operations",
		Short:   "Coordinate named work with leases, explicit contention policy, and fenced ownership",
		Subcommands: []cliSub{
			{Name: "policy", Short: "List or configure account operation policies", Subcommands: []cliSub{
				{Name: "list", Short: "List operation policies"},
				{Name: "upsert", Short: "Create or revise a policy from JSON", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "file", Value: "POLICY.json", Short: "policy JSON file", Req: true}}},
				{Name: "retire", Short: "Retire an idle policy and preserve ownership history", Positionals: []string{"<name>"}},
			}},
			{Name: "bind-trigger", Short: "Route an account-owned cron, inbound webhook, broker trigger, or Job schedule through a policy", Positionals: []string{"<cron|inbound_webhook|broker|job_schedule>", "<trigger-id>"}, Flags: []cliFlag{
				{Name: "policy", Value: "NAME", Short: "managed operation policy", Req: true},
				{Name: "key", Value: "JSON", Short: "JSON scalar business coordination key", Req: true},
				{Name: "tenant", Value: "ID", Short: "account-authorized platform customer tenant"},
				{Name: "equivalence-key", Value: "KEY", Short: "equivalent request identity for join_existing"},
			}},
			{Name: "unbind-trigger", Short: "Remove a trigger's managed operation policy binding", Positionals: []string{"<cron|inbound_webhook|broker|job_schedule>", "<trigger-id>"}},
			{Name: "reconcile", Short: "Apply policies and trigger bindings declared in the project manifest", Flags: []cliFlag{{Name: "dir", Value: "PROJECT_DIR", Short: "project directory containing gregale.yaml or gregale.toml"}}},
			{Name: "start", Short: "Submit work through a named coordination key", Positionals: []string{"<app-slug>"}, Flags: []cliFlag{
				{Name: "policy", Value: "NAME", Short: "managed operation policy", Req: true},
				{Name: "key", Value: "JSON", Short: "JSON scalar concurrency key", Req: true},
				{Name: "tenant", Value: "ID", Short: "authorized platform customer tenant"},
				{Name: "self", Short: "derive tenant identity from a platform-customer credential"},
				{Name: "equivalence-key", Value: "KEY", Short: "equivalent request identity for join_existing"},
				{Name: "idempotency-key", Value: "KEY", Short: "stable retry identity for this submission"},
				{Name: "payload", Value: "JSON", Short: "request body delivered to the app"},
				{Name: "method", Value: "METHOD", Short: "HTTP method delivered to the app"},
				{Name: "path", Value: "PATH", Short: "app route delivered to the app"},
			}, Examples: []string{"gregale operations start --policy crm-sync --key '\"customer:acme:crm-sync\"' --tenant TENANT_ID my-api"}},
			{Name: "start-job", Short: "Submit a Job run through a named coordination key", Positionals: []string{"<job-name>"}, Flags: []cliFlag{
				{Name: "policy", Value: "NAME", Short: "managed operation policy", Req: true},
				{Name: "key", Value: "JSON", Short: "JSON scalar business coordination key", Req: true},
				{Name: "equivalence-key", Value: "KEY", Short: "equivalent request identity for join_existing"},
				{Name: "idempotency-key", Value: "KEY", Short: "stable retry identity for this submission"},
				{Name: "tasks", Value: "N", Short: "number of Job tasks (default 1; omit with --run-file)"},
				{Name: "run-file", Value: "FILE", Short: "JSON CreateJobRunRequest"},
			}, Examples: []string{"gregale operations start-job --policy imports --key '\"customer:acme:import\"' nightly-import"}},
			{Name: "get", Short: "Inspect operation state, committed result, and effect delivery status", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "self", Short: "use the authenticated platform-customer scope"}}},
			{Name: "wait", Short: "Wait for a terminal operation state", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "self", Short: "use the authenticated platform-customer scope"}, {Name: "timeout", Value: "DURATION", Short: "stop waiting after this duration"}, {Name: "interval", Value: "DURATION", Short: "time between status checks"}}},
			{Name: "cancel", Short: "Request cancellation of pending or active work", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "self", Short: "use the authenticated platform-customer scope"}}},
		},
	},
	{
		Name:    "debug",
		DocSlug: "debug",
		Short:   "Inspect production requests and regressions",
		Subcommands: []cliSub{
			debugRequestsCLISubcommand(),
			{Name: "profiles", Short: "Sampled CPU functions and deployment comparison", Positionals: []string{"<slug>"}, Flags: []cliFlag{{Name: "deployment-id", Value: "UUID", Short: "candidate deployment"}, {Name: "runtime", Value: "NAME", Short: "runtime name"}, {Name: "start", Value: "RFC3339", Short: "capture start"}, {Name: "end", Value: "RFC3339", Short: "capture end"}, {Name: "baseline-id", Value: "UUID", Short: "baseline deployment"}, {Name: "baseline-start", Value: "RFC3339", Short: "baseline capture start"}, {Name: "baseline-end", Value: "RFC3339", Short: "baseline capture end"}}},
			{Name: "dependencies", Short: "Show observed dependency latency and regressions", Positionals: []string{"<slug>"}, Flags: []cliFlag{{Name: "since", Short: "lookback window", Value: "DURATION"}}},
			{Name: "coverage", Short: "Observed debugger signal coverage (coverage <slug> [--since D])", Positionals: []string{"<slug>"}, Flags: []cliFlag{{Name: "since", Short: "lookback window", Value: "DURATION"}}},
			{Name: "running", Short: "Explain why an app is still running, with request evidence when available (running <slug> [--since D] [--limit N])", Positionals: []string{"<slug>"}, Flags: []cliFlag{{Name: "since", Short: "lookback window", Value: "DURATION"}, {Name: "limit", Short: "maximum recent observations (1..100; default 20)", Value: "N"}}},
			{Name: "regressions", Short: "List, watch, triage, or roll back regressions", Positionals: []string{"[<slug>]"}, Flags: []cliFlag{{Name: "since", Short: "lookback window", Value: "DURATION"}, {Name: "all", Short: "include every app in the account"}}, Subcommands: []cliSub{
				{Name: "watch", Short: "Watch regression changes (live stream by default)", Positionals: []string{"[<slug>]"}, Flags: []cliFlag{{Name: "since", Short: "lookback window", Value: "DURATION"}, {Name: "interval", Short: "poll interval (250ms..1h)", Value: "DURATION"}, {Name: "all", Short: "watch every app in the account"}, {Name: "poll", Short: "poll instead of using the live event stream"}, {Name: "once", Short: "poll once and exit"}}},
				{Name: "ack", Short: "Acknowledge a regression", Positionals: []string{"<slug>"}, Flags: []cliFlag{{Name: "deployment-id", Short: "regression deployment UUID", Value: "UUID", Req: true}, {Name: "route", Short: "regression route", Value: "PATH", Req: true}}},
				{Name: "acknowledge", Short: "Acknowledge a regression", Positionals: []string{"<slug>"}, Flags: []cliFlag{{Name: "deployment-id", Short: "regression deployment UUID", Value: "UUID", Req: true}, {Name: "route", Short: "regression route", Value: "PATH", Req: true}}},
				{Name: "dismiss", Short: "Dismiss a regression", Positionals: []string{"<slug>"}, Flags: []cliFlag{{Name: "deployment-id", Short: "regression deployment UUID", Value: "UUID", Req: true}, {Name: "route", Short: "regression route", Value: "PATH", Req: true}, {Name: "dismissed-until", Short: "dismissal expiry (RFC3339; default 24h)", Value: "RFC3339"}}},
				{Name: "resolve", Short: "Resolve a regression", Positionals: []string{"<slug>"}, Flags: []cliFlag{{Name: "deployment-id", Short: "regression deployment UUID", Value: "UUID", Req: true}, {Name: "route", Short: "regression route", Value: "PATH", Req: true}}},
				{Name: "reopen", Short: "Reopen a regression", Positionals: []string{"<slug>"}, Flags: []cliFlag{{Name: "deployment-id", Short: "regression deployment UUID", Value: "UUID", Req: true}, {Name: "route", Short: "regression route", Value: "PATH", Req: true}}},
				{Name: "rollback", Short: "Roll back an app from the regressions view", Positionals: []string{"<slug>"}, Flags: []cliFlag{{Name: "to", Short: "target superseded deployment ID", Value: "ID"}, {Name: "yes", Short: "confirm the rollback", Req: true, Bool: true}}},
			}},
			{Name: "compare", Short: "Per-route deployment-vs-deployment compare", Positionals: []string{"<slug>"}, Flags: []cliFlag{{Name: "source", Short: "source deployment ID", Value: "ID", Req: true}, {Name: "mirror", Short: "comparison deployment ID", Value: "ID", Req: true}, {Name: "route", Short: "exact route filter", Value: "PATH"}, {Name: "since", Short: "lookback window", Value: "DURATION"}, {Name: "until", Short: "end of comparison window (RFC3339)", Value: "RFC3339"}}},
			{Name: "bundle", Short: "Export a redacted incident bundle with coverage", Positionals: []string{"<slug>", "<request-id-or-row-id>"}, Flags: []cliFlag{{Name: "since", Short: "regression and comparison lookback", Value: "DURATION"}, {Name: "route", Short: "route filter for optional deployment comparison", Value: "PATH"}, {Name: "source", Short: "source deployment ID (requires --mirror)", Value: "ID"}, {Name: "mirror", Short: "comparison deployment ID (requires --source)", Value: "ID"}, {Name: "output", Short: "write bundle to PATH (default stdout; use - for stdout)", Value: "PATH"}}},
		},
		Positionals: []string{"[flags]", "<slug>", "[<request-id>]"},
	},
	{
		Name:    "trace",
		DocSlug: "trace",
		Short:   "Look up a W3C trace through the account trace index",
		Flags: []cliFlag{
			{Name: "watch", Short: "poll until linked invocations reach a terminal state"},
			{Name: "interval", Short: "poll interval (default 1s)", Value: "DURATION"},
			{Name: "timeout", Short: "maximum watch duration (default 5m)", Value: "DURATION"},
		},
		Positionals: []string{"<trace-id>"},
	},
	{
		Name:    "invitations",
		DocSlug: "invitations",
		Short:   "Standalone invitation actions (invitations peek <token>|accept <token>)",
		Subcommands: []cliSub{
			{Name: "peek", Short: "Look up an invitation by token", Positionals: []string{"<token>"}},
			{Name: "accept", Short: "Accept an invitation", Positionals: []string{"<token>"}},
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
			{Name: "add", Short: "Mint a new API key", Positionals: []string{"<label>"}, Flags: []cliFlag{
				{Name: "scopes", Short: "comma-separated API key scopes; omit for full admin access", Value: "SCOPE,..."},
			}, Examples: []string{"gregale keys add agent-runner --scopes runs:write"}},
			{Name: "rm", Short: "Revoke an API key", Positionals: []string{"<id>"}},
			{Name: subRotate, Short: "Rotate an API key", Positionals: []string{"<key-id>"}, Examples: []string{"gregale keys rotate 7f8c2a1e-6d3b-4c55-9a7e-0b1d2c3e4f5a"}},
			{Name: "grace-window", Short: "Read or update the rotation grace window", Flags: []cliFlag{
				{Name: "reset", Short: "clear the account override and use the plan default", Bool: true},
				{Name: "days", Short: "new grace window in days (0 or greater)", Value: "N"},
			}},
		},
	},
	{
		Name:     "login",
		DocSlug:  "auth",
		Short:    "Authenticate this machine",
		Examples: []string{"gregale login", "printf '%s' \"$GREGALE_TOKEN\" | gregale login --token-stdin"},
		Flags: []cliFlag{
			{Name: "token", Short: "use a pre-minted token (CI)", Value: "TOKEN"},
			{Name: "token-stdin", Short: "read a pre-minted token from stdin (CI)"},
		},
	},
	{
		Name:        "link",
		DocSlug:     "link",
		Short:       "Link this checkout to a Gregale project",
		Positionals: []string{"<project-slug>"},
		Flags: []cliFlag{
			{Name: "app", Short: "workload/app slug for app-scoped commands", Value: "slug"},
			{Name: "environment", Short: "default project environment scope", Value: "environment"},
			{Name: "no-gitignore", Short: "do not add .gregale/ to .gitignore"},
		},
	},
	{
		Name:    "logout",
		DocSlug: "auth",
		Short:   "Revoke the managed CLI session and remove the stored token",
	},
	{
		Name:    "unlink",
		DocSlug: "link",
		Short:   "Remove the linked project from this checkout",
	},
	{Name: "profile", DocSlug: "config", Short: "Manage named API connections and isolated credentials", Subcommands: []cliSub{
		{Name: "add", Short: "Add a connection without changing the active profile", Positionals: []string{"<name>", "<api-url>"}},
		{Name: "list", Short: "List connections and the active profile"},
		{Name: "check", Short: "Verify the selected API connection and account identity", Examples: []string{"gregale profile check", "gregale --profile staging profile check --timeout 5s --json"}, Flags: []cliFlag{{Name: "timeout", Short: "maximum request duration (default 10s)", Value: "DURATION"}}},
		{Name: "use", Short: "Select the default connection", Positionals: []string{"<name>"}},
		{Name: "remove", Short: "Remove an inactive connection and its credentials", Positionals: []string{"<name>"}},
	}},

	{
		Name:    "context",
		DocSlug: "link",
		Short:   "Show the linked project and default app context",
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
		Name:        "logs",
		DocSlug:     "logs",
		Short:       "Query runtime logs and HTTP request events",
		Examples:    []string{"gregale logs my-api --follow", "gregale logs my-api --since 1h --level error"},
		Positionals: []string{"[<slug>]"},
		Flags: []cliFlag{
			{Name: "follow", Short: "stream logs until interrupted"},
			{Name: "deployment", Short: "deployment id or vN revision (default: latest)", Value: "ID"},
			{Name: "release", Short: "release id or revision (alias for --deployment)", Value: "ID|vN"},
			{Name: "source", Short: "log source", Value: "SOURCE", ClosedSet: []string{"runtime", "http"}},
			{Name: "grep", Short: "only show lines containing this substring", Value: "SUBSTR"},
			{Name: "since", Short: "lookback duration or RFC3339 timestamp", Value: "15m|3d|RFC3339"},
			{Name: "level", Short: "only show lines at this level", Value: "LEVEL", ClosedSet: []string{"info", "warn", "error"}},
			{Name: "status", Short: "only show HTTP requests with this status", Value: "100..599"},
			{Name: "route", Short: "only show HTTP requests for this route", Value: "PATH"},
			{Name: "request", Short: "show one HTTP request by public request id or row id", Value: "ID"},
			{Name: "trace", Short: "show HTTP access logs correlated with a W3C trace id", Value: "TRACE_ID"},
			{Name: "limit", Short: "HTTP request page size (1..200)", Value: "N"},
			{Name: "all", Short: "read every retained HTTP request page"},
			{Name: "explain", Short: "summarize the last failure and common error patterns"},
			{Name: "archive", Short: "read durable logs for one instance and UTC day"},
			{Name: "instance", Short: "instance id for --archive", Value: "ID"},
			{Name: "date", Short: "UTC day for --archive", Value: "YYYY-MM-DD"},
		},
	},
	{
		Name:    "metrics",
		DocSlug: "metrics",
		Short:   "Per-app or account-wide metrics (slug defaults to linked context)",
		Flags: []cliFlag{
			{Name: "range", Short: "window (5m|15m|1h|6h|24h|7d)", Value: "WINDOW", ClosedSet: []string{"5m", "15m", "1h", "6h", "24h", "7d"}},
			{Name: "account", Short: "account-wide roll-up"},
		},
		Positionals: []string{"[<slug>]"},
	},
	{
		Name:    "analytics",
		DocSlug: "analytics",
		Short:   "Historical request analytics (analytics <slug> [--since 24h] [--by route|country|referrer_host|ua_family|status]; slug defaults to linked context)",
		Flags: []cliFlag{
			{Name: "since", Short: "lookback window", Value: "WINDOW"},
			{Name: "until", Short: "exclusive RFC3339 end", Value: "TIMESTAMP"},
			{Name: "by", Short: "grouping dimension", Value: "DIMENSION", ClosedSet: []string{"route", "country", "referrer_host", "ua_family", "status"}},
		},
		Positionals: []string{"[<slug>]"},
	},
	{
		Name:    "mfa",
		DocSlug: "mfa",
		Short:   "Manage account MFA (mfa enroll|confirm|verify|recover|disable)",
		Subcommands: []cliSub{
			{Name: "enroll", Short: "Begin TOTP enrolment", Flags: []cliFlag{{Name: "qr-out", Short: "write the QR PNG to this path", Value: "PATH"}}},
			{Name: "confirm", Short: "Confirm an enrolment code (positional code or --code)", Positionals: []string{"[<6-digit-code>]"}, Flags: []cliFlag{{Name: "code", Short: "6-digit TOTP (alternative to positional code)", Value: "CODE"}}},
			{Name: "verify", Short: "Verify a TOTP code (step-up; positional code or --code)", Positionals: []string{"[<6-digit-code>]"}, Flags: []cliFlag{{Name: "code", Short: "6-digit TOTP (alternative to positional code)", Value: "CODE"}}},
			{Name: "recover", Short: "Use a recovery code (positional code or --code)", Positionals: []string{"[<recovery-code>]"}, Flags: []cliFlag{{Name: "code", Short: "recovery code (alternative to positional code)", Value: "CODE"}}},
			{Name: "disable", Short: "Disable MFA", Flags: []cliFlag{{Name: "password", Short: "account password (prompts when omitted)", Value: "PASSWORD"}, {Name: "recovery-code", Short: "single-use recovery code (alternative to password)", Value: "CODE"}}},
		},
	},
	{
		Name:    "open",
		DocSlug: "open",
		Short:   "Open the app's URL (slug defaults to linked context)",
		Subcommands: []cliSub{
			{Name: "docs", Short: "Open a CLI docs page (open docs [<slug>])"},
		},
		Positionals: []string{"[<slug>]"},
	},
	{
		Name:    "orgs",
		DocSlug: "orgs",
		Short:   "Manage orgs, members, and workspace activity",
		Subcommands: []cliSub{
			{Name: "ls", Short: "List orgs"},
			{Name: "create", Short: "Create an org", Flags: []cliFlag{
				{Name: "slug", Short: "org slug (lowercase alphanumeric + dashes)", Value: "SLUG", Req: true},
				{Name: "name", Short: "display name", Value: "TEXT", Req: true},
			}},
			{Name: "info", Short: "Show one org", Positionals: []string{"<slug>"}},
			{Name: "activity", Short: "Show the global infrastructure timeline", Flags: []cliFlag{
				{Name: "org", Short: "organization slug", Value: "SLUG", Req: true},
				{Name: "before", Short: "alias for --cursor", Value: "CURSOR"},
				{Name: "cursor", Value: "CURSOR", Short: "opaque continuation cursor"},
				{Name: "all", Short: "walk every page"},
				{Name: "kind-prefix", Short: "filter by activity kind prefix", Value: "PREFIX"},
				{Name: "actor-type", Short: "filter by actor category", Value: "TYPE", ClosedSet: []string{"user", "api_key", "github", "system", "operator"}},
				{Name: "app-id", Short: "filter by application UUID", Value: "UUID"},
				{Name: "limit", Short: "page size (1..100)", Value: "N"},
			}},
			{Name: "rm", Short: "Delete one org", Positionals: []string{"<slug>"}, Flags: []cliFlag{{Name: "q", ShortName: "q", Short: "skip the confirmation prompt", Bool: true}}},
			{Name: "members", Short: "Manage org members", Subcommands: []cliSub{
				{Name: "list", Short: "List org members", Positionals: []string{"<slug>"}},
				{Name: "invite", Short: "Invite a member by email", Flags: []cliFlag{
					orgSlugFlag,
					{Name: "email", Short: "invitee email", Value: "ADDR", Req: true},
					{Name: "role", Short: "role (default developer; owner is rejected)", Value: "ROLE", ClosedSet: orgMemberRoles},
				}},
				{Name: "change-role", Short: "Change a member's role", Flags: []cliFlag{
					orgSlugFlag,
					{Name: "user", Short: "member user ID", Value: "USER-ID", Req: true},
					{Name: "role", Short: "new role (owner is rejected)", Value: "ROLE", Req: true, ClosedSet: orgMemberRoles},
				}},
				{Name: "rm", Short: "Remove a member", Flags: []cliFlag{
					orgSlugFlag,
					{Name: "user", Short: "member user ID", Value: "USER-ID", Req: true},
				}},
			}},
			{Name: "keys", Short: "Manage org API keys", Subcommands: []cliSub{
				{Name: "list", Short: "List org API keys", Flags: []cliFlag{orgSlugFlag}},
				{Name: "add", Short: "Create an org API key; the plaintext is shown once", Flags: []cliFlag{
					orgSlugFlag,
					{Name: "label", Short: "key label", Value: "TEXT", Req: true},
					{Name: "scopes", Short: "comma-separated scopes (default admin)", Value: "SCOPES"},
				}},
				{Name: "info", Short: "Show one org API key", Positionals: []string{"<key-id>"}, Flags: []cliFlag{orgSlugFlag}},
				{Name: "rm", Short: "Revoke an org API key", Positionals: []string{"<key-id>"}, Flags: []cliFlag{orgSlugFlag}},
				{Name: "rotate", Short: "Rotate an org API key; the new plaintext is shown once", Positionals: []string{"<key-id>"}, Flags: []cliFlag{
					orgSlugFlag,
					{Name: "label", Short: "new label (empty keeps the current one)", Value: "TEXT"},
				}},
			}},
			{Name: "transfer-ownership", Short: "Transfer org ownership", Flags: []cliFlag{
				orgSlugFlag,
				{Name: "to", Short: "user ID of the new owner", Value: "USER-ID", Req: true},
			}},
			{Name: "seat-usage", Short: "Show seat usage", Flags: []cliFlag{orgSlugFlag}},
			{Name: "invitations", Short: "Manage org invitations", Subcommands: []cliSub{
				{Name: "list", Short: "List pending invitations", Flags: []cliFlag{
					orgSlugFlag,
					{Name: "limit", Short: "max rows (1..200)", Value: "N"},
				}},
				{Name: "list-all", Short: "List invitations in every state", Flags: []cliFlag{orgSlugFlag}},
				{Name: "revoke", Short: "Revoke a pending invitation", Flags: []cliFlag{
					orgSlugFlag,
					{Name: "invitation", Short: "invitation ID", Value: "ID", Req: true},
				}},
			}},
			{Name: "me", Short: "Show current org membership"},
			{Name: "update", Short: "Update org metadata", Flags: []cliFlag{
				orgSlugFlag,
				{Name: "name", Short: "new display name (1..120 chars)", Value: "TEXT"},
				{Name: "plan", Short: "new plan", Value: "PLAN", ClosedSet: []string{"free", "hobby", "pro", "scale"}},
			}},
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
		Name:        "park",
		DocSlug:     "park-wake",
		Short:       "Park an app cold (kill all live instances)",
		Positionals: []string{"<slug>"},
		Examples:    []string{"gregale park my-api"},
	},
	{
		Name:      "plan",
		DocSlug:   "plan",
		Short:     "Change plan (free|hobby|pro|scale); paid upgrades open the provider checkout",
		ClosedSet: []string{"free", "hobby", "pro", "scale"},
	},
	{
		Name: "data-api", DocSlug: "data-api", Short: "Create schema-generated PostgreSQL APIs and export application types",
		Subcommands: []cliSub{
			{Name: "create", Short: "Deploy a managed PostgREST Data API", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "database", Value: "DATABASE", Req: true, Short: "ready managed database name or ID"},
				{Name: "schema", Value: "SCHEMA", Short: "exposed schema (api)"},
				{Name: "scope", Value: "SCOPE", Short: "environment scope"},
				{Name: "issuer", Value: "HTTPS_URL", Req: true, Short: "application JWT issuer"},
				{Name: "jwks-url", Value: "HTTPS_URL", Req: true, Short: "application JWKS URL"},
				{Name: "audience", Value: "AUDIENCE", Req: true, Short: "application JWT audience"},
				{Name: "origins", Value: "ORIGINS", Short: "comma-separated browser origins"},
				{Name: "resume", Short: "resume configuration and deployment of an existing app"},
			}},
			{Name: "types", Short: "Generate types in an owner-authenticated app task", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "output", Value: "FILE", Short: "generated TypeScript output"},
				{Name: "check", Short: "fail if the output file is stale"},
				{Name: "timeout", Value: "DURATION", Short: "task wait deadline (default 2m)"},
			}},
			{Name: "refresh", Short: "Request a fresh restart to reload the database schema", Positionals: []string{"<name>"}},
		},
	},
	{
		Name:     "postgres",
		DocSlug:  "postgres",
		Short:    "Operator preview: manage PostgreSQL databases and bindings",
		Audience: cliAudienceOperator,
		Subcommands: []cliSub{
			{Name: "list", Short: "List managed PostgreSQL databases"},
			{Name: "capabilities", Short: "Show plan and region PostgreSQL feature support", Flags: []cliFlag{
				{Name: "region", Short: "region (defaults to configured region)", Value: "REGION"},
			}, Examples: []string{"gregale postgres capabilities --json"}},
			{Name: "usage", Short: "Show monthly managed PostgreSQL usage and guardrail state"},
			{Name: "reconcile", Short: "Preview or apply legacy identity and shutdown evidence (operator only)", Positionals: []string{"<account_id>"}, Flags: []cliFlag{
				{Name: "file", Short: "verified identity and shutdown evidence JSON file", Value: "FILE", Req: true},
				{Name: "apply", Short: "apply with expected_revision from preview"},
				{Name: "session-file", Short: "private operator session cookie file (required for apply)", Value: "FILE"},
			}, Examples: []string{"gregale postgres reconcile ACCOUNT_ID --file reconciliation.json --json"}},
			{Name: "usage-import", Short: "Preview or apply retained usage evidence (operator only)", Positionals: []string{"<account_id>"}, Flags: []cliFlag{
				{Name: "file", Short: "normalized retained evidence JSON file", Value: "FILE", Req: true},
				{Name: "apply", Short: "apply with expected_revision from preview"},
				{Name: "session-file", Short: "private operator session cookie file (required for apply)", Value: "FILE"},
			}, Examples: []string{"gregale postgres usage-import ACCOUNT_ID --file retained-usage.json --json"}},
			{Name: "diagnostics", Short: "Explain accounting blockers for an account (operator only)", Positionals: []string{"<account_id>"}, Flags: []cliFlag{
				{Name: "after", Short: "resume after next_cursor", Value: "UUID"},
				{Name: "limit", Short: "maximum databases in this page (1-100)", Value: "N"},
			}, Examples: []string{"gregale postgres diagnostics ACCOUNT_ID --json"}},
			{Name: "create", Short: "Create a managed PostgreSQL database", Flags: []cliFlag{
				{Name: "region", Short: "provider-neutral region", Req: true, Value: "REGION"},
				{Name: "postgres-major", Short: "PostgreSQL major version", Value: "N"},
				{Name: "class", Short: "service class", Value: "CLASS", ClosedSet: []string{"development", "burstable", "production"}},
				{Name: "availability", Short: "availability mode", Value: "MODE", ClosedSet: []string{"single_zone", "high_availability"}},
				{Name: "scale-to-zero", Short: "suspend compute when idle"},
				{Name: "storage-bytes", Short: "storage limit in bytes", Value: "N"},
				{Name: "restore-window-seconds", Short: "point-in-time restore window", Value: "N"},
			}},
			{Name: "get", Short: "Show one managed PostgreSQL database"},
			{Name: "delete", Short: "Delete a managed PostgreSQL database"},
			{Name: "resize", Short: "Durably resize compute; clients may disconnect", Positionals: []string{"<database>"}, Flags: []cliFlag{
				{Name: "class", Short: "target service class", Req: true, Value: "CLASS", ClosedSet: []string{"development", "burstable", "production"}},
				{Name: "request-id", Short: "stable request UUID; reuse after uncertain responses", Req: true, Value: "UUID"},
			}},
			{Name: "resize-status", Short: "Read compute resize progress", Positionals: []string{"<database>", "<request_uuid>"}},
			{Name: "restore", Short: "Restore a database to a new database", Flags: []cliFlag{
				{Name: "name", Short: "name for the restored database", Req: true, Value: "NAME"},
				{Name: "point-in-time", Short: "RFC3339 restore timestamp", Req: true, Value: "TIMESTAMP"},
			}},
			{Name: "cutover", Short: "Stage and verify a restore target without moving workloads", Subcommands: []cliSub{
				{Name: "prepare", Short: "Stage all source bindings for one app and scope", Positionals: []string{"<source>", "<target>", "<app>"}, Flags: []cliFlag{{Name: "scope", Short: "environment scope", Value: "SCOPE"}}},
				{Name: "get", Short: "Read cutover progress and SQL evidence", Positionals: []string{"<id>"}},
				{Name: "verify", Short: "Queue control-plane SQL verification", Positionals: []string{"<id>"}},
				{Name: "cancel", Short: "Revoke staged credentials and release pins", Positionals: []string{"<id>"}},
			}},
			{Name: "bindings", Short: "Manage app database bindings", Subcommands: []cliSub{
				{Name: "rotate", Short: "Rotate a binding and optionally wait for the previous credential to retire", Positionals: []string{"<id>"}, Flags: []cliFlag{
					{Name: "wait", Short: "wait for the previous credential to retire"},
					{Name: "wait-timeout", Short: "maximum time to wait for rotation (default 5m)", Value: "DURATION"},
					{Name: "poll-interval", Short: "status polling interval while waiting (default 1s)", Value: "DURATION"},
				}, Examples: []string{"gregale postgres bindings rotate BINDING_ID --wait"}},
			}},
			{Name: "attach", Short: "Attach a database to an app", Flags: []cliFlag{
				{Name: "scope", Short: "environment scope (defaults to linked project environment, otherwise production)", Value: "SCOPE"},
				{Name: "env", Short: "connection environment variable", Value: "KEY"},
				{Name: "access", Short: "credential access", Value: "MODE", ClosedSet: []string{"read_write", "read_only", "migration", "data_api"}},
			}},
		},
	},
	{
		Name:    "ps",
		DocSlug: "ps",
		Short:   "Show live instances + state for an app (slug defaults to linked context)",
		Flags: []cliFlag{
			{Name: "all", Short: "include the newest 100 retained history rows (parked rows expire after 30d by default)"},
		},
		Positionals: []string{"[<slug>]"},
	},
	{
		Name:    "queue",
		DocSlug: "queue",
		Short:   "Inspect queues and manage first-class queue bindings",
		Subcommands: []cliSub{
			{Name: "tail", Short: "Tail the wake queue", Positionals: []string{"<slug>"}},
			{Name: "send", Short: "Enqueue a wake request", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "payload", Short: "JSON payload (inline | @file | -)", Value: "J"},
				{Name: "queue-name", Short: "logical queue name", Value: "QUEUE"},
				{Name: "environment", Short: "registered project environment with an enabled queue binding", Value: "ENV"},
				{Name: "work-policy", Short: "named work policy for an unnamed queue", Value: "NAME"},
				{Name: "work-key", Short: "JSON scalar identifying related work", Value: "JSON"},
				{Name: "work-fairness-key", Short: "JSON scalar shared by related work keys", Value: "JSON"},
			}},
			{Name: "receive", Short: "Wait for the next queue row the platform delivers", Positionals: []string{"<slug>"}},
			{Name: "state", Short: "Show queue state", Positionals: []string{"<slug>"}},
			{Name: statusLiteral, Short: "Show queue depth, scaling, bindings, and liveness", Positionals: []string{"<slug>"}},
			{Name: "peek", Short: "Peek at the next wake", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "limit", Short: "page size (1..100, default 50)", Value: "N"},
				{Name: "cursor", Short: "opaque continuation cursor", Value: "CURSOR"},
				{Name: "before", Short: "alias for --cursor", Value: "CURSOR"},
				{Name: "all", Short: "walk every page using --limit and --cursor"},
			}},
			{Name: "dead-letter", Short: "Inspect the dead-letter queue", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "limit", Short: "page size (1..100, default 50)", Value: "N"},
				{Name: "cursor", Short: "opaque continuation cursor", Value: "CURSOR"},
				{Name: "before", Short: "alias for --cursor", Value: "CURSOR"},
				{Name: "all", Short: "walk every page using --limit and --cursor"},
			}},
			{Name: "ack", Short: "Ack a wake", Positionals: []string{"<slug>", "<row-id>"}},
			{Name: "setup", Short: "Configure a simple push workload with queue-depth scaling", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "queue-name", Short: "logical queue name", Value: "QUEUE"},
				{Name: "target-depth", Short: "messages per worker before scaling out", Value: "N"},
				{Name: "max-concurrency", Short: "maximum concurrent deliveries per worker", Value: "N"},
				{Name: "max-attempts", Short: "maximum delivery attempts (0 uses the plan default)", Value: "N"},
				{Name: "retry-base-seconds", Short: "base retry delay in seconds", Value: "N"},
				{Name: "retry-max-seconds", Short: "maximum retry delay in seconds", Value: "N"},
				{Name: "retry-jitter-seconds", Short: "retry jitter in seconds (0..1)", Value: "N"},
				{Name: "force", Short: "replace an existing default binding on another queue"},
			}},
			{Name: "bindings", Short: "Manage queue bindings", Subcommands: []cliSub{
				{Name: "list", Short: "List app queue bindings", Positionals: []string{"<slug>"}, Flags: []cliFlag{
					{Name: "include-retired", Short: "include retained binding UUIDs for reviewed recovery"},
				}},
				{Name: "create", Short: "Bind the app to a queue", Positionals: []string{"<slug>"}, Flags: []cliFlag{
					{Name: "name", Short: "binding name", Value: "NAME", Req: true},
					{Name: "queue-name", Short: "queue to bind", Value: "QUEUE", Req: true},
					{Name: "mode", Short: "delivery mode", Value: "MODE", ClosedSet: []string{"pull", "push"}},
					{Name: "workload-class", Short: "consumer workload class", Value: "CLASS", ClosedSet: []string{"worker", "job", "http"}},
					{Name: "max-concurrency", Short: "maximum concurrent deliveries", Value: "N"},
				}},
				{Name: "update", Short: "Update a queue binding", Positionals: []string{"<slug>", "<binding-id>"}, Flags: []cliFlag{
					{Name: "queue-name", Short: "queue to bind", Value: "QUEUE"},
					{Name: "mode", Short: "delivery mode", Value: "MODE", ClosedSet: []string{"pull", "push"}},
					{Name: "workload-class", Short: "consumer workload class", Value: "CLASS", ClosedSet: []string{"worker", "job", "http"}},
					{Name: "max-concurrency", Short: "maximum concurrent deliveries", Value: "N"},
				}},
				{Name: "rm", Short: "Retire a queue binding", Positionals: []string{"<slug>", "<binding-id>"}},
			}},
		},
	},
	{
		Name:    "dlq",
		DocSlug: "dlq",
		Short:   "Inspect, replay, or purge unified dead-letter events",
		Subcommands: []cliSub{
			{Name: "list", Short: "List app dead-letter events", Positionals: []string{"<app>"}, Flags: []cliFlag{
				{Name: "limit", Short: "max events (1..200)", Value: "N"},
				{Name: "before", Short: "pagination cursor", Value: "ID"},
			}},
			{Name: "inspect", Short: "Inspect one dead-letter event", Positionals: []string{"<app>", "<event-id>"}},
			{Name: "replay", Short: "Replay one event or --all", Positionals: []string{"<app>", "[<event-id>]"}, Flags: []cliFlag{
				{Name: "all", Short: "replay pending events"},
				{Name: "limit", Short: "maximum events (1..200)", Value: "N"},
			}},
			{Name: "purge", Short: "Purge one event or --all", Positionals: []string{"<app>", "[<event-id>]"}, Flags: []cliFlag{
				{Name: "all", Short: "purge all events"},
				{Name: "limit", Short: "page size (1..200)", Value: "N"},
			}},
		},
		Positionals: []string{"<app>", "[<event-id>]"},
	},
	{
		Name:    "registry",
		DocSlug: "registry",
		Short:   "Manage private registry credentials and deploy published images",
		Subcommands: []cliSub{
			{Name: "published", Short: "Deploy an image after CI publishes its immutable digest", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "image", Short: "published digest-pinned image reference", Req: true, Value: "REF"},
				{Name: "scope", Short: "deployment scope", Value: "SLUG"},
				{Name: "environment", Short: "registered project environment", Value: "SLUG"},
				{Name: "wait", Short: "wait for the image deployment"},
				{Name: "timeout", Short: "deployment wait timeout", Value: "DURATION"},
			}},
			{Name: "list", Short: "List registry credentials", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "slug"}}},
			{Name: "set", Short: "Set a registry credential", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "registry", Short: "registry host", Req: true, Value: "host"},
				{Name: "user", Short: "registry username", Req: true, Value: "user"},
				{Name: "password-stdin", Short: "read the registry password/token from stdin"},
			}},
			{Name: "rm", Short: "Remove a registry credential", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "registry", Short: "registry host", Req: true, Value: "host"},
			}},
		},
	},
	{
		Name:    "realtime",
		DocSlug: "realtime",
		Short:   "Manage realtime endpoints, policies, connections, channels, and auth",
		Subcommands: []cliSub{
			{Name: "list", Short: "List managed realtime endpoints", Positionals: []string{"<app>"}},
			{Name: "get", Short: "Show one endpoint and safe auth-rotation status", Positionals: []string{"<app>", "<endpoint-id>"}},
			{Name: "create", Short: "Create a managed realtime endpoint", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "callback-url", Short: "application callback URL", Req: true, Value: "URL"},
				{Name: "callback-auth-token-stdin", Short: "read the callback bearer token from stdin (this or --callback-auth-token is required)"},
				{Name: "callback-auth-token", Short: "callback bearer token your app verifies (prefer --callback-auth-token-stdin)", Value: "TOKEN"},
				{Name: "connect-path", Short: "callback path for connect events", Value: "PATH"},
				{Name: "message-path", Short: "callback path for message events", Value: "PATH"},
				{Name: "disconnect-path", Short: "callback path for disconnect events", Value: "PATH"},
				{Name: "auth-mode", Short: "client auth mode", Value: "MODE", ClosedSet: []string{"none", "static_bearer", "oidc_jwt"}},
				{Name: "auth-token-stdin", Short: "read the client static bearer token from stdin"},
				{Name: "auth-token", Short: "client static bearer token (prefer --auth-token-stdin)", Value: "TOKEN"},
				{Name: "auth-issuer", Short: "OIDC issuer URL", Value: "URL"},
				{Name: "auth-jwks-url", Short: "OIDC JWKS URL", Value: "URL"},
				{Name: "auth-audience", Short: "OIDC audience (repeatable)", Value: "AUDIENCE", Repeatable: true},
				{Name: "auth-algorithm", Short: "OIDC signing algorithm (repeatable)", Value: "ALG", Repeatable: true},
				{Name: "auth-claim", Short: "required OIDC claim as KEY=VALUE (repeatable)", Value: "KEY=VALUE", Repeatable: true},
				{Name: "allowed-origin", Short: "exact browser origin (repeatable)", Value: "ORIGIN", Repeatable: true},
				{Name: "max-connections", Short: "per-endpoint connection cap (0 inherits the default)", Value: "N"},
				{Name: "max-message-bytes", Short: "decoded message size cap (0 inherits the default)", Value: "N"},
				{Name: "max-connection-age-seconds", Short: "connection age cap (0 inherits the default)", Value: "SECONDS"},
				{Name: "enabled", Short: "create enabled or disabled", Bool: true, ClosedSet: []string{"true", "false"}},
			}},
			{Name: "update", Short: "Update endpoint callback, auth, or connection policy", Positionals: []string{"<app>", "<endpoint-id>"}, Flags: []cliFlag{
				{Name: "callback-url", Short: "new application callback URL", Value: "URL"},
				{Name: "callback-auth-token-stdin", Short: "read replacement callback bearer token from stdin"},
				{Name: "callback-auth-token", Short: "replacement callback bearer token (prefer --callback-auth-token-stdin)", Value: "TOKEN"},
				{Name: "connect-path", Short: "new callback path for connect events", Value: "PATH"},
				{Name: "message-path", Short: "new callback path for message events", Value: "PATH"},
				{Name: "disconnect-path", Short: "new callback path for disconnect events", Value: "PATH"},
				{Name: "auth-token-stdin", Short: "read replacement client bearer token from stdin"},
				{Name: "auth-token", Short: "new client static bearer token (prefer --auth-token-stdin)", Value: "TOKEN"},
				{Name: "auth-mode", Short: "new client auth mode", Value: "MODE", ClosedSet: []string{"none", "static_bearer", "oidc_jwt"}},
				{Name: "auth-issuer", Short: "new OIDC issuer URL", Value: "URL"},
				{Name: "auth-jwks-url", Short: "new OIDC JWKS URL", Value: "URL"},
				{Name: "auth-audience", Short: "replace OIDC audiences (repeatable)", Value: "AUDIENCE", Repeatable: true},
				{Name: "auth-algorithm", Short: "replace OIDC signing algorithms (repeatable)", Value: "ALG", Repeatable: true},
				{Name: "auth-claim", Short: "set required OIDC claim as KEY=VALUE (repeatable)", Value: "KEY=VALUE", Repeatable: true},
				{Name: "clear-auth-claims", Short: "remove all required OIDC claims"},
				{Name: "allowed-origin", Short: "replace exact browser origins (repeatable)", Value: "ORIGIN", Repeatable: true},
				{Name: "clear-allowed-origins", Short: "remove the browser-origin allowlist"},
				{Name: "max-connections", Short: "new per-endpoint connection cap", Value: "N"},
				{Name: "max-message-bytes", Short: "new decoded message size cap", Value: "N"},
				{Name: "max-connection-age-seconds", Short: "new connection age cap", Value: "SECONDS"},
				{Name: "enable", Short: "enable the endpoint"},
				{Name: "disable", Short: "disable the endpoint"},
			}},
			{Name: "delete", Short: "Delete a managed realtime endpoint", Positionals: []string{"<app>", "<endpoint-id>"}, Flags: []cliFlag{{Name: "yes", Short: "confirm the deletion", Req: true}}},
			{Name: "connections", Short: "List live connections for an endpoint", Positionals: []string{"<app>", "<endpoint-id>"}, Flags: []cliFlag{
				{Name: "channel", Short: "only connections subscribed to this channel", Value: "CHANNEL"},
				{Name: "principal", Short: "only connections for this authenticated principal", Value: "PRINCIPAL"},
				{Name: "limit", Short: "maximum connections to return (1-1000)", Value: "N"},
				{Name: "cursor", Short: "continue from a previous response's next_cursor", Value: "TOKEN"},
			}},
			{Name: "drain", Short: "Close a bounded, filtered set of live connections", Positionals: []string{"<app>", "<endpoint-id>"}, Flags: []cliFlag{
				{Name: "reason", Short: "required audit reason", Req: true, Value: "TEXT"},
				{Name: "channel", Short: "only connections subscribed to this channel", Value: "CHANNEL"},
				{Name: "principal", Short: "only connections for this principal", Value: "PRINCIPAL"},
				{Name: "connection-id", Short: "select a specific connection; repeat up to 100 times", Value: "ID"},
				{Name: "limit", Short: "maximum connections to select (1-1000)", Value: "N"},
				{Name: "dry-run", Short: "preview without closing connections"},
				{Name: "allow-partial", Short: "allow the reachable subset when nodes are unavailable"},
				{Name: "wait", Short: "wait for the drain to reach a terminal state"},
				{Name: "timeout", Short: "maximum time to wait with --wait", Value: "DURATION"},
			}},
			{Name: "drain-status", Short: "Show or wait for a durable realtime drain", Positionals: []string{"<app>", "<endpoint-id>", "<operation-id>"}, Flags: []cliFlag{
				{Name: "wait", Short: "wait for the drain to reach a terminal state"},
				{Name: "timeout", Short: "maximum time to wait with --wait", Value: "DURATION"},
			}},
			{Name: "send", Short: "Send a message to one live connection", Positionals: []string{"<app>", "<endpoint-id>", "<connection-id>"}, Flags: []cliFlag{{Name: "data", Short: "message text (or --data-stdin)", Value: "DATA"}, {Name: "data-stdin", Short: "read the message from stdin"}, {Name: "binary", Short: "send as a binary frame"}}},
			{Name: "close", Short: "Close one live connection", Positionals: []string{"<app>", "<endpoint-id>", "<connection-id>"}, Flags: []cliFlag{{Name: "reason", Short: "close reason", Value: "TEXT"}}},
			{Name: "subscribe", Short: "Subscribe one live connection to a channel", Positionals: []string{"<app>", "<endpoint-id>", "<connection-id>", "<channel>"}},
			{Name: "unsubscribe", Short: "Remove one live connection from a channel", Positionals: []string{"<app>", "<endpoint-id>", "<connection-id>", "<channel>"}},
			{Name: "publish", Short: "Publish a message to a channel", Positionals: []string{"<app>", "<endpoint-id>", "<channel>"}, Flags: []cliFlag{
				{Name: "data", Short: "message text (or --data-stdin)", Value: "DATA"},
				{Name: "data-stdin", Short: "read the message from stdin"},
				{Name: "binary", Short: "send as a binary frame"},
				{Name: "delivery", Short: "live by default or preview-only retained (up to 4 KiB)", Value: "MODE", ClosedSet: []string{"live", "retained"}},
				{Name: "idempotency-key", Short: "stable retry key; required for retained delivery", Value: "KEY"},
			}},
			{Name: "auth", Short: "Rotate, finalize, or inspect static bearer auth", Subcommands: []cliSub{
				{Name: "rotate", Short: "Stage a new bearer token; the old one stays valid for the grace period", Positionals: []string{"<app>", "<endpoint-id>"}, Flags: []cliFlag{
					{Name: "token-stdin", Short: "read the new token from stdin"},
					{Name: "token", Short: "new token (prefer --token-stdin)", Value: "TOKEN"},
					{Name: "grace-period", Short: "seconds the previous token stays valid", Value: "SECONDS"},
				}},
				{Name: "finalize", Short: "Retire the previous bearer token now", Positionals: []string{"<app>", "<endpoint-id>"}},
				{Name: "status", Short: "Show bearer token rotation state", Positionals: []string{"<app>", "<endpoint-id>"}},
			}},
		},
	},
	{
		Name:        "rollback",
		DocSlug:     "rollback",
		Short:       "Restore a previous deployment, or check an exact historical rollback",
		Examples:    []string{"gregale rollback my-api", "gregale rollback my-api --to v41", "gregale rollback my-api --to v41 --expected-current v42 --wait"},
		Positionals: []string{"<slug>"},
		Flags: []cliFlag{
			{Name: "to", Short: "target deployment id or vN revision (e.g. v41)", Value: "deployment_id|vN"},
			{Name: "expected-current", Short: "exact completed serving deployment; requires --to", Value: "deployment_id|vN"},
			{Name: "reason", Short: "one-line reason of at most 256 bytes; requires --expected-current", Value: "TEXT"},
			{Name: "wait", Short: "wait for binding checks and service handoff completion; requires --expected-current"},
			{Name: "timeout", Short: "wait deadline (default 10m)", Value: "duration"},
			{Name: "poll-interval", Short: "poll interval (default 2s)", Value: "duration"},
			{Name: "json", Short: "machine-readable output"},
		},
		Subcommands: []cliSub{{Name: "status", Short: "Read an exact rollback operation; waiting never submits another rollback", Positionals: []string{"<slug>"}, Flags: []cliFlag{
			{Name: "operation", Short: "accepted rollback operation UUID", Value: "UUID", Req: true},
			{Name: "wait", Short: "wait for completion with a committed audit receipt"},
			{Name: "timeout", Short: "wait deadline (default 10m)", Value: "duration"},
			{Name: "poll-interval", Short: "poll interval (default 2s)", Value: "duration"},
			{Name: "json", Short: "print the operation receipt"},
		}}},
	},
	{
		// SAFE-RELEASES-R (issue #976 / ADR-122): the
		// operator manual-recovery escape hatch — see
		// cmd/gregale/commands_rollouts.go. The CLI
		// subcommand `gregale rollouts recover <slug>` is
		// the canonical caller; the route is mounted at
		// POST /v1/apps/{slug}/rollouts/recover (apid).
		Name:     "rollouts",
		DocSlug:  "rollouts",
		Short:    "Operator manual rollout recovery (rollouts recover <slug> --action advance|promote|abort --reason <text>)",
		Audience: cliAudienceOperator,
		Subcommands: []cliSub{
			{Name: "recover", Short: "Manually advance / promote / abort a stuck rollout (operator escape hatch)"},
			{Name: "status", Short: "Inspect an exact rollout and optionally wait for handoff completion", Flags: []cliFlag{{Name: "deployment", Value: "ID|vN", Req: true, Short: "exact deployment"}, {Name: "wait", Short: "wait for completion"}, {Name: "timeout", Value: "duration", Short: "wait deadline (default 10m)"}, {Name: "poll-interval", Value: "duration", Short: "poll interval (default 2s)"}}},
		},
		Positionals: []string{"<slug>"},
		Flags: []cliFlag{
			{Name: "action", Short: "recover action", ClosedSet: []string{"advance", "promote", "abort"}, Req: true},
			{Name: "reason", Short: "operator-supplied reason (logged to deployment_audit)", Value: "text"},
			{Name: "deployment", Short: "exact deployment for abort", Value: "ID|vN"},
			{Name: "expected-predecessor", Short: "exact retained predecessor to restore (requires --deployment)", Value: "ID|vN"},
		},
	},
	{
		Name:    "projects",
		DocSlug: "projects",
		Short:   "Inspect and recover repository projects",
		Subcommands: []cliSub{
			{Name: "list", Short: "List projects in this account"},
			{Name: "info", Short: "Show a project and its workloads", Positionals: []string{"<slug>"}},
			{Name: "environments", Aliases: []string{"envs"}, Short: "Manage project environments (list|create|protect|unprotect|inspect|release-sets|releases|qualify|preflight|history|config [set]|routes set|queues get|queues set|diff|preview|promote|status|rollback); qualify binds probes to release, config, and secret revisions", Subcommands: []cliSub{
				{Name: "list", Aliases: []string{"ls"}, Short: "List environments"},
				{Name: "create", Short: "Create or clone an environment"},
				{Name: "protect", Short: "Protect an environment"},
				{Name: "unprotect", Short: "Remove environment protection"},
				{Name: "inspect", Short: "Inspect the active graph and environment deployments"},
				{Name: "release-sets", Short: "List release graphs and their retention deadlines", Flags: []cliFlag{{Name: "before", Value: "CURSOR", Short: "alias for --cursor"}, {Name: "cursor", Value: "CURSOR", Short: "opaque continuation cursor"}, {Name: "all", Short: "walk every page"}, {Name: "limit", Value: "N", Short: "page size"}}},
				{Name: "releases", Aliases: []string{"release"}, Short: "List live workload deployments"},
				{Name: "qualify", Short: "Run health and smoke GET probes against exact active release-set deployments", Positionals: []string{"<project-slug>", "<environment-slug>"}, Flags: []cliFlag{
					{Name: "profile", Short: "YAML probe profile defining every release-set workload", Value: "FILE", Req: true},
				}},
				{Name: "preflight", Short: "Qualify the source release and check promotion readiness for CI", Positionals: []string{"<project-slug>"}, Flags: []cliFlag{
					{Name: "from", Short: "source environment to qualify and promote", Value: "ENV", Req: true},
					{Name: "to", Short: "target environment to check", Value: "ENV", Req: true},
					{Name: "profile", Short: "YAML probe profile defining every source workload", Value: "FILE", Req: true},
					{Name: "sync-config", Short: "include non-secret source config in the promotion preview"},
				}},
				{Name: "history", Short: "List environment promotions", Flags: []cliFlag{{Name: "before", Value: "CURSOR", Short: "alias for --cursor"}, {Name: "cursor", Value: "CURSOR", Short: "opaque continuation cursor"}, {Name: "all", Short: "walk every page"}, {Name: "limit", Value: "N", Short: "page size (1..100)"}, {Name: "from", Value: "ENV", Short: "source environment filter"}, {Name: "status", Value: "STATUS", Short: "running|succeeded|failed"}}},
				{Name: "config", Short: "Read or update environment configuration", Positionals: []string{"<project-slug>", "<environment-slug>"}, Subcommands: []cliSub{
					{Name: "set", Short: "Update environment configuration with optimistic hash checking", Positionals: []string{"<project-slug>", "<environment-slug>"}, Flags: []cliFlag{{Name: "file", Short: "JSON configuration file (choose --file or --stdin)", Value: "PATH"}, {Name: "stdin", Short: "read JSON configuration from stdin"}, {Name: "dry-run", Short: "preview the change without writing it"}, {Name: "if-hash", Short: "apply only if the current config hash matches", Value: "HASH"}, {Name: "yes", Short: "confirm the configuration update"}}},
					{Name: "apply", Short: "Alias for config set", Positionals: []string{"<project-slug>", "<environment-slug>"}, Flags: []cliFlag{{Name: "file", Short: "JSON configuration file (choose --file or --stdin)", Value: "PATH"}, {Name: "stdin", Short: "read JSON configuration from stdin"}, {Name: "dry-run", Short: "preview the change without writing it"}, {Name: "if-hash", Short: "apply only if the current config hash matches", Value: "HASH"}, {Name: "yes", Short: "confirm the configuration update"}}},
				}},
				{Name: "routes", Short: "Manage environment routes"},
				{Name: "policies", Short: "Manage environment policies"},
				{Name: "queues", Short: "Read or replace a stage workload's complete desired queue collection; consumer activation is unavailable. Set input contains expected_revision and bindings; [] removes all definitions", Positionals: []string{"<get|set>", "<project>", "<stage>", "<workload>"}, Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "set reads this JSON file (choose --file or --stdin)"}, {Name: "stdin", Short: "set reads JSON from stdin"}}, Examples: []string{"gregale projects environments queues get shop staging shop-worker", "gregale projects environments queues set shop staging shop-worker --file queues.json"}, Subcommands: []cliSub{
					{Name: "get", Short: "Read queue definitions and their workload revision as JSON", Positionals: []string{"<project>", "<stage>", "<workload>"}, Examples: []string{"gregale projects environments queues get shop staging shop-worker"}},
					{Name: "set", Short: "Replace queue definitions from JSON containing expected_revision and bindings; [] removes all stage bindings", Positionals: []string{"<project>", "<stage>", "<workload>"}, Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "JSON queue configuration file (choose --file or --stdin)"}, {Name: "stdin", Short: "read JSON queue configuration from stdin"}}, Examples: []string{"gregale projects environments queues set shop staging shop-worker --file queues.json"}},
				}},
				{Name: "gitops", Short: "Review Git definitions, adopt owned fields, and inspect reconciliation (JSON output)", Subcommands: []cliSub{
					{Name: "status", Positionals: []string{"<project>", "<environment>"}, Short: "Inspect the source and recent reconciliation attempts"},
					{Name: "bind", Positionals: []string{"<project>", "<environment>"}, Short: "Bind the verified project repository to a definition", Flags: []cliFlag{
						{Name: "manifest-path", Value: "PATH", Short: "Environment definition path in Git"},
						{Name: "ref", Value: "REF", Short: "Git ref selecting revision candidates"},
						{Name: "mode", Value: "MODE", Short: "report (default; enforcement unavailable in preview)"},
						{Name: "prune", Short: "Allow removal of previously owned fields"},
					}},
					{Name: "rebind", Positionals: []string{"<project>", "<environment>"}, Short: "Release ownership and replace the source binding", Flags: []cliFlag{{Name: "expected-generation", Value: "N", Short: "Reviewed current source generation", Req: true}, {Name: "manifest-path", Value: "PATH", Short: "Replacement definition path", Req: true}, {Name: "ref", Value: "REF", Short: "Replacement Git ref"}, {Name: "approval-policy", Value: "POLICY", Short: "manual or protected_branch"}, {Name: "yes", Short: "Confirm ownership release while preserving values"}}},
					{Name: "unbind", Positionals: []string{"<project>", "<environment>"}, Short: "Disconnect the source and release its ownership", Flags: []cliFlag{{Name: "expected-generation", Value: "N", Short: "Reviewed current source generation", Req: true}, {Name: "yes", Short: "Confirm ownership release while preserving values"}}},
					{Name: "review", Positionals: []string{"<project>", "<environment>"}, Short: "Fetch an immutable commit and output a review receipt", Flags: []cliFlag{{Name: "commit", Value: "SHA", Short: "Exact lowercase GitHub commit SHA"}}},
					{Name: "approve", Positionals: []string{"<project>", "<environment>"}, Short: "Approve the digest and generation in a reviewed receipt", Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "Saved revision review JSON"}, {Name: "yes", Short: "Confirm approval of the reviewed bytes"}}},
					{Name: "adoption-preview", Positionals: []string{"<project>", "<environment>"}, Short: "Inspect ownership transfer without changing values"},
					{Name: "adopt", Positionals: []string{"<project>", "<environment>"}, Short: "Transfer ownership from a reviewed adoption plan", Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "Saved adoption plan JSON"}, {Name: "yes", Short: "Confirm the reviewed ownership transfer"}}},
					{Name: "controls", Positionals: []string{"<project>", "<environment>"}, Short: "Update fenced reporting, pruning, or suspension controls", Flags: []cliFlag{{Name: "generation", Value: "N", Short: "Current source generation"}, {Name: "mode", Value: "MODE", Short: "report (enforcement unavailable in preview)"}, {Name: "prune", Short: "Set pruning (accepts =false)"}, {Name: "suspended", Short: "Set suspension (accepts =false)"}}},
					{Name: "override", Positionals: []string{"<project>", "<environment>"}, Short: "Permit an expiring edit to an owned field", Flags: []cliFlag{{Name: "resource", Value: "RESOURCE", Short: "Logical resource"}, {Name: "path", Value: "PATH", Short: "Owned field path"}, {Name: "reason", Value: "REASON", Short: "Reason for the temporary edit"}, {Name: "expires", Value: "RFC3339", Short: "Expiry within twenty-four hours"}}},
					{Name: "remove-override", Positionals: []string{"<project>", "<environment>"}, Short: "Revoke a field override", Flags: []cliFlag{{Name: "resource", Value: "RESOURCE", Short: "Logical resource"}, {Name: "path", Value: "PATH", Short: "Owned field path"}}},
				}},
				{Name: "diff", Short: "Compare environments"},
				{Name: "preview", Aliases: []string{"promotion-preview"}, Short: "Plan a promotion", Flags: []cliFlag{
					{Name: "from", Short: "source environment", Value: "ENV", Req: true},
					{Name: "to", Short: "target environment", Value: "ENV", Req: true},
					{Name: "sync-config", Short: "include non-secret source config in the promotion preview"},
				}},
				{Name: "promote", Short: "Promote workloads", Flags: []cliFlag{
					{Name: "from", Short: "source environment", Value: "ENV", Req: true},
					{Name: "to", Short: "target environment", Value: "ENV", Req: true},
					{Name: "sync-config", Short: "copy source non-secret environment configuration to the target"},
					{Name: "yes", Short: "confirm the promotion"},
					{Name: "idempotency-key", Short: "stable key for retrying this promotion", Value: "KEY"},
					{Name: "wait", Short: "wait for the promotion to reach a terminal status"},
					{Name: "progress", Short: "print promotion transitions while waiting (human output only)"},
					{Name: "timeout", Short: "maximum wait for promotion completion (seconds, or a duration such as 10m)", Value: "SECONDS|DURATION"},
				}},
				{Name: "status", Short: "Inspect a promotion"},
				{Name: "rollback", Short: "Roll back a promotion"},
			}},
			{Name: "update", Short: "Update repository or production branch", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "repo", Short: "GitHub repository owner/name; empty unbinds", Value: "OWNER/NAME"},
				{Name: "branch", Short: "production branch", Value: "BRANCH"},
			}},
			{Name: "rm", Aliases: []string{"delete"}, Short: "Preview or delete a project", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "dry-run", Short: "preview affected state"},
				{Name: "yes", Short: "confirm project deletion"},
			}},
		},
		Positionals:         []string{"<project-slug>"},
		CompletionPositions: projectsCLICompletionPositions(),
	},
	{
		Name:    "scan",
		DocSlug: "scan",
		Short:   "Decomposition dry-run (--tarball | --path | --repo OWNER/NAME)",
		Flags: []cliFlag{
			{Name: "tarball", Short: "scan a source tarball", Value: "PATH"},
			{Name: "path", Short: "scan a local directory", Value: "DIR"},
			{Name: "repo", Short: "scan a GitHub repo after gregale connect", Value: "OWNER/NAME"},
			{Name: "repository", Short: "GitHub owner/name to bind to the project (defaults to --repo)", Value: "OWNER/NAME"},
			{Name: "install-id", Short: "optional GitHub installation id; normally resolved from the connected account", Value: "N"},
			{Name: "production-branch", Short: "production branch for the project", Value: "BRANCH"},
			{Name: "project-slug", Short: "kebab slug; default = repo dir basename", Value: "SLUG"},
			{Name: "environment", Short: "registered project environment to scan", Value: "SLUG"},
			// ADR-124 follow-up #1: --exclude + --show-affected
			// ship on scan as well as deploy (the partition is the
			// preview surface, scan is the operator's first stop).
			// Same rationale as the deploy entries above: they were
			// added to cmdScan in PR-#1065 but missing from the
			// manifest that drives `gregale man scan` and the shell
			// completion tables.
			{Name: "exclude", Short: "omit workloads (comma-separated slugs; cannot combine with --only)", Value: "SLUGS"},
			{Name: "show-affected", Short: "show workloads that deploy or stay unchanged"},
			{Name: "explain", Short: "show detector provenance and skipped/merged decisions"},
			// ADR-124 follow-up #3 (PR-B commit 5): symmetric flag
			// set on scan (no-op on the scan path; the scan handler
			// ignores persist_exclude). Accepted so a single flag set
			// is reusable across the scan + apply pair.
			{Name: "persist-exclude", Short: "save --exclude slugs for future project deploys"},
		},
	},
	{
		Name:    "secrets",
		DocSlug: "secrets",
		Short:   "Manage sealed secrets and environment secret references",
		Subcommands: []cliSub{
			{Name: "refs", Short: "Manage destination-to-source names in a registered environment", Subcommands: []cliSub{
				{Name: "list", Short: "List reference names and shared environment-key quota", Examples: []string{"gregale secrets refs list --app my-api --environment production"}, Flags: secretReferenceCLIFlags()},
				{Name: "set", Short: "Select an existing scoped secret; respects Git field ownership", Examples: []string{"gregale secrets refs set --app my-api --environment production DATABASE_URL=secret:DATABASE_PRIMARY"}, Positionals: []string{"<KEY=secret:NAME>"}, Flags: secretReferenceCLIFlags()},
				{Name: "unset", Short: "Suppress a primary workload secret destination and preserve the sealed source", Examples: []string{"gregale secrets refs unset --app my-api --environment production DATABASE_URL"}, Positionals: []string{"<KEY>"}, Flags: secretReferenceCLIFlags()},
			}},
			{Name: "list", Short: "List sealed secrets", Examples: []string{"gregale secrets list --app my-api", "gregale secrets list --app my-api --scope __all__", "gregale secrets list --app my-api --class ephemeral", "gregale secrets list --app my-api --older-than 90d"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Value: "slug", Req: true},
				{Name: "scope", Short: "env scope filter (defaults to linked project environment)", Value: "SCOPE|__all__"},
				{Name: "class", Short: "filter by snapshot-retention class", Value: "CLASS", ClosedSet: []string{api.SecretClassPersistent, api.SecretClassEphemeral}},
				{Name: "older-than", Short: "filter to secrets not updated within a duration (for example 90d or 2160h); unknown timestamps are excluded", Value: "DURATION"},
			}},
			{Name: "set", Short: "Set a sealed secret; ephemeral values disable VM snapshots for the scope", Examples: []string{"gregale secrets set --app my-api DATABASE_URL=\"$DATABASE_URL\"", "printf '%s\\n' \"DATABASE_URL=$DATABASE_URL\" | gregale secrets set --app my-api --from-stdin", "gregale secrets set --app my-api DATABASE_URL=\"$DATABASE_URL\" --restart", "gregale secrets set --app my-api SESSION_TOKEN=\"$SESSION_TOKEN\" --class ephemeral"}, Positionals: []string{"[<KEY=VALUE>...]"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Value: "slug", Req: true},
				{Name: "from-stdin", Short: "read KEY=VALUE pairs from stdin"},
				{Name: "scope", Short: "env scope to write (defaults to linked project environment)", Value: "SCOPE"},
				{Name: "class", Short: "retention: persistent by default; ephemeral disables init/warm captures and forces cold boots; omission preserves an existing class", Value: "CLASS", ClosedSet: []string{api.SecretClassPersistent, api.SecretClassEphemeral}},
				{Name: "restart", Short: "restart the app and apply updated secrets now"},
			}},
			{Name: "unset", Short: "Remove a sealed secret (alias: rm)", Examples: []string{"gregale secrets unset --app my-api OLD_API_KEY", "gregale secrets unset --app my-api OLD_API_KEY --scope staging", "gregale secrets unset --app my-api OLD_API_KEY --wait-for-ack", "gregale secrets unset --app my-api OLD_API_KEY --restart"}, Positionals: []string{"<KEY>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Value: "slug", Req: true},
				{Name: "scope", Short: "env scope to delete from (defaults to linked project environment)", Value: "SCOPE"},
				{Name: "restart", Short: "restart the app so running instances drop the removed secret now"},
				{Name: "wait-for-ack", Short: "wait until every active authorized runtime confirms it removed the secret"},
				{Name: "timeout", Short: "maximum time to wait for runtime acknowledgements", Value: "DURATION"},
			}},
			{Name: "list-all", Short: "List every secret across apps", Examples: []string{"gregale secrets list-all --class ephemeral", "gregale secrets list-all --older-than 90d"}, Flags: []cliFlag{
				{Name: "before", Short: "pagination cursor from a previous call's next_before", Value: "slug|key"},
				{Name: "limit", Short: "page size (1..100; server caps at 100)", Value: "N"},
				{Name: "class", Short: "filter this page by snapshot-retention class", Value: "CLASS", ClosedSet: []string{api.SecretClassPersistent, api.SecretClassEphemeral}},
				{Name: "older-than", Short: "filter this page to secrets not updated within a duration; unknown timestamps are excluded", Value: "DURATION"},
			}},
			{Name: "audit", Short: "Audit secret update age and report unknown timestamps without exposing values", Examples: []string{"gregale secrets audit --older-than 90d", "gregale secrets audit --older-than 90d --fail-on-stale --json"}, Flags: []cliFlag{
				{Name: "older-than", Short: "required threshold based on when Gregale last updated the value, not provider rotation time (for example 90d or 2160h)", Value: "DURATION", Req: true},
				{Name: "fail-on-stale", Short: "exit non-zero when any secret exceeds the age threshold"},
			}},
			{Name: subRotate, Short: "Rotate a secret and optionally wait for runtime application", Examples: []string{"printf '%s\\n' \"DATABASE_URL=$DATABASE_URL\" | gregale secrets rotate --app my-api --from-stdin --restart --wait-for-ack", "printf '%s\\n' \"DATABASE_URL=$DATABASE_URL\" | gregale secrets rotate --app my-api --from-stdin --scope production --restart --wait-for-ack --timeout 5m"}, Positionals: []string{"[<KEY=VALUE>]"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Value: "slug", Req: true},
				{Name: "from-stdin", Short: "read one KEY=VALUE pair from stdin"},
				{Name: "scope", Short: "env scope to rotate (defaults to linked project environment)", Value: "SCOPE"},
				{Name: "restart", Short: "restart the app and apply the rotated secret now"},
				{Name: "wait-for-ack", Short: "wait until every active authorized runtime confirms it applied the secret (works with --restart)"},
				{Name: "timeout", Short: "maximum time to wait for restart and application acknowledgements", Value: "DURATION"},
			}},
		},
	},
	{
		// Compatibility surface for installation-scoped secrets used by
		// legacy non-GitHub senders. Standard GitHub App webhooks use the
		// single platform App secret documented in ADR-012 §8.
		Name:     "github-webhook-secret",
		DocSlug:  "github-webhook-secret",
		Short:    "Manage legacy installation-scoped webhook secrets (admin)",
		Audience: cliAudienceCompatibility,
		Subcommands: []cliSub{
			{Name: "set", Short: "Rotate the secret for one installation_id", Flags: []cliFlag{{Name: "installation-id", Short: "GitHub App installation_id", Req: true, Value: "ID"}, {Name: "secret", Short: "secret hex (32-64 chars; prefer --from-stdin)", Value: "HEX"}, {Name: "from-stdin", Short: "read the secret hex from stdin"}}},
		},
	},
	// operator-side verbs (sign-keys, node-key) moved to gregalectl
	// in PR-6.5; see cmd/gregale/constants.go for the dispatch consts.
	{
		Name:    "slo",
		DocSlug: "slo",
		Short:   "Per-app SLO panel (gregale slo <slug> [--window 24h]; slug defaults to linked context)",
		Flags: []cliFlag{
			{Name: "window", Short: "window (1h|24h|7d)", Value: "WINDOW", ClosedSet: []string{"1h", "24h", "7d"}},
		},
		Positionals: []string{"[<slug>]"},
	},
	{
		Name:    statusLiteral,
		DocSlug: "status",
		Short:   "Platform status: API availability, wake p95 and deployment success (not account-specific)",
	},
	{
		Name:    "tail",
		DocSlug: "tail",
		Short:   "Live tail of the unified event stream (app defaults to linked context)",
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
			{Name: "add", Short: "Add a trusted publisher", Positionals: []string{"<slug>", "<name>", "<pub.pem>"}},
			{Name: "remove", Short: "Remove a trusted publisher", Positionals: []string{"<slug>", "<name>"}},
			{Name: "list", Short: "List trusted publishers", Positionals: []string{"<slug>"}},
		},
	},
	{
		Name:    "usage",
		DocSlug: "usage",
		Short:   "Show this month's usage (gregale usage [--month YYYY-MM]|daily [--day YYYY-MM-DD]|storage [--day YYYY-MM-DD]|summary)",
		Subcommands: []cliSub{
			{Name: "daily", Short: "Per-day breakdown"},
			{Name: "storage", Short: "Per-app storage bytes"},
			{Name: "object-storage", Short: "Account object storage observations, safety policy and billing state"},
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
		Name:    "config",
		DocSlug: "config",
		Short:   "Manage non-secret local CLI settings (config get|set|list)",
		Subcommands: []cliSub{
			{Name: "get", Short: "Show one effective setting", Positionals: []string{"<api-base|json>"}},
			{Name: "set", Short: "Persist one non-secret setting", Positionals: []string{"<api-base|json>", "<value>"}},
			{Name: "list", Short: "Show all effective settings"},
		},
	},
	{
		Name:    "wake-timeline",
		DocSlug: "wake-timeline",
		Short:   "Walk the per-wake event stream (wake-timeline [<slug>] <wake-id> [--app SLUG] [--since RFC3339] [--limit N] [--all] [--verbose]; slug defaults to linked context)",
		Flags: []cliFlag{
			{Name: "app", Short: "app slug (alternative to the leading slug positional)", Value: "SLUG"},
			{Name: "since", Short: "RFC3339 timestamp", Value: "RFC3339"},
			{Name: "limit", Short: "page size (1..1000)", Value: "N"},
			{Name: "all", Short: "walk every page"},
			{Name: "verbose", Short: "show detailed restore phases in human output"},
		},
		Positionals: []string{"[<slug>]", "<wake-id>"},
	},
	{
		Name:    "throttle-suggestions",
		DocSlug: "throttle-suggestions",
		Short:   "Per-route throttle recommendations + dry-run preview (gregale throttle-suggestions <slug> [--range 5m] [--dry-run --candidate-rps N --candidate-burst N])",
		Flags: []cliFlag{
			{Name: "range", Short: "observation window (" + strings.Join(appmetrics.Ranges(), "|") + ")", Value: "WINDOW", ClosedSet: appmetrics.Ranges()},
			{Name: "dry-run", Short: "enable the dry-run preview pass (requires --candidate-rps)"},
			{Name: "candidate-rps", Short: "candidate rate-limit rps for the dry-run preview", Value: "N"},
			{Name: "candidate-burst", Short: "candidate burst for the dry-run preview", Value: "N"},
		},
		Positionals: []string{"<slug>"},
	},
	{
		Name:     "mail",
		DocSlug:  "mail-dry-run",
		Short:    "Render every mail template against a fixture account as JSON (operator dry-run before enabling a mail transport)",
		Audience: cliAudienceOperator,
		Subcommands: []cliSub{
			{Name: "dry-run", Short: "render every mail template against a fixture; print wire JSON"},
		},
		Flags: []cliFlag{
			{Name: "unsubscribe-url", Short: "List-Unsubscribe URL (RFC 8058); empty disables the header", Value: "URL"},
		},
	},
	{
		Name:        "wake",
		DocSlug:     "park-wake",
		Short:       "Wake a parked app (pulls out of snapshot)",
		Positionals: []string{"<slug>"},
		Flags: []cliFlag{
			{Name: "wait", Short: "wait for the requested wake to reach running"},
			{Name: "timeout", Short: "maximum time to wait for the requested wake (default 1m)", Value: "DURATION"},
			{Name: "poll-interval", Short: "interval between instance status checks (default 250ms)", Value: "DURATION"},
		},
		Examples: []string{"gregale wake my-api", "gregale wake --wait --timeout 2m my-api"},
	},
	{
		Name:    "traffic",
		DocSlug: "traffic",
		Short:   "Manage deployment traffic split (available on every plan)",
		Subcommands: []cliSub{
			{
				Name:        "set",
				Short:       "Set the traffic split for a deployment",
				Positionals: []string{"[<slug>]"},
				Flags: []cliFlag{
					{Name: "app", Short: "app slug; only needed to resolve a vN revision outside a linked project", Value: "SLUG"},
					{Name: "deployment", Short: "deployment id or vN revision to set the traffic split on", Req: true, Value: "ID"},
					{Name: "percent", Short: "traffic weight in [0, 100]; -1 = unset (server default 100)", Req: true, Value: "N"},
				},
			},
			{
				Name:        "promote",
				Short:       "Promote a live deployment to 100% production traffic",
				Positionals: []string{"[<slug>]"},
				Flags: []cliFlag{
					{Name: "app", Short: "app slug; only needed to resolve a vN revision outside a linked project", Value: "SLUG"},
					{Name: "deployment", Short: "deployment id or vN revision to promote", Req: true, Value: "ID"},
					{Name: "if-serving", Short: "require this deployment id or vN revision to remain at 100% traffic", Value: "ID"},
					{Name: "require-bindings", Short: "enforce a bindings check at the server's traffic write"},
					{Name: "max-verification-age", Short: "maximum probe age (default 10m); requires --require-bindings", Value: "DURATION"},
					{Name: "allow-unsupported", Short: "waive unsupported queue/outbound probes; requires --require-bindings"},
					{Name: "require-application-ack", Short: "require current application acknowledgements; requires --require-bindings"},
				},
			},
			{
				Name:        "status",
				Short:       "Show live deployment traffic weights for an app",
				Positionals: []string{"<slug>"},
			},
		},
	},
	{
		Name:    "log-drains",
		DocSlug: "log-drains",
		Short:   "Ship app runtime logs to an HTTP JSON or OTLP endpoint",
		Subcommands: []cliSub{
			{Name: "list", Short: "List an app's log drains", Positionals: []string{"[<slug>]"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Value: "SLUG"},
			}, Examples: []string{"gregale log-drains list --app my-app"}},
			{Name: "add", Short: "Add a log drain; the credential is read from an environment variable", Positionals: []string{"[<slug>]"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Value: "SLUG"},
				{Name: "url", Short: "destination URL", Value: "URL", Req: true},
				{Name: "kind", Short: "destination format (default http_json)", Value: "KIND", ClosedSet: []string{"http_json", "otlp"}},
				{Name: "auth-header-env", Short: "environment variable holding the Authorization header value", Value: "ENV"},
				{Name: "disabled", Short: "create the drain disabled"},
			}, Examples: []string{"LOG_TOKEN='Bearer …' gregale log-drains add --app my-app --url https://logs.example.com/ingest --auth-header-env LOG_TOKEN"}},
			{Name: "get", Short: "Show one log drain (credential masked)", Positionals: []string{"[<slug>]"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Value: "SLUG"},
				{Name: "id", Short: "log drain id", Value: "ID", Req: true},
			}},
			{Name: "health", Short: "Show delivery health: queue, delivered, failed and last error", Positionals: []string{"[<slug>]"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Value: "SLUG"},
				{Name: "id", Short: "log drain id", Value: "ID", Req: true},
			}, Examples: []string{"gregale log-drains health --app my-app --id <drain-id>"}},
			{Name: "update", Short: "Change a drain's URL or credential, or pause and resume it", Positionals: []string{"[<slug>]"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Value: "SLUG"},
				{Name: "id", Short: "log drain id", Value: "ID", Req: true},
				{Name: "url", Short: "new destination URL", Value: "URL"},
				{Name: "auth-header-env", Short: "environment variable holding the new Authorization header value", Value: "ENV"},
				{Name: "enable", Short: "resume delivery"},
				{Name: "disable", Short: "pause delivery"},
			}},
			{Name: "rm", Short: "Delete a log drain", Positionals: []string{"[<slug>]"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Value: "SLUG"},
				{Name: "id", Short: "log drain id", Value: "ID", Req: true},
			}},
		},
	},
	{
		Name:    "mirror",
		DocSlug: "mirror",
		Short:   "Manage traffic mirroring and sanitized replay (Pro/Scale only). Rules default to 5% and mirror only safe methods; bodies over 64 KiB are skipped, and raw bodies are never retained.",
		Subcommands: []cliSub{
			{Name: "list", Short: "List mirror rules", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			}},
			{Name: "create", Short: "Create a mirror rule", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "source", Short: "source deployment id or vN revision (live)", Req: true, Value: "ID"},
				{Name: "mirror", Short: "mirror deployment id or vN revision (live; same app)", Req: true, Value: "ID"},
				{Name: "percent", Short: "fan-out percent in [0, 100]; defaults to a 5% sample", Value: "N"},
				{Name: "include-body", Short: "compare response values using hashes; raw response bodies are never retained"},
				{Name: "allow-unsafe-methods", Short: "also mirror POST, PUT, PATCH, and DELETE; these can cause side effects"},
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
				{Name: "include-body", Short: "enable body-hash comparison (mutually exclusive with --no-include-body)"},
				{Name: "no-include-body", Short: "disable body-hash comparison"},
				{Name: "allow-unsafe-methods", Short: "also mirror POST, PUT, PATCH, and DELETE"},
				{Name: "safe-methods-only", Short: "skip POST, PUT, PATCH, and DELETE"},
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
			{Name: "replay", Short: "Replay a sanitized historical request corpus", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "id", Short: "mirror rule id", Req: true, Value: "ID"},
				{Name: "file", Short: "corpus JSON file, or - for stdin", Req: true, Value: "PATH"},
				{Name: "allow-unsafe-methods", Short: "allow POST, PUT, PATCH, and DELETE"},
			}},
		},
	},
	{
		Name:    "cache",
		DocSlug: "cache",
		Short:   "Declare or purge response caching (cache GET /path/:id for 30s)",
		Subcommands: []cliSub{
			{Name: "GET", Short: "Cache GET responses for a route", Positionals: []string{"<path>", "for", "<duration>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug (defaults to linked project context)", Value: "SLUG"},
				{Name: "host", Short: "hostname override", Value: "HOST"},
				{Name: "stale-while-revalidate", Short: "serve stale while refreshing", Value: "DURATION"},
				{Name: "stale-if-error", Short: "serve stale when the origin fails", Value: "DURATION"},
				{Name: "vary-on", Short: "header included in the cache key", Value: "HEADER", ClosedSet: []string{"Accept-Language", "Accept-Encoding"}},
				{Name: "priority", Short: "match priority (lower wins)", Value: "N"},
			}},
			{Name: "HEAD", Short: "Cache HEAD responses for a route", Positionals: []string{"<path>", "for", "<duration>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug (defaults to linked project context)", Value: "SLUG"},
				{Name: "host", Short: "hostname override", Value: "HOST"},
				{Name: "stale-while-revalidate", Short: "serve stale while refreshing", Value: "DURATION"},
				{Name: "stale-if-error", Short: "serve stale when the origin fails", Value: "DURATION"},
				{Name: "vary-on", Short: "header included in the cache key", Value: "HEADER", ClosedSet: []string{"Accept-Language", "Accept-Encoding"}},
				{Name: "priority", Short: "match priority (lower wins)", Value: "N"},
			}},
			{Name: "purge", Short: "Purge cached responses for one app", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "path", Short: "optional normalized request path glob", Value: "GLOB"},
				{Name: "tag", Short: "optional cache tag", Value: "TAG"},
			}},
		},
	},
	{
		Name:    dispatchUploadCache,
		DocSlug: "cli",
		Short:   "Inspect or clean resumable source-upload recovery state",
		Subcommands: []cliSub{
			{Name: "list", Short: "List resumable, stale, and orphaned cache entries"},
			{Name: "cleanup", Short: "Remove stale and excess state safely", Flags: []cliFlag{
				{Name: "older-than", Short: "maximum recovery-state age", Value: "D"},
				{Name: "max-entries", Short: "maximum recovery records to retain", Value: "N"},
				{Name: "dry-run", Short: "show actions without deleting files"},
			}},
		},
	},
	{
		Name:    "webhooks",
		DocSlug: "webhooks",
		Short:   "Manage app and account release webhooks (webhooks account <verb>)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List an app's webhooks (slug defaults to linked context)", Positionals: []string{"[<slug>]"}, Flags: []cliFlag{{Name: "app", Short: "app slug (alternative to the positional)", Value: "slug"}}},
			{Name: "add", Short: "Add a webhook", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "slug"}, {Name: "target-url", Short: "HTTPS target URL", Req: true, Value: "URL"}, {Name: "event", Short: "event to deliver (repeat)", Value: "EVENT"}, {Name: "retry-policy", Short: "default|aggressive|none", Value: "POLICY"}, {Name: "delivery-format", Short: "json|cloudevents", Value: "FORMAT"}}},
			{Name: "info", Short: "Show one webhook", Positionals: []string{"<webhook-id>"}},
			{Name: "update", Short: "Update one webhook", Positionals: []string{"<id>"}},
			{Name: "rm", Short: "Delete one webhook", Positionals: []string{"<id>"}},
			{Name: "deliveries", Short: "Show the delivery ledger", Positionals: []string{"<id>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "SLUG"},
				{Name: "status", Short: "filter delivery status", Value: "STATUS", ClosedSet: []string{"pending", "in_flight", "succeeded", "failed", "dead"}},
				{Name: "limit", Short: "page size (1..100, default 50)", Value: "N"},
				{Name: "page-size", Short: "alias for --limit", Value: "N"},
				{Name: "cursor", Short: "opaque continuation cursor", Value: "CURSOR"},
				{Name: "page-token", Short: "alias for --cursor", Value: "CURSOR"},
				{Name: "all", Short: "walk every page using --limit and --cursor"},
			}},
			{Name: "retry", Short: "Retry a failed delivery", Positionals: []string{"<webhook-id>", "<delivery-id>"}},
			{Name: "rotate-secret", Short: "Rotate the webhook signing secret", Positionals: []string{"<webhook-id>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "secret", Short: "replacement HMAC-SHA256 secret", Value: "VALUE"},
				{Name: "from-stdin", Short: "read the replacement secret from stdin"},
			}},
			accountWebhookCLISubcommand(),
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
