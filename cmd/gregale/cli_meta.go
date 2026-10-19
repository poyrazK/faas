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
// Internal hosting additionally requires opt-in on all discovery surfaces.
type cliAudience uint8

const (
	cliAudienceCustomer cliAudience = iota
	cliAudienceOperator
	cliAudienceCompatibility
)

const resourceProfileHelpNote = "Connection: use --profile NAME before the command; RAM/CPU: use --profile SIZE after the command."

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
	// CompactHelp shows only Common flags and subcommands by default.
	// --help --all, completion, and references retain the complete manifest.
	CompactHelp bool
	// HelpNotes explain defaults and argument placement across help and references.
	HelpNotes []string
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
	// Unreleased keeps a dark-launched command out of every customer
	// surface, and out of dispatch, unless it is turned on (unreleased.go).
	Unreleased bool
}

// customerCliCommands returns the commands intended for normal application
// developers. Keep the full cliCommands manifest intact: hidden operator and
// legacy entries still need completion/man/dispatch coverage.
func customerCliCommands() []cliCommand {
	commands := make([]cliCommand, 0, len(cliCommands))
	for _, command := range visibleCliCommands() {
		if command.Audience == cliAudienceCustomer {
			commands = append(commands, command)
		}
	}
	return commands
}

func advancedCliCommands() []cliCommand {
	commands := make([]cliCommand, 0, len(cliCommands))
	for _, command := range visibleCliCommands() {
		if command.Audience != cliAudienceCustomer && cliCommandAvailable(command.Name) {
			commands = append(commands, command)
		}
	}
	return commands
}

// Internal hosting requires opt-in for discovery as well as execution.
// Keep the full manifest available for contract checks and operator builds.
func cliCommandAvailable(name string) bool {
	return name != "sites" || hostingCLIEnabled()
}

// cliHelpGroup maps the customer command vocabulary to the workflow sections
// used by root help. Operator and compatibility entries intentionally fall
// through to Advanced so `gregale help --all` keeps available commands
// discoverable without making them part of the normal customer path.
func cliHelpGroup(command cliCommand) string {
	if command.Audience != cliAudienceCustomer {
		return "Advanced"
	}
	switch command.Name {
	case "account", "billing", "capabilities", "context", "dashboard", "doctor", "invitations", "invoices", "keys", "link", "login", "logout", "menu", "mfa", "open", "orgs", "overage-cap", "plan", "signup", "unlink", "upload-cache", "usage", "version", "completion", "man", "whoami":
		return "Core"
	case "apps", "app", "build", "connect", "cors", "deploy", "deployment", "deployments", "deploys", "dev", "domains", "edge-rules", "env", "github", "init", "invoke", "mcp", "openapi", "preview", "projects", "registry", "rollback", "routes", "scan", "secrets", "start", "tenant-surfaces", "platform-tenants", "consumers", "trusted-publishers":
		return "API"
	case "add", "ai", "automations", "bindings", "bucket", "crons", "delayed-task", "events", "send", "deliver", "invocations", "entities", "jobs", "operations", "customer-operations", "run", "runs", "triggers", "webhooks", "workflows", "cache", "postgres":
		return "Data"
	case "canary", "mirror", "park", "ps", "queue", "dlq", "traffic", "wake", "wake-ahead", "wake-timeline", "workers":
		return "Delivery"
	case "alerts", "analytics", "channels", "audit-events", "debug", "inspect", "log-drains", "logs", "metrics", "realtime", "slo", "slos", "status", "synthetics", "tail", "throttle-suggestions", "trace":
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
	// Common keeps this verb visible in a parent's compact help.
	Common bool
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
	// Unreleased keeps a dark-launched flag out of every customer surface
	// unless it is turned on (unreleased.go).
	Unreleased bool
	// Common keeps this option visible in compact command help. Group labels
	// organize expanded help without changing parser or completion behavior.
	Common bool
	Group  string
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
		{Name: "profile", Short: "RAM/CPU resource size (connection --profile goes before app)", Value: "SIZE", ClosedSet: []string{"micro", "small", "medium", "large", "xlarge"}},
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
		"environments gitops plan",
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

// templateNames13 is the customer template catalog. The historical name is
// retained because tests and completion metadata refer to this package-local
// symbol; it contains every listed embedded template. Mirrors
// cmd/gregale/templates/embed.go::ListedNames verbatim; the ClosedSet literals
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
	// platformTenantStatementIDFlag addresses one cross-app statement revision.
	platformTenantStatementIDFlag = cliFlag{Name: "statement-id", Short: "statement revision UUID", Value: "UUID", Req: true}
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
	"function-rust",
	"event-worker",
	"queue-worker",
	"s3-uploader",
	"object-storage-node",
	"object-storage-python",
	"slack-bot",
	"rest-api-postgres",
	"rest-api-postgres-python",
	"cron-worker",
	"webhook-receiver",
	"ai-chat",
	"secret-reload-node",
	"customer-platform",
	"mcp-node",
	"mcp-go",
	"mcp-python",
	"customer-operation-export",
	"customer-operation-job-export",
	"customer-operation-workflow-export",
	"data-api",
	"data-api-starter",
	"data-api-teams",
	"data-api-support-console",
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
	{
		Name: "sites", DocSlug: "sites", Audience: cliAudienceOperator,
		Short: "Internal static hosting (requires the frontend hosting flag)",
		Subcommands: []cliSub{
			{Name: "create", Short: "Create an internal site", Flags: []cliFlag{{Name: "name", Value: "NAME"}, {Name: "project", Value: "PROJECT"}, {Name: "environment", Value: "ENV"}}},
			{Name: "list", Short: "List owned internal sites"},
			{Name: "status", Short: "Inspect intent, publication, and pending retries", Flags: []cliFlag{{Name: "site", Value: "ID"}}},
			{Name: "serve", Short: "Preview a validated build on loopback (independent internal opt-in)", Flags: []cliFlag{{Name: "path", Value: "DIR"}, {Name: "port", Value: "PORT"}, {Name: "spa", Bool: true}}},
			{Name: "plan", Short: "Compare a local build and prospective quotas without uploading (internal opt-in)", Flags: []cliFlag{{Name: "site", Req: true, Value: "UUID"}, {Name: "path", Value: "DIR"}, {Name: "spa", Bool: true}}},
			{Name: "deploy", Short: "Publish a prebuilt static directory", Flags: []cliFlag{{Name: "site", Value: "ID"}, {Name: "path", Value: "DIR"}, {Name: "spa", Bool: true}, {Name: "source-repository", Value: "LABEL"}, {Name: "source-commit", Value: "SHA"}, {Name: "source-branch", Value: "BRANCH"}, {Name: "description", Value: "TEXT"}, {Name: "no-promote", Bool: true}, {Name: "plan", Value: "FILE", Short: "reviewed saved plan; requires --no-promote and independent opt-in"}, {Name: "resume-release", Value: "UUID", Short: "recover an exact retained release; requires --plan and independent opt-in"}, {Name: "wait", Bool: true}, {Name: "wait-timeout", Value: "DURATION"}}},
			{Name: "tokens", Short: "Create, list or revoke one-site CI credentials (internal opt-in)", Flags: []cliFlag{{Name: "site", Value: "ID"}, {Name: "credential", Value: "ID"}, {Name: "label", Value: "LABEL"}, {Name: "out", Value: "PRIVATE_FILE"}, {Name: "ttl", Value: "DURATION"}, {Name: "operations", Value: "stage,preview"}}},
			{Name: "cleanup", Short: "Preview eligible/protected releases and catalog reservations without retiring anything", Flags: []cliFlag{{Name: "site", Value: "ID"}, {Name: "pending-age", Value: "DURATION"}, {Name: "staged-age", Value: "DURATION"}}},
			{Name: "retention", Short: "Show or set the default-off per-site cleanup policy", Flags: []cliFlag{{Name: "site", Value: "ID"}, {Name: "pending-age", Value: "DURATION"}, {Name: "staged-age", Value: "DURATION"}, {Name: "enabled", Bool: true}, {Name: "expected-revision", Value: "N"}}},
			{Name: "runs", Short: "List recorded CI runs and stored release/preview evidence", Flags: []cliFlag{{Name: "site", Req: true, Value: "UUID"}, {Name: "before", Value: "CURSOR"}}},
			{Name: "ci-setup", Short: "Export a reviewable GitHub workflow from an existing site trust", Flags: []cliFlag{{Name: "config", Req: true, Value: "FILE"}, {Name: "out", Req: true, Value: "FILE"}}},
			{Name: "ci-preflight", Short: "Compare a saved workflow and local preparation Action without uploading them", Flags: []cliFlag{{Name: "config", Req: true, Value: "FILE"}, {Name: "repo-root", Req: true, Value: "DIR"}}},
			{Name: "diagnose", Short: "Explain stored deployment evidence without mutations or health probes", Flags: []cliFlag{{Name: "site", Req: true, Value: "UUID"}, {Name: "run-id", Value: "UUID"}, {Name: "path", Value: "DIR"}, {Name: "spa", Bool: true}, {Name: "oidc-trust", Value: "UUID"}}},
			{Name: "ci", Short: "Resume a stable prebuilt-to-preview CI intent without promoting", Flags: []cliFlag{{Name: "oidc-trust", Value: "UUID", Short: "explicit GitHub site trust; requires independent OIDC opt-in"}, {Name: "site", Value: "ID"}, {Name: "run-id", Value: "UUID"}, {Name: "path", Value: "DIR"}, {Name: "spa", Bool: true}, {Name: "preview-ttl", Value: "DURATION"}, {Name: "wait-timeout", Value: "DURATION"}, {Name: "source-repository", Value: "LABEL"}, {Name: "source-commit", Value: "SHA"}, {Name: "source-branch", Value: "BRANCH"}, {Name: "description", Value: "TEXT"}}},
			{Name: "history", Short: "Inspect retained accepted-promotion history (internal opt-in)", Flags: []cliFlag{{Name: "site", Value: "ID"}, {Name: "before-revision", Value: "N"}}},
			{Name: "previews", Short: "Create, list, or revoke a private immutable release preview", Flags: []cliFlag{{Name: "site", Value: "ID"}, {Name: "release", Value: "ID"}, {Name: "preview", Value: "ID"}, {Name: "ttl", Value: "DURATION"}, {Name: "expected-generation", Value: "N"}}},
			{Name: "diff", Short: "Compare a verified release with the active site revision", Flags: []cliFlag{{Name: "site", Value: "ID"}, {Name: "release", Value: "ID"}}},
			{Name: "promote", Short: "Publish using the reviewed site revision", Flags: []cliFlag{{Name: "site", Value: "ID"}, {Name: "release", Value: "ID"}, {Name: "expected-revision", Value: "N"}, {Name: "wait", Bool: true}, {Name: "wait-timeout", Value: "DURATION"}}},
			{Name: "releases", Short: "List releases", Flags: []cliFlag{{Name: "site", Value: "ID"}}},
			{Name: "rollback", Short: "Promote a retained ready release", Flags: []cliFlag{{Name: "site", Value: "ID"}, {Name: "release", Value: "ID"}}},
			{Name: "suspend", Short: "Suspend traffic for a durable site", Flags: []cliFlag{{Name: "site", Value: "ID"}}},
			{Name: "resume", Short: "Resume traffic for a durable site", Flags: []cliFlag{{Name: "site", Value: "ID"}}},
			{Name: "retire", Short: "Retire an inactive release", Flags: []cliFlag{{Name: "site", Value: "ID"}, {Name: "release", Value: "ID"}}},
			{Name: "delete", Short: "Disable a site and retire its artifacts", Flags: []cliFlag{{Name: "site", Value: "ID"}}},
		},
	},
	mcpCLICommand(),
	{
		Name: "menu", DocSlug: "cli", Short: "Choose a common task and open its guided command",
		Examples: []string{"gregale menu"},
	},
	{
		Name: "start", DocSlug: "deploy", Short: "Get your first app live with a few guided prompts",
		Positionals: []string{"[path]"},
		Examples:    []string{"gregale start", "gregale start ./my-api"},
	},
	{
		Name:    "account",
		DocSlug: "account",
		Short:   "Manage the local account: export, deletion, status, DPA and SLO",
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
			{Name: "billing-connection", Short: "Connect, show, or disconnect a Stripe, Paddle or Lago key for statement invoice exports; connect reads the key from stdin", Positionals: []string{"{connect|status|disconnect}", "{stripe|paddle|lago}"}, Flags: []cliFlag{
				{Name: "api-url", Short: "Lago API URL for the EU region or a self-hosted Lago (connect; default https://api.getlago.com)", Value: "URL"},
				{Name: "collection", Short: "what the provider does with exported invoices: none leaves drafts, send emails them (Stripe), charge charges the saved payment method (Stripe, Lago) (connect; default none)", Value: "MODE", ClosedSet: []string{"none", "send", "charge"}},
				{Name: "days-until-due", Short: "days until a sent invoice is due (connect --collection send; default 30)", Value: "N"},
				{Name: "automatic-tax", Short: "have Stripe Tax calculate tax on exported invoices; needs Stripe Tax and customer addresses (connect; Stripe only)"},
			}, Examples: []string{"gregale account billing-connection connect paddle < paddle-key.txt", "gregale account billing-connection connect lago --api-url https://api.eu.getlago.com < lago-key.txt", "gregale account billing-connection connect stripe --collection charge < stripe-key.txt"}},
			{Name: "billing-run", Short: "Bill an ended month for every API customer: draft, check, finalize and optionally export each statement", Subcommands: []cliSub{
				{Name: "start", Short: "Start a run for one ended UTC month", Flags: []cliFlag{
					{Name: "month", Short: "UTC calendar month to bill (default: last month)", Value: "YYYY-MM"},
					{Name: "sources", Short: "comma-separated app_consumers,platform_tenants (default: both)", Value: "LIST"},
					{Name: "no-finalize", Short: "only draft statements", Bool: true},
					{Name: "export", Short: "none, stripe, paddle or lago (default none)", Value: "TARGET"},
					{Name: "no-hold-on-gaps", Short: "finalize consumers even when request telemetry shows unbilled requests", Bool: true},
					{Name: "wait", Short: "wait until the run completes and list its customers", Bool: true},
				}, Examples: []string{"gregale account billing-run start --month 2026-09 --export stripe --wait"}},
				{Name: "list", Short: "List billing runs, newest first", Flags: []cliFlag{{Name: "limit", Short: "runs to list (1-100)", Value: "N"}}},
				{Name: "show", Short: "Show a run and what it did for each customer", Positionals: []string{"RUN_ID"}, Flags: []cliFlag{
					{Name: "outcome", Short: "only customers with this outcome, e.g. held or failed", Value: "OUTCOME"},
				}},
				{Name: "schedule", Short: "Show the monthly billing run schedule"},
				{Name: "schedule-set", Short: "Start a run automatically after each month ends", Flags: []cliFlag{
					{Name: "sources", Short: "comma-separated app_consumers,platform_tenants for scheduled runs (default: both)", Value: "LIST"},
					{Name: "no-finalize", Short: "scheduled runs only draft statements", Bool: true},
					{Name: "export", Short: "none, stripe, paddle or lago for scheduled runs (default none)", Value: "TARGET"},
					{Name: "no-hold-on-gaps", Short: "scheduled runs finalize consumers with completeness gaps", Bool: true},
					{Name: "delay-hours", Short: "hours after month end before the run starts (1-240, default 24)", Value: "N"},
				}, Examples: []string{"gregale account billing-run schedule-set --export stripe --delay-hours 6"}},
				{Name: "schedule-delete", Short: "Stop scheduled runs; a started run continues"},
			}},
			{Name: "invoices", Short: "List invoices exported from usage statements and whether they were paid", Flags: []cliFlag{
				{Name: "status", Short: "unpaid, overdue, or one payment status: draft, open, paid, void, uncollectible, deleted", Value: "STATUS"},
			}, Examples: []string{"gregale account invoices --status overdue"}},
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
			{Name: "bucket", Short: "Provision or attach object storage and inject sealed S3 settings", Positionals: []string{"<name>"}, Flags: []cliFlag{
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
			{Name: "datadog", Short: "Send the app's logs and deployment events to Datadog (ADR-752)", Examples: []string{
				"DD_API_KEY=... gregale add datadog --app my-api --site eu1",
				"gregale add datadog --app my-api --dry-run",
				"gregale add datadog --app my-api --remove",
			}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "APP"},
				{Name: "site", Short: "Datadog site (default us1)", Value: "SITE", ClosedSet: []string{"ap1", "eu1", "us1", "us3", "us5"}},
				{Name: "api-key-env", Short: "environment variable holding the API key (default DD_API_KEY)", Value: "NAME"},
				{Name: "api-key-stdin", Short: "read the API key from stdin"},
				{Name: "dry-run", Short: "show the plan without changing anything"},
				{Name: "remove", Short: "remove the app's Datadog log drain and webhook"},
			}},
		},
	},
	{
		Name: "bucket", DocSlug: "object-storage", Short: "Manage object encryption, Object Lock, copy sources, tags, versioning, lifecycle rules, receipts and capacity",
		Subcommands: []cliSub{
			{Name: "list", Short: "List an app's object-storage buckets and their IDs", Positionals: []string{"<app>"}},
			{Name: "delete", Short: "Delete an empty bucket (including retained versions)", Positionals: []string{"<app>", "<bucket-id>"}, Flags: []cliFlag{{Name: "yes", Short: "skip the typed confirmation (alias: --quiet)"}}},
			{Name: "uploads", Short: "Inspect owned multipart upload sessions and parts", Subcommands: []cliSub{
				{Name: "list", Short: "List multipart sessions", Positionals: []string{"<app>", "<bucket-id>"}, Flags: []cliFlag{{Name: "limit", Value: "N", Short: "page size (1..1000)"}, {Name: "cursor", Value: "ID", Short: "next page cursor"}}},
				{Name: "status", Short: "Inspect a multipart session returned by an upload", Positionals: []string{"<app>", "<bucket-id>", "<upload-id>"}},
				{Name: "parts", Short: "List uploaded multipart parts", Positionals: []string{"<app>", "<bucket-id>", "<upload-id>"}, Flags: []cliFlag{{Name: "limit", Value: "N", Short: "page size (1..1000)"}, {Name: "part-number-marker", Value: "N", Short: "last part from the previous page"}}},
			}},
			{Name: "upload", Short: "Upload a file with resumable multipart transfers", Positionals: []string{"<app>", "<bucket-id>", "<key>", "<file>"}, Flags: []cliFlag{{Name: "content-type", Value: "TYPE", Short: "object MIME type"}, {Name: "if-match", Value: "ETAG", Short: "replace a matching object (single PUT only)"}, {Name: "if-none-match", Value: "*", Short: "create only if absent (single PUT only)"}, {Name: "resume", Value: "UPLOAD-ID", Short: "resume a multipart upload from its local checkpoint"}, {Name: "timeout", Value: "DURATION", Short: "transfer deadline (default 30m)"}}},
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
			{Name: "deployment", Value: "ID|vN", Short: "inspect exact deployment service bindings, including frozen environment routes"},
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
					{Name: "object-storage-require", Value: "PREFIX=read|write|read_write", Short: "require storage operations (repeatable; replaces all storage requirements)"},
					{Name: "clear-object-storage-requirements", Short: "explicitly remove all storage operation requirements"},
					{Name: "queue-require", Value: "NAME=consumer_readiness", Short: "require push queue readiness (repeatable; connectivity remains partial)"},
					{Name: "clear-queue-requirements", Short: "explicitly remove all queue readiness requirements"},
					{Name: "expected-revision", Value: "N", Req: true, Short: "current policy revision; use 0 initially"},
					{Name: "reason", Value: "TEXT", Short: "update reason; required when disabling enforcement"},
				}, Examples: []string{"gregale bindings release-policy set public-api --scope production --require-verification --max-age 10m --expected-revision 0"}},
			}},
			{Name: "service", Short: "Configure standalone service bindings", Subcommands: []cliSub{
				{Name: "explain", Short: "Explain service authorization, optionally for an exact deployment; exits 0 allowed, 1 denied, 2 indeterminate/error", Positionals: []string{"<caller>", "<target>"}, Flags: []cliFlag{{Name: "deployment", Value: "ID", Short: "exact caller deployment UUID; use server routing and effective policy"}, {Name: "scope", Value: "ENV", Short: "assert deployment environment; requires --deployment"}, {Name: "method", Value: "METHOD", Short: "HTTP method (default GET)"}, {Name: "path", Value: "PATH", Short: "origin-form request path (default /)"}}},
				{Name: "export", Short: "Export live service settings as a reusable policy", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "out", Value: "PATH", Short: "write to a new private file (default stdout)"}}},
				{Name: "check", Short: "Compare live settings with a policy; exits 0 match, 1 drift, 2 error", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "file", Value: "PATH", Req: true, Short: "JSON service binding settings policy"}}},
				{Name: "configure", Short: "Patch service targets, permissions, reliability and URL transport", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "plan-out", Value: "PATH", Short: "save a reviewed plan without changes; destination must be new"}, {Name: "file", Value: "PATH", Req: true, Short: "JSON service binding settings patch"}, {Name: "dry-run", Bool: true, Short: "show current and proposed binding settings without changes"}}, Examples: []string{"gregale bindings service configure public-api --file policy.json --dry-run"}},
				{Name: "apply", Short: "Apply a saved service binding plan if reviewed settings are unchanged", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "plan", Value: "PATH", Req: true, Short: "saved service binding plan"}}},
			}},
			{Name: "probe-policy", Short: "Configure or remove an outbound integration probe", Positionals: []string{"<integration-id>"}, Flags: []cliFlag{{Name: "path", Value: "PATH", Short: "provider path declared safe to probe"}, {Name: "method", Value: "METHOD", Short: "GET or HEAD (default GET)"}, {Name: "expect-status", Value: "STATUS", Short: "expected successful response status (default 200)"}, {Name: "delete", Short: "remove probe configuration"}}, Examples: []string{"gregale bindings probe-policy INTEGRATION_ID --path /health --method GET --expect-status 200"}},
			{
				Name:        "check",
				Short:       "Evaluate recorded binding evidence and runtime freshness for CI",
				Positionals: []string{"[<app>]"},
				Flags: []cliFlag{
					{Name: "interactive", Bool: true, Short: "choose a live deployment and readiness options"},
					{Name: "app", Value: "SLUG", Short: "app slug (linked app or picker in interactive mode)"},
					{Name: "scope", Value: "SCOPE", Short: "require the selected deployment to use this scope (default its current scope)"},
					{Name: "max-verification-age", Value: "DURATION", Short: "maximum age of passed probe evidence (default 10m)"},
					{Name: "deployment", Value: "ID|vN", Short: "exact live deployment whose evidence must pass, including zero-traffic candidates"},
					{Name: "allow-unsupported", Short: "waive connectivity coverage for active queue and outbound bindings"},
					{Name: "require-application-ack", Short: "require current PostgreSQL/object-storage application acknowledgements"},
					{Name: "release-policy", Short: "apply stored scope requirements and confirm the policy after every inventory evaluation (read-only)"},
					{Name: "object-storage-require", Value: "PREFIX=read|write|read_write", Short: "require recorded storage operation evidence (repeatable; no writes)"},
					{Name: "queue-require", Value: "NAME=consumer_readiness", Short: "require recorded push queue readiness (repeatable; connectivity remains partial)"},
					{Name: "wait", Short: "poll read-only inventory while probes, refreshes or application acknowledgements are pending"},
					{Name: "timeout", Value: "DURATION", Short: "maximum waiting or policy-aware preflight time (default 5m)"},
					{Name: "poll-interval", Value: "DURATION", Short: "inventory polling interval with --wait (default 1s)"},
				}, Examples: []string{"gregale bindings check --app my-api --interactive", "gregale bindings check my-api --max-verification-age 10m --json", "gregale bindings check my-api --scope production", "gregale bindings check my-api --deployment v12 --release-policy --wait --json", "gregale bindings check my-api --deployment v12 --max-verification-age 10m --json"},
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
			{Name: "verification-runs", Short: "Inspect, resume or cancel a durable binding verification run", Subcommands: []cliSub{
				{Name: "list", Short: "List retained receipt summaries without requalifying evidence", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "limit", Value: "N", Short: "page size (default 20; range 1–100)"}, {Name: "cursor", Value: "CURSOR", Short: "next page cursor; reuse filters"}, {Name: "deployment", Value: "UUID", Short: "exact deployment filter"}, {Name: "status", Value: "STATUS", Short: "stored state or observed expiry"}, {Name: "scope", Value: "SCOPE", Short: "deployment scope"}, {Name: "include-members", Short: "include graph members with root linkage"}}},
				{Name: "diagnostics", Short: "Explain blockers, public changes and recovery actions", Positionals: []string{"<app>", "<run-id>"}},
				{Name: "status", Short: "Read the original plan and current resource progress", Positionals: []string{"<app>", "<run-id>"}},
				{Name: "resume", Short: "Continue the original review without renewing deadlines", Positionals: []string{"<app>", "<run-id>"}, Flags: []cliFlag{{Name: "progress", Value: "MODE", Short: "durable stderr progress: auto, plain, ndjson or off"}, {Name: "status-read-retries", Value: "N", Short: "transient durable status GET retries (default 3; range 0–8)"}, {Name: "concurrency", Value: "N", Short: "concurrent probe limit (default 2; range 1–8)"}, {Name: "poll-interval", Value: "DURATION", Short: "probe polling interval"}, {Name: "wait-timeout", Value: "DURATION", Short: "wait budget bounded by the original deadline"}}},
				{Name: "cancel", Short: "Block new admission and cancel unfinished owned work", Positionals: []string{"<app>", "<run-id>"}},
			}},

			{
				Name:        "verify",
				Short:       "Check a private service route or test a managed PostgreSQL or object-storage binding",
				Positionals: []string{"<app>", "[<service>]"},
				Flags: []cliFlag{
					{Name: "all", Short: "verify services, PostgreSQL, storage, outbound and eligible queues (publication opt-in)"},
					{Name: "require-complete", Short: "with --all, fail unless every eligible binding is checked and passes"},
					{Name: "release-policy", Short: "reuse current evidence, verify required bindings and wait for policy-aware preflight"},
					{Name: "durable", Short: "persist a single-app release-policy verification receipt"},
					{Name: "run-id", Value: "UUID", Short: "caller-selected durable run identity for recovery"},
					{Name: "progress", Value: "MODE", Short: "durable stderr progress: auto, plain, ndjson or off"}, {Name: "status-read-retries", Value: "N", Short: "transient durable status GET retries (default 3; range 0–8)"}, {Name: "concurrency", Value: "N", Short: "release-policy probe limit (default 2; range 1–8)"},
					{Name: "dry-run", Short: "preview --all or --release-policy probes and required consent without creating tasks or messages"},
					{Name: "postgres", Short: "verify one managed PostgreSQL binding by environment key", Value: "ENVIRONMENT_KEY"},
					{Name: "outbound", Short: "verify a configured outbound integration by UUID", Value: "INTEGRATION_ID"},
					{Name: "object-storage", Short: "verify one object-storage binding by environment prefix (read access only)", Value: "PREFIX"},
					{Name: "queue", Value: "BINDING", Short: "verify a safe push queue message on the exact deployment"},
					{Name: "queue-publish", Short: "consent to safe queue messages; --all also requires --deployment"},
					{Name: "object-storage-write", Short: "opt into storage upload and cleanup (plus download for read/write); --all requires --deployment"},
					{Name: "deployment", Value: "ID|vN", Short: "exact live deployment to verify, including zero-traffic candidates"},
					{Name: "poll-interval", Short: "status polling interval while the canary runs", Value: "D"},
					{Name: "wait-timeout", Short: "maximum time to wait for canaries; bounds the entire --release-policy workflow", Value: "D"},
				}, Examples: []string{"gregale bindings verify my-api billing", "gregale bindings verify my-api --all", "gregale bindings verify my-api --deployment v12 --all", "gregale bindings verify my-api --all --deployment v12 --dry-run --json", "gregale bindings verify my-api --postgres DATABASE_URL", "gregale bindings verify my-api --object-storage GREGALE_S3_ASSETS", "gregale bindings verify my-api --deployment v12 --release-policy --dry-run --json", "gregale bindings verify my-api --deployment v12 --release-policy --object-storage-write --json"},
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
		Name:    "ai",
		DocSlug: "ai",
		Short:   "AI gateway: providers, hard token and cost caps, per-app and per-end-user limits, guardrails, prices and the built-in catalog, usage, routes, key pools, alerts and request logs",
		Examples: []string{
			"gregale ai policy <integration-id> --provider anthropic --daily-tokens 2000000 --daily-cost 25 --request-log on",
			"gregale ai guardrails <integration-id> --app my-api --rpm 60 --models 'gpt-4o-mini,o3*' --max-output-tokens 2048",
			"gregale ai prices set <integration-id> --price model=gpt-4o-mini,input=0.15,cached=0.075,output=0.60",
			"gregale ai usage <integration-id> --date 2026-10-11",
			"gregale ai policy <integration-id> --user-daily-tokens 200000 --user-daily-cost 2",
			"gregale ai users <integration-id> --limit 20",
			"gregale ai users export <integration-id> --from 2026-10-01 > ai-users.csv",
			"gregale ai catalog openai --model gpt-4o-mini",
			"gregale ai routes create --name chat --format openai --target <openai-id> --target <anthropic-id>:claude-sonnet-4",
			"gregale ai routes canary <route-id> --position 2 --percent 10 --max-latency-increase 50",
			"gregale ai routes set <route-id> --target <openai-id> --target <anthropic-id>:claude-sonnet-4-5 --strategy latency",
			"gregale ai content-policy <integration-id> --moderation block --moderation-kind classifier --moderation-integration <id> --moderation-model gpt-5-nano --moderation-categories prompt_injection,harassment",
			"printf '%s\\n' \"$SECOND_KEY\" | gregale ai keys add <integration-id> --label second --from-stdin",
			"gregale ai logs <integration-id> --limit 20",
			"gregale ai tags <integration-id> --key feature",
			"gregale ai tags export <integration-id> --from 2026-10-01 --to 2026-10-31 --key customer > ai-tags.csv",
			"gregale ai tag-limits <integration-id> --set feature=search:20",
			"gregale ai logs show <integration-id> <request-id>",
			"gregale ai content-policy <integration-id> --email redact --secrets block --block-term 'Project Falcon'",
			"gregale ai content-policy <integration-id> --response-email redact --response-secrets block --response-terms block --response-moderation flag",
			"gregale ai replay <integration-id> <request-id> --target <anthropic-id>:claude-sonnet-4-5 --target <openai-id>:gpt-4o-mini",
			"gregale ai evaluate <route-id> --position 2 --judge <openai-id> --judge-model gpt-5 --canary-percent 10",
			"gregale ai tokens create --app my-api --name laptop --expires-in-hours 24",
		},
		Subcommands: []cliSub{
			{Name: "policy", Short: "Set the AI provider and token or cost limits (unchanged flags keep their values)", Positionals: []string{"<integration-id>"}, Flags: []cliFlag{
				{Name: "provider", Short: "AI provider", Value: "PROVIDER", ClosedSet: []string{"openai", "anthropic", "gemini", "azure_openai", "bedrock", "vertex", "off"}},
				{Name: "daily-tokens", Short: "UTC-day input+output token limit, or none", Value: "N"},
				{Name: "daily-cost", Short: "UTC-day cost limit in EUR, or none", Value: "EUR"},
				{Name: "monthly-cost", Short: "calendar-month cost limit in EUR, or none", Value: "EUR"},
				{Name: "request-log", Short: "log per-call metadata for 7 days", Value: "on|off", ClosedSet: []string{"on", "off"}},
				{Name: "request-log-content", Short: "also keep the first 32 KiB of each logged call's bodies for 3 days", Value: "on|off", ClosedSet: []string{"on", "off"}},
				{Name: "user-daily-tokens", Short: "each end user's UTC-day token limit (X-Gregale-AI-User), or none", Value: "N"},
				{Name: "user-daily-cost", Short: "each end user's UTC-day cost limit in EUR, or none", Value: "EUR"},
				{Name: "log-export", Short: "send each app call's metadata to the app's log drains", Value: "on|off", ClosedSet: []string{"on", "off"}},
				{Name: "allow-unmetered", Short: "let through calls the gateway cannot meter (realtime client secrets, vector stores, fine-tuning), outside the caps", Value: "on|off", ClosedSet: []string{"on", "off"}},
				{Name: "prompt-cache", Short: "add prompt-cache breakpoints to Claude requests that carry none: after a repeated system prompt or tools, and after a continuing conversation", Value: "on|off", ClosedSet: []string{"on", "off"}},
			}},
			{Name: "content-policy", Short: "Show or set content guardrails: redact or block e-mail, cards, IBANs and secrets, and block terms, in requests and responses (unchanged flags keep their values)", Positionals: []string{"<integration-id>"}, Flags: []cliFlag{
				{Name: "email", Short: "e-mail addresses", Value: "ACTION", ClosedSet: []string{"off", "redact", "block"}},
				{Name: "payment-card", Short: "payment card numbers", Value: "ACTION", ClosedSet: []string{"off", "redact", "block"}},
				{Name: "iban", Short: "IBANs", Value: "ACTION", ClosedSet: []string{"off", "redact", "block"}},
				{Name: "secrets", Short: "API keys, tokens and private keys", Value: "ACTION", ClosedSet: []string{"off", "redact", "block"}},
				{Name: "block-term", Short: "block requests containing this term, ignoring case (repeat)", Value: "TERM"},
				{Name: "clear-terms", Short: "remove every blocked term first", Bool: true},
				{Name: "moderation", Short: "check requests with a model: off, flag or block", Value: "ACTION", ClosedSet: []string{"off", "flag", "block"}},
				{Name: "moderation-kind", Short: "openai_moderation or classifier", Value: "KIND", ClosedSet: []string{"openai_moderation", "classifier"}},
				{Name: "moderation-integration", Short: "the AI integration that runs the check", Value: "ID"},
				{Name: "moderation-model", Short: "the check's model (required for classifier)", Value: "MODEL"},
				{Name: "moderation-categories", Short: "comma-separated categories to check", Value: "LIST"},
				{Name: "moderation-on-error", Short: "when the check fails", Value: "allow|block", ClosedSet: []string{"allow", "block"}},
				{Name: "response-email", Short: "e-mail addresses in responses", Value: "ACTION", ClosedSet: []string{"off", "redact", "block"}},
				{Name: "response-payment-card", Short: "payment card numbers in responses", Value: "ACTION", ClosedSet: []string{"off", "redact", "block"}},
				{Name: "response-iban", Short: "IBANs in responses", Value: "ACTION", ClosedSet: []string{"off", "redact", "block"}},
				{Name: "response-secrets", Short: "API keys, tokens and private keys in responses", Value: "ACTION", ClosedSet: []string{"off", "redact", "block"}},
				{Name: "response-terms", Short: "block responses containing a blocked term", Value: "off|block", ClosedSet: []string{"off", "block"}},
				{Name: "response-moderation", Short: "check responses with the moderation check", Value: "ACTION", ClosedSet: []string{"off", "flag", "block"}},
			}},
			{Name: "tags", Short: "Show one UTC day's AI usage by X-Gregale-AI-Tags tag, with each tag's limit, or export a day range: tags export <integration-id>", Positionals: []string{"[export]", "<integration-id>"}, Flags: []cliFlag{
				{Name: "date", Short: "UTC day within the last 31 days (default today)", Value: "YYYY-MM-DD"},
				{Name: "key", Short: "show or export only this tag key's tags", Value: "KEY"},
				{Name: "limit", Short: "tags to show, 1 to 1000", Value: "N"},
				{Name: "from", Short: "export: first UTC day within the last 31 days (default today)", Value: "YYYY-MM-DD"},
				{Name: "to", Short: "export: last UTC day (default today)", Value: "YYYY-MM-DD"},
				{Name: "format", Short: "export: csv (default) or ndjson", Value: "FORMAT", ClosedSet: []string{"csv", "ndjson"}},
			}},
			{Name: "tag-limits", Short: "Show or change per-tag daily cost limits (unchanged limits are kept)", Positionals: []string{"<integration-id>"}, Flags: []cliFlag{
				{Name: "set", Short: "cap a tag's UTC-day cost: key=value:EUR (repeat)", Value: "SPEC"},
				{Name: "remove", Short: "remove a tag's limit (repeat)", Value: "TAG"},
				{Name: "clear", Short: "remove every limit before applying --set", Bool: true},
			}},
			{Name: "app-limit", Short: "Set or clear one app's own daily AI token limit", Positionals: []string{"<integration-id>"}, Flags: []cliFlag{
				{Name: "app", Short: "bound app slug", Req: true, Value: "slug"},
				{Name: "daily-tokens", Short: "token limit, or none", Req: true, Value: "N"},
			}},
			{Name: "guardrails", Short: "Set one app's per-minute limits, model allow-list and output-token ceiling (unchanged flags keep their values)", Positionals: []string{"<integration-id>"}, Flags: []cliFlag{
				{Name: "app", Short: "bound app slug", Req: true, Value: "slug"},
				{Name: "rpm", Short: "admitted calls per UTC minute, or none", Value: "N"},
				{Name: "tpm", Short: "tokens per UTC minute, or none", Value: "N"},
				{Name: "models", Short: "comma-separated models or prefixes ending in *, or none", Value: "LIST"},
				{Name: "max-output-tokens", Short: "per-request output-token ceiling, or none", Value: "N"},
			}},
			{Name: "prices", Short: "Show the price list, or replace it with: prices set <integration-id> --price ...", Positionals: []string{"[set]", "<integration-id>"}, Flags: []cliFlag{
				{Name: "price", Short: "model=PREFIX,input=EUR,output=EUR[,cached=EUR][,cache-write=EUR][,input-image=EUR][,output-image=EUR] per million tokens, [,image=EUR] per image, [,audio-minute=EUR] per minute, [,characters=EUR] per million characters, [,video-second=EUR] per second of video, with model=<model>@<resolution> for one resolution, [,web-search=EUR] [,file-search=EUR] per use, [,code-session-gb=EUR] per GB container session (repeat; none clears)", Value: "SPEC"},
			}},
			{Name: "catalog", Short: "Show the built-in prices that apply to models missing from a price list", Positionals: []string{"<provider>"}, Flags: []cliFlag{
				{Name: "model", Short: "show only the entry that prices this model", Value: "NAME"},
			}},
			{Name: "usage", Short: "Show one UTC day of token usage and cost by caller and model", Positionals: []string{"<integration-id>"}, Flags: []cliFlag{
				{Name: "date", Short: "UTC day within the last 90 days", Value: "YYYY-MM-DD"},
			}},
			{Name: "users", Short: "Show one UTC day of usage by end user (X-Gregale-AI-User), most expensive first, or export a day range", Positionals: []string{"[export]", "<integration-id>"}, Flags: []cliFlag{
				{Name: "date", Short: "UTC day within the last 31 days", Value: "YYYY-MM-DD"},
				{Name: "user", Short: "show only this end user", Value: "ID"},
				{Name: "limit", Short: "end users to show, 1 to 1000", Value: "N"},
				{Name: "from", Short: "export: first UTC day within the last 31 days (default today)", Value: "YYYY-MM-DD"},
				{Name: "to", Short: "export: last UTC day (default today)", Value: "YYYY-MM-DD"},
				{Name: "format", Short: "export format", Value: "csv|ndjson", ClosedSet: []string{"csv", "ndjson"}},
			}},
			{Name: "routes", Short: "List, create, update (set) or delete (rm) AI routes, run a model canary on one, or show its targets' recent stats", Positionals: []string{"<list|create|set|rm|canary|stats>", "[<route-id>]", "[status|promote|stop]"}, Flags: []cliFlag{
				{Name: "name", Short: "route name for create", Value: "NAME"},
				{Name: "format", Short: "route format for create: native or openai (translates OpenAI chat completions)", Value: "FORMAT", ClosedSet: []string{"native", "openai"}},
				{Name: "target", Short: "<integration-id>[:<model>] in fallback order (repeat)", Value: "TARGET"},
				{Name: "strategy", Short: "first-target choice for create or set: ordered, weighted, latency or cost", Value: "STRATEGY", ClosedSet: []string{"ordered", "weighted", "latency", "cost"}},
				{Name: "weights", Short: "a weighted route's target weights, 0 to 100, in target order", Value: "70,30"},
				{Name: "max-ttfb-ms", Short: "a cost route's latency target in milliseconds", Value: "N"},
				{Name: "position", Short: "canary: the route target (2 to 5) tried first", Value: "N"},
				{Name: "percent", Short: "canary: share of calls sent to it first, 1 to 50", Value: "N"},
				{Name: "max-error-increase", Short: "canary: roll back past this many error-rate points worse (default 5)", Value: "N"},
				{Name: "max-latency-increase", Short: "canary: roll back past this many percent slower time to first byte", Value: "N"},
				{Name: "min-calls", Short: "canary: calls needed before judging (default 50)", Value: "N"},
				{Name: "window", Short: "canary: minutes of calls to judge (default 30)", Value: "N"},
			}},
			{Name: "keys", Short: "List, add (value from stdin) or remove (rm) extra provider keys", Positionals: []string{"<list|add|rm>", "<integration-id>", "[<key-id>]"}, Flags: []cliFlag{
				{Name: "label", Short: "key label for add", Value: "LABEL"},
				{Name: "from-stdin", Short: "read the key value from stdin for add", Bool: true},
			}},
			{Name: "alerts", Short: "List budget alerts recorded at 80% and 100%", Positionals: []string{"<integration-id>"}, Flags: []cliFlag{
				{Name: "limit", Short: "alerts to show, 1 to 100", Value: "N"},
			}},
			{Name: "replay", Short: "Resend a captured call to other integrations or models and compare status, tokens, cost, latency and output, or show a replay", Positionals: []string{"[show]", "<integration-id|replay-id>", "[<request-id>]"}, Flags: []cliFlag{
				{Name: "target", Short: "<integration-id>[:<model>] to replay on (repeat, at most 5)", Value: "TARGET"},
				{Name: "no-wait", Short: "print the replay without waiting for results", Bool: true},
				{Name: "timeout", Short: "how long to wait for results (default 3m)", Value: "duration"},
			}},
			{Name: "evaluate", Short: "Judge a route target against the route's first target on recent captured calls, optionally starting a canary when it passes, or list and show evaluations", Positionals: []string{"[list|show]", "<route-id|evaluation-id>"}, Flags: []cliFlag{
				{Name: "position", Short: "the route target (2 to 5) to evaluate", Value: "N"},
				{Name: "judge", Short: "the AI integration whose model judges the answers", Value: "INTEGRATION"},
				{Name: "judge-model", Short: "the judge model", Value: "MODEL"},
				{Name: "sample", Short: "captured calls to replay, 1 to 50 (default 20)", Value: "N"},
				{Name: "max-loss-rate", Short: "pass when losses and errors are at most this percent of the sample (default 20)", Value: "N"},
				{Name: "canary-percent", Short: "start a canary with this share of calls when the evaluation passes", Value: "N"},
				{Name: "no-wait", Short: "print the evaluation without waiting for its verdict", Bool: true},
				{Name: "timeout", Short: "how long to wait for the verdict (default 30m)", Value: "duration"},
			}},
			{Name: "tokens", Short: "List, create or revoke access tokens that let local development, CI or notebooks call the AI gateway as an app", Positionals: []string{"<list|create|revoke>", "[<token-id>]"}, Flags: []cliFlag{
				{Name: "app", Short: "the app the token acts as", Value: "SLUG"},
				{Name: "name", Short: "token name for create", Value: "NAME"},
				{Name: "expires-in-hours", Short: "token lifetime for create, 1 to 2160 (default 168)", Value: "N"},
				{Name: "integration", Short: "limit the token to this AI integration (repeat)", Value: "INTEGRATION"},
			}},
			{Name: "logs", Short: "Page the opt-in AI request log, newest first, or show one call's captured bodies", Positionals: []string{"[show]", "<integration-id>", "[<request-id>]"}, Flags: []cliFlag{
				{Name: "limit", Short: "calls to show, 1 to 200", Value: "N"},
				{Name: "before", Short: "show calls before this RFC 3339 time", Value: "TIME"},
			}},
		},
	},
	{
		Name:    "channels",
		DocSlug: "channels",
		Short:   "Alert notification channels: Slack, PagerDuty, email (channels list|add|test|rm)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List channels with their last delivery"},
			{Name: "add", Short: "Add a channel; Slack URLs and PagerDuty keys are read from stdin", Flags: []cliFlag{
				{Name: "name", Short: "channel name, unique per account", Req: true, Value: "NAME"},
				{Name: "kind", Short: "destination type", Req: true, Value: "KIND", ClosedSet: []string{"slack", "pagerduty", "email"}},
				{Name: "secret-stdin", Short: "read the Slack webhook URL or PagerDuty routing key from stdin"},
				{Name: "pagerduty-region", Short: "PagerDuty service region", Value: "REGION", ClosedSet: []string{"us", "eu"}},
				{Name: "email", Short: "your account email (kind email)", Value: "EMAIL"},
			}},
			{Name: "test", Short: "Send a marked test notification", Positionals: []string{"<channel-id>"}},
			{Name: "rm", Short: "Delete a channel", Positionals: []string{"<channel-id>"}},
		},
	},
	{
		Name:    "slos",
		DocSlug: "slos",
		Short:   "Customer-defined SLOs and error budgets (slos list|create|status|rm --app <slug>)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List the app's SLOs", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			}},
			{Name: "create", Short: "Define an availability or latency SLO", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "name", Short: "SLO name, unique per app", Req: true, Value: "NAME"},
				{Name: "objective", Short: "objective percentage, 90 to 99.99", Req: true, Value: "PCT"},
				{Name: "sli", Short: "service level indicator", Value: "SLI", ClosedSet: []string{"availability", "latency"}},
				{Name: "latency-threshold-ms", Short: "latency threshold (5|10|25|50|100|250|500|1000|2000|5000|10000)", Value: "MS"},
				{Name: "window-days", Short: "rolling window", Value: "DAYS", ClosedSet: []string{"7", "30"}},
			}},
			{Name: "status", Short: "Show an SLO's attainment, budget remaining and burn rate", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			}, Positionals: []string{"<slo-id>"}},
			{Name: "rm", Short: "Delete one SLO", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			}, Positionals: []string{"<slo-id>"}},
		},
	},
	{
		Name:    "synthetics",
		DocSlug: "synthetics",
		Short:   "Scheduled HTTP checks against an app (synthetics list|create|status|pause|resume|rm --app <slug>)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List the app's synthetic checks", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			}},
			{Name: "create", Short: "Schedule a GET or HEAD check on a path", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "name", Short: "check name, unique per app", Req: true, Value: "NAME"},
				{Name: "path", Short: "path on the app (default /)", Value: "PATH"},
				{Name: "method", Short: "HTTP method", Value: "METHOD", ClosedSet: []string{"GET", "HEAD"}},
				{Name: "expect-status", Short: "exact expected status (default any 2xx)", Value: "N"},
				{Name: "timeout-ms", Short: "timeout including any wake, 1000-30000", Value: "MS"},
				{Name: "every-minutes", Short: "interval", Value: "MIN", ClosedSet: []string{"5", "15", "60"}},
			}},
			{Name: "status", Short: "Show a check's uptime, latency and recent runs", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			}, Positionals: []string{"<check-id>"}},
			{Name: "pause", Short: "Stop running a check", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			}, Positionals: []string{"<check-id>"}},
			{Name: "resume", Short: "Resume a paused check", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			}, Positionals: []string{"<check-id>"}},
			{Name: "rm", Short: "Delete a check and its history", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			}, Positionals: []string{"<check-id>"}},
		},
	},
	{
		Name:    "alerts",
		DocSlug: "alerts",
		Short:   "Per-app alert rules (alerts list|add|info|update|rm|rotate-secret|preset|actions --app <slug>)",
		Subcommands: []cliSub{
			{Name: "actions", Short: "Read or wait for automatic rollback status, deployment evidence and service handoffs", Examples: []string{"gregale alerts actions --app my-api --interactive"}, Flags: []cliFlag{
				{Name: "interactive", Short: "choose an action, inspect details, and optionally follow it", Bool: true},
				{Name: "app", Short: "app slug (linked app or picker in interactive mode)", Value: "slug"},
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
				{Name: "metric", Short: "metric; MCP task gauges: mcp_tasks_oldest_age_seconds, mcp_tasks_metrics_stale (includes min=0 observer heartbeat)", Value: "METRIC"},
				{Name: "comparison", Short: "gt|gte|lt|lte|above_baseline|below_baseline", Value: "OP"},
				{Name: "threshold", Short: "threshold value", Value: "N"},
				{Name: "window-spec", Short: "5m|15m|1h|6h|24h|7d|15d", Value: "WINDOW"},
				{Name: "event-subscription-id", Short: "Subscription UUID for event consumer metrics", Value: "UUID"},
				{Name: "edge-rule-id", Short: "edge rule for edge_rule_hits / edge_rule_log_hits (empty = all rules)", Value: "ID"},
				{Name: "mcp-readiness-scope", Short: "readiness environment; omitted means all environments (immutable)", Value: "SCOPE"},
				{Name: "failure-source", Short: "any|cron|queue|delayed_task|async_invoke|inbound_webhook", Value: "SOURCE"},
				{Name: "channel", Short: "notification channel id (repeatable); with one, the webhook is optional", Value: "CHANNEL_ID", Repeatable: true},
				{Name: "custom-metric", Short: "Pushed custom metric to watch; required with --metric custom_metric", Value: "NAME"},
				{Name: "slo", Short: "SLO id for slo_budget_burn or slo_budget_remaining_pct", Value: "SLO_ID"},
				{Name: "synthetic-check", Short: "synthetic check id for synthetic_check_* metrics", Value: "CHECK_ID"},
				{Name: "webhook-url", Short: "http(s) webhook URL (https recommended: deliveries are signed, not encrypted); required unless --channel", Value: "URL"},
				{Name: flagNameAction, Short: "alert action", Value: "ACTION", ClosedSet: api.AllowedAlertRuleActions},
				{Name: "post-deploy-rollback-window", Short: "completed-release rollback window (0 off; up to 1h)", Value: "duration"},
				{Name: "webhook-secret-stdin", Short: "read the webhook signing secret from stdin (this or --webhook-secret is required)"},
				{Name: "webhook-secret", Short: "webhook signing secret (prefer --webhook-secret-stdin)", Value: "VALUE"},
			}, Examples: []string{`printf '%s\n' "$WEBHOOK_SECRET" | gregale alerts add --app my-api --name p95-latency --metric latency_p95_ms --comparison gt --threshold 800 --window-spec 15m --webhook-url https://hooks.example.com/gregale --webhook-secret-stdin`, `printf '%s\n' "$WEBHOOK_SECRET" | gregale alerts add --app mcp-worker --name mcp-oldest-task-age --metric mcp_tasks_oldest_age_seconds --comparison gt --threshold 300 --window-spec 5m --webhook-url https://hooks.example.com/gregale --webhook-secret-stdin`, `printf '%s\n' "$WEBHOOK_SECRET" | gregale alerts add --app mcp-worker --name mcp-task-metrics-stale --metric mcp_tasks_metrics_stale --comparison gt --threshold 0 --window-spec 5m --webhook-url https://hooks.example.com/gregale --webhook-secret-stdin`}},
			{Name: "info", Short: "Show one alert rule and its last delivery", Positionals: []string{"<alert-id>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
			}},
			{Name: "deliveries", Short: "List a rule's webhook deliveries, newest first", Positionals: []string{"[<alert-id>]"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug (linked app or picker in interactive mode)", Value: "slug"},
				{Name: "interactive", Short: "choose a rule and delivery options", Bool: true},
				{Name: "limit", Short: "max deliveries (1..100, default 20)", Value: "N"},
				{Name: "include-test", Short: "include test deliveries", Bool: true},
			}, Examples: []string{"gregale alerts deliveries --app my-api --interactive"}},
			{Name: "update", Short: "Update one alert rule", Positionals: []string{"[<alert-id>]"}, Flags: []cliFlag{
				{Name: "interactive", Short: "choose a rule, edit current settings, and confirm changes", Bool: true},
				{Name: "app", Short: "app slug (linked app or picker in interactive mode)", Value: "slug"},
				{Name: flagNameAction, Short: "alert action", Value: "ACTION", ClosedSet: api.AllowedAlertRuleActions},
				{Name: "post-deploy-rollback-window", Short: "completed-release rollback window (0 off; up to 1h)", Value: "duration"},
				{Name: "webhook-secret-stdin", Short: "read the replacement webhook secret from stdin"},
			}, Examples: []string{"gregale alerts update --app my-api --interactive"}},
			{Name: "rm", Short: "Delete one alert rule", Positionals: []string{"<alert-id>"}, Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "slug"}}},
			{Name: "rotate-secret", Short: "Rotate the alert's webhook secret", Positionals: []string{"<alert-id>"}, Flags: []cliFlag{
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
		Name: "data-repair", DocSlug: "data-repair", Short: "Review and repair recorded PostgreSQL field updates (internal)", Audience: cliAudienceOperator,
		Subcommands: []cliSub{
			{Name: "schema", Short: "Print explicit owner-installation SQL", Flags: []cliFlag{{Name: "upgrade", Short: "print the transactional upgrade for an existing installation"}}},
			{Name: "doctor", Short: "Inspect read-only repair configuration; exit 0 only when ready", Flags: []cliFlag{
				{Name: "profile", Value: "NAME", Short: "executor (default) or local application capture readiness"},
				{Name: "database-env", Value: "NAME", Short: "capture: explicit writer connection environment variable"},
				{Name: "scope-file", Value: "PATH", Short: "capture: trusted application scope JSON"},
				{Name: "release", Value: "VERSION", Short: "capture: configured application release"},
				{Name: "receipt-protocol", Value: "NAME", Short: "capture: managed (default) or customer receipt inbox"},
				{Name: "table", Value: "NAME", Req: true, Short: "exact qualified enrolled table name"},
				{Name: "app", Value: "SLUG", Short: "hosted application; uses normal login"},
				{Name: "environment", Value: "NAME", Short: "configured hosted environment"},
				{Name: "customer", Value: "UUID", Short: "configured hosted customer; empty selects account scope"},
				{Name: "url", Value: "URL", Short: "executor URL"},
				{Name: "token-env", Value: "NAME", Short: "executor token environment variable"},
			}},
			{Name: "sources", Short: "Find recorded operations by release, table and capture time", Flags: []cliFlag{
				{Name: "app", Value: "SLUG", Short: "hosted application; uses normal login"},
				{Name: "environment", Value: "NAME", Short: "configured hosted environment"},
				{Name: "customer", Value: "UUID", Short: "configured hosted customer; empty selects account scope"},
				{Name: "scopes", Short: "list configured hosted source scopes"},
				{Name: "release", Value: "VERSION", Short: "exact captured release"},
				{Name: "table", Value: "NAME", Short: "exact enrolled qualified table"},
				{Name: "since", Value: "RFC3339", Short: "inclusive capture time"},
				{Name: "before", Value: "RFC3339", Short: "exclusive capture time"},
				{Name: "limit", Value: "N", Short: "maximum sources per page (1..100; default 25)"},
				{Name: "cursor", Value: "CURSOR", Short: "next_cursor; keep filters and limit unchanged"},
				{Name: "url", Value: "URL", Short: "executor URL"},
				{Name: "token-env", Value: "NAME", Short: "executor token environment variable"},
			}},
			{Name: "history", Positionals: []string{"<source-operation-id>"}, Short: "Find retained repair plans, replacement links, closures and receipts", Flags: []cliFlag{
				{Name: "app", Value: "SLUG", Short: "hosted application; uses normal login"},
				{Name: "environment", Value: "NAME", Short: "configured hosted environment"},
				{Name: "customer", Value: "UUID", Short: "configured hosted customer; empty selects account scope"},
				{Name: "limit", Value: "N", Short: "maximum plans per page (1..100; default 25)"},
				{Name: "cursor", Value: "CURSOR", Short: "next_cursor; keep source, scope and limit unchanged"},
				{Name: "url", Value: "URL", Short: "executor URL"},
				{Name: "token-env", Value: "NAME", Short: "executor token environment variable"},
			}},
			{Name: "retention", Short: "Review and compact old repair payloads while preserving receipts", Subcommands: []cliSub{
				{Name: "schema", Short: "Print the explicit owner-installed retention upgrade"},
				{Name: "plan", Short: "Save a bounded inventory of eligible payloads", Flags: []cliFlag{
					{Name: "database-env", Value: "NAME", Short: "local maintenance connection environment variable"},
					{Name: "scope-file", Value: "PATH", Req: true, Short: "fixed application scope"},
					{Name: "before", Value: "RFC3339", Short: "cutoff at least seven days old"},
					{Name: "limit", Value: "N", Short: "maximum entries (default 32)"},
				}},
				{Name: "get", Positionals: []string{"<retention-plan-id>"}, Short: "Read the saved inventory and purge receipt", Flags: []cliFlag{
					{Name: "database-env", Value: "NAME", Short: "local maintenance connection environment variable"},
					{Name: "scope-file", Value: "PATH", Req: true, Short: "fixed application scope"},
				}},
				{Name: "purge", Positionals: []string{"<retention-plan-id>"}, Short: "Compact the exact reviewed payload inventory", Flags: []cliFlag{
					{Name: "database-env", Value: "NAME", Short: "local maintenance connection environment variable"},
					{Name: "scope-file", Value: "PATH", Req: true, Short: "fixed application scope"},
					{Name: "digest", Value: "SHA256", Req: true, Short: "exact reviewed retention plan digest"},
					{Name: "actor", Value: "NAME", Req: true, Short: "recorded purge operator"},
					{Name: "yes", Req: true, Bool: true, Short: "confirm the reviewed payload purge"},
				}},
			}},
			{Name: "enroll", Short: "Enroll a declared update-only table", Flags: []cliFlag{{Name: "database-env", Value: "NAME", Short: "database URL environment variable"}, {Name: "table", Value: "TABLE", Req: true, Short: "qualified table"}, {Name: "fields", Value: "COLUMNS", Req: true, Short: "comma-separated repairable columns"}}},
			{Name: "serve", Short: "Start an authenticated operator executor and review UI", Flags: []cliFlag{{Name: "database-env", Value: "NAME", Short: "database URL environment variable"}, {Name: "scope-file", Value: "PATH", Req: true, Short: "fixed authenticated executor scope"}, {Name: "actor", Value: "NAME", Req: true, Short: "recorded operator identity"}, {Name: "token-env", Value: "NAME", Short: "executor token environment variable"}, {Name: "listen", Value: "ADDRESS", Short: "default 127.0.0.1:8796"}, {Name: "pg-dump", Value: "PATH", Short: "pg_dump executable"}, {Name: "checks-file", Value: "PATH", Short: "reviewed SQL SELECT returning one boolean"}, {Name: "checks-policy-file", Value: "PATH", Short: "version 1 JSON policy containing named SQL checks"}, {Name: "managed-coordinator", Short: "trust the managed API approval actor"}, {Name: "sandbox-image", Value: "IMAGE", Short: "pre-pulled digest-pinned PostgreSQL image"}}},
			{Name: "plan", Positionals: []string{"<operation-id>"}, Short: "Prepare an immutable candidate/conflict plan", Flags: []cliFlag{{Name: "repair-operation", Value: "UUID", Short: "saved identity required for hosted preparation"}, {Name: "app", Value: "SLUG", Short: "use the hosted API and normal login"}, {Name: "url", Value: "URL", Short: "executor URL"}, {Name: "token-env", Value: "NAME", Short: "executor token environment variable"}}},
			{Name: "get", Positionals: []string{"<plan-id>"}, Short: "Inspect a plan and its durable apply receipt", Flags: []cliFlag{{Name: "source", Value: "UUID", Short: "original source for read-only hosted recovery after execution history expires"}, {Name: "environment", Value: "NAME", Short: "configured environment with --source"}, {Name: "customer", Value: "UUID", Short: "configured customer with --source; empty selects account scope"}, {Name: "app", Value: "SLUG", Short: "use the hosted API and normal login"}, {Name: "url", Value: "URL", Short: "executor URL"}, {Name: "token-env", Value: "NAME", Short: "executor token environment variable"}}},
			{Name: "validate", Positionals: []string{"<plan-id>"}, Short: "Read current drift without changing a saved plan or approval", Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "use the hosted API and normal login"}, {Name: "url", Value: "URL", Short: "executor URL"}, {Name: "token-env", Value: "NAME", Short: "executor token environment variable"}, {Name: "digest", Value: "SHA256", Req: true, Short: "exact plan digest"}}},
			{Name: "verify", Positionals: []string{"<plan-id>"}, Short: "Observe committed repair rows without changing receipt history", Flags: []cliFlag{{Name: "source", Value: "UUID", Short: "original source for read-only hosted recovery after execution history expires"}, {Name: "environment", Value: "NAME", Short: "configured environment with --source"}, {Name: "customer", Value: "UUID", Short: "configured customer with --source; empty selects account scope"}, {Name: "app", Value: "SLUG", Short: "use the hosted API and normal login"}, {Name: "url", Value: "URL", Short: "executor URL"}, {Name: "token-env", Value: "NAME", Short: "executor token environment variable"}, {Name: "digest", Value: "SHA256", Req: true, Short: "exact plan digest"}}},
			{Name: "replace", Positionals: []string{"<plan-id>"}, Short: "Prepare a linked replacement and disable further use of the old plan", Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "use the hosted API and normal login"}, {Name: "url", Value: "URL", Short: "executor URL"}, {Name: "token-env", Value: "NAME", Short: "executor token environment variable"}, {Name: "digest", Value: "SHA256", Req: true, Short: "exact parent plan digest"}, {Name: "repair-operation", Value: "UUID", Req: true, Short: "saved new replacement identity; reuse on response loss"}, {Name: "yes", Req: true, Bool: true, Short: "confirm replacement of this exact plan"}}},
			{Name: "resolve", Positionals: []string{"<plan-id>"}, Short: "Resolve apply outcome and permanently close an uncommitted plan", Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "use the hosted API and normal login"}, {Name: "url", Value: "URL", Short: "executor URL"}, {Name: "token-env", Value: "NAME", Short: "executor token environment variable"}, {Name: "digest", Value: "SHA256", Req: true, Short: "exact plan digest"}, {Name: "repair-operation", Value: "UUID", Short: "original apply identity; required for standalone resolution"}, {Name: "yes", Req: true, Bool: true, Short: "confirm permanent closure if not committed"}}},
			{Name: "rehearse", Positionals: []string{"<plan-id>"}, Short: "Rehearse exact candidates in an isolated database", Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "use the hosted API and normal login"}, {Name: "url", Value: "URL", Short: "executor URL"}, {Name: "token-env", Value: "NAME", Short: "executor token environment variable"}, {Name: "digest", Value: "SHA256", Req: true, Short: "exact plan digest"}}},
			{Name: "approve", Positionals: []string{"<plan-id>"}, Short: "Record approval for the exact rehearsed plan", Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "use the hosted API and normal login"}, {Name: "url", Value: "URL", Short: "executor URL"}, {Name: "token-env", Value: "NAME", Short: "executor token environment variable"}, {Name: "digest", Value: "SHA256", Req: true, Short: "exact plan digest"}, {Name: "yes", Req: true, Bool: true, Short: "confirm reviewed approval"}}},
			{Name: "apply", Positionals: []string{"<plan-id>"}, Short: "Atomically apply an approved plan with fresh row guards", Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "use the hosted API and normal login"}, {Name: "url", Value: "URL", Short: "executor URL"}, {Name: "token-env", Value: "NAME", Short: "executor token environment variable"}, {Name: "digest", Value: "SHA256", Req: true, Short: "exact plan digest"}, {Name: "repair-operation", Value: "UUID", Short: "stable ID required by standalone executor apply"}, {Name: "yes", Req: true, Bool: true, Short: "confirm correction submission"}}},
			{Name: "cancel-rehearsal", Positionals: []string{"<repair-id>"}, Short: "Cancel a scheduled rehearsal and await sandbox cleanup", Flags: []cliFlag{{Name: "app", Value: "SLUG", Req: true, Short: "hosted application"}, {Name: "digest", Value: "SHA256", Req: true, Short: "exact plan digest"}}},
			{Name: "reconcile", Positionals: []string{"<repair-id>"}, Short: "Read the application receipt and reconcile the hosted Operation", Flags: []cliFlag{{Name: "app", Value: "SLUG", Req: true, Short: "hosted application"}, {Name: "digest", Value: "SHA256", Req: true, Short: "exact plan digest"}}},
			{Name: "reject", Positionals: []string{"<repair-id>"}, Short: "Reject a hosted repair approval", Flags: []cliFlag{{Name: "app", Value: "SLUG", Req: true, Short: "hosted application"}, {Name: "digest", Value: "SHA256", Req: true, Short: "exact plan digest"}, {Name: "yes", Req: true, Bool: true, Short: "confirm rejection"}}},
		},
	},
	{
		Name: "commit", DocSlug: "commit", Short: "Manage transactional PostgreSQL outbox sources (internal)",
		Subcommands: []cliSub{
			{Name: "add", Short: "Register a fixed app destination", Flags: []cliFlag{{Name: "name", Short: "account source name", Req: true, Value: "NAME"}, {Name: "destination", Short: "immutable destination (default operation)", Value: "DESTINATION", ClosedSet: []string{"operation", "event"}}, {Name: "operation-policy", Short: "required for operation destinations; forbidden for event destinations", Value: "NAME"}, {Name: "contract-version", Short: "immutable source contract version (default 1)", Value: "VERSION", ClosedSet: []string{"1", "2", "3"}}, {Name: "allow-tenant-selection", Short: "grant version 2 account-owner customer selection"}}},
			{Name: "connection", Short: "Seal database credentials from a file", Flags: []cliFlag{{Name: "file", Short: "connection URL file", Req: true, Value: "PATH"}}},
			{Name: "pause", Short: "Pause new acceptance"},
			{Name: "resume", Short: "Resume new acceptance"},
			{Name: "info", Short: "Inspect source health and pending/blocked work"},
			{Name: "doctor", Short: "Read-only source diagnostics and optional local database checks", Flags: []cliFlag{{Name: "file", Short: "local TLS PostgreSQL credential file; never uploaded", Value: "PATH"}}},
			{Name: "recovery-alerts", Short: "Manage saved historical alerts without polling", Flags: []cliFlag{{Name: "checkpoint", Value: "PATH", Short: "saved recovery watch checkpoint"}, {Name: "replay", Short: "emit pending historical alerts as JSON lines"}, {Name: "ack-alerts", Value: "PATH", Short: "atomically acknowledge exact incident_id/sequence entries"}}},
			{Name: "recovery-status", Short: "Read current delivery evidence for saved recovery attempts", Positionals: []string{"<source>"}, Flags: []cliFlag{{Name: "plan", Value: "PATH", Short: "reviewed recovery plan JSON"}, {Name: "result", Value: "PATH", Short: "saved apply result JSON"}, {Name: "resume", Value: "PATH", Short: "continue saved status unread items and failed reads"}, {Name: "refresh", Value: "PATH", Short: "reread saved status selection and track changes"}, {Name: "watch", Short: "monitor acknowledged recovery executions"}, {Name: "watch-diagnostics", Short: "watch until complete, fresh diagnostic coverage; enables watch and diagnostics"}, {Name: "checkpoint", Value: "PATH", Short: "atomically save watch progress for restart"}, {Name: "read-retries", Value: "N", Short: "total transient watch read retries (0..20; default 0)"}, {Name: "diagnostics", Short: "include bounded live delivery blocker diagnostics"}, {Name: "ack-alerts", Value: "PATH", Short: "exact alert acknowledgements JSON (requires checkpoint and resume or refresh)"}, {Name: "alerts-json", Short: "watch blocker lifecycle JSON lines on stderr (requires stall-threshold)"}, {Name: "stall-threshold", Value: "DURATION", Short: "persistent blocker alerts (100ms-24h; requires watch, diagnostics and max-diagnostic-age)"}, {Name: "max-diagnostic-age", Value: "DURATION", Short: "diagnostic sample age limit (100ms-24h; requires diagnostics)"}, {Name: "max-observation-age", Value: "DURATION", Short: "watch evidence age limit (100ms-24h; default disabled)"}, {Name: "interval", Value: "DURATION", Short: "watch interval (100ms-1m; default 1s)"}, {Name: "limit", Value: "N", Short: "subscriber observations (1..50; default 50)"}, {Name: "recipient-pages", Value: "N", Short: "receipt pages per item (1..20; default 1)"}, {Name: "timeout", Value: "DURATION", Short: "overall timeout (default 2m)"}}},
			{Name: "recovery-apply", Short: "Apply freshly revalidated reviewed recovery actions", Positionals: []string{"<source>"}, Flags: []cliFlag{{Name: "plan", Value: "PATH", Short: "reviewed recovery plan JSON"}, {Name: "yes", Short: "confirm recovery submissions"}, {Name: "limit", Value: "N", Short: "eligible items examined (1..50; default 50)"}, {Name: "recipient-pages", Value: "N", Short: "receipt pages per item (1..20; default 1)"}, {Name: "timeout", Value: "DURATION", Short: "overall timeout (default 2m)"}}},
			{Name: "recovery-plan", Short: "Plan selective recovery from fresh receipt evidence", Positionals: []string{"<source>"}, Flags: []cliFlag{{Name: "report", Value: "PATH", Short: "saved delivery report; fixes selection and filter"}, {Name: "recipient-pages", Value: "N", Short: "recipient pages per publication (1..20; default 1)"}, {Name: "timeout", Value: "DURATION", Short: "overall read timeout (default 2m)"}}},
			{Name: "delivery-report", Short: "Sample recent publication subscriber outcomes", Positionals: []string{"<source>"}, Flags: []cliFlag{{Name: "refresh", Value: "PATH", Short: "reread saved selection with fresh observations"}, {Name: "resume", Value: "PATH", Short: "resume saved report; fixes selection and filter"}, {Name: "recipient-pages", Value: "N", Short: "recipient pages per publication (1..20; default 1)"}, {Name: "limit", Value: "N", Short: "publications per page (1..200; default 50)"}, {Name: "before", Value: "CURSOR", Short: "history next_before cursor"}, {Name: "subscription-id", Value: "UUID", Short: "filter original subscriber"}, {Name: "timeout", Value: "DURATION", Short: "overall read timeout (default 2m)"}}},
			{Name: "history", Short: "List retained publication acceptance receipts", Positionals: []string{"<source>"}, Flags: []cliFlag{{Name: "before", Short: "next_before continuation cursor", Value: "CURSOR"}, {Name: "limit", Short: "receipts per page (1..200; default 50)", Value: "N"}}},
			{Name: "inspect", Short: "Inspect event acceptance, retained execution and blocked observations"},
			{Name: "wait", Short: "Wait without cancelling durable work on timeout", Flags: []cliFlag{{Name: "subscription-id", Short: "original application subscriber; requires --until completed", Value: "UUID"}, {Name: "until", Short: "accepted or completed", Value: "STATE"}, {Name: "timeout", Short: "maximum wait (default 2m)", Value: "D"}, {Name: "interval", Short: "poll interval (default 1s)", Value: "D"}}},
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
			{Name: "retention", Short: "Inspect receipt expiry, backfill holds and account storage", Flags: []cliFlag{
				{Name: "source", Short: "exact event source", Value: "SOURCE"},
				{Name: "app", Short: "receipt application filter; storage remains account-wide", Value: "APP"},
				{Name: "window", Short: "expiry lookahead (1s..720h; default 24h)", Value: "DURATION"},
				{Name: "limit", Short: "maximum sampled receipts (1..100)", Value: "N"}}},
			{Name: "publish-app-verify", Short: "Compare an original publish file with retained content without publishing", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "expected-accepted-at", Value: "RFC3339", Short: "Pin the exact accepted_at from the saved receipt"}, {Name: "file", Value: "PATH", Short: "Original publish JSON with key, type and data"}}, Examples: []string{"gregale events publish-app-verify my-app --file event.json --json"}},
			{Name: "publish-app-status", Short: "Reconcile retained publication and consumer evidence without submitting an event", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "expected-accepted-at", Value: "RFC3339", Short: "Pin the exact accepted_at from the saved receipt"}, {Name: "key", Value: "KEY", Short: "Original exact application producer key"}, {Name: "after", Value: "CURSOR", Short: "Continue the recipient evidence page"}, {Name: "limit", Value: "N", Short: "Recipients per page (1..200; default 100)"}}, Examples: []string{"gregale events publish-app-status my-app --key order-123-created --json"}},
			{Name: "publish-app", Short: "Publish an event using a durable application-scoped producer key", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "JSON event with a stable key, type and data"}}, Examples: []string{"gregale events publish-app my-app --file event.json --json"}},
			{Name: "publish-batch", Short: "Publish a bounded JSONL batch with per-event results", Flags: []cliFlag{{Name: "file", Short: "JSONL file or - for stdin; stable ids required", Value: "FILE", Req: true}}},
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
			{Name: "subscription-lineage-case-backfill-pause", Short: "Pause a retained lineage diagnostic scan", Positionals: []string{"<app>", "<subscription-id>", "<request-id>"}, Flags: []cliFlag{{Name: "expected-created-at", Value: "TIMESTAMP", Short: "original RFC3339 creation timestamp from the reviewed scan"}}},
			{Name: "subscription-lineage-case-backfill-resume", Short: "Resume a retained lineage diagnostic scan", Positionals: []string{"<app>", "<subscription-id>", "<request-id>"}, Flags: []cliFlag{{Name: "expected-created-at", Value: "TIMESTAMP", Short: "original RFC3339 creation timestamp from the reviewed scan"}}},
			{Name: "subscription-lineage-case-backfill-cancel", Short: "Cancel a retained lineage diagnostic scan", Positionals: []string{"<app>", "<subscription-id>", "<request-id>"}, Flags: []cliFlag{{Name: "expected-created-at", Value: "TIMESTAMP", Short: "original RFC3339 creation timestamp from the reviewed scan"}}},
			{Name: "subscription-lineage-case-backfill", Short: "Inspect latest lineage diagnostic scan", Positionals: []string{"<app>", "<subscription-id>"}},
			{Name: "subscription-lineage-case-backfill-request", Short: "Backfill missing lineage diagnostics from retained evidence", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "request-id", Value: "UUID", Short: "stable scan identity"}, {Name: "expected-previous-request-id", Value: "UUID", Short: "reviewed completed, cancelled or expired scan identity"}}},
			{Name: "subscription-lineage-retry-preview", Short: "Preview retained unresolved lineage cases", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "limit", Value: "N", Short: "page size (default 50, maximum 200)"}, {Name: "after", Value: "ID", Short: "exclusive outbox cursor"}, {Name: "reason", Value: "CODE", Short: "exact case reason filter"}}},
			{Name: "subscription-lineage-retry-export", Short: "Save a guarded preview as a reviewable retry request", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "new scoped retry file (never overwrite)"}, {Name: "raw", Short: "legacy unscoped API body"}, {Name: "limit", Value: "N", Short: "selected cases (default 50, maximum 200)"}, {Name: "after", Value: "ID", Short: "exclusive outbox cursor"}, {Name: "reason", Value: "CODE", Short: "exact case reason filter"}}},
			{Name: "subscription-lineage-retry-control-plan-export", Short: "Save fixed guarded controls from one attention page", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "new private plan file (never overwrite)"}, {Name: "action", Value: "ACTION", Short: "pause, resume or cancel"}, {Name: "attention", Value: "WARNING", Short: "any, stalled or retention_risk (default any)"}, {Name: "status", Value: "STATE", Short: "pending or paused"}, {Name: "limit", Value: "N", Short: "fixed jobs (default 50, maximum 200)"}, {Name: "after", Value: "CURSOR", Short: "attention-list cursor"}, {Name: "timeout", Value: "DURATION", Short: "total duration (default 2m)"}}},
			{Name: "subscription-lineage-retry-control-plan-apply", Short: "Apply a reviewed fixed control plan with independent results", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "report", Value: "PATH", Short: "new private durable execution report"}, {Name: "resume-report", Value: "PATH", Short: "inspect prior report before retry; requires new --report"}, {Name: "file", Value: "PATH", Short: "reviewed plan; unchanged on apply"}, {Name: "timeout", Value: "DURATION", Short: "total duration (default 2m)"}}},
			{Name: "subscription-lineage-retry-control-plan-preflight", Short: "Check reviewed commands with read-only job and receipt inspection", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "reviewed plan; unchanged on preflight"}, {Name: "timeout", Value: "DURATION", Short: "total duration (default 2m)"}}},
			{Name: "subscription-lineage-retry-control-plan-reconcile", Short: "Reconcile execution evidence with read-only receipt inspection", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "unchanged reviewed plan"}, {Name: "source-report", Value: "PATH", Short: "previous matching execution report"}, {Name: "report", Value: "PATH", Short: "new private reconciled report; never overwrite"}, {Name: "timeout", Value: "DURATION", Short: "total duration (default 2m)"}}},
			{Name: "subscription-lineage-retry-jobs", Short: "List retained bulk lineage retry job summaries", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "attention", Value: "WARNING", Short: "any, stalled or retention_risk (active jobs only)"}, {Name: "status", Value: "STATE", Short: "pending, paused, completed or cancelled (default all)"}, {Name: "limit", Value: "N", Short: "page size (default 50, maximum 200)"}, {Name: "after", Value: "CURSOR", Short: "continuation cursor from the previous page"}}},
			{Name: "subscription-lineage-retry-preflight", Short: "Check a saved retry request without changing it", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "scoped retry file or legacy request body"}, {Name: "timeout", Value: "DURATION", Short: "maximum preflight duration (default 2m)"}}},
			{Name: "subscription-lineage-retry-job-create", Short: "Submit a reviewed fixed lineage retry selection", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "scoped retry file or legacy request body (maximum 200)"}}},
			{Name: "subscription-lineage-retry-job-controls-inspect", Short: "Inspect or compare saved control history offline", Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "saved control-history artifact (local read only)"}, {Name: "compare", Value: "BASELINE_PATH", Short: "earlier artifact with the same job scope and filters"}}},
			{Name: "subscription-lineage-retry-job-controls-export", Short: "Export a guarded private control-history snapshot", Positionals: []string{"<app>", "<subscription-id>", "<request-id>"}, Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "new private JSON artifact (never overwrite)"}, {Name: "expected-created-at", Value: "TIMESTAMP", Short: "required original job creation timestamp"}, {Name: "action", Value: "ACTION", Short: "exact action: pause, resume or cancel"}, {Name: "actor-id", Value: "UUID_OR_STORE", Short: "exact original actor identity"}, {Name: "credential-type", Value: "TYPE", Short: "exact original credential type"}, {Name: "timeout", Value: "DURATION", Short: "maximum export duration (default 2m)"}}},
			{Name: "subscription-lineage-retry-job-controls", Short: "List retained immutable bulk retry command receipts", Positionals: []string{"<app>", "<subscription-id>", "<request-id>"}, Flags: []cliFlag{{Name: "action", Value: "ACTION", Short: "exact action: pause, resume or cancel"}, {Name: "actor-id", Value: "UUID_OR_STORE", Short: "exact original actor identity"}, {Name: "credential-type", Value: "TYPE", Short: "exact original credential type"}, {Name: "expected-created-at", Value: "TIMESTAMP", Short: "required original job creation timestamp"}, {Name: "limit", Value: "N", Short: "page size (default 50, maximum 200)"}, {Name: "after", Value: "SEQUENCE", Short: "exclusive recorded command sequence"}}},
			{Name: "subscription-lineage-retry-job-pause", Short: "Pause unprocessed bulk lineage retries", Positionals: []string{"<app>", "<subscription-id>", "<request-id>"}, Flags: []cliFlag{{Name: "expected-created-at", Value: "TIMESTAMP", Short: "original RFC3339 creation timestamp from the reviewed job"}, {Name: "command-id", Value: "UUID", Short: "optional immutable receipt identity; reuse after timeouts"}}},
			{Name: "subscription-lineage-retry-job-resume", Short: "Resume a paused bulk lineage retry job", Positionals: []string{"<app>", "<subscription-id>", "<request-id>"}, Flags: []cliFlag{{Name: "expected-created-at", Value: "TIMESTAMP", Short: "original RFC3339 creation timestamp from the reviewed job"}, {Name: "command-id", Value: "UUID", Short: "optional immutable receipt identity; reuse after timeouts"}}},
			{Name: "subscription-lineage-retry-job-cancel", Short: "Cancel unprocessed lineage retries", Positionals: []string{"<app>", "<subscription-id>", "<request-id>"}, Flags: []cliFlag{{Name: "command-id", Value: "UUID", Short: "optional reusable UUID for an immutable control receipt"}, {Name: "expected-created-at", Value: "TIMESTAMP", Short: "original RFC3339 creation timestamp from the reviewed job"}}},
			{Name: "subscription-lineage-retry-job-export", Short: "Export current guards for remaining cases from a finished job", Positionals: []string{"<app>", "<subscription-id>", "<source-request-id>"}, Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "new scoped retry file (never overwrite)"}, {Name: "raw", Short: "legacy unscoped API body without provenance"}, {Name: "expected-created-at", Value: "TIMESTAMP", Short: "required reviewed source creation timestamp"}, {Name: "outcomes", Value: "STATES", Short: "original outcomes (default unresolved,conflict,changed,unavailable; cancelled also allowed)"}, {Name: "timeout", Value: "DURATION", Short: "maximum export duration (default 2m)"}}},
			{Name: "subscription-lineage-retry-job-watch", Short: "Watch a retained bulk retry job and summarize outcomes", Positionals: []string{"<app>", "<subscription-id>", "<request-id>"}, Flags: []cliFlag{{Name: "timeout", Value: "DURATION", Short: "maximum watch duration (default 10m)"}, {Name: "poll-interval", Value: "DURATION", Short: "delay between reads (default 2s, between 1s and 1h)"}, {Name: "expected-created-at", Value: "TIMESTAMP", Short: "optional reviewed original RFC3339 creation timestamp"}}},
			{Name: "subscription-lineage-retry-job", Short: "Inspect durable lineage retry progress", Positionals: []string{"<app>", "<subscription-id>", "<request-id>"}},
			{Name: "subscription-lineage-cases", Short: "List retained lineage repair cases", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "limit", Value: "N", Short: "page size (default 50, maximum 200)"}, {Name: "after", Value: "ID", Short: "exclusive outbox cursor"}}},
			{Name: "subscription-lineage-retry", Short: "Retry one reviewed lineage case", Positionals: []string{"<app>", "<subscription-id>", "<outbox-id>"}, Flags: []cliFlag{{Name: "request-id", Value: "UUID", Short: "stable request identity"}, {Name: "expected-revision", Value: "N", Short: "reviewed case revision"}, {Name: "expected-diagnostic-id", Value: "UUID", Short: "incarnation guard for a backfilled case"}}},
			{Name: "subscription-execution-reconciliation", Short: "Inspect durable execution coverage reconciliation", Positionals: []string{"<app>", "<subscription-id>"}},
			{Name: "subscription-reconcile-execution", Short: "Request a forward-only execution coverage boundary", Positionals: []string{"<app>", "<subscription-id>"}},
			{Name: "subscription-execution-health", Short: "Inspect executions, handler outcomes, and active retry-job health", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "window", Value: "5m|15m|1h|6h|24h", Short: "attempt and completion window (default 5m)"}}},
			{Name: "delivery-health-alerts", Short: "Inspect, replay, or acknowledge a local delivery health alert queue", Flags: []cliFlag{{Name: "checkpoint", Value: "PATH", Short: "delivery health checkpoint"}, {Name: "replay", Short: "replay pending JSON lines"}, {Name: "ack-alerts", Value: "PATH", Short: "exact acknowledgements JSON"}}},
			{Name: "delivery-retry", Short: "Retry a deferred delivery using its current secret token", Positionals: []string{"<app>", "<invocation-id>"}, Flags: []cliFlag{{Name: "request-file", Value: "PATH", Short: "protected JSON file containing token and non-sensitive reason"}}},
			{Name: "delivery-dead-letter", Short: "Dead-letter a deferred delivery using its current secret token", Positionals: []string{"<app>", "<invocation-id>"}, Flags: []cliFlag{{Name: "request-file", Value: "PATH", Short: "protected JSON file containing token and non-sensitive reason"}}},
			{Name: "deadline-alert-investigate", Short: "Inspect current deadline identities and optional blockers for a saved Go alert", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "alert-file", Value: "PATH", Short: "single Go/CLI lifecycle alert JSON object (maximum 64 KiB)"}, {Name: "limit", Value: "N", Short: "current recipients per page (default 50; 1..100, or 1..50 with explain)"}, {Name: "cursor", Value: "CURSOR", Short: "opaque next_cursor for the same app, alert selection and limit"}, {Name: "explain", Short: "read acceptance-guarded blocker explanations for this page"}}},
			{Name: "subscription-delivery-health", Short: "Inspect routing, executions, retry jobs, and pending blockers", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "deadline-expired-ratio-threshold", Value: "PERCENT", Short: "expired percentage warning, 0.01..100 (two decimal places); zero disables"}, {Name: "deadline-expired-ratio-recovery", Value: "PERCENT", Short: "inclusive percentage below warning; default warning minus 0.01 percentage points"}, {Name: "deadline-expired-ratio-min-recipients", Value: "N", Short: "required minimum pending recipients with deadlines, 1..9007199254740991"}, {Name: "deadline-window", Value: "DURATION", Short: "upcoming horizon, whole milliseconds 100ms-24h; required for deadline alerts"}, {Name: "deadline-upcoming-threshold", Value: "N", Short: "upcoming deadline count warning; zero disables"}, {Name: "deadline-expired-threshold", Value: "N", Short: "all expired deadline count warning; zero disables"}, {Name: "deadline-upcoming-recovery", Value: "N", Short: "inclusive recovery count below upcoming warning; default warning minus one"}, {Name: "deadline-expired-recovery", Value: "N", Short: "inclusive recovery count below expired warning; default warning minus one"}, {Name: "deadline-upcoming-reason", Value: "REASON=N", Short: "repeatable upcoming reason count warning"}, {Name: "deadline-expired-reason", Value: "REASON=N", Short: "repeatable expired reason count warning"}, {Name: "deadline-upcoming-reason-recovery", Value: "REASON=N", Short: "repeatable inclusive recovery count for enabled upcoming reason"}, {Name: "deadline-expired-reason-recovery", Value: "REASON=N", Short: "repeatable inclusive recovery count for enabled expired reason"}, {Name: "deadline-sustain", Value: "DURATION", Short: "deadline warning confirmation, 100ms-24h; default 1m"}, {Name: "deadline-recovery-sustain", Value: "DURATION", Short: "deadline recovery confirmation, 100ms-24h; default 1m"}, {Name: "renewal-rejection-threshold", Value: "FRACTION", Short: "recorded renewal rejection fraction threshold (0..1)"}, {Name: "lease-expiry-rate-threshold", Value: "RATE", Short: "recorded lease expiry transitions per second"}, {Name: "renewal-min-samples", Value: "N", Short: "minimum recorded renewal attempts (default 10)"}, {Name: "processing-window-sustain", Value: "DURATION", Short: "breach and recovery confirmation duration (at least 1m)"}, {Name: "processing-metrics-recording-since", Value: "RFC3339", Short: "manual recording coverage override; default uses verified server inventory"}, {Name: "processing-oldest-threshold", Value: "DURATION", Short: "oldest active processing attempt age; confirms for 30s"}, {Name: "processing-lease-risk-threshold", Value: "DURATION", Short: "sustained nonzero leases in their final third (capped at 30s)"}, {Name: "processing-expired-threshold", Value: "DURATION", Short: "sustained expired processing leases"}, {Name: "lineage-stalled-threshold", Value: "N", Short: "stalled pending retry-job count threshold"}, {Name: "lineage-retention-risk-threshold", Value: "N", Short: "active retry-job retention-risk count threshold"}, {Name: "lineage-paused-threshold", Value: "DURATION", Short: "sustained nonzero paused retry-job backlog"}, {Name: "receipt-budget-full-threshold", Value: "DURATION", Short: "sustained full capacity-command receipt budget"}, {Name: "receipt-budget-remaining-threshold", Value: "N", Short: "warn below N remaining receipt slots (1..128; zero disables)"}, {Name: "receipt-budget-low-sustain", Value: "DURATION", Short: "confirm low remaining slots (default 30s when enabled)"}, {Name: "receipt-budget-recovery-threshold", Value: "N", Short: "opt-in recovery at N remaining slots; N must be at least the warning threshold"}, {Name: "receipt-budget-recovery-sustain", Value: "DURATION", Short: "fresh recovery confirmation (default 30s when recovery enabled)"}, {Name: "window", Short: "5m|15m|1h|6h|24h", Value: "WINDOW"}, {Name: "watch", Short: "watch configured local health thresholds"}, {Name: "alerts-json", Short: "lifecycle alert JSON lines on stderr"}, {Name: "confirmation-progress", Short: "live delivery-health confirmation details on text stdout; omit with --json"}, {Name: "interval", Value: "DURATION", Short: "poll interval (100ms-1m)"}, {Name: "timeout", Value: "DURATION", Short: "watch deadline (up to 24h)"}, {Name: "max-age", Value: "DURATION", Short: "maximum sample age"}, {Name: "oldest-pending-threshold", Value: "DURATION", Short: "pending acceptance age threshold"}, {Name: "retrying-threshold", Value: "N", Short: "retained retrying execution threshold"}, {Name: "dead-letters-threshold", Value: "N", Short: "retained dead-letter execution threshold"}, {Name: "pause-threshold", Value: "DURATION", Short: "subscription pause duration threshold"}, {Name: "circuit-threshold", Value: "DURATION", Short: "locally sampled circuit restriction duration"}, {Name: "capacity-threshold", Value: "DURATION", Short: "locally sampled capacity wait duration"}, {Name: "waiting-reason-count", Value: "REASON=N", Short: "repeatable pending reason count threshold"}, {Name: "waiting-reason-duration", Value: "REASON=DURATION", Short: "repeatable sustained nonzero reason threshold"}, {Name: "checkpoint", Value: "PATH", Short: "persist delivery health watch state"}, {Name: "resume", Short: "resume checkpoint with fresh evidence"}, {Name: "ack-inbox", Value: "PATH", Short: "live acknowledgement JSON batch; requires checkpoint"}, {Name: "slo-routing-latency", Value: "DURATION", Short: "recorded routing p95 target"}, {Name: "slo-completion-latency", Value: "DURATION", Short: "successful completion p95 target"}, {Name: "slo-retry-rate", Value: "N", Short: "routing retries/second target"}, {Name: "slo-dead-letter-rate", Value: "N", Short: "dead letters/second target"}, {Name: "slo-routing-failure-budget", Value: "PERCENT", Short: "terminal routing failure budget"}, {Name: "slo-min-samples", Value: "N", Short: "minimum window samples"}, {Name: "slo-sustain", Value: "DURATION", Short: "sustained fresh breach duration"}}},
			{Name: "subscription-health", Short: "Inspect consumer backlog, routing rates, latency, and pause duration", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "window", Short: "5m|15m|1h|6h|24h", Value: "WINDOW"}}},
			{Name: "subscription-status", Short: "Inspect delivery pause, pacing, and oldest waiting event", Positionals: []string{"<app>", "<subscription-id>"}},
			{Name: "subscription-capacity", Short: "Set a lower live-delivery limit for one subscription", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "command-id", Value: "UUID", Short: "retain original outcome; reuse unchanged on uncertain response"}, {Name: "expected-max-pending", Value: "N", Short: "reviewed saved setting; use with expected-plan-max-pending"}, {Name: "expected-plan-max-pending", Value: "N", Short: "reviewed plan ceiling; use with expected-max-pending"}, {Name: "max-pending", Value: "N", Short: "pending plus dispatching ceiling; 0 restores plan default"}, {Name: "yes", Short: "confirm changing subscription capacity"}}},
			{Name: "subscription-capacity-commands", Short: "List a page of retained original capacity-command receipts", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "outcome", Value: "FILTER", Short: "any (default), applied or unchanged"}, {Name: "limit", Value: "N", Short: "receipts per page (default 25; maximum 100)"}, {Name: "after", Value: "CURSOR", Short: "next_after for the same app, subscription and outcome filter"}, {Name: "timeout", Value: "DURATION", Short: "total read deadline (default 5s; maximum 1m)"}}},
			{Name: "subscription-capacity-command", Short: "Read retained original capacity-command evidence without applying a change", Positionals: []string{"<app>", "<subscription-id>", "<command-id>"}, Flags: []cliFlag{{Name: "timeout", Value: "DURATION", Short: "total read deadline (default 5s; maximum 1m)"}}},
			{Name: "subscription-capacity-preview", Short: "Preview delivery capacity and command receipt budget without changing controls", Positionals: []string{"<app>", "<subscription-id>"}, Flags: []cliFlag{{Name: "max-pending", Value: "N", Short: "proposed live ceiling; 0 restores plan default"}, {Name: "timeout", Value: "DURATION", Short: "total read deadline (default 5s)"}, {Name: "max-age", Value: "DURATION", Short: "maximum pressure snapshot age (default 30s)"}}},
			{Name: "recovery-preview", Short: "Preview a bounded selection of failed event consumers", Positionals: []string{"<app>"}, Flags: []cliFlag{
				{Name: "mode", Short: "routing (default) or execution recovery; parent-job defaults to execution", Value: "MODE"},
				{Name: "parent-job", Short: "select saved failures from one terminal execution recovery", Value: "UUID"},
				{Name: "request-id", Short: "stable request UUID; required for child recovery creation", Value: "UUID"},
				{Name: "outcome", Short: "execution outcome: failed or dead_letter", Value: "OUTCOME"},
				{Name: "subscription-id", Short: "filter by captured consumer identifier", Value: "ID"},
				{Name: "event-source", Short: "filter by exact event source", Value: "SOURCE"},
				{Name: "event-type", Short: "filter by exact event type", Value: "TYPE"},
				{Name: "failure-code", Short: "filter by failure classification", Value: "CODE"},
				{Name: "min-age", Short: "minimum failure age in whole seconds", Value: "DURATION"},
				{Name: "include-non-retryable", Short: "include failures classified as non-retryable", Bool: true},
				{Name: "protect-receipts", Short: "hold pending receipts until admission or job expiry; preview creates no holds", Bool: true},
				{Name: "rate", Short: "maximum retries per second (1..100; default 10)", Value: "N"},
			}},
			{Name: "recovery-create", Short: "Create a durable bulk recovery job", Positionals: []string{"<app>"}, Flags: []cliFlag{
				{Name: "mode", Short: "routing (default) or execution recovery; parent-job defaults to execution", Value: "MODE"},
				{Name: "parent-job", Short: "select saved failures from one terminal execution recovery", Value: "UUID"},
				{Name: "request-id", Short: "stable request UUID; required for child recovery creation", Value: "UUID"},
				{Name: "outcome", Short: "execution outcome: failed or dead_letter", Value: "OUTCOME"},
				{Name: "subscription-id", Short: "filter by captured consumer identifier", Value: "ID"},
				{Name: "event-source", Short: "filter by exact event source", Value: "SOURCE"},
				{Name: "event-type", Short: "filter by exact event type", Value: "TYPE"},
				{Name: "failure-code", Short: "filter by failure classification", Value: "CODE"},
				{Name: "min-age", Short: "minimum failure age in whole seconds", Value: "DURATION"},
				{Name: "include-non-retryable", Short: "include failures classified as non-retryable", Bool: true},
				{Name: "protect-receipts", Short: "hold pending receipts until admission or job expiry; preview creates no holds", Bool: true},
				{Name: "rate", Short: "maximum retries per second (1..100; default 10)", Value: "N"},
				{Name: "reason", Short: "optional operator reason (at most 512 bytes)", Value: "TEXT"}, {Name: "yes", Short: "confirm creating a recovery job", Bool: true, Req: true}}},
			{Name: "recovery-preflight", Short: "Assess frozen recovery eligibility and optimistic timing", Positionals: []string{"<job-id>"}},
			{Name: "recovery-notifications", Short: "Inspect recovery notification capture and receiver acknowledgements", Positionals: []string{"<job-id>"}},
			{Name: "notification-retry-reconcile", Short: "Read saved plan decisions and original-generation delivery outcomes", Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "Prepared retry plan JSON"}, {Name: "wait", Bool: true, Short: "Wait while all unresolved outcomes are pending"}, {Name: "timeout", Value: "DURATION", Short: "Overall wait deadline (default 5m); requires --wait"}}, Examples: []string{"gregale events notification-retry-reconcile --file plan.json --wait --timeout 5m"}},
			{Name: "notification-retry-plan", Short: "Preview explicit notification retries across up to ten app jobs and save a plan", Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "Selection JSON with app_id and stable per-job requests"}, {Name: "output", Value: "NEW_PATH", Short: "Create a new private plan file; never overwrite"}}},
			{Name: "notification-retry-apply", Short: "Apply a prepared plan and emit a JSON decision receipt", Flags: []cliFlag{{Name: "file", Value: "PATH", Short: "Prepared plan; preserve request IDs when resuming"}}},
			{Name: "notification-retry-backlog", Short: "Inspect notification retry requests across retained app recovery jobs", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "status", Value: "STATUS,...", Short: "Request statuses (default failed,pending,inconclusive)"}, {Name: "page-size", Value: "N", Short: "Jobs inspected per page (1..10; default 5)"}, {Name: "cursor", Value: "CURSOR", Short: "Continue to the next job page"}}, Examples: []string{"gregale events notification-retry-backlog my-app --status failed,pending,inconclusive --json"}},
			{Name: "recovery-notification-retry-history", Short: "Inspect saved retry decisions and current delivery status", Positionals: []string{"<job-id>"}, Flags: []cliFlag{{Name: "status", Value: "STATUS,...", Short: "Filter list by succeeded, failed, pending, or inconclusive; incompatible with --request-id"}, {Name: "request-id", Value: "UUID", Short: "Show one saved retry request in detail"}, {Name: "wait", Bool: true, Short: "Wait for the requested retry generations; requires --request-id"}, {Name: "timeout", Value: "DURATION", Short: "Maximum wait duration (default 5m); requires --wait"}}, Examples: []string{"gregale events recovery-notification-retry-history JOB_ID --status failed,inconclusive --json", "gregale events recovery-notification-retry-history JOB_ID --request-id REQUEST_ID --wait --timeout 5m --json"}},
			{Name: "recovery-notification-retry-preview", Short: "Preview eligible recovery notification receivers", Positionals: []string{"<job-id>"}},
			{Name: "recovery-notification-retry", Short: "Retry an explicit recovery notification receiver selection", Positionals: []string{"<job-id>"}, Flags: []cliFlag{{Name: "request-file", Req: true, Value: "PATH", Short: "JSON request with stable request_id and explicit targets"}}},
			{Name: "recovery-health", Short: "Inspect active recovery progress and expiry risk", Positionals: []string{"<app>"}},
			{Name: "recovery-history", Short: "Inspect recovery control audit history", Positionals: []string{"<job-id>"}, Flags: []cliFlag{{Name: "after", Short: "last entry ID from the previous page", Value: "ID"}, {Name: "limit", Short: "entries per page (1..100; default 100)", Value: "N"}}},
			{Name: "recovery-list", Short: "Discover retained recovery jobs", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "state", Short: "filter by recovery state", Value: "STATE"}, {Name: "mode", Short: "routing, execution or workflow", Value: "MODE"}, {Name: "subscription-id", Short: "selected or captured subscription", Value: "ID"}, {Name: "created-after", Short: "exclusive creation lower bound (RFC3339)", Value: "TIME"}, {Name: "created-before", Short: "exclusive creation upper bound (RFC3339)", Value: "TIME"}, {Name: "cursor", Short: "next cursor from the previous page", Value: "CURSOR"}, {Name: "limit", Short: "jobs per page (1..50; default 50)", Value: "N"}}},
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
			{Name: "replay", Short: "Retry one terminal pre-invocation recipient failure using its event identity and subscription ID from events deliveries", Positionals: []string{"<app>"}, Flags: []cliFlag{{Name: "expected-accepted-at", Value: "RFC3339", Short: "guard original acceptance in replay transaction"}, {Name: "expected-generation", Value: "N", Short: "guard routing generation; requires expected-accepted-at"}, {Name: "allow-expired", Bool: true, Short: "explicitly bypass captured delivery age"},
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
		Examples: []string{"gregale apps", "gregale apps --json", "gregale apps rm my-api --dry-run", "gregale apps rm my-api --yes"},
		Subcommands: []cliSub{
			{Name: "ls", Short: "Alias for the default list action"},
			{Name: "rm", Short: "Delete an app (alias: delete); restore it within the deletion grace window", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "dry-run", Short: "preview deletion without changing resources", Bool: true},
				{Name: "yes", Short: "confirm deletion without prompting; required in non-interactive and JSON mode", Bool: true},
				{Name: "quiet", Short: "confirm deletion without prompting (alias of --yes)", Bool: true},
				{Name: "q", ShortName: "q", Short: "confirm deletion without prompting (alias of --yes)", Bool: true},
			}},
			{Name: "restore", Short: "Restore an app during its deletion grace window", Positionals: []string{"<slug>"}},
			{Name: "routes", Short: "List admitted per-route labels for one app", Positionals: []string{"<slug>"}},
			appTCPListenerCLISubcommand(true),
			appUDPListenerCLISubcommand(true),
			{Name: "streaming-cap", Short: "Show app streaming classification", Positionals: []string{"<slug>"}},
		},
		Flags: []cliFlag{{Name: "dry-run", Short: "preview app deletion without changing resources", Bool: true}, {Name: "quiet", ShortName: "q", Short: "delete one app without prompting"}, {Name: "yes", Short: "confirm app deletion without prompting", Bool: true}},
	},
	{
		Name:        appSlugFallback,
		DocSlug:     "apps",
		Short:       "Inspect an app, change settings, or manage its runtime",
		CompactHelp: true,
		HelpNotes: []string{
			"Without update flags, shows the app. Omitted settings stay unchanged.",
			resourceProfileHelpNote,
		},
		Examples: []string{
			"gregale app my-api",
			"gregale app my-api scale --interactive",
			"gregale app my-api --environment staging --ram 512",
		},
		SubcommandsAfterPositionals: true,
		Subcommands: []cliSub{
			{Name: subHealth, Common: true, Short: "Check serving health and readiness", Examples: []string{"gregale app my-api health --wait --timeout 2m"}, Flags: []cliFlag{
				{Name: "check", Short: "single read; exit 0 only for a fresh healthy default-scope assessment; excludes wait and history options", Bool: true},
				{Name: "wait", Short: "wait for a fresh healthy default-scope assessment", Bool: true},
				{Name: "timeout", Short: "maximum wait (default 2m; requires --wait)", Value: "DURATION"},
				{Name: "poll-interval", Short: "poll interval (default 2s; 1s to 1h; requires --wait)", Value: "DURATION"},
				{Name: "history", Short: "show recorded health changes; excludes --wait", Bool: true},
				{Name: "limit", Short: "history page size", Value: "N"},
				{Name: "before", Short: "history cursor", Value: "CURSOR"},
				{Name: "environment", Short: "assess this project environment instead of the default scope; excludes history options", Value: "SLUG"},
			}},
			{Name: subChanges, Short: "List recorded changes and health transitions, newest first (ADR-751)", Examples: []string{
				"gregale app my-api changes",
				"gregale app my-api changes --since 2026-10-08T12:00:00Z --json",
			}, Flags: []cliFlag{
				{Name: "since", Short: "window start, RFC3339 (default: 24h before until)", Value: "RFC3339"},
				{Name: "until", Short: "window end, RFC3339 (default: now)", Value: "RFC3339"},
			}},
			{Name: "scale", Common: true, Short: "Preview or change resources and settings", Examples: []string{
				"gregale app my-api scale --interactive",
				"gregale app my-api scale --plan --ram 512 --out scale-change.json",
				"gregale app my-api scale --apply scale-change.json --confirm",
			}, Flags: append([]cliFlag{
				{Name: "interactive", Short: "choose settings with a preview and confirmation; accepts only --environment", Bool: true},
				{Name: "plan", Short: "show changes and supported plan effects without applying them"},
				{Name: "out", Short: "write a reusable plan JSON to a new file (requires --plan)", Value: "PATH"},
				{Name: "apply", Short: "apply a saved scale plan JSON file", Value: "PATH"},
				{Name: "confirm", Short: "confirm applying the saved plan (requires --apply)"},
			}, appScaleCLIFlags()...)},
			{Name: "costs", Short: "Show this app's attributed usage costs and source coverage", Flags: []cliFlag{
				{Name: "month", Short: "UTC usage month (defaults to current)", Value: "YYYY-MM"},
				{Name: "json", Short: "Print the machine-readable app cost report"},
			}, Examples: []string{"gregale app my-api costs", "gregale app my-api costs --month 2026-10 --json"}},
			{Name: "rename", Short: "Rename an app; the old hostname stops routing", Positionals: []string{"<new-slug>"}, Examples: []string{"gregale app my-api rename my-api-v2"}},
			{Name: "restart", Common: true, Short: "Restart from a snapshot or fresh configuration", Flags: []cliFlag{
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
			{Name: subExec, Common: true, Short: "Run a command against the live deployment", Flags: []cliFlag{
				{Name: "interactive", Short: "attach stdin and stream output live (-i; default command /bin/sh)"},
				{Name: "tty", Short: "allocate a remote terminal (-t; use -it)"},
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
			{Name: "cp", Short: "Copy a path out of a fresh VM of the live deployment", Positionals: []string{"<remote-path>", "<local-path|->"}, Flags: []cliFlag{
				{Name: "wait-timeout", Short: "maximum time to wait for the task VM to start", Value: "D"},
			}},
			{Name: "port-forward", Short: "Forward a local port to HOST:PORT reached from inside the app's network", Positionals: []string{"[LOCAL_PORT:]HOST:PORT"}, Flags: []cliFlag{
				{Name: "address", Short: "local address to listen on (default 127.0.0.1)", Value: "A"},
				{Name: "wait-timeout", Short: "maximum time to wait for the task VM to start", Value: "D"},
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
			{Name: "concurrency", Group: "Scope and inspection", Short: "print only the per-VM concurrency bound for the app's plan", Bool: true},
			{Name: "environment", Group: "Scope and inspection", Common: true, Short: "read or edit this project environment", Value: "SLUG"},
			{Name: "visibility", Group: "Access and security", Short: "set public edge exposure", Value: "public|internal", ClosedSet: []string{"public", "internal"}},
			{Name: "profile", Group: "Resources and scaling", Short: "set RAM/CPU resource size", Value: "SIZE", ClosedSet: []string{"micro", "small", "medium", "large", "xlarge"}},
			{Name: "ram", Group: "Resources and scaling", Short: "set RAM in MB", Value: "MB"},
			{Name: "cpu-millicores", Group: "Resources and scaling", Short: "set sustained CPU allowance", Value: "250|500|1000", ClosedSet: []string{"250", "500", "1000"}},
			{Name: "max-concurrency", Group: "Resources and scaling", Short: "set maximum concurrent requests", Value: "N"},
			{Name: "concurrency-overflow", Group: "Queues and timeouts", Short: "set saturated concurrency behavior", ClosedSet: []string{"queue", "drop"}},
			{Name: "max-queue-depth", Group: "Queues and timeouts", Short: "set maximum queued requests (0 uses plan default)", Value: "N"},
			{Name: "max-queue-wait", Group: "Queues and timeouts", Short: "set queue wait, e.g. 750ms (0 uses plan default); excludes --max-queue-wait-ms", Value: "DURATION"},
			{Name: "max-queue-wait-ms", Group: "Queues and timeouts", Short: "set queue wait in milliseconds (0 uses plan default); excludes --max-queue-wait", Value: "N"},
			{Name: "wake-max-queue-depth", Group: "Queues and timeouts", Short: "set per-app cold-wake waiter cap", Value: "N"},
			{Name: "wake-max-queue-wait-seconds", Group: "Queues and timeouts", Short: "set per-app cold-wake wait budget", Value: "N"},
			{Name: "idle", Group: "Queues and timeouts", Short: "set idle timeout in seconds", Value: "SEC"},
			{Name: "request-timeout", Group: "Queues and timeouts", Short: "set per-app request timeout in seconds", Value: "SEC"},
			{Name: "function-concurrency", Group: "Resources and scaling", Short: "set requests one Node/Python function worker serves at once", Value: "N"},
			{Name: "revision-pin-ttl", Group: "Queues and timeouts", Short: "revision pin lifetime; 0 disables, maximum 168h", Value: "DURATION"},
			{Name: "require-signed", Group: "Access and security", Short: "require signed images on deploy", Value: "true|false", ClosedSet: []string{"true", "false"}},
			{Name: "security-policy", Group: "Access and security", Short: "deploy posture policy", ClosedSet: []string{"off", "warn", "enforce"}},
			{Name: "basic-user", Group: "Access and security", Short: "basic-auth username (required with --public-auth=basic)", Value: "USER"},
			{Name: "basic-pass", Group: "Access and security", Short: "basic-auth password (required with --public-auth=basic)", Value: "PASS"},
			{Name: "min", Group: "Resources and scaling", Short: "set minimum warm instances (Pro/Scale; 0 scales to zero)", Value: "N"},
			{Name: "autoscale-target-rps", Group: "Resources and scaling", Short: "set per-instance RPS scale-up target; 0 disables", Value: "N"},
			{Name: "autoscale-target-cpu-pct", Group: "Resources and scaling", Short: "set per-instance CPU scale-up target; 0 disables", Value: "1..100"},
			{Name: "warm-snapshot", Group: "Wake behavior", Short: "enable the warm-snapshot tier"},
			{Name: "no-warm-snapshot", Group: "Wake behavior", Short: "disable the warm-snapshot tier"},
			{Name: "warm-snapshot-min-requests", Group: "Wake behavior", Short: "set the warm-snapshot request threshold", Value: "N"},
			{Name: "warm-snapshot-min-ms", Group: "Wake behavior", Short: "set the warm-snapshot ready-time threshold", Value: "MS"},
			{Name: "warm-pool-size", Group: "Wake behavior", Short: "set the paused warm-pool size", Value: "N"},
			{Name: "eviction-priority", Group: "Resources and scaling", Short: "set the app eviction tier", Value: "best_effort|reserved", ClosedSet: []string{"best_effort", "reserved"}},
			{Name: "require-authn", Group: "Access and security", Short: "require a Gregale bearer token on every request (Pro/Scale only)"},
			{Name: "no-require-authn", Group: "Access and security", Short: "disable the per-deployment token requirement"},
			{Name: "platform-tenant-required", Group: "Access and security", Short: "require verified customer identity on app traffic (Hobby and above)"},
			{Name: "no-platform-tenant-required", Group: "Access and security", Short: "allow app traffic without verified customer identity"},
			{Name: "maintenance", Group: "Request handling", Common: true, Short: "put every request into 503 maintenance mode"},
			{Name: "no-maintenance", Group: "Request handling", Common: true, Short: "resume normal request handling"},
			{Name: "streaming-enabled", Group: "Request handling", Short: "enable streamed responses (plan eligibility is checked by the API)"},
			{Name: "no-streaming-enabled", Group: "Request handling", Short: "use buffered responses"},
			{Name: "websocket-enabled", Group: "Request handling", Short: "allow WebSocket upgrade forwarding (plan eligibility is checked by the API)"},
			{Name: "no-websocket", Group: "Request handling", Short: "disable WebSocket upgrade forwarding"},
			{Name: "route-metrics", Group: "Request handling", Short: "enable per-route gateway metrics (plan eligibility is checked by the API)"},
			{Name: "no-route-metrics", Group: "Request handling", Short: "disable per-route gateway metrics"},
			{Name: "consumer-auth-mode", Group: "Access and security", Short: "end-customer API-key policy: optional|required", Value: "optional|required", ClosedSet: []string{api.ConsumerAuthModeOptional, api.ConsumerAuthModeRequired}},
			{Name: "only-declared-routes", Group: "Request handling", Short: "reject undeclared paths before waking the app (OpenAPI or explicit route list)"},
			{Name: "no-only-declared-routes", Group: "Request handling", Short: "disable the declared-route pre-wake gate"},
			{Name: "head-wakes", Group: "Wake behavior", Short: "wake a parked app for HEAD /", Bool: true, ClosedSet: []string{"true", "false"}},
			{Name: "crawler-policy", Group: "Wake behavior", Short: "monitor/crawler wake policy", Value: "wake|cached|block", ClosedSet: []string{"wake", "cached", "block"}},
			{Name: "pre-auth", Group: "Access and security", Short: "per-source pre-auth limit mode; enforce first prints the 24h readiness check", Value: "off|observe|enforce", ClosedSet: []string{"off", "observe", "enforce"}},
			{Name: "pre-auth-rps", Group: "Access and security", Short: "pre-auth requests per second per source", Value: "N"},
			{Name: "pre-auth-burst", Group: "Access and security", Short: "pre-auth burst per source", Value: "N"},
			{Name: "release-policy", Group: "Releases", Short: "default rollout for production releases (ADR-1446, ADR-1450)", Value: "safe|fast|immediate", ClosedSet: []string{"safe", "fast", "immediate"}},
			{Name: "release-probe-paths", Group: "Releases", Short: "GET paths (at most 3) the default rollout probe checks instead of /; empty restores / (ADR-1449)", Value: "PATH,PATH"},
			{Name: "health-path", Group: "Wake behavior", Short: "set the monitor-facing health path", Value: "PATH"},
			{Name: "health-path-wakes", Group: "Wake behavior", Short: "allow health probes to wake the app"},
			{Name: "no-health-path-wakes", Group: "Wake behavior", Short: "answer health probes without waking the app"},
			{Name: "app-protocol", Group: "Request handling", Short: "set the wire-protocol selector", Value: "http1|http2|grpc", ClosedSet: []string{"http1", "http2", "grpc"}},
			{Name: "public-auth", Group: "Access and security", Short: "set public URL authentication; internal_only admits Gregale internal services, ip_allowlist is Pro+", Value: "open|bearer|basic|ip_allowlist|internal_only", ClosedSet: []string{"open", "bearer", "basic", "ip_allowlist", "internal_only"}},
			{Name: "ip-allowlist", Group: "Access and security", Short: "allow a CIDR through the public URL; repeat for multiple ranges; requires --public-auth ip_allowlist", Value: "CIDR", Repeatable: true},
			{Name: "overflow-node", Group: "Request handling", Short: "set or clear the preferred overflow compute node", Value: "NAME"},
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
				{Name: "canary-preset", Short: "canary ladder preset", Value: "PRESET", ClosedSet: []string{"slow", "balanced", "aggressive", "1-10-50-100", "adaptive"}},
			}},
		},
		// No group positional: gate and advance take a deployment id and
		// simulate takes the slug, so a group-level <slug> misled (H10-22).
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
				{Name: "rollout", Short: "production rollout mode: standard|safe", Value: "MODE", ClosedSet: []string{"standard", "safe"}},
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
			{Name: "next", Short: "Show upcoming expression times across app tasks, with state and policy caveats", Examples: []string{"gregale crons next --app my-api", "gregale crons next --app my-api --json"}, Flags: []cliFlag{{Name: "app", Short: "app slug (linked app or picker by default)", Value: "SLUG"}}},
			{Name: "list", Short: "List cron rules", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "slug"}}},
			{Name: "add", Short: "Schedule a POST to an app path, or a deployment command", Examples: []string{"gregale crons add --app my-api --interactive"}, Flags: []cliFlag{
				{Name: "interactive", Short: "choose an HTTP path, schedule, and timezone, preview run times, and confirm creation", Bool: true},
				{Name: "app", Short: "app slug (interactive mode can use linked app or picker)", Value: "slug"},
				{Name: "schedule", Short: "five-field cron expression (required unless interactive)", Value: "EXPR"},
				{Name: "path", Short: "path the cron sends a POST to (mutually exclusive with --command)", Value: "PATH"},
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
			{Name: "update", Short: "Update one cron rule", Positionals: []string{"[<id>]"}, Examples: []string{"gregale crons update --app my-api --interactive"}, Flags: []cliFlag{
				{Name: "interactive", Short: "choose an HTTP task, edit current values, preview times, and confirm changes", Bool: true},
				{Name: "app", Short: "app slug for interactive task selection (linked app or picker by default)", Value: "SLUG"},
				{Name: "schedule", Short: "new five-field cron expression", Value: "EXPR"},
				{Name: "path", Short: "path the cron sends a POST to", Value: "PATH"},
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
			{Name: "rm", Short: "Delete one cron rule", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "yes", Short: "accepted for scripts; crons rm does not prompt", Bool: true}}},
			{Name: "run", Short: "Fire one cron immediately or choose and follow a manual request", Positionals: []string{"[<cron-id>]"}, Examples: []string{"gregale crons run --app my-api --interactive", "gregale crons run <cron-id>"}, Flags: []cliFlag{
				{Name: "interactive", Short: "choose a task, review and confirm one manual run, then follow its request", Bool: true},
				{Name: "app", Short: "app slug for interactive selection (linked app or picker by default)", Value: "SLUG"},
				{Name: "timeout", Short: "maximum interactive follow duration (default 2m)", Value: "DURATION"},
			}},
			{Name: "fire-now", Short: "Read or follow an existing manual fire request", Positionals: []string{"<request-id>"}, Examples: []string{"gregale crons fire-now REQUEST_ID --wait --timeout 5m"}, Flags: []cliFlag{
				{Name: "wait", Short: "follow the existing request until terminal status", Bool: true},
				{Name: "timeout", Short: "maximum wait duration (default 2m; requires --wait)", Value: "DURATION"},
			}},
			{Name: "runs", Short: "Show execution history", Positionals: []string{"[<id>]"}, Examples: []string{"gregale crons runs --app my-api --interactive", "gregale crons runs <id>"}, Flags: []cliFlag{
				{Name: "interactive", Short: "choose a task, browse runs, and inspect command output", Bool: true},
				{Name: "app", Short: "app slug for interactive selection (linked app or picker by default)", Value: "SLUG"},
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
		Name:     "jobs",
		DocSlug:  "jobs",
		Short:    "Manage jobs (run-to-completion workloads)",
		Examples: []string{"gregale jobs", "gregale jobs overview nightly-report", "gregale jobs --help"},
		Subcommands: []cliSub{
			{Name: "resume", Short: "Recover a saved Job run: observe, read failure logs, or collect results", Flags: resumeRecordedRunCLIFlags("job"), Examples: []string{"gregale jobs resume --list", "gregale jobs resume --job nightly-report --overview", "gregale jobs resume --job nightly-report --logs", "gregale jobs resume --job nightly-report --results", "gregale jobs resume --run RUN_ID"}},
			{Name: "list", Short: "List jobs in this account", Flags: []cliFlag{
				{Name: "limit", Short: "page size (1..200, default 50)", Value: "N"},
				{Name: "offset", Short: "starting offset (>= 0)", Value: "N"},
				{Name: "all", Short: "walk every page using --limit and --offset"},
			}},
			{Name: "next", Short: "Show upcoming recurring Job expression times and readiness", Examples: []string{"gregale jobs next", "gregale jobs next --json"}},
			{Name: "add", Short: "Create a new job", Positionals: []string{"[<name>]"}, Examples: []string{"gregale jobs add --interactive"}, Flags: []cliFlag{
				{Name: "interactive", Bool: true, Short: "choose a name, image, command, resources, and optional recurring schedule"},
				{Name: "image", Value: "REF", Short: "OCI image (required unless interactive)"},
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
			{Name: "info", Short: "Inspect Job readiness (saved Job by default)", Positionals: []string{"[<name>]"}, Flags: []cliFlag{
				{Name: "check", Bool: true, Short: "exit 0 only for an active Job with a ready image; excludes --wait-ready"},
				{Name: "wait-ready", Bool: true, Short: "wait for selected image preparation"},
				{Name: "timeout", Value: "DURATION", Short: "maximum image wait (default 5m)"},
				{Name: "poll-interval", Value: "DURATION", Short: "polling interval (default 2s)"},
			}, Examples: []string{"gregale jobs info", "gregale jobs info --check", "gregale jobs info --wait-ready", "gregale jobs info my-job --wait-ready --timeout 5m"}},
			{Name: "update", Short: "Update one job", Positionals: []string{"[<name>]"}, Examples: []string{"gregale jobs update --interactive"}, Flags: []cliFlag{
				{Name: "interactive", Bool: true, Short: "choose a Job, edit resources and schedule, and confirm changes"},
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
			{Name: "rm", Short: "Soft-delete one job", Positionals: []string{"[<name>]"}, Flags: []cliFlag{{Name: "interactive", Bool: true, Short: "choose a Job, review configuration, and confirm removal"}}, Examples: []string{"gregale jobs rm --interactive"}},
			{Name: "run", Short: "Dispatch a new run (fan-out N tasks)", Positionals: []string{"[<job-name>]"}, Examples: []string{"gregale jobs run --interactive", "gregale jobs run <job-name>", "gregale jobs run nightly-report --tasks 10 --wait --wait-timeout 10m", "gregale jobs run nightly-report --tasks 10 --out-dir ./results", "gregale jobs run nightly-report --tasks 10 --results", "gregale jobs run --dry-run", "gregale jobs run --saved-job --dry-run", "gregale jobs run --wait-ready --results"}, Flags: []cliFlag{
				{Name: "saved-job", Bool: true, Short: "use local jobs defaults for the Job name and omitted settings; excludes a positional Job name"},
				{Name: "wait-ready", Bool: true, Short: "wait for selected image preparation before submitting"},
				{Name: "image-wait-timeout", Value: "DURATION", Short: "image preparation deadline (default 5m; requires --wait-ready)"},
				{Name: "image-poll-interval", Value: "DURATION", Short: "image polling interval (default 2s; requires --wait-ready)"},
				{Name: "interactive", Bool: true, Short: "choose a Job, review settings, and confirm one run"},
				{Name: "wait", Bool: true, Short: "wait for the accepted run to succeed (explicit Job only)"},
				{Name: "results", Bool: true, Short: "wait and collect outputs (default ./results/JOB/RUN_ID)"},
				{Name: "out-dir", Value: "DIR", Short: "collect whole-run managed artifacts after success; implies --wait"},
				{Name: "all-artifacts", Bool: true, Short: "ignore saved artifact filters for this invocation"},
				{Name: "include", Value: "GLOB", Short: "repeatable artifact-name glob for collection; requires --out-dir or --results; quote patterns"}, {Name: "exclude", Value: "GLOB", Short: "repeatable artifact-name glob to omit; requires --out-dir or --results; exclusions take precedence"},
				{Name: "wait-timeout", Value: "DURATION", Short: "wait deadline after acceptance (default 10m; requires --wait, --out-dir, or --results)"},
				{Name: "poll-interval", Value: "DURATION", Short: "wait read interval (default 2s; 1s to 1h; requires --wait, --out-dir, or --results)"},
				{Name: "dry-run", Bool: true, Short: "preview effective run settings locally without submitting"},
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
			{Name: "runs", Short: "List runs (saved Job by default)", Positionals: []string{"[<name>]"}, Examples: []string{"gregale jobs runs", "gregale jobs runs --failed-only --all"}, Flags: []cliFlag{
				{Name: "failed-only", Bool: true, Short: "show failed and dead-letter runs in fetched history; use --all for subsequent pages"},
				{Name: "status", Short: "filter fetched history by aggregate status; use --all to scan subsequent pages", Value: "STATUS", ClosedSet: []string{"queued", "running", "succeeded", "failed", "cancelled", "dead_letter"}},
				{Name: "limit", Short: "page size (1..200, default 50)", Value: "N"},
				{Name: "offset", Short: "starting offset (>= 0)", Value: "N"},
				{Name: "all", Short: "walk every page using --limit and --offset"},
			}},
			{Name: "overview", Short: "Summarize Job readiness, latest run, and next commands (saved Job by default)", Positionals: []string{"[<name>]"}, Flags: []cliFlag{{Name: "check", Bool: true, Short: "exit 0 only for an active Job, ready image, and succeeded latest run"}}, Examples: []string{"gregale jobs overview", "gregale jobs overview --check", "gregale jobs overview nightly-report --json"}},
			{Name: "status", Short: "Read one exact or latest Job run summary (saved Job and latest by default)", Positionals: []string{"[<name>]", "[<run-id|latest>]"}, Flags: []cliFlag{{Name: "check", Bool: true, Short: "exit 0 only when the recorded run status is succeeded"}}, Examples: []string{"gregale jobs status", "gregale jobs status nightly-report", "gregale jobs status nightly-report RUN_ID --check", "gregale jobs status nightly-report latest", "gregale --json jobs status nightly-report RUN_ID"}},
			{Name: "occurrences", Short: "Inspect recurring schedule decisions", Positionals: []string{"[<name>]"}, Examples: []string{"gregale jobs occurrences --interactive"}, Flags: []cliFlag{
				{Name: "interactive", Bool: true, Short: "choose a Job and browse scheduled occurrence decisions"},
				{Name: "before", Short: "alias for --cursor", Value: "ID"},
				{Name: "cursor", Short: "opaque continuation cursor", Value: "ID"},
				{Name: "all", Short: "walk every page using --limit and --cursor"},
				{Name: "limit", Short: "max occurrence decisions (1..200)", Value: "N"},
			}},
			{Name: "wait", Short: "Follow a Job run (saved Job and latest by default)", Positionals: []string{"[<name>]", "[<run-id|latest>]"}, Flags: []cliFlag{
				{Name: "interactive", Bool: true, Short: "choose a Job and run"},
				{Name: "out-dir", Value: "DIR", Short: "collect artifacts after success, verifying matching existing files; non-interactive target only"},
				{Name: "include", Value: "GLOB", Short: "repeatable artifact-name glob for collection; requires --out-dir; quote patterns"}, {Name: "exclude", Value: "GLOB", Short: "repeatable artifact-name glob to omit; requires --out-dir; exclusions take precedence"},
				{Name: "timeout", Value: "DURATION", Short: "maximum wait duration (default 10m)"},
				{Name: "poll-interval", Value: "DURATION", Short: "polling interval (default 2s)"},
			}, Examples: []string{"gregale jobs wait", "gregale jobs wait nightly-report", "gregale jobs wait nightly-report latest --timeout 10m", "gregale jobs wait --interactive", "gregale jobs wait my-job RUN_ID --timeout 10m --json"}},
			{Name: "cancel", Short: "Cancel a run", Positionals: []string{"[<name>]", "[<run-id>]"}, Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "inspect target status, task counts, and cancellation effects without cancelling"}, {Name: "interactive", Bool: true, Short: "choose an unfinished run, review progress, and confirm cancellation"}}, Examples: []string{"gregale jobs cancel nightly-report RUN_ID --dry-run", "gregale jobs cancel --interactive"}},
			{Name: "defaults", Short: "Show or manage non-secret project-local Job defaults", Examples: []string{"gregale jobs defaults", "gregale jobs defaults show", "gregale jobs defaults --help"}, Subcommands: []cliSub{
				{Name: "set", Short: "Replace saved Job defaults", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "preview current/proposed settings without saving or submitting"}, {Name: "tasks", Value: "N", Short: "default positive task count"}, {Name: "results", Bool: true, Short: "collect results by default"}, {Name: "wait-ready", Bool: true, Short: "wait for image preparation before submitting by default"}, {Name: "include", Value: "GLOB", Short: "repeatable saved artifact-name glob; quote patterns"}, {Name: "exclude", Value: "GLOB", Short: "repeatable saved artifact-name glob to omit"}}},
				{Name: "update", Short: "Change saved Job defaults while preserving omitted settings", Positionals: []string{"[<name>]"}, Examples: []string{"gregale jobs defaults update --tasks 3 --dry-run", "gregale jobs defaults update --clear-tasks --dry-run", "gregale jobs defaults update --results=false", "gregale jobs defaults update --all-artifacts"}, Flags: []cliFlag{
					{Name: "dry-run", Bool: true, Short: "preview changes while preserving omitted settings; no writes or submissions"},
					{Name: "tasks", Value: "N", Short: "replace default positive task count"},
					{Name: "clear-tasks", Bool: true, Short: "unset saved task count while preserving other settings; excludes --tasks"},
					{Name: "results", Bool: true, Short: "replace results preference; use --results=false to disable"},
					{Name: "wait-ready", Bool: true, Short: "replace image-wait preference; use --wait-ready=false to disable"},
					{Name: "include", Value: "GLOB", Short: "repeatable glob; explicit filters replace both saved lists"},
					{Name: "exclude", Value: "GLOB", Short: "repeatable glob to omit; explicit filters replace both saved lists"},
					{Name: "all-artifacts", Bool: true, Short: "clear saved artifact filters"},
					{Name: "add-include", Value: "GLOB", Repeatable: true, Short: "append include glob; preserve other filters and skip duplicates"},
					{Name: "remove-include", Value: "GLOB", Repeatable: true, Short: "remove exact saved include glob; unknown patterns fail"},
					{Name: "add-exclude", Value: "GLOB", Repeatable: true, Short: "append exclude glob; preserve other filters and skip duplicates"},
					{Name: "remove-exclude", Value: "GLOB", Repeatable: true, Short: "remove exact saved exclude glob; unknown patterns fail"},
				}},
				{Name: "show", Short: "Show saved Job settings, their path, and local recovery readiness"},
				{Name: "clear", Short: "Remove saved Job defaults", Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "show settings and inheritance effects without removing the file"}}, Examples: []string{"gregale jobs defaults clear --dry-run"}},
			}},
			{Name: "results", Short: "Wait for success and collect outputs, verifying matching existing files", Positionals: []string{"[<name>]", "[<run-id|latest>]"}, Examples: []string{"gregale jobs results nightly-report", "gregale jobs results nightly-report latest --out-dir ./results", "gregale jobs results nightly-report latest --out-dir ./results --dry-run"}, Flags: []cliFlag{
				{Name: "out-dir", Value: "DIR", Short: "local destination (default ./results/JOB/RUN_ID)"},
				{Name: "all-artifacts", Bool: true, Short: "ignore saved artifact filters for this invocation"},
				{Name: "dry-run", Bool: true, Short: "preview current artifacts without waiting or writing files"},
				{Name: "timeout", Value: "DURATION", Short: "maximum wait duration (default 10m)"},
				{Name: "poll-interval", Value: "DURATION", Short: "polling interval (default 2s)"},
				{Name: "include", Value: "GLOB", Short: "repeatable artifact-name glob; quote patterns"},
				{Name: "exclude", Value: "GLOB", Short: "repeatable artifact-name glob to omit; exclusions take precedence"},
			}},
			{Name: "tasks", Short: "List tasks (saved Job and latest run by default)", Positionals: []string{"[<name>]", "[<run-id|latest>]"}, Flags: []cliFlag{{Name: "all", Bool: true, Short: "read all task pages from the starting offset"}, {Name: "limit", Value: "N", Short: "task page size (1..200)"}, {Name: "offset", Value: "N", Short: "starting task offset (non-negative)"}, {Name: "unsuccessful-only", Bool: true, Short: "scan all task pages for failed, timeout, oom, and cancelled tasks"}, {Name: "failed-only", Bool: true, Short: "scan all task pages and show failed, timeout, and oom tasks"}, {Name: "artifacts-only", Bool: true, Short: "scan task pages for valid manifests containing artifacts"}, {Name: "include", Value: "GLOB", Short: "repeatable artifact-name glob; requires --artifacts-only; quote patterns"}, {Name: "exclude", Value: "GLOB", Short: "repeatable artifact-name glob to omit; requires --artifacts-only; exclusions take precedence"}, {Name: "interactive", Bool: true, Short: "choose a run, browse task pages, and filter unsuccessful tasks"}}, Examples: []string{"gregale jobs tasks --unsuccessful-only", "gregale jobs tasks", "gregale jobs tasks --failed-only", "gregale jobs tasks --artifacts-only", "gregale jobs tasks nightly-report latest --all", "gregale jobs tasks nightly-report latest --failed-only", "gregale jobs tasks nightly-report latest", "gregale jobs tasks --interactive"}},
			{Name: "attempts", Short: "List retained attempts for one task in an exact or latest run", Positionals: []string{"[<name>]", "[<run-id|latest>]", "[<task-index>]"}, Flags: []cliFlag{{Name: "task", Value: "N", Short: "task index; omitted Job uses local defaults, omitted run uses latest"}, {Name: "unsuccessful-only", Bool: true, Short: "filter fetched history to failed, timeout, oom, and cancelled attempts"}, {Name: "failed-only", Bool: true, Short: "filter fetched history to failed, timeout, and oom attempts"}, {Name: "all", Bool: true, Short: "read all retained attempt pages from the starting offset"}, {Name: "limit", Value: "N", Short: "history page size (1..200; server default 50)"}, {Name: "offset", Value: "N", Short: "starting history offset (non-negative)"}, {Name: "interactive", Bool: true, Short: "choose a task and inspect retained attempts and output"}}, Examples: []string{"gregale jobs attempts --task 2 --unsuccessful-only --all", "gregale jobs attempts --task 2 --failed-only --all", "gregale jobs attempts nightly-report latest 0 --all --failed-only", "gregale jobs attempts nightly-report latest 0 --all", "gregale jobs attempts nightly-report latest 0", "gregale jobs attempts --interactive"}},
			{Name: "retry", Short: "Retry one failed task; --task uses the saved Job and newest run", Positionals: []string{"[<name>]", "[<run-id>]", "[<task-index>]"}, Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "read task state and preview retry without starting another attempt"}, {Name: "results", Bool: true, Short: "wait for success and collect outputs into ./results/JOB/RUN_ID, verifying existing files"}, {Name: "include", Value: "GLOB", Short: "artifact-name glob; requires --results"}, {Name: "exclude", Value: "GLOB", Short: "artifact-name glob to omit; requires --results"}, {Name: "all-artifacts", Bool: true, Short: "ignore saved artifact filters; requires --results"}, {Name: "task", Value: "N", Short: "task index; omitted Job uses local defaults, omitted run uses latest"}, {Name: "wait", Bool: true, Short: "wait for the whole run after one task retry"}, {Name: "wait-timeout", Value: "DURATION", Short: "wait deadline after retry acceptance (default 10m; requires --wait or --results)"}, {Name: "poll-interval", Value: "DURATION", Short: "wait read interval (default 2s; 1s to 1h; requires --wait or --results)"}, {Name: "interactive", Bool: true, Short: "choose a failed task, review eligibility, and confirm one retry"}}, Examples: []string{"gregale jobs retry --task 2 --dry-run", "gregale jobs retry --task 2 --results", "gregale jobs retry --task 2", "gregale jobs retry nightly-report latest --task 2 --wait", "gregale jobs retry nightly-report RUN_ID 0 --wait", "gregale jobs retry --interactive"}},
			{Name: "replay-failed", Short: "Replay unsuccessful tasks; omitted Job uses local defaults, omitted run uses latest", Positionals: []string{"[<name>]", "[<run-id|latest>]"}, Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "read source tasks and preview replay without starting a new run"}, {Name: "results", Bool: true, Short: "wait for recovery success and collect outputs into ./results/JOB/NEW_RUN_ID, verifying existing files"}, {Name: "include", Value: "GLOB", Short: "artifact-name glob; requires --results"}, {Name: "exclude", Value: "GLOB", Short: "artifact-name glob to omit; requires --results"}, {Name: "all-artifacts", Bool: true, Short: "ignore saved artifact filters; requires --results"}, {Name: "wait", Bool: true, Short: "wait for the new recovery run"}, {Name: "wait-timeout", Value: "DURATION", Short: "wait deadline after replay acceptance (default 10m; requires --wait or --results)"}, {Name: "poll-interval", Value: "DURATION", Short: "wait read interval (default 2s; 1s to 1h; requires --wait or --results)"}, {Name: "interactive", Bool: true, Short: "choose a source run, review unsuccessful tasks, and confirm replay"}}, Examples: []string{"gregale jobs replay-failed nightly-report latest --dry-run", "gregale jobs replay-failed --results", "gregale jobs replay-failed", "gregale jobs replay-failed nightly-report latest --wait", "gregale jobs replay-failed nightly-report RUN_ID --wait", "gregale jobs replay-failed --interactive"}},
			{Name: "artifact-url", Short: "Verify a managed result and get a signed URL", Positionals: []string{"[<name>]", "[<run-id|latest>]", "[<task-index>]", "[<artifact-name>]"}, Flags: []cliFlag{{Name: "interactive", Bool: true, Short: "choose a task and managed output artifact"}}, Examples: []string{"gregale jobs artifact-url nightly-report latest 0 report.csv", "gregale jobs artifact-url --interactive", "gregale jobs artifact-url <name> <run-id> <task-index> <artifact-name>"}},
			{Name: "artifact-download", Short: "Download and verify current managed artifacts", Positionals: []string{"[<name>]", "[<run-id|latest>]", "[<task-index>]", "[<artifact-name>]"}, Flags: []cliFlag{{Name: "task", Value: "N", Short: "single-artifact task index; omitted Job uses local defaults, omitted run uses latest"}, {Name: "out", Value: "PATH", Short: "new destination file; required for a single artifact"}, {Name: "all", Bool: true, Short: "download all current managed artifacts"}, {Name: "all-tasks", Bool: true, Short: "download current managed artifacts across all task pages into task directories"}, {Name: "include", Value: "GLOB", Short: "repeatable artifact-name glob for bulk modes; quote patterns"}, {Name: "exclude", Value: "GLOB", Short: "repeatable artifact-name glob to omit in bulk modes; exclusions take precedence"}, {Name: "dry-run", Bool: true, Short: "preview bulk destinations, bytes, and conflicts; requires --all or --all-tasks"}, {Name: "resume", Bool: true, Short: "verify and skip matching files; requires bulk mode and an explicit run UUID"}, {Name: "out-dir", Value: "DIR", Short: "destination directory, created when missing; required in bulk mode"}}, Examples: []string{"gregale jobs artifact-download --task 2 report.csv --out report.csv", "gregale jobs artifact-download nightly-report latest 0 report.csv --out report.csv"}},
			{Name: "artifacts", Short: "List recorded task artifacts and managed download commands", Flags: []cliFlag{{Name: "task", Value: "N", Short: "task index; omitted Job uses local defaults, omitted run uses latest"}, {Name: "attempt", Short: "retained attempt number (metadata only)", Value: "N"}, {Name: "include", Value: "GLOB", Short: "repeatable artifact-name glob; quote patterns"}, {Name: "exclude", Value: "GLOB", Short: "repeatable artifact-name glob to omit; exclusions take precedence"}}, Positionals: []string{"[<name>]", "[<run-id|latest>]", "[<task-index>]"}, Examples: []string{"gregale jobs artifacts --task 2", "gregale jobs artifacts --task 2 --attempt 1", "gregale jobs artifacts nightly-report latest 0"}},
			{Name: "logs", Short: "Read unsuccessful-task logs by default, or select one task", Positionals: []string{"[<name>]", "[<run-id|latest>]", "[<task-index>]"}, Examples: []string{"gregale jobs logs", "gregale jobs logs nightly-report latest 0 --follow", "gregale jobs logs nightly-report", "gregale jobs logs --failed-only", "gregale jobs logs --task 0", "gregale jobs logs nightly-report --task 0", "gregale jobs logs nightly-report latest 0", "gregale jobs logs --interactive"}, Flags: []cliFlag{
				{Name: "follow", Bool: true, Short: "stream the task's output live until it finishes (-f; ADR-1163)"},
				{Name: "failed-only", Bool: true, Short: "scan task pages and read current logs for failed, timeout, and oom tasks"},
				{Name: "unsuccessful-only", Bool: true, Short: "scan task pages and read current logs for failed, timeout, oom, and cancelled tasks"},
				{Name: "task", Value: "N", Short: "task index shortcut; omitted Job uses saved defaults, omitted run uses latest"},
				{Name: "attempt", Value: "N", Short: "read retained output for a positive attempt number (explicit task only)"},
				{Name: "interactive", Bool: true, Short: "choose a Job, run, and task to inspect"},
				{Name: "max-bytes", Short: "maximum log payload size (1..1048576)", Value: "N"},
			}},
			{Name: "registry", Short: "Manage private registry credentials for one job", Subcommands: []cliSub{
				{Name: "list", Short: "List registry credentials for the job", Positionals: []string{"<job>"}},
				{Name: "set", Short: "Store a registry credential for the job", Positionals: []string{"[<job>]"}, Examples: []string{"gregale jobs registry set --interactive"}, Flags: []cliFlag{
					{Name: "interactive", Bool: true, Short: "choose a Job, enter a hidden token, and review add or replace"},
					{Name: "registry", Short: "registry host", Value: "HOST", Req: true},
					{Name: "user", Short: "registry user", Value: "USER", Req: true},
					{Name: "password-stdin", Short: "read the password from stdin"},
					{Name: "password", Short: "registry password (prefer --password-stdin)", Value: "PASSWORD"},
				}},
				{Name: "rm", Short: "Remove a registry credential from the job", Positionals: []string{"[<job>]"}, Examples: []string{"gregale jobs registry rm --interactive"}, Flags: []cliFlag{{Name: "interactive", Bool: true, Short: "choose a credential, review metadata, and confirm removal"},
					{Name: "registry", Short: "registry host (required unless interactive)", Value: "HOST"},
				}},
			}},
		},
	},
	{
		Name:    "automations",
		DocSlug: "automations",
		Short:   "Build, monitor and control customer-built automations",
		Examples: []string{
			"gregale automations init --list",
			"gregale automations check --app billing --scenarios scenarios.yaml",
			"gregale automations diff --app billing --name approval-flow",
			"gregale automations init --template scheduled-report --path reports",
			"gregale automations runs --app billing --name approval-flow --status failed",
			"gregale automations diagnose --app billing --name approval-flow --run RUN_ID",
			"gregale automations recovery-preview --app billing --name approval-flow --from 2026-10-01T00:00:00Z --until 2026-10-02T00:00:00Z",
			"gregale automations recover --app billing --name approval-flow --run RUN_ID:COUNT --idempotency-key recovery-batch-1",
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
			{Name: "live", Short: "Start, observe and check saved cases against real execution", Subcommands: automationLiveCLISubs()},
			{Name: "diff", Short: "Compare draft and published definition fields without exposing values", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}}},
			{Name: "check", Short: "Check simulation scenarios and expected step outcomes", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "scenarios", Short: "scenario suite YAML or JSON file", Req: true, Value: "PATH"}, {Name: "require-coverage", Short: "fail on unexcluded coverage hints"}, {Name: "suggest", Short: "write coverage scenario starters to a new file"}, {Name: "suggest-out", Short: "new suggestion file (requires --suggest; defaults beside the suite)", Value: "PATH"}}},
			{Name: "init", Short: "Create a local automation starter with simulation samples", Flags: []cliFlag{{Name: "template", Short: "automation starter name", Value: "NAME"}, {Name: "path", Short: "new directory (defaults to starter name)", Value: "DIR"}, {Name: "list", Short: "list starters without creating files"}}},
			{Name: "list", Short: "List automation versions and ownership for an app", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}}},
			{Name: "get", Short: "Inspect automation state or export a definition", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "definition-out", Short: "export the selected definition as JSON to a new file", Value: "PATH"}, {Name: "published", Short: "export the published definition instead of the draft"}}},
			{Name: "runs", Short: "List run history for one automation", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "status", Short: "pending, running, awaiting_event, succeeded, failed or dead", Value: "STATUS"}, {Name: "limit", Short: "page size (1..100, default 50)", Value: "N"}, {Name: "offset", Short: "page offset", Value: "N"}, {Name: "created-after", Short: "inclusive RFC3339 creation-time start", Value: "RFC3339"}, {Name: "created-before", Short: "inclusive RFC3339 creation-time end", Value: "RFC3339"}}},
			{Name: "diagnose", Short: "Diagnose a run belonging to the selected automation", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "run", Short: "workflow run UUID", Req: true, Value: "UUID"}}},
			{Name: "history", Short: "Inspect retained recovery automation history", Flags: []cliFlag{{Name: "app", Req: true, Value: "SLUG", Short: "app slug"}, {Name: "workflow", Value: "NAME", Short: "native workflow filter"}, {Name: "run", Value: "UUID", Short: "native run filter"}, {Name: "policy-version", Value: "N", Short: "policy version filter"}, {Name: "created-after", Value: "RFC3339", Short: "inclusive start"}, {Name: "created-before", Value: "RFC3339", Short: "inclusive end"}, {Name: "after", Value: "CURSOR", Short: "next page cursor"}, {Name: "limit", Value: "N", Short: "page size (default 25, max 100)"}}},
			{Name: "health", Short: "Inspect app recovery health or named automation run reliability", Flags: []cliFlag{{Name: "stall-after-seconds", Value: "SECONDS", Short: "app recovery evaluation threshold (default 300)"}, {Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Value: "NAME"}, {Name: "created-after", Short: "inclusive RFC3339 window start (max 30 days)", Value: "RFC3339"}, {Name: "created-before", Short: "inclusive RFC3339 window end", Value: "RFC3339"}}},
			{Name: "recover", Short: "Resume explicitly selected failed runs with per-run generation checks and outcomes", Flags: []cliFlag{{Name: "plan-out", Short: "save reviewed selection to a new plan file without resuming", Value: "PATH"}, {Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "run", Short: "run UUID and original expected resume count; repeatable, at most 25", Req: true, Value: "UUID:COUNT"}, {Name: "idempotency-key", Short: "stable batch key; reuse for interrupted retries", Req: true, Value: "KEY"}}},
			{Name: "event-recovery-plan", Short: "Save selected retained automation routing actions for review", Flags: []cliFlag{{Name: "app", Req: true, Value: "SLUG", Short: "app slug"}, {Name: "name", Req: true, Value: "NAME", Short: "automation name"}, {Name: "source", Req: true, Value: "SOURCE", Short: "event source"}, {Name: "event", Req: true, Value: "ID", Short: "selected event ID; repeatable, at most 25"}, {Name: "idempotency-key", Req: true, Value: "KEY", Short: "stable batch key"}, {Name: "out", Req: true, Value: "PATH", Short: "new event plan file"}}},
			{Name: "recovery-health", Short: "Inspect recovery execution attention and explicit action hints", Flags: []cliFlag{{Name: "app", Req: true, Value: "SLUG", Short: "app slug"}}},

			{Name: "recovery-policy", Short: "Inspect, preview or update recovery policy", Flags: []cliFlag{{Name: "app", Req: true, Value: "SLUG", Short: "app slug"}, {Name: "file", Value: "PATH", Short: "policy JSON with expected_version"}, {Name: "preview", Short: "preview --file without changing policy"}, {Name: "queue", Short: "inspect automatic retry queue"}, {Name: "control", Value: "pause|resume", Short: "control automatic execution"}, {Name: "reason", Value: "TEXT", Short: "operator control reason"}}},
			{Name: "recovery-policy-reviews", Short: "Inspect policy decisions and export prepared plans", Flags: []cliFlag{{Name: "app", Req: true, Value: "SLUG", Short: "app slug"}, {Name: "review", Value: "UUID", Short: "retained review"}, {Name: "plan-out", Value: "PATH", Short: "export selected prepared plan"}, {Name: "after", Value: "UUID", Short: "review pagination cursor"}, {Name: "limit", Value: "N", Short: "page limit"}}},
			{Name: "recovery-response-plan", Short: "Save reviewed cancellation and retry actions", Flags: []cliFlag{{Name: "app", Req: true, Value: "SLUG", Short: "app slug"}, {Name: "job", Req: true, Value: "UUID", Short: "native recovery job"}, {Name: "key", Req: true, Value: "KEY", Short: "stable response key"}, {Name: "reason", Req: true, Value: "TEXT", Short: "operator reason"}, {Name: "plan-out", Req: true, Value: "PATH", Short: "new saved plan"}, {Name: "cancel-position", Value: "N", Short: "item to cancel (repeatable)"}, {Name: "retry-position", Value: "N", Short: "saved failed item to retry (repeatable)"}, {Name: "rate", Value: "N", Short: "retry admission rate"}}},
			{Name: "recovery-response-preview", Short: "Recheck saved actions without applying", Flags: []cliFlag{{Name: "plan", Req: true, Value: "PATH", Short: "saved response plan"}}},
			{Name: "recovery-response-apply", Short: "Apply saved actions with durable receipts", Flags: []cliFlag{{Name: "plan", Req: true, Value: "PATH", Short: "saved response plan"}, {Name: "yes", Req: true, Bool: true, Short: "confirm reviewed actions"}}},
			{Name: "recovery-response-status", Short: "Reconcile retained action receipts and execution outcomes", Flags: []cliFlag{{Name: "plan", Req: true, Value: "PATH", Short: "saved response plan"}}},
			{Name: "recovery-cancel-preview", Short: "Review or cancel one admitted recovery generation", Flags: []cliFlag{{Name: "app", Req: true, Value: "SLUG", Short: "app slug"}, {Name: "job", Req: true, Value: "UUID", Short: "native recovery job"}, {Name: "position", Req: true, Value: "N", Short: "frozen item position"}, {Name: "resume-count", Req: true, Value: "N", Short: "reviewed admitted generation"}, {Name: "reason", Req: true, Value: "TEXT", Short: "operator reason"}}},
			{Name: "recovery-cancel", Short: "Review or cancel one admitted recovery generation", Flags: []cliFlag{{Name: "app", Req: true, Value: "SLUG", Short: "app slug"}, {Name: "job", Req: true, Value: "UUID", Short: "native recovery job"}, {Name: "position", Req: true, Value: "N", Short: "frozen item position"}, {Name: "resume-count", Req: true, Value: "N", Short: "reviewed admitted generation"}, {Name: "reason", Req: true, Value: "TEXT", Short: "operator reason"}, {Name: "yes", Req: true, Bool: true, Short: "confirm exact generation cancellation"}}},
			{Name: "failures", Short: "List captured initial workflow failures", Flags: []cliFlag{{Name: "app", Req: true, Value: "SLUG", Short: "app slug"}, {Name: "name", Value: "NAME", Short: "workflow filter"}, {Name: "after", Value: "CURSOR", Short: "next page cursor"}, {Name: "limit", Value: "N", Short: "page size (at most 100)"}}},
			{Name: "recovery-retry-evidence", Short: "Diagnose or repair verified automatic retry evidence", Flags: []cliFlag{{Name: "app", Req: true, Value: "SLUG", Short: "app slug"}, {Name: "review", Req: true, Value: "UUID", Short: "automatic retry review"}, {Name: "action", Req: true, Value: "UUID", Short: "automatic retry action"}, {Name: "parent", Bool: true, Short: "inspect or repair pending retry parent evidence"}, {Name: "repair", Bool: true, Short: "save a verified terminal outcome"}, {Name: "request-id", Value: "UUID", Short: "stable repair request identifier"}, {Name: "reason", Value: "TEXT", Short: "operator repair reason"}}},
			{Name: "failure-recovery-evidence", Short: "Diagnose or repair verified initial recovery evidence", Flags: []cliFlag{{Name: "app", Req: true, Value: "SLUG", Short: "app slug"}, {Name: "review", Req: true, Value: "UUID", Short: "initial recovery review"}, {Name: "repair", Bool: true, Short: "save a verified terminal outcome"}, {Name: "request-id", Value: "UUID", Short: "stable repair request identifier"}, {Name: "reason", Value: "TEXT", Short: "operator repair reason"}}},
			{Name: "failure-recovery-reviews", Short: "List retained durable first recovery reviews", Flags: []cliFlag{{Name: "app", Req: true, Value: "SLUG", Short: "app slug"}, {Name: "after", Value: "UUID", Short: "next page cursor"}, {Name: "limit", Value: "N", Short: "page size"}}},
			{Name: "failure-recovery-review", Short: "Prepare, inspect, submit or cancel a durable first recovery review", Flags: []cliFlag{{Name: "app", Req: true, Value: "SLUG", Short: "app slug"}, {Name: "failure", Value: "UUID", Short: "captured initial failure"}, {Name: "expected-policy-version", Value: "N", Short: "saved policy version"}, {Name: "review", Value: "UUID", Short: "durable review"}, {Name: "decision", Value: "DECISION", Short: "submit or cancel"}, {Name: "reason", Value: "TEXT", Short: "operator reason"}}},
			{Name: "failure-recovery-plan", Short: "Preview an initial failure and save a reviewed first recovery plan", Flags: []cliFlag{{Name: "app", Req: true, Value: "SLUG", Short: "app slug"}, {Name: "failure", Req: true, Value: "UUID", Short: "captured initial failure"}, {Name: "policy", Value: "PATH", Short: "candidate policy dry run"}, {Name: "plan-out", Value: "PATH", Short: "new reviewed recovery plan"}, {Name: "key", Value: "KEY", Short: "stable recovery key"}}},
			{Name: "recovery-retry-plan", Short: "Review saved workflow recovery failures and save an explicit retry plan", Flags: []cliFlag{{Name: "app", Req: true, Value: "SLUG", Short: "app slug"}, {Name: "job", Req: true, Value: "UUID", Short: "parent recovery job"}, {Name: "position", Value: "N", Short: "reviewed parent item position (repeatable)"}, {Name: "plan-out", Value: "PATH", Short: "new reviewed retry plan file"}, {Name: "key", Value: "KEY", Short: "stable idempotency key for the retry plan"}}},
			{Name: "recovery-start", Short: "Submit a reviewed automation run plan as a durable server job", Flags: []cliFlag{{Name: "plan", Req: true, Value: "PATH", Short: "reviewed run plan"}, {Name: "rate", Value: "N", Short: "maximum recoveries per second (1..100)"}, {Name: "execution-deadline", Value: "DURATION", Short: "advisory execution deadline (zero disables)"}, {Name: "execution-stall", Value: "DURATION", Short: "no-progress warning threshold (default 15m)"}, {Name: "reason", Value: "TEXT", Short: "optional operator reason"}}},
			{Name: "event-recovery-start", Short: "Submit a reviewed automation event plan as a durable server job", Flags: []cliFlag{{Name: "plan", Req: true, Value: "PATH", Short: "reviewed event plan"}, {Name: "rate", Value: "N", Short: "maximum recoveries per second (1..100)"}, {Name: "reason", Value: "TEXT", Short: "optional operator reason"}}},
			{Name: "event-recover", Short: "Execute a reviewed event plan with fresh receipt checks and a journal", Flags: []cliFlag{{Name: "plan", Req: true, Value: "PATH", Short: "reviewed event plan"}, {Name: "journal", Req: true, Value: "PATH", Short: "new execution journal"}}},
			{Name: "recovery-plan", Short: "Execute a saved recovery plan with a durable per-attempt journal", Flags: []cliFlag{{Name: "plan", Short: "reviewed recovery plan file", Req: true, Value: "PATH"}, {Name: "journal", Short: "new journal file for this execution", Req: true, Value: "PATH"}}},
			{Name: "recovery-preview", Short: "Inspect failed run recovery, retained events and schedule catch-up without writes", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "from", Short: "RFC3339 event acceptance and schedule preview start", Req: true, Value: "TIME"}, {Name: "until", Short: "RFC3339 end/evaluation time (default now)", Value: "TIME"}, {Name: "limit", Short: "runs per status and event scan page (1..25, default 10)", Value: "N"}, {Name: "run-offset", Short: "offset in each failed/dead run page", Value: "N"}, {Name: "event-after", Short: "retained event preview cursor", Value: "CURSOR"}}},
			{Name: "failure-policy", Short: "Inspect failure pause status, history and resume preview or configure monitoring", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "enabled", Short: "enable or disable monitoring; required when configuring", Value: "BOOL"}, {Name: "expected-version", Short: "current failure policy version; required when configuring", Value: "N"}, {Name: "failure-threshold", Short: "terminal failures needed to pause (1..10000, default 3)", Value: "N"}, {Name: "min-completed-runs", Short: "minimum completed outcomes (1..10000, default 5)", Value: "N"}, {Name: "window-seconds", Short: "observation window (60..86400, default 300)", Value: "N"}}},
			{Name: "failure-resume", Short: "Preview retained work and explicitly clear a failure pause", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "expected-generation", Short: "current failure pause generation", Req: true, Value: "N"}}},
			{Name: "pause", Short: "Stop future scheduled and event-triggered admissions", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "expected-version", Short: "current automation version", Req: true, Value: "N"}}},
			{Name: "resume", Short: "Resume automatic scheduled and event-triggered admissions", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "expected-version", Short: "current automation version", Req: true, Value: "N"}}},
			{Name: "revisions", Short: "Inspect immutable published snapshots", Subcommands: []cliSub{
				{Name: "list", Short: "List published revisions for an automation", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "limit", Short: "page size (1..100, default 50)", Value: "N"}, {Name: "offset", Short: "number of revisions to skip", Value: "N"}}},
				{Name: "show", Short: "Inspect a revision or export its definition", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "revision", Short: "published revision number", Req: true, Value: "N"}, {Name: "definition-out", Short: "export the definition as JSON to a new file", Value: "PATH"}}},
			}},
			{Name: "restore", Short: "Restore a published revision as a draft", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "revision", Short: "published revision number to restore", Req: true, Value: "N"}, {Name: "expected-version", Short: "current version; use 0 if deleted", Req: true, Value: "N"}}},
			{Name: "delete", Short: "Delete an automation using its current version", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "expected-version", Short: "current automation version", Req: true, Value: "N"}, {Name: "yes", Short: "required explicit confirmation of automation deletion", Req: true, Bool: true}, {Name: "restore-manifest", Short: "allow the current YAML definition to own this automation again"}}},
			{Name: "validate", Short: "Validate an automation definition without saving it", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "file", Short: "YAML or JSON definition file", Req: true, Value: "PATH"}}},
			{Name: "simulate", Short: "Trace an automation using sample input and mocked outputs, without running steps", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "file", Short: "YAML or JSON definition file", Req: true, Value: "PATH"}, {Name: "input-file", Short: "sample workflow input JSON file", Value: "PATH"}, {Name: "mock-outputs-file", Short: "JSON object of action outputs keyed by step name", Value: "PATH"}, {Name: "mock-item-outputs-file", Short: "JSON object of for_each output arrays keyed by step name", Value: "PATH"}, {Name: "mock-attempts-file", Short: "JSON object of ordered attempt outcomes keyed by step name", Value: "PATH"}, {Name: "mock-item-attempts-file", Short: "JSON object mapping loops and zero-based item indexes to attempt outcomes", Value: "PATH"}, {Name: "require-complete", Short: "fail if mocks leave steps unresolved"}}},
			{Name: "apply", Short: "Save an automation definition as a draft", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "file", Short: "YAML or JSON definition file", Req: true, Value: "PATH"}, {Name: "expected-version", Short: "current version; use 0 for a new draft", Req: true, Value: "N"}}},
			{Name: "publish-policy", Short: "Inspect or set required server publishing checks", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "mode", Short: "set publishing requirement (admin scope)", Value: "MODE", ClosedSet: []string{"optional", "scenarios", "coverage"}}, {Name: "expected-version", Short: "current policy version (required when setting mode)", Value: "N"}}},
			{Name: "publish", Short: "Publish the current automation draft", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "SLUG"}, {Name: "name", Short: "automation name", Req: true, Value: "NAME"}, {Name: "expected-version", Short: "current version of the draft", Req: true, Value: "N"}, {Name: "take-over-manifest", Short: "explicitly take over YAML ownership"}, {Name: "scenarios", Short: "check scenarios against the saved draft before publishing", Value: "PATH"}, {Name: "require-coverage", Short: "require coverage before publishing (requires --scenarios)"}}},
		},
	},
	{
		Name:    "workflows",
		DocSlug: "workflows",
		Short:   "Manage durable execution workflows",
		Subcommands: []cliSub{
			{Name: "defaults", Short: "Show or manage project-local workflow recovery selectors", Examples: []string{"gregale workflows defaults", "gregale workflows defaults --help"}, Subcommands: []cliSub{
				{Name: "set", Short: "Save a preferred workflow name and recorded app locally", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Req: true, Short: "recorded app slug for recovery; target existence is not checked"}, {Name: "dry-run", Bool: true, Short: "preview destination, before/after selectors, and proposed recovery readiness without saving"}}, Examples: []string{"gregale workflows defaults set order-processing --app payments-api", "gregale workflows defaults set order-processing --app payments-api --dry-run"}},
				{Name: "show", Short: "Show saved workflow/app selectors, preference path, and local recovery readiness"},
				{Name: "update", Short: "Change saved workflow selectors while preserving omitted settings", Positionals: []string{"[<name>]"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "replace the saved recorded app selector"}, {Name: "dry-run", Bool: true, Short: "preview updated selectors and local recovery readiness without saving"}}, Examples: []string{"gregale workflows defaults update --app billing-api --dry-run", "gregale workflows defaults update nightly-report --dry-run"}},
				{Name: "clear", Short: "Clear workflow recovery preferences without deleting run records", Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "preview saved selectors and removal effects without deleting files"}}, Examples: []string{"gregale workflows defaults clear --dry-run"}},
			}},
			{Name: "resume-wait", Short: "Recover a saved Workflow run: observe or read diagnostics", Flags: resumeRecordedRunCLIFlags("workflow"), Examples: []string{"gregale workflows resume-wait --saved-workflow --latest --dry-run", "gregale workflows resume-wait --list", "gregale workflows resume-wait --workflow order-processing --overview", "gregale workflows resume-wait --workflow order-processing --diagnose", "gregale workflows resume-wait --run RUN_ID"}},
			{Name: "list", Short: "List workflow runs for an app", Examples: []string{"gregale workflows list --workflow-name nightly-report --all", "gregale workflows list --app billing --workflow-name paid-invoice --status failed", "gregale workflows list --app billing --created-after 2026-10-01T00:00:00Z --created-before 2026-10-05T23:59:59Z"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug (defaults to linked project app)", Value: "SLUG"},
				{Name: "limit", Short: "page size (1..100)", Value: "N"},
				{Name: "offset", Short: "starting page offset", Value: "N"},
				{Name: "all", Short: "walk all matching pages from the selected offset", Bool: true},
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
			{Name: "run", Short: "Trigger a new workflow run", Positionals: []string{"<workflow-name>"}, Examples: []string{"gregale workflows run nightly-report --input-file input.json --wait", "gregale workflows run nightly-report --input-file input.json", "gregale workflows run nightly-report --input-file -"}, Flags: []cliFlag{{Name: "wait", Short: "follow the accepted run to success without resubmitting", Bool: true}, {Name: "timeout", Short: "wait deadline after acceptance (default 5m; requires --wait)", Value: "DURATION"}, {Name: "poll-interval", Short: "wait read interval (default 2s; 1s to 1h; requires --wait)", Value: "DURATION"}, {Name: "app", Short: "app slug (defaults to linked project app)", Value: "slug"}, {Name: "input", Short: "JSON input payload (default {}; excludes --input-file)", Value: "JSON"}, {Name: "input-file", Short: "read one JSON value from a file, or - for stdin; maximum 1 MiB; excludes --input", Value: "PATH|-"}, {Name: "idempotency-key", Short: "stable key for retrying an uncertain run start", Value: "KEY"}}},
			{Name: "wait", Short: "Wait for an exact or latest workflow run to succeed; failure or timeout exits nonzero", Positionals: []string{"<run_id|latest>"}, Examples: []string{"gregale workflows wait RUN_ID --timeout 5m", "gregale workflows wait latest --workflow-name billing --timeout 5m"}, Flags: []cliFlag{
				{Name: "app", Value: "SLUG", Short: "app for latest (defaults to linked project app)"},
				{Name: "workflow-name", Value: "NAME", Short: "exact workflow name required for latest"},
				{Name: "timeout", Short: "maximum wait (default 5m; positive)", Value: "DURATION"},
				{Name: "poll-interval", Short: "read interval (default 2s; 1s to 1h)", Value: "DURATION"},
			}},
			{Name: "status", Short: "Show details of a workflow run", Positionals: []string{"[<run_id|latest>]"}, Examples: []string{"gregale workflows status RUN_ID --check", "gregale workflows status latest --workflow-name nightly-report", "gregale workflows status --interactive", "gregale workflows status --app billing --interactive", "gregale workflows status <run_id>"}, Flags: []cliFlag{{Name: "check", Bool: true, Short: "exit 0 only for a succeeded, non-cancelled run (explicit read only)"}, {Name: "interactive", Bool: true, Short: "choose a run for inspection"}, {Name: "app", Value: "slug", Short: "app for latest or interactive selection (defaults to linked project app)"}, {Name: "workflow-name", Short: "exact workflow name required for latest", Value: "NAME"}}},
			{Name: "diagnose", Short: "Inspect queue reasons and preview recovery without changing the run", Positionals: []string{"<run_id|latest>"}, Examples: []string{"gregale workflows diagnose latest --workflow-name nightly-report", "gregale workflows diagnose <run_id>", "gregale --json workflows diagnose <run_id>"}, Flags: []cliFlag{{Name: "app", Short: "app slug for latest (defaults to linked project app)", Value: "SLUG"}, {Name: "workflow-name", Short: "exact workflow name required for latest", Value: "NAME"}}},
			{Name: "steps", Short: "List steps for an exact or latest workflow run", Positionals: []string{"<run_id|latest>"}, Examples: []string{"gregale workflows steps latest --workflow-name billing --failed-only", "gregale workflows steps latest --workflow-name nightly-report"}, Flags: []cliFlag{{Name: "failed-only", Bool: true, Short: "show only failed steps with inspection commands"}, {Name: "app", Short: "app slug for latest (defaults to linked project app)", Value: "SLUG"}, {Name: "workflow-name", Short: "exact workflow name required for latest", Value: "NAME"}}},
			{Name: "attempts", Short: "List retry attempts and managed effect delivery status for a workflow step", Positionals: []string{"<run_id|latest>", "<step_name>"}, Examples: []string{"gregale workflows attempts latest charge-card --workflow-name billing --failed-only", "gregale workflows attempts latest charge-card --workflow-name billing"}, Flags: []cliFlag{{Name: "failed-only", Bool: true, Short: "show only failed attempts"}, {Name: "app", Value: "SLUG", Short: "app for latest (defaults to linked project app)"}, {Name: "workflow-name", Value: "NAME", Short: "exact workflow name required for latest"}}},
			{Name: "retry", Short: "Retry one safely resumable failed HTTP step", Positionals: []string{"[<run_id>]", "[<step_name>]"}, Examples: []string{"gregale workflows retry --app billing --interactive", "gregale workflows retry <run_id> <step_name>", "gregale workflows retry RUN_ID STEP_NAME --wait"}, Flags: append(workflowInteractiveFlags(), cliFlag{Name: "wait", Short: "follow the retried run to success (explicit run and step only)", Bool: true}, cliFlag{Name: "timeout", Short: "wait deadline after retry acceptance (default 5m; requires --wait)", Value: "DURATION"}, cliFlag{Name: "poll-interval", Short: "wait read interval (default 2s; 1s to 1h; requires --wait)", Value: "DURATION"})},
			{Name: "resume", Short: "Resume eligible failed actions in a workflow run", Positionals: []string{"<run_id>"}, Examples: []string{"gregale workflows resume RUN_ID --expected-resume-count 1 --wait"}, Flags: []cliFlag{{Name: "wait", Short: "follow the resumed run to success without another resume request", Bool: true}, {Name: "timeout", Short: "wait deadline after resume acceptance (default 5m; requires --wait)", Value: "DURATION"}, {Name: "poll-interval", Short: "wait read interval (default 2s; 1s to 1h; requires --wait)", Value: "DURATION"}, {Name: "expected-resume-count", Short: "current resume_count shown by workflows status", Req: true, Value: "N"}, {Name: "idempotency-key", Short: "stable key for retrying the same resume request", Value: "KEY"}}},
			{Name: "resumes", Short: "List continuation history for an exact or latest workflow run", Positionals: []string{"<run_id|latest>"}, Examples: []string{"gregale workflows resumes latest --workflow-name billing"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "app for latest (defaults to linked project app)"}, {Name: "workflow-name", Value: "NAME", Short: "exact workflow name required for latest"}}},
			{Name: "cancel", Short: "Cancel an active workflow run", Positionals: []string{"[<run_id>]"}, Examples: []string{"gregale workflows cancel --app billing --interactive", "gregale workflows cancel <run_id>"}, Flags: workflowInteractiveFlags()},
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
			{Name: "events", Short: "Send external event to a workflow run", Positionals: []string{"<run_id>", "<event_name>"}, Examples: []string{"gregale workflows events RUN_ID approved --payload-file approval.json", "gregale workflows events RUN_ID approved --payload-file -"}, Flags: []cliFlag{{Name: "payload", Short: "JSON event payload (default {}; excludes --payload-file)", Value: "JSON"}, {Name: "payload-file", Short: "read one JSON value from a file, or - for stdin; request maximum 1 MiB; excludes --payload", Value: "PATH|-"}}},
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
			{Name: "get", Short: "Show one delayed task", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "app", Short: "require the task to belong to this app", Value: "SLUG"}}},
			{Name: "info", Short: "Alias for get", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "app", Short: "require the task to belong to this app", Value: "SLUG"}}},
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
			{Name: "failed-only", Bool: true, Short: "show only failed deployments in fetched history"},
			{Name: "limit", Short: "page size (1-200)", Value: "N"},
			{Name: "before", Short: "alias for --cursor", Value: "CURSOR"},
			{Name: "cursor", Short: "opaque cursor from a prior page", Value: "CURSOR"},
			{Name: "all", Short: "walk every page"},
			{Name: "wide", Short: "include the revision (vN) and annotation columns (by / pr / tag / reason)"},
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
			"gregale deployment latest --app my-api",
		},
		Subcommands: []cliSub{
			{Name: "retry-hold", Positionals: []string{"<ID|vN>"}, Short: "Schedule an MCP promotion hold retry with all activation checks", Examples: []string{"gregale deployment retry-hold v42 --app my-api"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "app slug, to resolve a vN revision"}}},
			{Name: "runtime", Positionals: []string{"<ID|vN>"}, Short: "Inspect runtime identity or preview a published runtime change", Examples: []string{"gregale deployment runtime v42 --app my-function", "gregale deployment runtime v42 --app my-function --target RELEASE_ID --json"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "app slug for a vN revision"}, {Name: "target", Value: "RELEASE_ID", Short: "published runtime release to preview without applying"}}},
			{Name: "advance", Positionals: []string{"<ID|vN>"}, Short: "Advance a canary by one stage with route enforcement", Examples: []string{"gregale deployment advance DEPLOYMENT_UUID --expected-step 1", "gregale deployment advance v42 --app my-api --expected-step 1"}, Flags: []cliFlag{{Name: "expected-step", Value: "N", Short: "observed current canary step (see deployment summary)", Req: true}, {Name: "app", Value: "SLUG", Short: "app slug, to resolve a vN revision"}}},
			{Name: "summary", Short: "Show the release diff and rollback target", Examples: []string{"gregale deployment summary --app my-api --interactive", "gregale deployment summary v42 --app my-api", "gregale deployment summary v42 --app my-api --json"}, Positionals: []string{"[<id|vN>]"}, Flags: []cliFlag{
				{Name: "interactive", Short: "choose a release from app history and show its summary", Bool: true},
				{Name: "app", Short: "app slug (defaults to linked context or interactive picker)", Value: "SLUG"},
			}},
			{Name: "wait", Short: "Wait until a deployment is live (or safe rollout completes)", Examples: []string{"gregale deployment wait <id|vN> --app my-api", "gregale deployment wait 00000000000000000000000000000001", "gregale deployment wait 00000000000000000000000000000001 --rollout --progress"}, Positionals: []string{"<id|vN|latest>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug for vN or latest outside a linked project", Value: "SLUG"},
				{Name: "rollout", Short: "wait for safe rollout to reach 100% traffic"},
				{Name: "progress", Short: "print rollout transitions while waiting (human output only)"},
				{Name: "timeout", Short: "maximum wait (seconds, or a duration such as 10m)", Value: "SECONDS|DURATION"},
			}},
			{Name: "set-min-instances", Short: "Set the per-deployment cold-wake floor", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "min", Short: "minimum warm instances for this deployment", Req: true, Value: "N"}}},
		},
		Positionals: []string{"<id|vN|latest>"},
		Flags: []cliFlag{
			{Name: "app", Short: "app slug for vN or latest outside a linked project", Value: "SLUG"},
			{Name: "show-scan", Short: "include the per-deploy grype scan payload"},
			{Name: "show-secret-scan", Short: "include the per-deploy image-layer secret-scan payload"},
			{Name: "min", Short: "min_instances floor (>= 0)", Value: "N"},
		},
	},
	{
		Name:    dispatchDeploys,
		DocSlug: "deploys",
		Short:   "Deployment drill-downs (deploys show|status|logs|cancel|reorder|clear|clear-obsolete|retry)",
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
			{Name: "logs", Short: "Print one deployment's build and deploy log", Examples: []string{"gregale deploys logs v42 --app my-api"}, Positionals: []string{"<id|vN>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug; only needed to resolve a vN revision outside a linked project", Value: "SLUG"},
				{Name: "limit", Short: "most recent log lines to print (1-5000)", Value: "N"},
			}},
			{Name: "cancel", Short: "Cancel one pending deployment", Positionals: []string{"<id>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug (defaults to the linked project, or the deployment's own app)", Value: "slug"},
				{Name: "reason", Short: "cancel reason (default user)", Value: "REASON", ClosedSet: []string{"user", "auto_quota", "auto_health", "system"}},
			}},
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
		Name:        "deploy",
		DocSlug:     "deploy",
		Short:       "Deploy an app, function, or project",
		CompactHelp: true,
		HelpNotes: []string{
			"Source defaults to committed HEAD when origin exists; otherwise local files. Use --source=worktree to include local changes.",
			resourceProfileHelpNote,
		},
		Examples: []string{"gregale deploy --plan", "gregale deploy --source=head --name my-api", "gregale deploy --path packages/api --source=worktree"},
		Flags: []cliFlag{
			{Name: "image", Group: "Source and build", Short: "deploy a digest-pinned container image", Value: "REF"},
			{Name: "tarball", Group: "Source and build", Short: "deploy from a source tarball", Value: "PATH"},
			{Name: "path", Group: "Source and build", Common: true, Short: "source directory (default: current directory)", Value: "DIR"},
			{Name: "source", Group: "Source and build", Common: true, Short: "local source policy (default: auto)", Value: "auto|head|worktree", ClosedSet: []string{"auto", "head", "worktree"}},
			{Name: "worktree", Group: "Source and build", Short: "deploy the selected source directory from the working tree, including local changes"},
			{Name: "repo", Group: "GitHub", Short: "deploy a GitHub repository (requires --ref)", Value: "OWNER/NAME"},
			{Name: "repository", Group: "GitHub", Short: "bind a project to GitHub (requires --install-id)", Value: "OWNER/NAME"},
			{Name: "install-id", Group: "GitHub", Short: "GitHub installation ID (requires --repository)", Value: "N"},
			{Name: "production-branch", Group: "GitHub", Short: "production branch for a project binding (default: main)", Value: "BRANCH"},
			// Issue #739 / ADR-092: --ref pairs with --repo to
			// drive the headless source-ref deploy (CI-friendly,
			// no install-token env). Required when --repo is set.
			{Name: "ref", Group: "GitHub", Short: "branch, tag, or 40-character SHA (required with --repo)", Value: "REF"},
			{Name: "source-branch", Group: "GitHub", Short: "reject promotion if the branch for a pinned --ref moves", Value: "BRANCH"},
			// Issue #270: --github emits a copy-paste Actions workflow
			// snippet; --pin-action can resolve the public Action tag.
			// The snippet uses --name / cwd as the app slug.
			{Name: "github", Group: "GitHub", Short: "emit a GitHub Actions workflow snippet for the Gregale deploy action"},
			{Name: "pinned-sha", Group: "GitHub", Short: "with --github only, pin the generated Action to this full 40-character commit SHA", Value: "SHA"},
			{Name: "pin-action", Group: "GitHub", Short: "with --github only, resolve the current v0 Action tag to its commit SHA"},
			{Name: "template", Group: "Source and build", Short: "scaffold from a built-in template", Value: "NAME", ClosedSet: templateNames13},
			{Name: "dockerfile", Group: "Source and build", Short: "build with the supplied Dockerfile inside --tarball"},
			{Name: "issue-source-maps", Group: "Source and build", Short: "publish a portable manifest from the selected source; requires zero production traffic", Value: "PATH"},
			{Name: "issue-source-map-directory", Group: "Source and build", Short: "deployed bundle directory corresponding to the manifest directory", Value: "PATH"},
			{Name: "runtime", Group: "Source and build", Short: "function runtime", Value: "RUNTIME", ClosedSet: []string{"node22", "python312", "go124", "go124-alpine", "node24", "python313", "binary"}},
			{Name: "handler", Group: "Source and build", Short: "function handler", Value: "HANDLER"},
			{Name: "name", Group: "App and resources", Common: true, Short: "app slug (default: source directory name)", Value: "SLUG"},
			{Name: "profile", Group: "App and resources", Short: "RAM/CPU size for one app; excludes --repo and project deploys", Value: "SIZE", ClosedSet: []string{"micro", "small", "medium", "large", "xlarge"}},
			{Name: "vcpu", Group: "App and resources", Short: "assert the plan guest vCPU shape (omit to use the plan default)", Value: "N"},
			{Name: "execution-mode", Group: "App and resources", Short: "app lifecycle mode", Value: "MODE", ClosedSet: []string{"request", "service", "worker", "job"}},
			{Name: "restart-policy", Group: "App and resources", Short: "app restart policy", Value: "POLICY", ClosedSet: []string{"no", "on-failure", "always", "unless-stopped"}},
			{Name: "startup-deadline-s", Group: "App and resources", Short: "maximum startup seconds (0 uses plan default)", Value: "SECONDS"},
			{Name: "max-retries", Group: "App and resources", Short: "maximum restart attempts (0 uses plan default)", Value: "N"},
			{Name: "function", Group: "Source and build", Short: "deploy as a function; skip shape auto-detection"},
			{Name: "app", Group: "Source and build", Short: "deploy as an app; skip shape auto-detection"},
			{Name: "only", Group: "Projects", Short: "workloads to apply; retain unselected project workloads (comma-separated)", Value: "SLUGS"},
			{Name: "project", Group: "Projects", Short: "deploy all detected workloads as one project (slug defaults from --name or source)"},
			{Name: "environment", Group: "Projects", Short: "deploy to a registered project environment (defaults to linked context)", Value: "SLUG"},
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
			{Name: "reason", Group: "Annotations", Short: "free-text deploy reason (≤280 chars)", Value: "text"},
			{Name: "tag", Group: "Annotations", Short: "deployment annotation tag", Value: "TAG", ClosedSet: DeploymentAnnotationTags},
			{Name: "deployed-by", Group: "Annotations", Short: "operator label (auto-resolved from git config user.name)", Value: "NAME"},
			{Name: "pr-number", Group: "Annotations", Short: "GitHub PR number (positive int; 0 = absent). CI paths stamp via the GitHub Action.", Value: "N"},
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
			{Name: "exclude", Group: "Projects", Short: "omit workloads (comma-separated slugs; cannot combine with --only)", Value: "SLUGS"},
			{Name: "show-affected", Group: "Projects", Short: "show workloads that deploy, stay unchanged, or are removed"},
			// ADR-124 follow-up #3 (PR-B commit 5): write-side
			// complement to --exclude. Records excluded slugs into
			// deployment_scope_exclusions on a successful apply so
			// subsequent deploys honor the persisted set automatically.
			{Name: "persist-exclude", Group: "Projects", Short: "save --exclude slugs for future project deploys"},
			{Name: "project-slug", Group: "Projects", Short: "kebab slug for the project (one-key provision)", Value: "SLUG"},
			{Name: "rollout", Group: "Rollout and health", Short: "how this release reaches traffic: safe (adaptive canary with rollback; waits for 100%), fast (5% -> 50% -> 100%) or immediate; default: the app release policy (ADR-1450)", Value: "MODE", ClosedSet: []string{"safe", "fast", "immediate"}},
			{Name: "canary-preset", Group: "Rollout and health", Short: "deprecated: use --rollout. Canary ladder preset", Value: "PRESET", ClosedSet: []string{"none", "slow", "balanced", "aggressive", "1-10-50-100", "adaptive", "custom"}},
			{Name: "canary-stages", Group: "Rollout and health", Short: "deprecated: use --rollout; custom percent@duration canary stages (requires --canary-preset custom)", Value: "STAGES"},
			{Name: "safe", Group: "Rollout and health", Short: "deprecated: use --rollout safe"},
			{Name: "require-authn", Group: "Access and secrets", Short: "require bearer auth on every request"},
			{Name: "no-require-authn", Group: "Access and secrets", Short: "drop the token requirement"},
			{Name: "platform-tenant-required", Group: "Access and secrets", Short: "require verified customer identity on selected apps (Hobby and above)"},
			{Name: "no-platform-tenant-required", Group: "Access and secrets", Short: "allow selected apps to receive traffic without customer identity"},
			{Name: "app-protocol", Group: "Rollout and health", Short: "wire protocol selector", Value: "PROTOCOL", ClosedSet: []string{"http1", "http2", "grpc"}},
			{Name: "traffic-percent", Group: "Rollout and health", Short: "deployment traffic split weight (0-100)", Value: "PERCENT"},
			{Name: "no-traffic", Group: "Rollout and health", Short: "stage with 0% production traffic and print the preview URL"},
			{Name: "rollback-on-5xx", Group: "Rollout and health", Short: "roll back after repeated first-wake 5xx responses"},
			{Name: "queue-canary-binding", Group: "Rollout and health", Short: "declare safe queue canary support for this deployment", Value: "NAME", Repeatable: true},
			{Name: "healthcheck-path", Group: "Rollout and health", Short: "startup HTTP readiness path", Value: "PATH"},
			{Name: "healthcheck-grpc", Group: "Rollout and health", Short: "use standard gRPC health for startup readiness"},
			{Name: "healthcheck-grpc-service", Group: "Rollout and health", Short: "service name (requires --healthcheck-grpc; default: overall server)", Value: "SERVICE"},
			{Name: "disable-startup-cpu-boost", Group: "Rollout and health", Short: "disable temporary CPU boost during VM startup"},
			{Name: "mcp-task-migration-override", Group: "Rollout and health", Short: "allow this deployment to replace incompatible MCP task workers once"},
			{Name: "no-triggers", Group: "Projects", Short: "skip gregale.yaml trigger and async-route changes"},
			{Name: "wait", Group: "Confirmation and waiting", Short: "wait for deployment to become live (default)"},
			{Name: "no-wait", Group: "Confirmation and waiting", Short: "return after deployment is queued"},
			{Name: "create-only", Group: "Confirmation and waiting", Short: "create or reserve the app without uploading a deployment"},
			{Name: "timeout", Group: "Confirmation and waiting", Short: "readiness wait limit; seconds or duration, e.g. 10m (default: 1200s; maximum: 24h)", Value: "DURATION"},
			{Name: "idempotency-key", Group: "Confirmation and waiting", Short: "stable logical retry key for this deployment", Value: "KEY"},
			{Name: "secrets-file", Group: "Access and secrets", Short: "seal KEY=VALUE pairs before the first deployment", Value: "PATH"},
			{Name: "secret-scan", Group: "Access and secrets", Short: "scan .env files before packing (default: on)", Value: "on|off", ClosedSet: []string{"on", "off"}},
			{Name: "diff", Group: "Preview and checks", Short: "alias of --dry-run"},
			{Name: "plan", Group: "Preview and checks", Common: true, Short: "preview local app defaults; no login needed"},
			{Name: "dry-run", Group: "Preview and checks", Common: true, Short: "check against remote state without deploying"},
			{Name: "yes", Group: "Confirmation and waiting", Common: true, Short: "skip the apply confirmation prompt"},
			{Name: "strict", Group: "Preview and checks", Short: "fail on schema/quota/env breaks (default with --dry-run, --diff, or --server-diff)"},
			{Name: "lenient", Group: "Preview and checks", Short: "allow diff breaks; requires --dry-run, --diff, or --server-diff; excludes --strict"},
			{Name: "server-diff", Group: "Preview and checks", Short: "preview changes using the server diff engine"},
			{Name: "doctor-strict", Group: "Preview and checks", Short: "run doctor before deploy and abort on errors"},
			{Name: "no-doctor", Group: "Preview and checks", Short: "skip the automatic local doctor preflight"},
		},
	},
	{
		Name:    "domains",
		DocSlug: "domains",
		Short:   "Manage custom domains",
		Subcommands: []cliSub{
			{Name: "setup", Short: "Guide DNS setup, wait for verification and TLS, and offer a default-domain change", Positionals: []string{"<domain>"}, Examples: []string{"gregale domains setup api.example.com --app my-api"}, Flags: []cliFlag{
				{Name: "app", Short: "app to attach to", Value: "SLUG", Req: true},
				{Name: "environment", Short: "project environment to route to", Value: "SLUG"},
				{Name: "timeout", Short: "verification wait deadline (default 10m, maximum 1h)", Value: "DURATION"},
				{Name: "poll-interval", Short: "verification interval (5s..1m, default 10s)", Value: "DURATION"},
			}},
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
		Examples: []string{"gregale dev --once", "gregale dev --path ./api --once", "gregale dev --all"},
		Flags: []cliFlag{
			{Name: "path", Short: "source directory", Value: "DIR"},
			{Name: "all", Short: "run one developer loop per deployable workspace app"},
			{Name: "name", Short: "developer-session project name", Value: "PROJECT"},
			{Name: "env-file", Short: "sync KEY=VALUE entries as developer secrets", Value: "PATH"},
			{Name: "service-override-file", Short: "sync validated service URLs as developer secrets", Value: "PATH"},
			{Name: "once", Short: "deploy once and exit"},
			{Name: "stop", Short: "tear down the developer environment"},
			{Name: "no-logs", Short: "do not attach the live runtime log stream"},
			{Name: "open", Short: "open the developer environment URL after the first live sync"},
			{Name: "bucket", Short: "provision a private workspace bucket; repeatable", Value: "NAME"},
			{Name: "postgres", Short: "provision an isolated PostgreSQL database and inject DATABASE_URL"},
			{Name: "postgres-region", Short: "choose managed database placement", Value: "REGION"},
			{Name: "postgres-seed", Short: "shell command run once in the developer app after its database is ready", Value: "CMD"},
			{Name: "reseed", Short: "run the seed again even if this database was already seeded"},
			{Name: "debug", Short: "start the Node.js inspector and expose it on a local port"},
			{Name: "debug-port", Short: "local port for --debug (default 9229)", Value: "PORT"},
			{Name: "ttl", Short: "environment lease after the latest sync (default 24h; plan maximum applies)", Value: "DURATION"},
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
			{Name: "info", Short: "show the developer environment URL, app slug, lease, and database without renewing it", Flags: []cliFlag{
				{Name: "path", Short: "source directory", Value: "DIR"},
				{Name: "name", Short: "developer-session project name", Value: "PROJECT"},
			}},
			{Name: "trigger", Short: "invoke the developer app, fire a declared cron route, or schedule a delayed task", Examples: []string{
				"gregale dev trigger invoke --method POST --path /orders --payload @order.json",
				"gregale dev trigger cron /jobs/nightly",
				"gregale dev trigger delayed-task --delay 5m --path /reminders",
			}, Flags: []cliFlag{
				{Name: "path", Short: "source directory (before the verb)", Value: "DIR"},
				{Name: "name", Short: "developer-session project name (before the verb)", Value: "PROJECT"},
			}, Subcommands: []cliSub{
				{Name: "invoke", Short: "run `gregale invoke` against the developer app"},
				{Name: "cron", Short: "POST to a cron route declared for this app in gregale.yaml", Positionals: []string{"[ROUTE]"}},
				{Name: "delayed-task", Short: "run `gregale delayed-task add` against the developer app"},
			}},
			{Name: "setup", Short: "preflight a project and prepare the first developer environment", Flags: []cliFlag{
				{Name: "path", Short: "source directory", Value: "DIR"},
				{Name: "name", Short: "developer-session project name", Value: "PROJECT"},
				{Name: "env-file", Short: "validate and sync developer secrets", Value: "PATH"},
				{Name: "service-override-file", Short: "validate and sync service URLs", Value: "PATH"},
				{Name: "start", Short: "start after preflight"},
				{Name: "save", Short: "save portable developer defaults in gregale.yaml"},
				{Name: "dry-run", Short: "preview saved defaults; requires --save"},
				{Name: "once", Short: "sync once and exit"},
				{Name: "no-logs", Short: "do not attach runtime logs"},
				{Name: "open", Short: "open the verified URL"},
				{Name: "bucket", Short: "provision a private workspace bucket; repeatable", Value: "NAME"},
				{Name: "postgres", Short: "provision an isolated PostgreSQL database"},
				{Name: "postgres-region", Short: "choose managed database placement", Value: "REGION"},
				{Name: "ttl", Short: "environment lease after the latest sync", Value: "DURATION"},
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
		Subcommands: []cliSub{{Name: "rehearse", Short: "Compare two pinned Git revisions with one trusted behavior scenario", Examples: []string{"gregale test rehearse --base main --candidate HEAD --scenario customer-export --engine simulated --report rehearsal.json --html rehearsal.html", "gregale test rehearse --base main --candidate HEAD --scenario customer-export --profile restored --max-workload-minutes 90 --report rehearsal.json"}, Flags: []cliFlag{
			{Name: "repo", Short: "local Git checkout containing both revisions and trusted inputs", Value: "DIR"},
			{Name: "base", Short: "baseline revision resolved once to a commit", Value: "REF", Req: true},
			{Name: "candidate", Short: "candidate revision resolved once to a commit", Value: "REF", Req: true},
			{Name: "manifest", Short: "trusted manifest from the invoking checkout", Value: "PATH"},
			{Name: "scenario", Short: "scenario with declared behavior observations", Value: "NAME", Req: true},
			{Name: "engine", Short: "execution engine (default real-vm)", Value: "ENGINE", ClosedSet: []string{"real-vm", "local", "simulated"}},
			{Name: "profile", Short: "real-vm lifecycle profile (default warm)", Value: "PROFILE", ClosedSet: []string{"warm", "cold", "restored", "all"}},
			{Name: "repeat", Short: "fresh runs per revision and profile (1..20; default 1)", Value: "N"},
			{Name: "max-workload-minutes", Short: "required total real-vm workload-minute limit across both revisions", Value: "N"},
			{Name: "validate", Short: "validate both snapshots without execution or platform access"},
			{Name: "report", Short: "required combined JSON receipt for execution; new file", Value: "PATH"},
			{Name: "html", Short: "standalone comparison HTML; new file", Value: "PATH"},
			{Name: "markdown", Short: "comparison Markdown; new file", Value: "PATH"},
		}}, {Name: "init", Short: "Create public GET smoke checks from a local OpenAPI document", Examples: []string{"gregale test init --from openapi.yaml --project my-api --source ."}, Flags: []cliFlag{
			{Name: "from", Short: "local OpenAPI 3.0 or 3.1 document", Value: "PATH", Req: true},
			{Name: "project", Short: "Gregale project slug", Value: "SLUG", Req: true},
			{Name: "source", Short: "application source directory", Value: "DIR"},
			{Name: "scenario", Short: "scenario name", Value: "NAME"},
			{Name: "output", Short: "new manifest path", Value: "PATH"},
		}}, {Name: "recovery-trend", Short: "Render offline recovery performance trends from ordered campaign reports", Flags: []cliFlag{
			{Name: "manifest", Short: "campaign scenario manifest", Value: "PATH"},
			{Name: "scenario", Short: "campaign scenario name", Value: "NAME", Req: true},
			{Name: "config", Short: "ordered reports and recovery tolerances YAML", Value: "PATH", Req: true},
			{Name: "html", Short: "standalone HTML recovery trend output", Value: "PATH", Req: true},
		}}, {Name: "coverage-trend", Short: "Render offline campaign coverage trends from ordered reports", Flags: []cliFlag{
			{Name: "manifest", Short: "campaign scenario manifest", Value: "PATH"},
			{Name: "scenario", Short: "campaign scenario name", Value: "NAME", Req: true},
			{Name: "config", Short: "ordered report inputs and tolerances YAML", Value: "PATH", Req: true},
			{Name: "html", Short: "standalone HTML trend output", Value: "PATH", Req: true},
		}}, {Name: "replay", Short: "Replay a real-VM failure or passing recovery/duration configuration or sweep run from a saved report", Examples: []string{"gregale test replay --from failed.json --run-id RUN_ID --manifest gregale-test.yaml --validate", "gregale test replay --from failed.json --run-id RUN_ID --manifest gregale-test.yaml --report replay.json"}, Flags: []cliFlag{
			{Name: "from", Short: "saved JSON report containing the failed run", Value: "PATH", Req: true},
			{Name: "run-id", Short: "replayable isolated run ID", Value: "ID", Req: true},
			{Name: "manifest", Short: "original scenario manifest", Value: "PATH"},
			{Name: "validate", Short: "validate replay without provisioning"},
			{Name: "report", Short: "write replay JSON report", Value: "PATH"},
			{Name: "junit", Short: "write replay JUnit report", Value: "PATH"},
			{Name: "html", Short: "write replay HTML report", Value: "PATH"},
		}}, {Name: "minimize", Short: "Minimize faults, outage durations, stability observations, or recovery deadlines", Examples: []string{"gregale test minimize --from failed.json --run-id RUN_ID --assertion load/recovery --mode duration --duration-resolution 100ms --max-runs 20 --max-workload-minutes 180 --report duration.json", "gregale test minimize --from failed.json --run-id RUN_ID --assertion load/recovery --max-runs 10 --max-workload-minutes 180 --validate", "gregale test minimize --from failed.json --run-id RUN_ID --assertion load/recovery/checkout --max-runs 10 --max-workload-minutes 180 --report minimized.json --html minimized.html", "gregale test minimize --from failed.json --run-id RUN_ID --assertion load/recovery --mode stability --stability-resolution 100ms --max-runs 20 --max-workload-minutes 180 --report stability.json", "gregale test minimize --from failed.json --run-id RUN_ID --assertion load/recovery --mode recovery --max-recovery-within 10s --recovery-resolution 100ms --max-runs 20 --max-workload-minutes 180 --report deadline.json"}, Flags: []cliFlag{
			{Name: "from", Short: "saved JSON report containing the original failure", Value: "PATH", Req: true},
			{Name: "run-id", Short: "original replayable isolated run ID", Value: "ID", Req: true},
			{Name: "assertion", Short: "recovery assertion: STEP/PHASE or STEP/PHASE/REQUEST", Value: "TARGET", Req: true},
			{Name: "mode", Short: "minimize rules (default), latency, bandwidth, percent, recovery, stability, or duration", Value: "MODE", ClosedSet: []string{"rules", "latency", "bandwidth", "percent", "recovery", "stability", "duration"}},
			{Name: "rule-index", Short: "required original zero-based rule index in severity modes", Value: "N"},
			{Name: "resolution", Short: "latency search resolution (default 25ms; whole milliseconds)", Value: "DURATION"},
			{Name: "max-rate-kib-per-second", Short: "required bandwidth search ceiling (1..1000000 KiB/s)", Value: "N"},
			{Name: "rate-resolution-kib-per-second", Short: "bandwidth search resolution (default 1 KiB/s)", Value: "N"},
			{Name: "percent-resolution", Short: "percentage search resolution (default 1; 1..100 percentage points)", Value: "N"},
			{Name: "max-recovery-within", Short: "required recovery deadline ceiling fitting the existing phase", Value: "DURATION"},
			{Name: "duration-resolution", Short: "fault duration search resolution (default 100ms; whole milliseconds)", Value: "DURATION"},
			{Name: "stability-resolution", Short: "stability observation search resolution (default 25ms; whole milliseconds)", Value: "DURATION"},
			{Name: "recovery-resolution", Short: "recovery deadline search resolution (default 25ms; whole milliseconds)", Value: "DURATION"},
			{Name: "manifest", Short: "original scenario manifest", Value: "PATH"},
			{Name: "max-runs", Short: "maximum fresh runs including confirmations (2..20; default 10)", Value: "N"},
			{Name: "max-workload-minutes", Short: "required maximum estimated VM workload-minutes", Value: "N", Req: true},
			{Name: "validate", Short: "validate selection and budgets without provisioning"},
			{Name: "report", Short: "required JSON output for execution", Value: "PATH"},
			{Name: "junit", Short: "write minimization JUnit report", Value: "PATH"},
			{Name: "html", Short: "write minimization HTML report", Value: "PATH"},
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
			{Name: "sweep-baseline", Short: "compare resilience sweeps with a saved JSON report", Value: "PATH"},
			{Name: "recovery-baseline", Short: "compare resilience recovery with a saved successful JSON report", Value: "PATH"},
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
			"gregale chaos inject --scenario cache-resilience --target cache --tcp-port 6379 --bandwidth 64 --direction downstream",
			"gregale chaos inject --scenario customer-export --target inventory --error 503 --percent 10 --duration 5m",
			"gregale chaos inject --scenario customer-export --target payment --latency 1500ms --percent 20 --from worker --profile restored",
			"gregale chaos inject --scenario customer-export --target inventory --dns-nxdomain --percent 100",
			"gregale chaos inject --scenario cache-resilience --target cache --tcp-port 6379 --connect-timeout --duration 8s",
			"gregale chaos inject --scenario cache-resilience --target cache --tcp-port 6379 --connect-refused",
		}, Flags: []cliFlag{
			{Name: "scenario", Short: "scenario declared in gregale-test.yaml", Req: true, Value: "NAME"},
			{Name: "manifest", Short: "scenario manifest path", Value: "PATH"},
			{Name: "target", Short: "scenario service workload to affect", Req: true, Value: "SERVICE"},
			{Name: "from", Short: "only affect calls from this workload", Value: "SERVICE"},
			{Name: "latency", Short: "add this delay to selected requests, such as 1500ms", Value: "DURATION"},
			{Name: "error", Short: "return this synthetic HTTP 5xx status", Value: "CODE"},
			{Name: "tcp-port", Short: "target TCP port; selects TCP latency", Value: "PORT"},
			{Name: "direction", Short: "TCP traffic direction", Value: "DIRECTION", ClosedSet: []string{"upstream", "downstream", "both"}},
			{Name: "bandwidth", Short: "TCP throughput cap in KiB/s", Value: "N"},
			{Name: "timeout", Short: "stall TCP traffic until the lease expires"},
			{Name: "reset", Short: "reset selected TCP connections"},
			{Name: "reset-after", Short: "delay before a TCP reset", Value: "DURATION"},
			{Name: "connect-timeout", Short: "hold new service connections until timeout or fault clear"},
			{Name: "connect-refused", Short: "reset new service connections before reaching the target"},
			{Name: "dns-latency", Short: "delay private service-name DNS responses", Value: "DURATION"},
			{Name: "dns-timeout", Short: "drop private service-name DNS queries"},
			{Name: "dns-servfail", Short: "return SERVFAIL for private service-name DNS queries"},
			{Name: "dns-nxdomain", Short: "return NXDOMAIN for private service-name DNS queries"},
			{Name: "percent", Short: "fraction of matching requests, DNS queries, or TCP connections affected (1..100)", Value: "N"},
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
				{Name: "route-removal-mode", Short: "CLI removal gate: report (default) or enforce", Value: "MODE", ClosedSet: []string{"report", "enforce"}},
				{Name: "route-readiness", Short: "migration readiness JSON for the serving production deployment", Value: "PATH"},
				{Name: "route-mapping", Short: "reviewed successor mapping JSON", Value: "PATH"},
				{Name: "route-owner-approval", Short: "owner attestation bound to this exact change and evidence", Value: "PATH"},
				{Name: "route-evidence-max-age", Short: "maximum route evidence age (default and maximum 72h)", Value: "DURATION"},
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
		Name:    "consumers",
		DocSlug: "consumers",
		Short:   "Identify your API's consumers, issue their keys and meter their requests",
		Examples: []string{
			"gregale consumers create my-api --external-ref customer-42 --name \"Customer 42\"",
			"gregale consumers key-create my-api CONSUMER_ID --name production --scopes read,write",
			"gregale consumers rate-card-create my-api --currency EUR --price-millicents 25 --included-units 10000",
			"gregale consumers rate-card-create my-api --currency EUR --tier 10000:0 --tier 1000000:25 --tier inf:10",
			"gregale consumers rate-card-create my-api --currency EUR --price-millicents 25 --weight \"POST /generate=20\"",
			"gregale consumers completeness my-api CONSUMER_ID --month 2026-09",
			"gregale consumers statement-draft my-api CONSUMER_ID --month 2026-09",
			"gregale consumers statement-handoff my-api CONSUMER_ID STATEMENT_ID --invoice-id INV-1001",
			"gregale consumers plan-create my-api --name free --max-requests-per-minute 60 --max-units-per-month 1000 --alert-at 80,100",
			"gregale consumers set-plan my-api CONSUMER_ID --plan free",
		},
		Subcommands: []cliSub{
			{Name: "list", Short: "List an app's API consumers", Positionals: []string{"<slug>"}},
			{Name: "create", Short: "Register an API consumer", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "external-ref", Short: "stable consumer reference", Value: "REF", Req: true},
				{Name: "name", Short: "consumer display name", Value: "TEXT", Req: true},
				{Name: "trial-days", Short: "free trial from the next minute: usage is priced at zero for this many days", Value: "N"},
			}},
			{Name: "info", Short: "Show one API consumer", Positionals: []string{"<slug>", "<consumer-id>"}},
			{Name: "revoke", Short: "Revoke an API consumer; its keys stop authenticating", Positionals: []string{"<slug>", "<consumer-id>"}},
			{Name: "keys", Short: "List a consumer's API keys", Positionals: []string{"<slug>", "<consumer-id>"}},
			{Name: "key-create", Short: "Issue a consumer API key (secret shown once)", Positionals: []string{"<slug>", "<consumer-id>"}, Flags: []cliFlag{
				{Name: "name", Short: "key display name", Value: "TEXT", Req: true},
				{Name: "scopes", Short: "comma-separated scopes: read, write, admin, billing (default write); billing lets the customer read its own usage and invoices", Value: "LIST"},
				{Name: "expires", Short: "key expiry", Value: "RFC3339"},
			}},
			{Name: "key-revoke", Short: "Revoke one consumer API key", Positionals: []string{"<slug>", "<consumer-id>", "<key-id>"}},
			{Name: "usage", Short: "Show a consumer's metered requests", Positionals: []string{"<slug>", "<consumer-id>"}, Flags: []cliFlag{
				{Name: "since", Short: "window start", Value: "RFC3339"},
				{Name: "until", Short: "window end", Value: "RFC3339"},
			}},
			{Name: "quote", Short: "Estimate a consumer's charges with current rate cards", Positionals: []string{"<slug>", "<consumer-id>"}, Flags: []cliFlag{
				{Name: "since", Short: "window start", Value: "RFC3339"},
				{Name: "until", Short: "window end", Value: "RFC3339"},
			}},
			{Name: "completeness", Short: "Check billed usage against request telemetry before invoicing", Positionals: []string{"<slug>", "<consumer-id>"}, Flags: []cliFlag{
				{Name: "month", Short: "calendar month", Value: "YYYY-MM"},
				{Name: "period-start", Short: "period start (UTC minute)", Value: "RFC3339"},
				{Name: "period-end", Short: "exclusive period end (UTC minute)", Value: "RFC3339"},
			}},
			{Name: "rate-cards", Short: "List an app's per-request price versions", Positionals: []string{"<slug>"}},
			{Name: "rate-card-create", Short: "Add an immutable per-request price version", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "currency", Short: "ISO-4217 currency", Value: "CODE", Req: true},
				{Name: "price-millicents", Short: "price per request; 100000 = 1.00", Value: "N", Req: true},
				{Name: "included-units", Short: "free requests per consumer per UTC calendar month", Value: "N"},
				{Name: "tier", Short: "graduated step; repeat in order, last UP_TO is inf (replaces price and included units)", Value: "UP_TO:PRICE", Repeatable: true},
				{Name: "weight", Short: "count each request on a route as N units; unlisted routes count 1", Value: "METHOD /template=N", Repeatable: true},
				{Name: "plan", Short: "add the price to a consumer plan instead of the app default", Value: "NAME"},
				{Name: "effective-from", Short: "UTC minute the price starts (default next minute)", Value: "RFC3339"},
				{Name: "unit", Short: "what the price is per: request, compute_second or response_megabyte (rounded per consumer minute), or app:<meter> for units the app reports in Gregale-Usage; cards of different units price side by side", Value: "UNIT"},
				{Name: "tier-mode", Short: "how tiers price a month: graduated (each unit at its step) or volume (every unit at the step the month reaches)", Value: "MODE", ClosedSet: []string{"graduated", "volume"}},
				{Name: "base-fee-millicents", Short: "fixed charge per consumer per UTC month, billed by the month's statement", Value: "N"},
				{Name: "minimum-millicents", Short: "minimum usage charge per consumer per UTC month; the statement tops up the difference", Value: "N"},
				{Name: "prorate-monthly-fees", Short: "scale the fee and minimum in a consumer's first month to the share of it left after the consumer was created"},
			}, Examples: []string{"gregale consumers rate-card-create my-api --currency EUR --price-millicents 50 --unit compute_second", "gregale consumers rate-card-create my-api --currency EUR --price-millicents 0 --tier 1000:100 --tier inf:60 --tier-mode volume", "gregale consumers rate-card-create my-api --currency EUR --price-millicents 50 --included-units 100000 --base-fee-millicents 4900000", "gregale consumers rate-card-create my-api --currency EUR --price-millicents 1 --unit app:output_tokens"}},
			{Name: "statements", Short: "List a consumer's usage statements and revisions", Positionals: []string{"<slug>", "<consumer-id>"}},
			{Name: "statement-draft", Short: "Snapshot a period, or draft an adjustment for late usage", Positionals: []string{"<slug>", "<consumer-id>"}, Flags: []cliFlag{
				{Name: "month", Short: "calendar month", Value: "YYYY-MM"},
				{Name: "period-start", Short: "period start (UTC minute)", Value: "RFC3339"},
				{Name: "period-end", Short: "exclusive period end (UTC minute)", Value: "RFC3339"},
			}},
			{Name: "statement-show", Short: "Show one statement revision", Positionals: []string{"<slug>", "<consumer-id>", "<statement-id>"}},
			{Name: "statement-finalize", Short: "Freeze a fully priced draft revision", Positionals: []string{"<slug>", "<consumer-id>", "<statement-id>"}},
			{Name: "statement-handoff", Short: "Record your invoice reference for a finalized revision", Positionals: []string{"<slug>", "<consumer-id>", "<statement-id>"}, Flags: []cliFlag{
				{Name: "invoice-id", Short: "your billing system's invoice reference", Value: "ID", Req: true},
			}},
			{Name: "statement-export", Short: "Create an invoice in Stripe, Paddle or Lago for a finalized revision and record its handoff", Positionals: []string{"<slug>", "<consumer-id>", "<statement-id>"}, Flags: []cliFlag{
				{Name: "customer", Short: "provider customer: Stripe cus_…, Paddle ctm_… or Lago external ID (default: the consumer's external_ref when it is one)", Value: "ID"},
				{Name: "provider", Short: "billing provider (default stripe)", Value: "PROVIDER", ClosedSet: []string{"stripe", "paddle", "lago"}},
			}},
			{Name: "plans", Short: "List an app's consumer plans", Positionals: []string{"<slug>"}},
			{Name: "plan-create", Short: "Create a consumer plan with request limits", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "name", Short: "plan name (lowercase letters, digits, hyphens)", Value: "NAME", Req: true},
				{Name: "max-requests-per-minute", Short: "requests per consumer per minute; 0 is unlimited", Value: "N"},
				{Name: "max-units-per-month", Short: "weighted units per consumer per UTC month; 0 is unlimited", Value: "N"},
				{Name: "alert-at", Short: "percentages of the monthly limit that fire consumer.usage_threshold webhooks; none clears", Value: "80,100"},
			}},
			{Name: "plan-update", Short: "Change a plan's limits (applied within 15 seconds)", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "plan", Short: "plan name", Value: "NAME", Req: true},
				{Name: "max-requests-per-minute", Short: "requests per consumer per minute; 0 is unlimited", Value: "N"},
				{Name: "max-units-per-month", Short: "weighted units per consumer per UTC month; 0 is unlimited", Value: "N"},
				{Name: "alert-at", Short: "percentages of the monthly limit that fire consumer.usage_threshold webhooks; none clears", Value: "80,100"},
			}},
			{Name: "set-plan", Short: "Move a consumer onto a plan from a minute", Positionals: []string{"<slug>", "<consumer-id>"}, Flags: []cliFlag{
				{Name: "plan", Short: "plan name; use default for the app default plan", Value: "NAME", Req: true},
				{Name: "effective-from", Short: "UTC minute the plan takes effect (default next minute)", Value: "RFC3339"},
			}},
			{Name: "plan-history", Short: "List a consumer's plan assignments", Positionals: []string{"<slug>", "<consumer-id>"}},
			{Name: "usage-alerts", Short: "List the plan usage alerts a consumer crossed", Positionals: []string{"<slug>", "<consumer-id>"}},
			{Name: "credits", Short: "List a consumer's prepaid credit grants and usable balance", Positionals: []string{"<slug>", "<consumer-id>"}},
			{Name: "credit-grant", Short: "Grant prepaid credit that finalized statements draw down", Positionals: []string{"<slug>", "<consumer-id>"}, Flags: []cliFlag{
				{Name: "currency", Short: "ISO-4217 currency of the credit, e.g. EUR", Value: "CODE", Req: true},
				{Name: "amount-millicents", Short: "credit in millicents; 100000 = 1.00", Value: "N", Req: true},
				{Name: "expires", Short: "when unused credit expires, RFC3339", Value: "TIME"},
				{Name: "note", Short: "note shown with the credit", Value: "TEXT"},
			}, Examples: []string{"gregale consumers credit-grant my-api CONSUMER_ID --currency EUR --amount-millicents 5000000 --note \"Starter pack\""}},
			{Name: "credit-void", Short: "Void what is left of a credit grant", Positionals: []string{"<slug>", "<consumer-id>", "<credit-id>"}},
			{Name: "spend-cap", Short: "Show a consumer's monthly spend cap and whether it is reached", Positionals: []string{"<slug>", "<consumer-id>"}},
			{Name: "spend-cap-set", Short: "Refuse a consumer's requests once the month's amount due after credit passes a cap", Positionals: []string{"<slug>", "<consumer-id>"}, Flags: []cliFlag{
				{Name: "currency", Short: "ISO-4217 currency of the cap, e.g. EUR", Value: "CODE", Req: true},
				{Name: "monthly-cap-millicents", Short: "cap in millicents; 0 allows prepaid usage only", Value: "N", Req: true},
			}, Examples: []string{"gregale consumers spend-cap-set my-api CONSUMER_ID --currency EUR --monthly-cap-millicents 0"}},
			{Name: "spend-cap-delete", Short: "Remove a consumer's spend cap", Positionals: []string{"<slug>", "<consumer-id>"}},
			{Name: "trial", Short: "Show a consumer's free trial", Positionals: []string{"<slug>", "<consumer-id>"}},
			{Name: "trial-set", Short: "Start a consumer's one free trial at the next minute, or move the end of a running one", Positionals: []string{"<slug>", "<consumer-id>"}, Flags: []cliFlag{
				{Name: "days", Short: "trial length from its start", Value: "N"},
				{Name: "ends-at", Short: "trial end (UTC minute, not in the past)", Value: "RFC3339"},
			}, Examples: []string{"gregale consumers trial-set my-api CONSUMER_ID --days 14"}},
			{Name: "trial-end", Short: "End a consumer's running trial at the next minute", Positionals: []string{"<slug>", "<consumer-id>"}},
			{Name: "billing-policy", Short: "Show the app's overdue-invoice rule", Positionals: []string{"<slug>"}},
			{Name: "billing-policy-set", Short: "Refuse consumers whose exported invoice is unpaid some days after its due date", Positionals: []string{"<slug>"}, Flags: []cliFlag{
				{Name: "block-overdue-after-days", Short: "days past the due date before the gateway answers 402 (0 blocks at once)", Value: "N", Req: true},
			}, Examples: []string{"gregale consumers billing-policy-set my-api --block-overdue-after-days 14"}},
			{Name: "billing-policy-delete", Short: "Stop blocking consumers for overdue invoices; holds lift within minutes", Positionals: []string{"<slug>"}},
			{Name: "overdue-holds", Short: "List consumers the gateway refuses because of an overdue invoice", Positionals: []string{"<slug>"}},
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
			{Name: "rate-cards", Short: "List a customer's cross-app price versions", Flags: []cliFlag{platformTenantIDFlag}},
			{Name: "rate-card-create", Short: "Add a customer-wide per-request price version", Flags: []cliFlag{
				platformTenantIDFlag,
				{Name: "currency", Short: "ISO-4217 currency", Value: "CODE", Req: true},
				{Name: "price-millicents", Short: "price per request; 100000 = 1.00", Value: "N", Req: true},
				{Name: "included-units", Short: "free requests per tenant per UTC calendar month across all apps", Value: "N"},
				{Name: "tier", Short: "graduated step counted per tenant per UTC month; repeat in order, last UP_TO is inf (replaces price and included units)", Value: "UP_TO:PRICE", Repeatable: true},
				{Name: "weight", Short: "units per request on a route, including surface and JWT-rule requests; repeat per route, unlisted routes count 1 (statements then cover calendar months)", Value: "\"METHOD /template=N\"", Repeatable: true},
				{Name: "effective-from", Short: "UTC minute the price starts (default next minute)", Value: "RFC3339"},
				{Name: "unit", Short: "what the price is per: request, compute_second or response_megabyte (rounded per subject minute), or app:<meter> for units the app reports in Gregale-Usage; cards of different units price side by side", Value: "UNIT"},
				{Name: "base-fee-millicents", Short: "fixed charge per tenant per UTC month, billed by the month's statement", Value: "N"},
				{Name: "minimum-millicents", Short: "minimum usage charge per tenant per UTC month; the statement tops up the difference", Value: "N"},
				{Name: "prorate-monthly-fees", Short: "scale the fee and minimum in a tenant's first month to the share of it left after the tenant was created"},
				{Name: "tier-mode", Short: "how tiers price a month: graduated (each unit at its step) or volume (every unit at the step the month reaches)", Value: "MODE", ClosedSet: []string{"graduated", "volume"}},
			}},
			{Name: "statements", Short: "List a period's cross-app statement revisions", Flags: []cliFlag{
				platformTenantIDFlag,
				{Name: "month", Short: "calendar month", Value: "YYYY-MM"},
				{Name: "period-start", Short: "period start (UTC minute)", Value: "RFC3339"},
				{Name: "period-end", Short: "exclusive period end (UTC minute)", Value: "RFC3339"},
			}},
			{Name: "statement-draft", Short: "Snapshot a period, or draft an adjustment for late usage", Flags: []cliFlag{
				platformTenantIDFlag,
				{Name: "month", Short: "calendar month", Value: "YYYY-MM"},
				{Name: "period-start", Short: "period start (UTC minute)", Value: "RFC3339"},
				{Name: "period-end", Short: "exclusive period end (UTC minute)", Value: "RFC3339"},
			}},
			{Name: "statement-show", Short: "Show one statement revision", Flags: []cliFlag{platformTenantIDFlag, platformTenantStatementIDFlag}},
			{Name: "statement-finalize", Short: "Freeze a fully priced draft revision", Flags: []cliFlag{platformTenantIDFlag, platformTenantStatementIDFlag}},
			{Name: "statement-handoff", Short: "Record your invoice reference for a finalized revision", Flags: []cliFlag{
				platformTenantIDFlag, platformTenantStatementIDFlag,
				{Name: "invoice-id", Short: "your billing system's invoice reference", Value: "ID", Req: true},
			}},
			{Name: "statement-export", Short: "Create an invoice in Stripe, Paddle or Lago for a finalized revision and record its handoff", Flags: []cliFlag{
				platformTenantIDFlag, platformTenantStatementIDFlag,
				{Name: "customer", Short: "provider customer: Stripe cus_…, Paddle ctm_… or Lago external ID (default: the tenant's external_ref when it is one)", Value: "ID"},
				{Name: "provider", Short: "billing provider (default stripe)", Value: "PROVIDER", ClosedSet: []string{"stripe", "paddle", "lago"}},
			}},
			{Name: "plans", Short: "List tenant plans: reusable price and request-budget bundles"},
			{Name: "plan-create", Short: "Create a tenant plan", Flags: []cliFlag{
				{Name: "name", Short: "plan name (lowercase letters, digits, hyphens)", Value: "NAME", Req: true},
				{Name: "currency", Short: "ISO-4217 currency (default EUR)", Value: "CODE"},
				{Name: "price-millicents", Short: "price per request unit; 100000 = 1.00", Value: "N"},
				{Name: "included-units", Short: "free units per tenant per UTC calendar month", Value: "N"},
				{Name: "tier", Short: "graduated step; repeat in order, last UP_TO is inf", Value: "UP_TO:PRICE", Repeatable: true},
				{Name: "weight", Short: "units per request on a route; repeat per route", Value: "\"METHOD /template=N\"", Repeatable: true},
				{Name: "max-requests-per-minute", Short: "request budget per minute the plan sets (with --max-requests-per-day)", Value: "N"},
				{Name: "max-requests-per-day", Short: "request budget per day the plan sets (with --max-requests-per-minute)", Value: "N"},
				{Name: "unit", Short: "what the price is per: request, compute_second or response_megabyte (rounded per subject minute), or app:<meter> for units the app reports in Gregale-Usage; cards of different units price side by side", Value: "UNIT"},
				{Name: "base-fee-millicents", Short: "fixed charge per tenant per UTC month, billed by the month's statement", Value: "N"},
				{Name: "minimum-millicents", Short: "minimum usage charge per tenant per UTC month; the statement tops up the difference", Value: "N"},
				{Name: "prorate-monthly-fees", Short: "scale the fee and minimum in a tenant's first month to the share of it left after the tenant was created"},
				{Name: "tier-mode", Short: "how tiers price a month: graduated (each unit at its step) or volume (every unit at the step the month reaches)", Value: "MODE", ClosedSet: []string{"graduated", "volume"}},
			}, Examples: []string{"gregale platform-tenants plan-create --name growth --currency EUR --price-millicents 20 --weight \"POST /generate=5\" --max-requests-per-minute 600 --max-requests-per-day 100000"}},
			{Name: "plan-update", Short: "Change a tenant plan; flags not given keep their value, and tenants get it when the plan is assigned again", Flags: []cliFlag{
				{Name: "plan", Short: "plan to change", Value: "NAME", Req: true},
				{Name: "name", Short: "new plan name", Value: "NAME"},
				{Name: "currency", Short: "ISO-4217 currency", Value: "CODE"},
				{Name: "price-millicents", Short: "price per request unit; 100000 = 1.00", Value: "N"},
				{Name: "included-units", Short: "free units per tenant per UTC calendar month", Value: "N"},
				{Name: "tier", Short: "graduated step; repeat in order, last UP_TO is inf", Value: "UP_TO:PRICE", Repeatable: true},
				{Name: "weight", Short: "units per request on a route; repeat per route", Value: "\"METHOD /template=N\"", Repeatable: true},
				{Name: "max-requests-per-minute", Short: "request budget per minute (with --max-requests-per-day)", Value: "N"},
				{Name: "max-requests-per-day", Short: "request budget per day (with --max-requests-per-minute)", Value: "N"},
				{Name: "unit", Short: "what the price is per: request, compute_second or response_megabyte (rounded per subject minute), or app:<meter> for units the app reports in Gregale-Usage; cards of different units price side by side", Value: "UNIT"},
				{Name: "base-fee-millicents", Short: "fixed charge per tenant per UTC month, billed by the month's statement", Value: "N"},
				{Name: "minimum-millicents", Short: "minimum usage charge per tenant per UTC month; the statement tops up the difference", Value: "N"},
				{Name: "prorate-monthly-fees", Short: "scale the fee and minimum in a tenant's first month to the share of it left after the tenant was created"},
				{Name: "tier-mode", Short: "how tiers price a month: graduated (each unit at its step) or volume (every unit at the step the month reaches)", Value: "MODE", ClosedSet: []string{"graduated", "volume"}},
			}},
			{Name: "set-plan", Short: "Move a tenant onto a plan: its rate card from the next minute, free cards for its other priced units, and its request budget at once", Flags: []cliFlag{
				platformTenantIDFlag,
				{Name: "plan", Short: "tenant plan name", Value: "NAME", Req: true},
			}},
			{Name: "plan-history", Short: "List a tenant's plan assignments, newest first", Flags: []cliFlag{platformTenantIDFlag}},
			{Name: "trial", Short: "Show a tenant's free trial", Flags: []cliFlag{platformTenantIDFlag}},
			{Name: "trial-set", Short: "Start a tenant's one free trial at the next minute, or move the end of a running one", Flags: []cliFlag{
				platformTenantIDFlag,
				{Name: "days", Short: "trial length from its start", Value: "N"},
				{Name: "ends-at", Short: "trial end (UTC minute, not in the past)", Value: "RFC3339"},
			}, Examples: []string{"gregale platform-tenants trial-set --id TENANT_ID --days 30"}},
			{Name: "trial-end", Short: "End a tenant's running trial at the next minute", Flags: []cliFlag{platformTenantIDFlag}},
			{Name: "billing-policy", Short: "Show the account's overdue-invoice rule for tenants"},
			{Name: "billing-policy-set", Short: "Refuse tenants whose exported invoice is unpaid some days after its due date", Flags: []cliFlag{
				{Name: "block-overdue-after-days", Short: "days past the due date before the gateway answers 402 (0 blocks at once)", Value: "N", Req: true},
			}, Examples: []string{"gregale platform-tenants billing-policy-set --block-overdue-after-days 14"}},
			{Name: "billing-policy-delete", Short: "Stop blocking tenants for overdue invoices; holds lift within minutes"},
			{Name: "overdue-holds", Short: "List tenants the gateway refuses because of an overdue invoice"},
		},
	},
	{
		Name:    "edge-rule-lists",
		DocSlug: "edge-rules",
		Short:   "Reusable IP/country/host/string lists for edge-rule match conditions (edge-rule-lists list|get|create|update|rm)",
		Subcommands: []cliSub{
			{Name: subList, Short: "List the account's edge-rule lists"},
			{Name: subGet, Short: "Show one list with its items", Positionals: []string{"<name>"}},
			{Name: subCreate, Short: "Create a list", Positionals: []string{"<name>"}, Examples: []string{
				"gregale edge-rule-lists create office-ips --kind ip --item 203.0.113.0/24 --item 2001:db8::1",
				"gregale edge-rule-lists create blocked --kind country --items-file countries.txt",
				`gregale edge-rules create --app my-api --kind throttle --match-host api.example.com --throttle-requests-per-second 5 --match '{"not":{"field":"client_ip","op":"in_list","list":"office-ips"}}'`,
			}, Flags: []cliFlag{
				{Name: "kind", Short: "list kind", Value: "KIND", Req: true, ClosedSet: []string{"ip", "country", "host", "string", "asn"}},
				{Name: "item", Short: "list item (repeat)", Value: "VALUE"},
				{Name: "items-file", Short: "file with one item per line (# comments allowed), or - for stdin", Value: "path|-"},
				{Name: "description", Short: "free-text description", Value: "TEXT"},
			}},
			{Name: subUpdate, Short: "Edit a list; referencing rules pick up the change within seconds", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "add", Short: "item to add (repeat)", Value: "VALUE"},
				{Name: "remove", Short: "item to remove (repeat)", Value: "VALUE"},
				{Name: "replace-file", Short: "replace every item from a file, or - for stdin", Value: "path|-"},
				{Name: "description", Short: "new description", Value: "TEXT"},
			}},
			{Name: subRm, Short: "Delete a list no rule references", Positionals: []string{"<name>"}},
		},
	},
	{
		Name:    "edge-rules",
		DocSlug: "edge-rules",
		Short:   "Per-app edge rules (edge-rules list|summary|trace|create|get|update|history|rollback|stats|events --app <slug>; edge-rules rm <id>)",
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
				{Name: "asn", Short: "simulated client autonomous system for asn conditions (e.g. AS13335)", Value: "AS"},
				{Name: "verified-bot", Short: "simulate a client IP that passes crawler verification (verified_bot = the crawler the User-Agent claims)", Bool: true},
				{Name: "header", Short: "simulated request header; repeat for multiple values", Value: "Name:Value"},
				{Name: "body-file", Short: "request body file or - for stdin (max 1 MiB; contents are withheld)", Value: "path|-"},
				{Name: "proposal", Short: "compare against a proposed change: JSON {add,update,remove} file or - for stdin", Value: "file|-"},
				{Name: "add-rule", Short: "compare against adding this rule (create-request JSON or @file; repeat)", Value: "JSON|@FILE"},
				{Name: "remove-rule", Short: "compare against removing this rule id (repeat)", Value: "ID"},
			}, Examples: []string{
				`gregale edge-rules trace --app my-api --url https://api.example.com/admin --add-rule '{"kind":"maintenance","match_host":"api.example.com","match_path":"/admin/*","action":{"maintenance":{"message":"migrating"}}}'`,
			}},
			{Name: subCreate, Short: "Add an edge rule", Examples: []string{
				"gregale edge-rules create --app my-api --kind throttle --match-host my-api.gregale.dev --match-path /search --throttle-requests-per-second 5 --throttle-burst 10",
				"gregale edge-rules create --app my-api --kind throttle --match-host my-api.gregale.dev --match-path /login --throttle-requests-per-second 0.1 --throttle-burst 5 --throttle-key-by composite --throttle-key-field ip --throttle-key-field header:x-username --throttle-count-status 401",
				"gregale edge-rules create --app my-api --kind redirect --match-host my-api.gregale.dev --match-path /old --redirect-status 308 --redirect-to https://my-api.gregale.dev/new",
				"gregale edge-rules create --app my-api --kind cache --match-host my-api.gregale.dev --match-path /catalog --cache-max-age-seconds 60",
				"gregale edge-rules create --app my-api --kind budget --match-host my-api.gregale.dev --match-path /reports --budget-ms 20000",
				"gregale edge-rules create --app my-api --kind validate --match-host api.example.com --validate-schema @schema.json --validate-content-type application/json --validate-mode block",
				"cat schema.json | gregale edge-rules create --app my-api --kind validate --match-host api.example.com --validate-schema -",
				`gregale edge-rules create --app my-api --kind validate --match-host api.example.com --match-path '/users/?*' --match-method GET --validate-path-template '/users/{id}' --validate-path-schema '{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}' --validate-query-schema @query.json`,
			}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Value: "slug", Req: true},
				{Name: "kind", Short: "rule kind", Value: "KIND", Req: true, ClosedSet: edgeRuleKindVocab},
				{Name: "match-host", Short: "host to match; @app covers every host the app serves", Value: "HOST", Req: true},
				{Name: "match-path", Short: "path glob to match (default / covers only the root; /** covers every path)", Value: "PATH"},
				{Name: "match-method", Short: "HTTP method to match (repeat for multiple)", Value: "METHOD"},
				{Name: "match-header", Short: "exact request header selector (repeat)", Value: "Name=Value"},
				{Name: "priority", Short: "match priority; lower wins (default 100)", Value: "N"},
				{Name: "enabled", Short: "whether the rule is enabled (default true)", Bool: true, ClosedSet: []string{"true", "false"}},
				{Name: "throttle-requests-per-second", Short: "kind=throttle: refill rate in requests per second", Value: "RPS"},
				{Name: "throttle-burst", Short: "kind=throttle: token-bucket burst", Value: "N"},
				{Name: "throttle-key-by", Short: "kind=throttle: bucket key (none|api_key|consumer_id|jwt_subject|jwt_claim|country|ip|composite)", Value: "KEY"},
				{Name: "redirect-status", Short: "kind=redirect: 301|302|307|308", Value: "CODE"},
				{Name: "redirect-to", Short: "kind=redirect: Location URL", Value: "URL"},
				{Name: "template", Short: "kind=redirect|headers: expand ${name} request values (host, path, query, client_ip, country, asn, header:<name>, …)", Bool: true},
				{Name: "rewrite-from", Short: "kind=rewrite: from path", Value: "PATH"},
				{Name: "rewrite-to", Short: "kind=rewrite: to path", Value: "PATH"},
				{Name: "route-target-slug", Short: "kind=route: target app slug", Value: "slug"},
				{Name: "cache-max-age-seconds", Short: "kind=cache: fresh window (default 60; max 3600)", Value: "N"},
				{Name: "cache-stale-while-revalidate-seconds", Short: "kind=cache: serve stale during a background refresh (max 300)", Value: "N"},
				{Name: "budget-ms", Short: "kind=budget: per-request wall-clock budget in ms (max 30000)", Value: "MS"},
				{Name: "retry-max-attempts", Short: "kind=retry: total attempts including the original (default 2; max 3)", Value: "N"},
				{Name: "circuit-failure-threshold", Short: "kind=circuit_breaker: failure ratio that opens the app's instance-health breaker (default 0.5); the highest-priority rule tunes every instance, selectors do not partition it", Value: "RATIO"},
				{Name: "circuit-open-seconds", Short: "kind=circuit_breaker: first open interval (default 5)", Value: "N"},
				{Name: "respond-status", Short: "kind=respond (preview apps only): response status (default 200)", Value: "CODE"},
				{Name: "respond-body", Short: "kind=respond: JSON response body (max 64 KiB)", Value: "JSON"},
				{Name: "ip-allow", Short: "kind=ip: allow CIDR (repeat)", Value: "CIDR"},
				{Name: "ip-deny", Short: "kind=ip: deny CIDR (repeat)", Value: "CIDR"},
				{Name: "geo-allow", Short: "kind=geo: allow ISO country code (repeat)", Value: "CC"},
				{Name: "geo-deny", Short: "kind=geo: deny ISO country code (repeat)", Value: "CC"},
				{Name: "jwt-issuer", Short: "kind=jwt: token issuer", Value: "ISSUER"},
				{Name: "jwt-jwks-url", Short: "kind=jwt: JWKS URL (https)", Value: "URL"},
				{Name: "jwt-auth", Short: "kind=jwt: Auth app whose tokens to require (sets issuer, JWKS URL, audience, ES256 and exp)", Value: "AUTH_APP", Unreleased: true},
				{Name: "on-success-webhook", Short: "success webhook subscription; repeat when updating async policy", Value: "ID"},
				{Name: "on-failure-webhook", Short: "failure webhook subscription; repeat when updating async policy", Value: "ID"},
				{Name: "async-max-attempts", Short: "total attempts (0 = plan default; capped by plan)", Value: "N"},
				{Name: "async-retry-base-seconds", Short: "exponential retry base delay", Value: "N"},
				{Name: "async-retry-max-seconds", Short: "maximum exponential retry delay", Value: "N"},
				{Name: "async-retry-jitter-seconds", Short: "retry jitter fraction (0..1)", Value: "N"},
				{Name: "async-max-age-seconds", Short: "invocation lifetime from acceptance (0 = plan default; capped by plan)", Value: "N"},
				{Name: "waf-mode", Short: "kind=waf: observe (default), warn, or block; warn and block check headers and URL in-path at paranoia level 1", Value: "MODE", ClosedSet: []string{"observe", "warn", "block"}},
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
				{Name: "throttle-key-field", Short: "kind=throttle: composite key field (ip|country|api_key|consumer_id|jwt_subject|jwt_claim|method|path|header:<name>; repeat)", Value: "FIELD"},
				{Name: "throttle-count-status", Short: "kind=throttle: charge the bucket only for responses with this status (repeat)", Value: "CODE"},
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
				{Name: "match", Short: "match condition ANDed with the selectors (ADR-962 JSON, @file, or -)", Value: "JSON|@FILE|-"},
				{Name: "mode", Short: "enforce (default) or log: a log-mode rule only counts matches", Value: "MODE", ClosedSet: []string{"enforce", "log"}},
				{Name: "cache-methods", Short: "kind=cache: cacheable method (repeatable)", Value: "METHOD", Repeatable: true},
				{Name: "cache-vary-on", Short: "kind=cache: header to vary on (repeatable)", Value: "HEADER", Repeatable: true},
				{Name: "cors-allow-header", Short: "kind=cors: allowed header (repeatable)", Value: "HEADER", Repeatable: true},
				{Name: "cors-allow-method", Short: "kind=cors: allowed method (repeatable)", Value: "METHOD", Repeatable: true},
				{Name: "cors-allow-origin", Short: "kind=cors: allowed origin (repeatable)", Value: "ORIGIN", Repeatable: true},
				{Name: "cors-expose-header", Short: "kind=cors: exposed header (repeatable)", Value: "HEADER", Repeatable: true},
				{Name: "description", Short: "operator-facing description (<=1000 chars)", Value: "TEXT"},
				{Name: "expires-at", Short: "stop applying the rule at this RFC 3339 time", Value: "RFC3339"},
				{Name: "expires-in", Short: "stop applying the rule after this duration (e.g. 2h)", Value: "DURATION"},
				{Name: "headers-request-add", Short: "kind=headers: request header to add (repeatable)", Value: "NAME:VALUE", Repeatable: true},
				{Name: "headers-request-remove", Short: "kind=headers: request header to remove (repeatable)", Value: "NAME", Repeatable: true},
				{Name: "headers-request-set", Short: "kind=headers: request header to set (repeatable)", Value: "NAME:VALUE", Repeatable: true},
				{Name: "headers-response-add", Short: "kind=headers: response header to add (repeatable)", Value: "NAME:VALUE", Repeatable: true},
				{Name: "headers-response-remove", Short: "kind=headers: response header to remove (repeatable)", Value: "NAME", Repeatable: true},
				{Name: "headers-response-set", Short: "kind=headers: response header to set (repeatable)", Value: "NAME:VALUE", Repeatable: true},
				{Name: "jwt-algorithm", Short: "kind=jwt: allowed signing algorithm (repeatable)", Value: "ALG", Repeatable: true},
				{Name: "jwt-audience", Short: "kind=jwt: required audience (repeatable)", Value: "AUDIENCE", Repeatable: true},
				{Name: "jwt-require-exp", Short: "kind=jwt: reject tokens without an exp claim"},
				{Name: "jwt-required-claim", Short: "kind=jwt: required claim (repeatable)", Value: "NAME=VALUE", Repeatable: true},
				{Name: "name", Short: "operator-facing rule name (<=100 chars)", Value: "NAME"},
				{Name: "redirect-header", Short: "kind=redirect: extra response header (repeatable)", Value: "NAME:VALUE", Repeatable: true},
			}},
			{Name: subGet, Short: "Show one edge rule", Positionals: []string{"<id>"}},
			{Name: subUpdate, Short: "Update one edge rule", Positionals: []string{"<id>"}, Examples: []string{
				"gregale edge-rules update RULE_ID --kind validate --validate-schema @schema.json --validate-mode block",
				"gregale edge-rules update RULE_ID --kind validate --validate-mode observe",
			}, Flags: []cliFlag{
				{Name: "match-host", Short: "new host to match; @app covers every host the app serves", Value: "HOST"},
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
				{Name: "template", Short: "kind=redirect|headers: expand ${name} request values (host, path, query, client_ip, country, asn, header:<name>, …)", Bool: true},
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
				{Name: "throttle-key-by", Short: "kind=throttle: bucket key", Value: "KEY", ClosedSet: []string{"none", "api_key", "consumer_id", "jwt_subject", "jwt_claim", "country", "ip", "composite"}},
				{Name: "throttle-jwt-claim", Short: "kind=throttle: JWT claim when key-by is jwt_claim", Value: "CLAIM"},
				{Name: "throttle-max-keys-per-rule", Short: "kind=throttle: maximum distinct consumer buckets", Value: "N"},
				{Name: "throttle-missing-key-policy", Short: "kind=throttle: behavior when identity is missing", Value: "POLICY", ClosedSet: []string{"shared", "reject"}},
				{Name: "throttle-key-field", Short: "kind=throttle: composite key field (ip|country|api_key|consumer_id|jwt_subject|jwt_claim|method|path|header:<name>; repeat)", Value: "FIELD"},
				{Name: "throttle-count-status", Short: "kind=throttle: charge the bucket only for responses with this status (repeat)", Value: "CODE"},
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
				{Name: "respond-status", Short: "kind=respond (preview apps only): response status", Value: "CODE"},
				{Name: "respond-body", Short: "kind=respond: JSON response body", Value: "JSON"},
				{Name: "on-success-webhook", Short: "success webhook subscription", Value: "ID"},
				{Name: "on-failure-webhook", Short: "failure webhook subscription", Value: "ID"},
				{Name: "async-max-attempts", Short: "total attempts (0 = plan default; capped by plan)", Value: "N"},
				{Name: "async-retry-base-seconds", Short: "exponential retry base delay", Value: "N"},
				{Name: "async-retry-max-seconds", Short: "maximum exponential retry delay", Value: "N"},
				{Name: "async-retry-jitter-seconds", Short: "retry jitter fraction (0..1)", Value: "N"},
				{Name: "async-max-age-seconds", Short: "invocation lifetime from acceptance (0 = plan default; capped by plan)", Value: "N"},
				{Name: "waf-mode", Short: "kind=waf: observe (default), warn, or block; warn and block check headers and URL in-path at paranoia level 1", Value: "MODE", ClosedSet: []string{"observe", "warn", "block"}},
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
				{Name: "match", Short: "replace the match condition (ADR-962 JSON, @file, or -)", Value: "JSON|@FILE|-"},
				{Name: "clear-match", Short: "remove the match condition"},
				{Name: "mode", Short: "enforce or log (log-mode rules only count matches)", Value: "MODE", ClosedSet: []string{"enforce", "log"}},
				{Name: "clear-expiry", Short: "remove the rule's expiry"},
				{Name: "description", Short: "operator-facing description (<=1000 chars)", Value: "TEXT"},
				{Name: "expires-at", Short: "stop applying the rule at this RFC 3339 time", Value: "RFC3339"},
				{Name: "expires-in", Short: "stop applying the rule after this duration (e.g. 2h)", Value: "DURATION"},
				{Name: "jwt-require-exp", Short: "kind=jwt: reject tokens without an exp claim"},
				{Name: "name", Short: "operator-facing rule name (<=100 chars)", Value: "NAME"},
			}},
			{Name: subRm, Short: "Delete one edge rule", Positionals: []string{"<id>"}, Flags: []cliFlag{
				{Name: "yes", Short: "skip the typed confirmation (alias: --quiet)", Bool: true},
				{Name: "app", Short: "require the rule to belong to this app", Value: "slug"},
			}},
			{Name: "history", Short: "List recorded versions of an app's edge-rule set (--version N shows its rules)", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "version", Short: "show the rules recorded in this version", Value: "N"},
			}},
			{Name: "recipes", Short: "List protection recipes: ready-made rules that start in log mode"},
			{Name: "recipe", Short: "Add a protection recipe's rule to an app, in log mode unless --enforce", Positionals: []string{"<recipe>"}, Examples: []string{
				"gregale edge-rules recipe block-unverified-bots --app my-api",
				"gregale edge-rules recipe allow-ips-only --app my-api --set cidrs=203.0.113.7,198.51.100.0/24",
			}, Flags: []cliFlag{
				{Name: "app", Short: "app slug (defaults to linked project context)", Value: "slug"},
				{Name: "host", Short: "restrict the rule to one hostname (default: every host the app serves)", Value: "HOST"},
				{Name: "set", Short: "recipe parameter name=value (repeat)", Value: "NAME=VALUE"},
				{Name: "enforce", Short: "enforce immediately instead of starting in log mode", Bool: true},
			}},
			{Name: "stats", Short: "Per-rule match counts (matched for enforced rules, logged for log-mode rules)", Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "window", Short: "window", Value: "WINDOW", ClosedSet: []string{"1h", "24h", "7d"}},
			}},
			{Name: "events", Short: "Sampled requests rules matched and kind=waf detections, newest first (up to 10 of each per rule per minute; kept 7 days)", Examples: []string{
				"gregale edge-rules events --app my-api --outcome logged --since 7d",
				"gregale edge-rules events --app my-api --outcome blocked",
			}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "rule", Short: "only this rule id", Value: "ID"},
				{Name: "outcome", Short: "outcome filter; detected, warned and blocked are kind=waf detections", Value: "OUTCOME", ClosedSet: []string{"matched", "logged", "detected", "warned", "blocked"}},
				{Name: "since", Short: "how far back, e.g. 1h, 24h, 7d (clamped to the plan window)", Value: "DURATION"},
				{Name: "limit", Short: "events per page (1..200)", Value: "N"},
				{Name: "cursor", Short: "continue from a previous page's next cursor", Value: "CURSOR"},
			}},
			{Name: "rollback", Short: "Restore an app's edge rules to a recorded version (recorded as a new version)", Examples: []string{
				"gregale edge-rules rollback --app my-api --to 12",
			}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "to", Short: "version to restore", Req: true, Value: "N"},
				{Name: "quiet", Short: "skip the typed confirmation (for scripts)"},
			}},
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
		Subcommands: []cliSub{{Name: "status", Positionals: []string{"<slug>"}, Short: "Show every route with its contract, traffic, canary health, production budget, rollback mode and gaps", Examples: []string{"gregale routes status my-api", "gregale routes status my-api --since 24h --json"}, Flags: []cliFlag{
			{Name: "deployment", Value: "ID", Short: "serving deployment UUID or vN (default: the app's serving deployment)"},
			{Name: "since", Value: "DURATION", Short: "observed usage window (default 168h; also accepts 7d or RFC3339)"},
		}}, {Name: "advise", Positionals: []string{"<slug>"}, Short: "Suggest cache, async and throttle edge rules from observed traffic, with what-if estimates", Examples: []string{"gregale routes advise my-api", "gregale routes advise my-api --cache-max-age 300", "gregale routes advise my-api --apply 3f9a1c2b7d4e"}, Flags: []cliFlag{
			{Name: "since", Value: "DURATION", Short: "traffic window to analyze (default 7d; clamped to telemetry retention)"},
			{Name: "until", Value: "RFC3339", Short: "end of the window (default now)"},
			{Name: "cache-max-age", Value: "SECONDS", Short: "what-if cache lifetime for cache suggestions (default 60)"},
			{Name: "apply", Value: "ID", Short: "create the edge rules of this suggestion (disabled for review)"},
			{Name: "enable", Short: "with --apply, create the rules enabled"},
		}}, {Name: "awake", Positionals: []string{"<slug>"}, Short: "Show which routes keep a scale-to-zero app awake, what each costs in awake time, and how to let it park", Examples: []string{"gregale routes awake my-api", "gregale routes awake my-api --since 24h --json"}, Flags: []cliFlag{
			{Name: "since", Value: "DURATION", Short: "window to analyze (default 7d; clamped to telemetry retention)"},
			{Name: "until", Value: "RFC3339", Short: "end of the window (default now)"},
		}}, {Name: "priority", Positionals: []string{"<slug>"}, Short: "Show or set which routes are served first, and turned away last, when every instance is busy", Examples: []string{"gregale routes priority my-api", "gregale routes priority my-api --critical \"POST /checkout\" --bulk \"/exports/*\"", "gregale routes priority my-api --reset"}, Flags: []cliFlag{
			{Name: "critical", Value: "[METHOD] PATH", Short: "route served first when the app is saturated (repeatable)"},
			{Name: "bulk", Value: "[METHOD] PATH", Short: "route served last and turned away first (repeatable)"},
			{Name: "clear", Short: "save no priorities, not even the route-health default"},
			{Name: "reset", Short: "delete saved priorities and use the route-health default"},
		}}, {Name: "requirements", Short: "Save or read versioned route requirements for an app", Subcommands: []cliSub{
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
				{Name: "on-violation", Value: "ACTION", Short: "report (default) or rollback to the last healthy deployment when an error budget is violated within 30 minutes of release", ClosedSet: []string{"report", "rollback"}},
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
			{Name: "declarations", Positionals: []string{"<slug>"}, Short: "Review lifecycle declarations between captured deployments", Flags: []cliFlag{
				{Name: "from-deployment", Value: "UUID", Short: "serving baseline deployment", Req: true}, {Name: "to-deployment", Value: "UUID", Short: "candidate deployment", Req: true}, {Name: "out", Value: "PATH", Short: "save JSON to a new file"}, {Name: "fail-on-findings", Short: "fail on lifecycle regressions or incomplete declarations"},
			}},
			{Name: "prepare-approval", Positionals: []string{"<slug>"}, Short: "Prepare a pinned successor approval request for review", Flags: []cliFlag{
				{Name: "from-deployment", Value: "UUID", Short: "serving baseline deployment", Req: true}, {Name: "to-deployment", Value: "UUID", Short: "candidate deployment", Req: true}, {Name: "mappings", Value: "PATH", Short: "explicit successor mappings and optional destination pins", Req: true}, {Name: "out", Value: "PATH", Short: "save request to a new file", Req: true},
			}},
			{Name: "approve", Positionals: []string{"<slug>"}, Short: "Submit a reviewed lifecycle successor approval", Flags: []cliFlag{{Name: "request", Value: "PATH", Short: "pinned request JSON", Req: true}}},
			{Name: "receipt", Positionals: []string{"<slug>"}, Short: "Read a persisted lifecycle approval receipt", Flags: []cliFlag{{Name: "id", Value: "UUID", Short: "approval receipt ID", Req: true}}},
			{Name: "history", Positionals: []string{"<slug>"}, Short: "Inspect applied and blocked production lifecycle reviews", Examples: []string{"gregale routes lifecycle history api --json"}, Flags: []cliFlag{{Name: "limit", Value: "N", Short: "reviews per page (default 10; maximum 20)"}, {Name: "before", Value: "ID", Short: "retained review ID from next_cursor"}}},

			{Name: "review", Positionals: []string{"<slug>"}, Short: "Compare captured routes, observed usage, source and requirements", Examples: []string{"gregale routes lifecycle review api --deployment DEPLOYMENT_UUID --since 14d --source-impact impact.json --out lifecycle-review.json"}, Flags: []cliFlag{
				{Name: "deployment", Value: "ID", Short: "immutable deployed contract UUID", Req: true},
				{Name: "since", Value: "WINDOW", Short: "route-usage window (default 14d; plan retention may clamp it)"},
				{Name: "source-impact", Value: "PATH", Short: "complete source impact report whose candidate revision matches the deployment commit"},
				{Name: "out", Value: "PATH", Short: "save full JSON review to a new file"},
				{Name: "fail-on-incomplete", Short: "exit nonzero when any route remains inconclusive"},
			}},
		}}, {Name: "sunsets", Positionals: []string{"<slug>"}, Short: "Review upcoming sunsets and remaining callers", Flags: []cliFlag{
			{Name: "source", Value: "SOURCE", Short: "metadata source (default deployment)", ClosedSet: []string{"deployment", "manual_import"}},
			{Name: "deployment", Value: "UUID", Short: "baseline deployment ID", Req: true}, {Name: "since", Value: "WINDOW", Short: "retained telemetry window (default 14d)"}, {Name: "within", Value: "DURATION", Short: "upcoming sunset horizon (default 720h)"}, {Name: "mapping", Value: "PATH", Short: "explicit successor mapping JSON"}, {Name: "out", Value: "PATH", Short: "save report JSON to a new file"}, {Name: "fail-on-overdue", Short: "fail on elapsed sunset dates"}, {Name: "fail-on-incomplete", Short: "fail on missing metadata or incomplete evidence"},
		}, Subcommands: []cliSub{{Name: "diff", Short: "Compare retained sunset evidence and caller regressions", Flags: []cliFlag{
			{Name: "before", Value: "PATH", Short: "previous saved report", Req: true}, {Name: "after", Value: "PATH", Short: "new saved report", Req: true}, {Name: "out", Value: "PATH", Short: "save diff JSON to a new file"}, {Name: "max-staleness", Value: "DURATION", Short: "maximum report age (default 24h)"}, {Name: "fail-on-regression", Short: "fail on observed regressions"}, {Name: "fail-on-incomplete", Short: "fail on stale, changed or incomplete evidence"},
		}}}}, {Name: "migration", Short: "Suggest successors, review contracts and assess customer cutover readiness", Subcommands: []cliSub{
			{Name: "policy", Short: "Read or set the app server policy for production route removal", Flags: []cliFlag{
				{Name: "app", Value: "APP", Req: true, Short: "production app slug"},
				{Name: "mode", Value: "MODE", ClosedSet: []string{"report", "enforce"}, Short: "omit to read; set report or enforce to write"},
				{Name: "expected-revision", Value: "N", Short: "current revision for a write; 0 creates the policy"},
				{Name: "grace-period", Value: "DURATION", Short: "server-observed quiet period (default 720h, minimum 1h)"},
				{Name: "max-approval-age", Value: "DURATION", Short: "approval TTL (default 1h, maximum 72h)"},
			}},
			{Name: "server-check", Short: "Read authoritative removal blockers for an exact candidate", Flags: []cliFlag{
				{Name: "app", Value: "APP", Req: true, Short: "production app slug"},
				{Name: "candidate-deployment", Value: "ID", Req: true, Short: "candidate deployment UUID"},
				{Name: "fail-on-blocked", Short: "exit 1 if the server check is blocked"},
			}},
			{Name: "authorize", Short: "Store an authenticated admin approval after local and server checks", Flags: []cliFlag{
				{Name: "app", Value: "APP", Req: true, Short: "production app slug"},
				{Name: "baseline-deployment", Value: "ID", Req: true, Short: "serving baseline UUID"},
				{Name: "candidate-deployment", Value: "ID", Req: true, Short: "candidate deployment UUID"},
				{Name: "expected-revision", Value: "N", Req: true, Short: "current server policy revision"},
				{Name: "readiness", Value: "PATH", Req: true, Short: "reviewed migration readiness JSON"},
				{Name: "mapping", Value: "PATH", Req: true, Short: "reviewed same-app successor mapping"},
				{Name: "acknowledge-observed-only", Req: true, Bool: true, Short: "acknowledge the limits of observed telemetry"},
				{Name: "out", Value: "PATH", Short: "save server approval receipt to a new file"},
			}},

			{Name: "gate", Short: "Explain removed-route blockers; optionally fail a local CI check", Examples: []string{"gregale routes migration gate --app api --baseline-deployment BASELINE_UUID --candidate-deployment CANDIDATE_UUID --readiness readiness.json --mapping route-successors.json --owner-approval approval.json --mode enforce"}, Flags: []cliFlag{
				{Name: "app", Short: "production app slug", Value: "APP", Req: true},
				{Name: "baseline-deployment", Short: "serving deployment UUID", Value: "ID", Req: true},
				{Name: "candidate-deployment", Short: "candidate deployment UUID", Value: "ID", Req: true},
				{Name: "candidate-app", Short: "candidate app slug (defaults to --app; may be a preview)", Value: "APP"},
				{Name: "readiness", Short: "migration readiness JSON for the serving deployment", Value: "PATH"},
				{Name: "mapping", Short: "reviewed successor mapping JSON", Value: "PATH"},
				{Name: "owner-approval", Short: "owner attestation JSON for this exact change", Value: "PATH"},
				{Name: "mode", Short: "report (default) or enforce (exit 1 for blockers)", Value: "MODE", ClosedSet: []string{"report", "enforce"}},
				{Name: "max-evidence-age", Short: "maximum evidence age (default and maximum 72h)", Value: "DURATION"},
				{Name: "out", Short: "save gate JSON to a new file", Value: "PATH"},
			}},
			{Name: "approve", Short: "Record a local owner attestation after all cutover checks pass", Examples: []string{"gregale routes migration approve --app api --baseline-deployment BASELINE_UUID --candidate-deployment CANDIDATE_UUID --readiness readiness.json --mapping route-successors.json --approved-by owner@example.com --out approval.json"}, Flags: []cliFlag{
				{Name: "app", Short: "production app slug", Value: "APP", Req: true},
				{Name: "baseline-deployment", Short: "serving deployment UUID", Value: "ID", Req: true},
				{Name: "candidate-deployment", Short: "candidate deployment UUID", Value: "ID", Req: true},
				{Name: "candidate-app", Short: "candidate app slug (defaults to --app; may be a preview)", Value: "APP"},
				{Name: "readiness", Short: "migration readiness JSON for the serving deployment", Value: "PATH", Req: true},
				{Name: "mapping", Short: "mapping reviewed with the route owner", Value: "PATH", Req: true},
				{Name: "approved-by", Short: "owner identity asserted in the local attestation", Value: "OWNER", Req: true},
				{Name: "mode", Short: "report or enforce; approvals always reject blockers", Value: "MODE", ClosedSet: []string{"report", "enforce"}},
				{Name: "max-evidence-age", Short: "maximum evidence age (default and maximum 72h)", Value: "DURATION"},
				{Name: "out", Short: "save the owner attestation to a new file", Value: "PATH", Req: true},
			}},
			{Name: "readiness", Short: "Refresh mapped contracts and join customer windows into an owner approval checkpoint", Examples: []string{"gregale routes migration readiness --mapping route-successors.json --from-deployment checkout=OLD_DEPLOYMENT --to-deployment checkout=NEW_DEPLOYMENT --snapshot migration-week-1.json --snapshot migration-week-2.json --format markdown --out readiness.json"}, Flags: []cliFlag{
				{Name: "mapping", Short: "version 1 explicit mapping reviewed with the route owner", Value: "PATH", Req: true},
				{Name: "from-deployment", Short: "baseline deployment as APP=ID; repeat for each app", Value: "APP=ID", Req: true, Repeatable: true},
				{Name: "to-deployment", Short: "successor deployment as APP=ID; repeat for each app", Value: "APP=ID", Repeatable: true},
				{Name: "snapshot", Short: "saved customer tracker JSON; at least two observation windows", Value: "PATH", Req: true, Repeatable: true},
				{Name: "grace-period", Short: "minimum continuous zero-traffic period before owner approval (default 30d)", Value: "DURATION"},
				{Name: "min-windows", Short: "minimum distinct complete observation windows (default 2)", Value: "COUNT"},
				{Name: "max-staleness", Short: "maximum age of the latest telemetry watermark (default 72h)", Value: "DURATION"},
				{Name: "format", Short: "text, Markdown, or prioritized CSV action queue (default text; --json emits JSON)", Value: "FORMAT", ClosedSet: []string{"text", "markdown", "csv"}},
				{Name: "out", Short: "save the full readiness report to a new JSON file", Value: "PATH"},
				{Name: "fail-on-breaking", Short: "exit 1 when any successor has a declared breaking change"},
				{Name: "fail-on-incomplete", Short: "exit 1 when contract or telemetry evidence is incomplete"},
				{Name: "fail-on-not-ready", Short: "exit 1 unless every route is ready for owner approval"},
			}},
			{Name: "suggest", Short: "Rank successor routes and write a draft mapping for owner review", Examples: []string{"gregale routes migration suggest --from-deployment checkout=OLD_DEPLOYMENT --to-deployment checkout=NEW_DEPLOYMENT --out route-successors.json --report-out suggestions.json"}, Flags: []cliFlag{
				{Name: "from-deployment", Short: "baseline deployment as APP=ID; repeat for each app", Value: "APP=ID", Req: true, Repeatable: true},
				{Name: "to-deployment", Short: "successor deployment as APP=ID; repeat for each app", Value: "APP=ID", Req: true, Repeatable: true},
				{Name: "sources", Short: "optional version 1 mapping selecting sources with empty successors", Value: "PATH"},
				{Name: "since", Short: "observed usage window (default 14d)", Value: "WINDOW"},
				{Name: "limit", Short: "candidate rows per source (1–10, default 3)", Value: "N"},
				{Name: "without-traffic", Short: "read captured contracts without usage"},
				{Name: "customer-details", Short: "include observed source consumer and tenant UUIDs in the report"},
				{Name: "out", Short: "save a draft version 1 mapping to a new file for owner review", Value: "PATH"},
				{Name: "report-out", Short: "save explanations and captured evidence to a new JSON file", Value: "PATH"},
			}},
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
			{Name: "confirm", Short: "confirm application of every reviewed change", Req: true, Bool: true},
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
			{Name: "push", Short: "Push KEY=VALUE pairs to sealed secrets (use --restart to apply now)", Examples: []string{"gregale env push --app my-api --dry-run", "printf 'LOG_LEVEL=info\\n' | gregale env push --app my-api --from-stdin", "gregale env push --app my-api --restart"}, Flags: []cliFlag{
				{Name: "wait-for-ack", Short: "wait for application acknowledgements for uploaded keys", Bool: true},
				{Name: "timeout", Short: "restart and acknowledgement wait limit after uploading (default 2m; requires --wait-for-ack)", Value: "DURATION"},
				{Name: "dry-run", Short: "preview key changes, scan findings, and quota without uploading or restarting", Bool: true},
				{Name: "app", Short: "app slug (defaults to linked context)", Value: "slug"},
				{Name: "scope", Short: "env scope (defaults to linked project environment)", Value: "SCOPE"},
				{Name: "f", ShortName: "f", Short: "input file (default .env)", Value: "PATH"},
				{Name: "from-stdin", Short: "read KEY=VALUE pairs from stdin"},
				{Name: "restart", Short: "restart app after applying changes (otherwise changes apply on next cold wake)"},
				{Name: "secret-scan", Short: "scan pairs before pushing", Value: "MODE", ClosedSet: []string{"on", "off", "strict", "source-tree"}},
				{Name: "no-guard", Short: "do not open a config guard for these changes (ADR-1451)", Bool: true},
			}},
			{Name: "diff", Short: "Render the env-diff matrix (presence / value-equality across scopes)", Examples: []string{"gregale env diff --app my-api", "gregale env diff --app my-api --json"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug (defaults to linked context)", Value: "slug"},
			}},
			{Name: "history", Short: "List config revisions (each env or secret change) and the config guard's verdicts (ADR-1451)", Examples: []string{"gregale env history --app my-api", "gregale env history --app my-api --scope staging --json"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug (defaults to linked context)", Value: "slug"},
				{Name: "scope", Short: "env scope (defaults to linked project environment)", Value: "SCOPE"},
				{Name: "limit", Short: "revisions to show (1-100, default 20)", Value: "N"},
			}},
			{Name: "rollback", Short: "Restore a scope's variables to a config revision as one guarded change; secrets that differ are listed, not restored", Positionals: []string{"<revision>"}, Examples: []string{"gregale env rollback 12 --app my-api", "gregale env rollback 12 --app my-api --yes"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug (defaults to linked context)", Value: "slug"},
				{Name: "scope", Short: "env scope (defaults to linked project environment)", Value: "SCOPE"},
				{Name: "yes", Short: "skip the typed confirmation", Bool: true},
				{Name: "no-guard", Short: "do not open a config guard for the rollback", Bool: true},
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
		Short:       "Explain an app from its runtime, deployment, API, data, scaling, and release signals (linked app or interactive picker)",
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
		Short:   "Run untrusted code in an isolated disposable microVM, or one command from an OCI image (--image)",
		Examples: []string{
			"gregale run --image alpine echo hello",
			"gregale run --image python:3.12-slim -e MODE=batch python -c 'print(42)'",
			"gregale run -it --image alpine sh",
		},
		Flags: []cliFlag{
			{Name: "image", Short: "run one command from this OCI image in a fresh microVM; arguments after the flags replace the image CMD (ADR-1161)", Value: "REF"},
			{Name: "interactive", Short: "with --image, attach stdin and relay the session until the command exits (-i; ADR-1330)"},
			{Name: "tty", Short: "with --image --interactive, give the command a terminal (-t; -it for both)"},
			{Name: "entrypoint", Short: "with --image, override the image ENTRYPOINT; with --dir, the bundle file to execute", Value: "CMD|FILE"},
			{Name: "env", Short: "with --image, KEY=VALUE environment variable (repeatable; -e)", Value: "KEY=VALUE"},
			{Name: "ram", Short: "with --image, memory in MB (default: Job default)", Value: "MB"},
			{Name: "timeout", Short: "with --image, task deadline (default: the plan's Job ceiling)", Value: "D"},
			{Name: "pull", Short: "with --image, re-pull a cached tag (always) or reuse it (missing)", Value: "P", ClosedSet: []string{"missing", "always"}},
			{Name: "detach", Short: "with --image, queue the run and print its id without waiting (-d); otherwise output streams live"},
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
		{Name: "list", Short: "List grouped issues", Flags: issueAppFlags}, {Name: "get", Short: "Read evidence and release history", Positionals: []string{"[<issue-id>]"}, Examples: []string{"gregale issues get --app my-api --interactive", "gregale issues get --app <SLUG> <issue-id>"}, Flags: append(append([]cliFlag{}, issueAppFlags...), cliFlag{Name: "interactive", Bool: true, Short: "browse issues by state and order, then inspect one"})}, {Name: "assign", Short: "Assign an issue to an account or team", Positionals: []string{"[<issue-id>]"}, Examples: []string{"gregale issues assign --app my-api --interactive"}, Flags: append(append([]cliFlag{}, issueAppFlags...), cliFlag{Name: "interactive", Bool: true, Short: "choose an open issue, assign to yourself or unassign, and confirm ownership"}, cliFlag{Name: "team", Value: "UUID", Short: "team assignment or shared inbox"}, cliFlag{Name: "assignee", Short: "owner account UUID (empty unassigns)", Value: "UUID"})}, {Name: "resolve", Short: "Resolve in a deployment", Positionals: []string{"[<issue-id>]"}, Examples: []string{"gregale issues resolve --app my-api --interactive"}, Flags: append(append([]cliFlag{}, issueAppFlags...), cliFlag{Name: "interactive", Bool: true, Short: "choose an open issue and fixing release, then confirm resolution"}, cliFlag{Name: "deployment", Short: "fixing deployment UUID (required unless interactive)", Value: "UUID"})}, {Name: "reopen", Short: "Reopen an issue", Positionals: []string{"[<issue-id>]"}, Examples: []string{"gregale issues reopen --app my-api --interactive"}, Flags: append(append([]cliFlag{}, issueAppFlags...), cliFlag{Name: "interactive", Bool: true, Short: "choose a resolved or ignored issue, review its state, and confirm reopening"})}, {Name: "ignore", Short: "Ignore until a timestamp", Positionals: []string{"[<issue-id>]"}, Examples: []string{"gregale issues ignore --app my-api --interactive"}, Flags: append(append([]cliFlag{}, issueAppFlags...), cliFlag{Name: "interactive", Bool: true, Short: "choose an open issue and duration, then confirm expiry"}, cliFlag{Name: "until", Short: "ignore until RFC3339 (required unless interactive)", Value: "RFC3339"})}, {Name: "impact-alert", Short: "Read or configure customer-impact alert threshold", Flags: issueAppFlags}, {Name: "compound-alerts", Short: "Manage and preview shared-window event and customer conditions", Positionals: []string{"<get|save|remove|preview>"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Req: true}, {Name: "key", Value: "KEY", Short: "stable rule key"}, {Name: "environment", Value: "NAME", Short: "exact environment; empty matches all"}, {Name: "minimum-events", Value: "N", Short: "event threshold in the shared window"}, {Name: "minimum-customers", Value: "N", Short: "verified distinct customer threshold in the shared window"}, {Name: "window-seconds", Value: "N"}, {Name: "cooldown-seconds", Value: "N"}, {Name: "enabled", Bool: true, ClosedSet: []string{"true", "false"}, Short: "use --enabled=false to disable a saved rule"}, {Name: "expected-revision", Value: "N", Short: "required for save/remove"}, {Name: "issue", Value: "UUID", Short: "existing issue for an unsaved preview"}}}, {Name: "tickets", Short: "Create tracker tickets or preview and apply lifecycle sync", Positionals: []string{"<create|sync|auto-sync|linear-connection>"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Req: true}, {Name: "issue", Value: "UUID", Req: true}, {Name: "provider", Value: "PROVIDER"}, {Name: "target", Value: "TARGET"}, {Name: "title", Value: "TEXT"}, {Name: "request-id", Value: "UUID"}, {Name: "description", Value: "TEXT"}, {Name: "token-env", Value: "NAME"}, {Name: "journal-dir", Value: "PATH"}, {Name: "recover-url", Value: "URL"}, {Name: "recover", Bool: true}, {Name: "link", Value: "UUID"}, {Name: "direction", Value: "push|pull|outbound|inbound|both"}, {Name: "apply", Bool: true}, {Name: "deployment", Value: "UUID"}, {Name: "linear-state", Value: "UUID"}, {Name: "webhook-secret-env", Value: "NAME"}, {Name: "linear-open-state", Value: "UUID"}, {Name: "linear-resolved-state", Value: "UUID"}, {Name: "native", Bool: true}, {Name: "enable", Bool: true}, {Name: "disable", Bool: true}, {Name: "connect", Bool: true}, {Name: "disconnect", Bool: true}, {Name: "team", Value: "UUID"}}}, {Name: "links", Short: "Manage GitHub and Linear tracker references", Positionals: []string{"<list|save|delete>"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Req: true}, {Name: "issue", Value: "UUID", Req: true}, {Name: "url", Value: "URL"}, {Name: "title", Value: "TEXT"}, {Name: "link", Value: "UUID"}, {Name: "cursor", Value: "CURSOR"}}}, {Name: "teams", Short: "Manage team members and shared inboxes", Positionals: []string{"<list|get|save|delete|inbox>"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Req: true}, {Name: "key", Value: "KEY"}, {Name: "name", Value: "NAME"}, {Name: "members", Value: "UUID,UUID"}, {Name: "cursor", Value: "CURSOR"}}}, {Name: "codeowners", Short: "Preview or replace ownership rules from a bounded CODEOWNERS subset", Positionals: []string{"<preview|apply>"}, Flags: []cliFlag{{Name: "file", Value: "PATH", Req: true}, {Name: "bindings-file", Value: "PATH", Req: true}, {Name: "source-root", Value: "PATH", Req: true}, {Name: "app", Value: "SLUG"}, {Name: "replace", Bool: true, Short: "explicitly replace the existing policy"}}}, {Name: "views", Short: "Manage and apply shared saved issue inbox filters", Positionals: []string{"<list|get|save|delete|apply>"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Req: true}, {Name: "key", Value: "KEY"}, {Name: "name", Value: "NAME"}, {Name: "state", Value: "STATE"}, {Name: "environment", Value: "NAME"}, {Name: "team", Value: "UUID", Short: "team assignment or shared inbox"}, {Name: "assignee", Value: "OWNER"}, {Name: "sort", Value: "ORDER"}, {Name: "min-customers", Value: "N"}, {Name: "tag", Value: "KEY=VALUE"}, {Name: "cursor", Value: "CURSOR"}}}, {Name: "compare-releases", Short: "Compare observed request health and Issues across deployments", Flags: []cliFlag{{Name: "app", Value: "SLUG", Req: true}, {Name: "baseline", Value: "UUID", Req: true}, {Name: "candidate", Value: "UUID", Req: true}, {Name: "environment", Value: "NAME"}}}, {Name: "bulk", Short: "Atomically triage up to 50 selected issue groups", Flags: []cliFlag{{Name: "app", Value: "SLUG", Req: true}, {Name: "issues", Value: "UUID,UUID", Req: true}, {Name: "action", Value: "ACTION", Req: true}, {Name: "team", Value: "UUID", Short: "team assignment or shared inbox"}, {Name: "assignee", Value: "UUID"}, {Name: "deployment", Value: "UUID"}, {Name: "until", Value: "RFC3339"}}}, {Name: "source-maps", Short: "Publish, inspect or retire deployment mapping artifacts", Positionals: []string{"<publish|status|retire>"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Req: true}, {Name: "deployment", Value: "UUID", Req: true}, {Name: "directory", Value: "PATH", Short: "deployed directory corresponding to the build scan root"}, {Name: "manifest-file", Value: "PATH", Short: "portable source-map manifest file"}}},
		{Name: "lifecycle-alerts", Short: "Read, save or preview new-issue/regression alert filters", Positionals: []string{"<get|save|preview>"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Req: true}, {Name: "environments", Value: "ENVS", Short: "comma-separated exact names; empty means all"}, {Name: "cooldown-seconds", Value: "N", Short: "shared cooldown; zero disables throttling"}, {Name: "expected-revision", Value: "N", Short: "required when saving"}, {Name: "issue", Value: "UUID", Short: "existing issue for draft preview"}, {Name: "event", Value: "EVENT", Short: "created or regressed for preview"}}},
		{Name: "ownership-rules", Short: "Read or replace automatic assignment rules", Flags: append(append([]cliFlag{}, issueAppFlags...), cliFlag{Name: "rules-file", Short: "JSON policy file to replace rules; use - for stdin", Value: "PATH"})}, {Name: "tokens", Short: "List ingest credentials", Flags: issueAppFlags}, {Name: "create-token", Short: "Create a deployment-bound ingest credential", Flags: append(append([]cliFlag{}, issueAppFlags...), cliFlag{Name: "deployment", Short: "deployment UUID the credential is bound to", Req: true, Value: "UUID"})}, {Name: "revoke-token", Short: "Revoke an ingest credential", Positionals: []string{"<token-id>"}, Flags: issueAppFlags},
	}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "application slug"}, {Name: "deployment", Value: "UUID", Short: "fixed or token-bound deployment"}, {Name: "state", Value: "STATE", Short: "filter issue state"}, {Name: "environment", Value: "ENV", Short: "environment filter"}, {Name: "cursor", Value: "CURSOR", Short: "issue-list or occurrence cursor"}, {Name: "release-cursor", Value: "CURSOR", Short: "release history cursor"}, {Name: "activity-cursor", Value: "CURSOR", Short: "activity history cursor"}, {Name: "team", Value: "UUID", Short: "team assignment or shared inbox"}, {Name: "assignee", Value: "OWNER", Short: "list filter me, unassigned, or account UUID; assignment owner UUID"}, {Name: "sort", Value: "ORDER", Short: "list order: recent or impact by verified customers in 24h"}, {Name: "tag", Value: "KEY=VALUE", Short: "allowed operational tag filter over retained occurrences in 24h"}, {Name: "min-customers", Value: "N", Short: "issue list threshold, or impact-alert policy threshold (0 disables)"}, {Name: "since", Value: "RFC3339", Short: "impact window start"}, {Name: "until", Value: "RFC3339", Short: "ignore until"}, {Name: "name", Value: "NAME", Short: "credential name"}, {Name: "expires-in", Value: "D", Short: "credential lifetime"}, {Name: "rules-file", Value: "PATH", Short: "ownership-rules JSON policy file to replace rules; use - for stdin"}}},

	{Name: "entities", DocSlug: "entities", Short: "Observe durable entity state and recovery metadata", Examples: []string{"gregale entities inspect --app my-app --namespace counters --key customer:123", "gregale entities watch --app my-app --namespace sessions --key run:1 --timeout 5m --json"}, Subcommands: entityVisibilityCLIManifest(), Audience: cliAudienceOperator},

	{Name: "product", DocSlug: "product-control", Short: "Manage internal product features, plans and outcome usage", Audience: cliAudienceOperator, Subcommands: productCLIManifest()},
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
		Positionals: []string{"[<project-slug>]"},
		Examples:    []string{"gregale link --interactive", "gregale link my-project --interactive", "gregale link my-project --app my-api --environment staging"},
		Flags: []cliFlag{
			{Name: "interactive", Short: "choose project, app, and environment, review defaults, and confirm saving", Bool: true},
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
	{Name: "profile", DocSlug: "config", Short: "List or manage named API connections and isolated credentials", Examples: []string{"gregale profile", "gregale profile list", "gregale profile --help"}, Subcommands: []cliSub{
		{Name: "add", Short: "Add a connection without changing the active profile", Positionals: []string{"<name>", "<api-url>"}},
		{Name: "list", Short: "List connections and the active profile"},
		{Name: "check", Short: "Verify the selected API connection and account identity", Examples: []string{"gregale profile check", "gregale --profile staging profile check --timeout 5s --json"}, Flags: []cliFlag{{Name: "timeout", Short: "maximum request duration (default 10s)", Value: "DURATION"}}},
		{Name: "use", Short: "Select the default connection or choose and check one interactively", Positionals: []string{"[<name>]"}, Examples: []string{"gregale profile use staging", "gregale profile use --interactive"}, Flags: []cliFlag{{Name: "interactive", Short: "choose a saved connection, check its identity, and confirm switching", Bool: true}, {Name: "timeout", Short: "interactive connection check deadline (default 10s)", Value: "DURATION"}}},
		{Name: "remove", Short: "Remove an inactive connection and its credentials", Positionals: []string{"<name>"}},
	}},

	{
		Name:     "context",
		DocSlug:  "link",
		Short:    "Show linked targets, configuration sources, and missing defaults",
		Examples: []string{"gregale context", "gregale context --check --json", "gregale context --local-config"},
		Flags: []cliFlag{
			{Name: "local-config", Short: "offline project-root summary of preferences, required names, presets, and recovery records; invalid files fail", Bool: true},
			{Name: "problems-only", Short: "show only invalid local files, broken references, and recovery issues; requires --local-config", Bool: true},
			{Name: "check", Short: "fail when a linked project or default app is missing; does not verify remote resources", Bool: true},
			{Name: "verify", Short: "read authenticated account and verify configured linked project/app/environment", Bool: true},
			{Name: "timeout", Value: "DURATION", Short: "overall verification deadline (default 10s; requires --verify)"},
		},
		Subcommands: []cliSub{
			{Name: "save", Short: "Save current linked targets as a named local preset", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "replace", Bool: true, Short: "explicitly replace an existing preset"}}},
			{Name: "use", Short: "Validate and apply a preset for the currently linked project", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "validate and preview without saving linked defaults"}}},
			{Name: "presets", Short: "List or manage local target presets", Examples: []string{"gregale context presets", "gregale context presets list", "gregale context presets --help"}, Subcommands: []cliSub{
				{Name: "copy", Short: "Copy a local preset as a starting point without changing active context", Positionals: []string{"<source-name>", "<new-name>"}, Examples: []string{"gregale context presets copy staging staging-eu"}},
				{Name: "rename", Short: "Rename a local preset while preserving its targets and active context", Positionals: []string{"<old-name>", "<new-name>"}, Examples: []string{"gregale context presets rename staging staging-eu"}},
				{Name: "update", Short: "Change selected preset targets without changing active context", Positionals: []string{"<name>"}, Flags: []cliFlag{
					{Name: "app", Value: "SLUG", Short: "validate and replace only the preset app"},
					{Name: "environment", Value: "SLUG", Short: "validate and replace only the preset environment"},
					{Name: "clear-app", Bool: true, Short: "remove only the preset app"},
					{Name: "clear-environment", Bool: true, Short: "remove only the preset environment"},
					{Name: "dry-run", Bool: true, Short: "validate and preview without saving the preset"},
				}},
				{Name: "list", Short: "List preset targets and their source file"},
				{Name: "show", Short: "Show one saved target preset", Positionals: []string{"<name>"}},
				{Name: "delete", Short: "Remove a local preset without changing linked targets", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "preview targets and source path without deleting"}}, Examples: []string{"gregale context presets delete staging --dry-run"}},
			}},
			{Name: "set", Short: "Update selected linked defaults while preserving omitted settings", Examples: []string{"gregale context set --environment staging", "gregale context set --app payments-api", "gregale context set --clear-environment"}, Flags: []cliFlag{
				{Name: "dry-run", Bool: true, Short: "validate and display proposed defaults without saving"},
				{Name: "app", Value: "SLUG", Short: "validate and replace the default app in the linked project"},
				{Name: "environment", Value: "SLUG", Short: "validate and replace the default project environment"},
				{Name: "clear-app", Bool: true, Short: "remove only the default app locally"},
				{Name: "clear-environment", Bool: true, Short: "remove only the default environment locally"},
			}}},
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
		Short:       "Query runtime logs and HTTP request events (linked app or interactive picker)",
		Examples:    []string{"gregale logs my-api --interactive", "gregale logs my-api --follow", "gregale logs my-api --since 1h --level error", "gregale logs views save runtime-errors --since 1h --level error", "gregale logs my-api --view runtime-errors"},
		Positionals: []string{"[<slug>]"},
		Flags: []cliFlag{
			{Name: "dry-run", Bool: true, Short: "preview effective target, source, and filters offline; no authentication or remote reads"},
			{Name: "view", Short: "reuse a local log view with compatible one-off filter overrides; saved view stays unchanged", Value: "NAME"},
			{Name: "saved-view", Bool: true, Short: "use the project-local preferred view and compatible overrides; excludes --view and other selectors"},
			{Name: "clear-status", Bool: true, Short: "remove the saved status filter for one ordinary HTTP view query; excludes --status"},
			{Name: "clear-grep", Bool: true, Short: "remove the saved text filter for one runtime view query; excludes --grep"},
			{Name: "clear-level", Bool: true, Short: "remove the saved level filter for one runtime view query; excludes --level"},
			{Name: "clear-route", Bool: true, Short: "remove the saved route filter for one HTTP view query; excludes --route"},
			{Name: "latest-failure", Short: "select latest failed deployment and read up to 500 retained build/deploy entries; accepts only an app target", Bool: true},
			{Name: "latest-request-error", Short: "select newest retained HTTP 5xx row; default 15m window; accepts app, since, route", Bool: true},
			{Name: "interactive", Short: "choose source, time window, and filters and show the equivalent command", Bool: true},
			{Name: "follow", Short: "stream logs until interrupted"},
			{Name: "deployment", Short: "deployment id or vN revision (default: latest)", Value: "ID"},
			{Name: "release", Short: "release id or revision (alias for --deployment)", Value: "ID|vN"},
			{Name: "source", Short: "log source", Value: "SOURCE", ClosedSet: []string{"runtime", "http"}},
			{Name: "grep", Short: "only show lines containing this substring", Value: "SUBSTR"},
			{Name: "since", Short: "lookback duration or RFC3339 timestamp", Value: "15m|3d|RFC3339"},
			{Name: "level", Short: "only show lines at this level", Value: "LEVEL", ClosedSet: []string{"info", "warn", "error"}},
			{Name: "status", Short: "only show HTTP requests with this status", Value: "100..599"},
			{Name: "route", Short: "only show HTTP requests for this route label", Value: "METHOD /path"},
			{Name: "request", Short: "show one HTTP request by public request id or row id", Value: "ID"},
			{Name: "trace", Short: "show HTTP access logs correlated with a W3C trace id", Value: "TRACE_ID"},
			{Name: "limit", Short: "HTTP request page size (1..200)", Value: "N"},
			{Name: "all", Short: "read every retained HTTP request page"},
			{Name: "explain", Short: "summarize the last failure and common error patterns"},
			{Name: "archive", Short: "read durable logs for one instance and UTC day"},
			{Name: "instance", Short: "instance id for --archive (1..128 ASCII letters, numbers, dots, underscores, or hyphens)", Value: "ID"},
			{Name: "date", Short: "UTC day for --archive: today, yesterday, or YYYY-MM-DD", Value: "DAY"},
		},
		Subcommands: []cliSub{
			{Name: "defaults", Short: "Show or manage the project-local preferred log view", Examples: []string{"gregale logs defaults", "gregale logs defaults --help"}, Subcommands: []cliSub{
				{Name: "set", Short: "Save one existing log view name locally", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "preview the preference path and selected filters without saving"}}, Examples: []string{"gregale logs defaults set errors --dry-run"}},
				{Name: "show", Short: "Show the preferred view, current filters, reference health, and preview command", Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "target the preview command at an explicit, unverified app"}}, Examples: []string{"gregale logs defaults show --app payments-api"}},
				{Name: "clear", Short: "Clear the local preference without changing saved views", Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "preview the preference removal without deleting files"}}, Examples: []string{"gregale logs defaults clear --dry-run"}},
			}},
			{Name: "views", Short: "List or manage reusable local log filters", Flags: []cliFlag{{Name: "name", Value: "TEXT", Short: "list view names containing literal, case-sensitive text"}, {Name: "source", Value: "SOURCE", ClosedSet: []string{"runtime", "http"}, Short: "list only saved views of this log source"}, {Name: "details", Bool: true, Short: "include saved filters and query/preview commands in JSON entries"}, {Name: "app", Value: "SLUG", Short: "target generated commands at an explicit, unverified app"}}, Examples: []string{"gregale logs views", "gregale logs views --source http", "gregale logs views --name errors", "gregale --json logs views --details", "gregale logs views list", "gregale logs views --help"}, Subcommands: []cliSub{
				{Name: "copy", Short: "Copy saved filters as a starting point while preserving the source view", Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "preview filters and destination without writing"}}, Positionals: []string{"<source-name>", "<new-name>"}, Examples: []string{"gregale logs views copy errors checkout-errors"}},
				{Name: "list", Short: "List saved log views and copyable query previews", Flags: []cliFlag{{Name: "name", Value: "TEXT", Short: "list view names containing literal, case-sensitive text"}, {Name: "source", Value: "SOURCE", ClosedSet: []string{"runtime", "http"}, Short: "list only saved views of this log source; HTTP includes latest-error views"}, {Name: "details", Bool: true, Short: "include saved filters and query/preview commands in JSON entries"}, {Name: "app", Value: "SLUG", Short: "target generated commands at an explicit, unverified app"}}, Examples: []string{"gregale logs views list", "gregale --json logs views list --details"}},
				{Name: "show", Short: "Show saved filters and copyable query previews", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "target generated commands at an explicit, unverified app"}, {Name: "details", Bool: true, Short: "include saved filters and query/preview commands in JSON output"}}, Examples: []string{"gregale logs views show errors --app my-api", "gregale --json logs views show errors --app my-api --details"}},
				{Name: "rename", Short: "Rename a saved view without changing filters or overwriting another view", Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "preview filters, destination, and preferred-view impact without writing"}}, Positionals: []string{"<old-name>", "<new-name>"}},
				{Name: "update", Short: "Change selected filters while preserving view type and omitted settings", Positionals: []string{"<name>"}, Flags: []cliFlag{
					{Name: "dry-run", Bool: true, Short: "validate proposed filters and show their run command without saving"},
					{Name: "since", Value: "DURATION", Short: "replace relative window"},
					{Name: "route", Value: "PATH", Short: "replace HTTP route"},
					{Name: "grep", Value: "TEXT", Short: "replace runtime text filter"},
					{Name: "level", Value: "LEVEL", Short: "replace runtime level"},
					{Name: "status", Value: "N", Short: "replace HTTP status; 0 clears it"},
					{Name: "limit", Value: "N", Short: "replace normal HTTP page size"},
					{Name: "follow", Bool: true, Short: "replace runtime follow mode; false disables it"},
					{Name: "clear-route", Bool: true, Short: "remove route filter"},
					{Name: "clear-grep", Bool: true, Short: "remove text filter"},
					{Name: "clear-level", Bool: true, Short: "remove level filter"},
				}},
				{Name: "delete", Short: "Delete a local log view", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "preview source, filters, and storage path without deleting"}}, Examples: []string{"gregale logs views delete checkout-errors --dry-run"}},
				{Name: "save", Short: "Save filters as a named view", Positionals: []string{"<name>"}, Flags: []cliFlag{
					{Name: "dry-run", Short: "preview proposed filters, replacement, and commands without saving", Bool: true},
					{Name: "latest-request-error", Short: "save newest HTTP 5xx selection with a relative window and optional route", Bool: true},
					{Name: "source", Short: "runtime (default) or http", Value: "SOURCE", ClosedSet: []string{"runtime", "http"}},
					{Name: "since", Short: "relative window (default 15m)", Value: "DURATION"},
					{Name: "level", Short: "runtime log level", Value: "LEVEL"},
					{Name: "grep", Short: "runtime text filter", Value: "TEXT"},
					{Name: "follow", Short: "follow runtime output", Bool: true},
					{Name: "status", Short: "exact HTTP status", Value: "100..599"},
					{Name: "route", Short: "HTTP route filter", Value: "PATH"},
					{Name: "limit", Short: "HTTP page size (default 100)", Value: "1..200"},
					{Name: "replace", Short: "replace an existing view", Bool: true},
				}},
			}},
		},
	},
	{
		Name:    "metrics",
		DocSlug: "metrics",
		Short:   "Per-app or account-wide metrics, or the service map (slug defaults to linked context)",
		Flags: []cliFlag{
			{Name: "range", Short: "window (5m|15m|1h|6h|24h|7d|15d)", Value: "WINDOW", ClosedSet: []string{"5m", "15m", "1h", "6h", "24h", "7d", "15d"}},
			{Name: "account", Short: "account-wide roll-up"},
			{Name: "services", Short: "service map of app-to-app calls (default range 1h)"},
			{Name: "custom", Short: "history of one pushed custom metric (ranges 1h|6h|24h|7d|15d)", Value: "NAME"},
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
		Short:   "Open the app's URL (linked app or interactive picker)",
		Subcommands: []cliSub{
			{Name: "docs", Short: "Open a CLI docs page (open docs [<slug>])"},
		},
		Flags:       []cliFlag{{Name: "app", Short: appSlugFlagUsage, Value: "SLUG"}, {Name: "dashboard", Short: "open the dashboard page instead of the live URL", Bool: true}},
		Positionals: []string{"[<slug>]"},
	},
	{
		Name:    "orgs",
		DocSlug: "orgs",
		Short:   "Manage orgs, members, and workspace activity",
		Subcommands: []cliSub{
			{Name: "standards", Short: "Manage versioned application standards", Subcommands: []cliSub{
				{Name: "assignments", Short: "Inspect current versions and revisions, including inactive assignments", Subcommands: standardAssignmentCLIHelp(), Examples: []string{"gregale orgs standards assignments list --org acme --limit 100", "gregale orgs standards assignments show --org acme --id ASSIGNMENT_UUID"}},
				{Name: "reviews", Short: "Preview assignment changes and inspect saved reviews", Subcommands: standardReviewCLIHelp()},
				{Name: "operation", Short: "Inspect rollout progress or apply gated operator controls", Flags: standardInspectionCLIHelp("operation UUID"), Subcommands: standardOperationCLIHelp()},
				{Name: "exceptions", Short: "Inspect history or manage bounded exceptions", Flags: standardExceptionCLIHelp(), Subcommands: standardExceptionMutationCLIHelp()},
				{Name: "local-intent", Short: "Replace permitted local settings with revision checks; release gated", Flags: standardMutationCLIHelp(false)},
				{Name: "application", Short: "Inspect application adoption and installed standards", Flags: []cliFlag{
					{Name: "org", Short: "organization slug", Value: "SLUG", Req: true},
					{Name: "app", Short: "application UUID", Value: "UUID", Req: true},
				}},
				{Name: "destinations", Short: "Manage immutable logging destination references", Subcommands: standardResourceCLIHelp()},
				{Name: "publishers", Short: "Manage immutable trusted publisher references", Subcommands: standardResourceCLIHelp()},
				{Name: "list", Short: "List the organization's latest standard versions", Flags: []cliFlag{
					{Name: "org", Short: "organization slug", Value: "SLUG", Req: true},
					{Name: "after", Short: "last standard slug from the previous page", Value: "SLUG"},
					{Name: "limit", Short: "page size (1..100)", Value: "N"},
				}},
				{Name: "show", Short: "Inspect an immutable standard version", Flags: []cliFlag{
					{Name: "org", Short: "organization slug", Value: "SLUG", Req: true},
					{Name: "standard", Short: "standard slug", Value: "SLUG", Req: true},
					{Name: "version", Short: "version; omitted reads the latest", Value: "N"},
				}},
				{Name: "publish", Short: "Publish a candidate version; does not activate it", Examples: []string{"gregale orgs standards publish --org acme --standard production-baseline --file standard.json --expected-version 0"}, Flags: []cliFlag{
					{Name: "org", Short: "organization slug", Value: "SLUG", Req: true},
					{Name: "standard", Short: "standard slug", Value: "SLUG", Req: true},
					{Name: "file", Short: "standard definition JSON file", Value: "PATH", Req: true},
					{Name: "expected-version", Short: "current version; 0 creates a new standard", Value: "N", Req: true},
					{Name: "description", Short: "version description", Value: "TEXT"},
				}},
			}},
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
		Short:       "Park an app cold (kill all live instances); a min-instances floor stays off, and unbilled, until the next request",
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
		Name: "auth", DocSlug: "application-auth", Short: "Add sign-up, sessions and users to your application (application Auth, not your Gregale login)", Unreleased: true,
		Subcommands: []cliSub{
			{Name: "create", Short: "Deploy a managed GoTrue Auth server bound to your database", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "database", Value: "DATABASE", Req: true, Short: "ready managed database name or ID"},
				{Name: "site-url", Value: "URL", Req: true, Short: "application URL users return to after sign-in"},
				{Name: "redirect-urls", Value: "URLS", Short: "comma-separated additional allowed redirect URLs"},
				{Name: "scope", Value: "SCOPE", Short: "environment scope"},
				{Name: "autoconfirm", Short: "sign users in without email confirmation (development only)"},
				{Name: "disable-signup", Short: "allow only invited or administrator-created users"},
				{Name: "authz", Value: "FILE", Short: "JSON file declaring custom roles and policies (applied on deploy)"},
				{Name: "email-provider", Value: "PROVIDER", Short: "HTTPS email provider", ClosedSet: []string{"resend", "postmark"}},
				{Name: "email-from", Value: "SENDER", Short: "sender address, or a display name with the address in angle brackets"},
				{Name: "email-api-key-stdin", Short: "read the email provider API key from stdin (prompted on a terminal)"},
				{Name: "workflows", Short: "let authorized users start workflows (mints an events:publish key sealed in the app)"},
				{Name: "resume", Short: "resume configuration and deployment of an existing app"},
			}, Examples: []string{"gregale auth create shop-auth --database shop-db --site-url https://shop.example.com", "gregale auth create shop-auth --database shop-db --site-url https://shop.example.com --resume --authz authz.json"}},
			{Name: "status", Short: "Show the issuer, JWKS URL and readiness of an Auth app", Positionals: []string{"<name>"}},
			{Name: "config", Short: "Show the running Auth settings, or change them and restart the app", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "site-url", Value: "URL", Short: "application URL users return to after sign-in"},
				{Name: "redirect-urls", Value: "URLS", Short: "comma-separated additional allowed redirect URLs; replaces the list (none clears it)"},
				{Name: "signup", Value: "on|off", Short: "whether anyone may sign up", ClosedSet: []string{"on", "off"}},
				{Name: "autoconfirm", Value: "on|off", Short: "sign users in without email confirmation (development only)", ClosedSet: []string{"on", "off"}},
				{Name: "password-min-length", Value: "N", Short: "minimum password length (6-72)"},
				{Name: "password-require", Value: "CLASSES", Short: "character classes every password needs: lower,upper,digit,symbol, or none"},
				{Name: "leaked-password-check", Value: "on|off", Short: "refuse passwords found in known breaches (Have I Been Pwned)", ClosedSet: []string{"on", "off"}},
				{Name: "mfa", Value: "on|off", Short: "let users enrol an authenticator app (TOTP); enrolled users keep verifying when off", ClosedSet: []string{"on", "off"}},
				{Name: "mfa-phone", Value: "on|off", Short: "let users enrol SMS codes as a second factor (needs an SMS provider)", ClosedSet: []string{"on", "off"}},
				{Name: "recovery-codes", Value: "on|off", Short: "let users create one-time recovery codes for their second factor", ClosedSet: []string{"on", "off"}},
				{Name: "email-provider", Value: "PROVIDER", Short: "HTTPS email provider, or none to remove it", ClosedSet: []string{"resend", "postmark", "none"}},
				{Name: "email-from", Value: "SENDER", Short: "sender address, or a display name with the address in angle brackets"},
				{Name: "email-api-key-stdin", Short: "read the email provider API key from stdin (prompted on a terminal)"},
				{Name: "authz", Value: "FILE", Short: "JSON file declaring custom roles and policies (applied on restart)"},
				{Name: "events", Value: "on|off", Short: "publish user lifecycle events (auth.user.*) to Gregale's event bus", ClosedSet: []string{"on", "off"}},
				{Name: "passkeys", Value: "on|off", Short: "let users sign in with a passkey", ClosedSet: []string{"on", "off"}},
				{Name: "passkey-rp-id", Value: "DOMAIN", Short: "parent domain shared by the Auth app and your site for passkeys (example.com), or none for the Auth app's host"},
				{Name: "brand-name", Value: "NAME", Short: "name shown on the hosted pages and in emails, or none for the site's host"},
				{Name: "brand-color", Value: "#RRGGBB", Short: "#rrggbb accent colour for the hosted pages, or none"},
				{Name: "brand-logo-url", Value: "URL", Short: "HTTPS URL of the logo on the hosted pages, or none"},
				{Name: "captcha", Value: "PROVIDER", Short: "CAPTCHA on sign-up and sign-in: turnstile, hcaptcha or off", ClosedSet: []string{"turnstile", "hcaptcha", "off"}},
				{Name: "captcha-site-key", Value: "KEY", Short: "the CAPTCHA provider's site key (public)"},
				{Name: "captcha-secret-stdin", Short: "read the CAPTCHA provider's secret key from stdin (prompted on a terminal)"},
				{Name: "activity-retention", Value: "DAYS", Short: "days to keep sign-in activity, or forever"},
				{Name: "sms", Value: "PROVIDER", Short: "phone sign-in by text message through your own SMS account, or off", ClosedSet: []string{"twilio", "twilio_verify", "messagebird", "textlocal", "vonage", "off"}},
				{Name: "sms-account", Value: "ID", Short: "Twilio account SID or Vonage API key"},
				{Name: "sms-sender", Value: "SENDER", Short: "Twilio messaging or Verify service SID (or phone number), MessageBird originator, Textlocal sender or Vonage from"},
				{Name: "sms-secret-stdin", Short: "read the Twilio auth token, MessageBird access key, Textlocal API key or Vonage API secret from stdin"},
				{Name: "sms-countries", Value: "CODES", Short: "country calling codes phone sign-in may text, comma-separated (1,44,49), or all"},
				{Name: "sso", Value: "on|off", Short: "organization single sign-on (SAML); manage identity providers with gregale auth sso", ClosedSet: []string{"on", "off"}},
				{Name: "org-sso", Value: "on|off", Short: "let organizations verify their email domains and connect their own identity provider (needs --sso on)", ClosedSet: []string{"on", "off"}},
				{Name: "oauth-server", Value: "on|off", Short: "the OAuth 2.1 authorization server, for third-party apps and MCP clients; manage clients with gregale auth oauth-clients", ClosedSet: []string{"on", "off"}},
				{Name: "oauth-registration", Value: "on|off", Short: "let OAuth clients register themselves (RFC 7591), as MCP clients do", ClosedSet: []string{"on", "off"}},
				{Name: "anonymous", Value: "on|off", Short: "let people start without an account (anonymous users); turn on CAPTCHA with it", ClosedSet: []string{"on", "off"}},
				{Name: "anonymous-retention", Value: "DAYS", Short: "days an anonymous user may stay idle before it is deleted, or forever"},
				{Name: "identity-linking", Value: "on|off", Short: "let signed-in users link and unlink sign-in methods", ClosedSet: []string{"on", "off"}},
				{Name: "self-deletion", Value: "on|off", Short: "let signed-in users delete their own accounts after a recent sign-in", ClosedSet: []string{"on", "off"}},
				{Name: "org-claims", Value: "on|off", Short: "put each session's active organization, role and permissions in its access tokens", ClosedSet: []string{"on", "off"}},
				{Name: "scope", Value: "SCOPE", Short: "environment scope"},
			}, Examples: []string{"gregale auth config shop-auth", "gregale auth config shop-auth --events on", "gregale auth config shop-auth --password-min-length 12 --password-require lower,upper,digit --mfa on"}},
			{Name: "providers", Short: "Manage OAuth sign-in providers such as Google or GitHub", Subcommands: []cliSub{
				{Name: "list", Short: "List the enabled providers", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "scope", Value: "SCOPE", Short: "environment scope"}}},
				{Name: "enable", Short: "Enable a provider, replacing its credentials", Positionals: []string{"<name>", "<provider>"}, Flags: []cliFlag{
					{Name: "client-id", Value: "ID", Req: true, Short: "OAuth client ID from the provider"},
					{Name: "client-secret-stdin", Short: "read the OAuth client secret from stdin (prompted on a terminal)"},
					{Name: "url", Value: "URL", Short: "provider server URL (Keycloak, or a self-hosted GitLab, Azure tenant or WorkOS)"},
					{Name: "scope", Value: "SCOPE", Short: "environment scope"},
				}, Examples: []string{"gregale auth providers enable shop-auth google --client-id ID --client-secret-stdin"}},
				{Name: "disable", Short: "Disable a provider and remove its credentials", Positionals: []string{"<name>", "<provider>"}, Flags: []cliFlag{{Name: "scope", Value: "SCOPE", Short: "environment scope"}}},
			}},
			{Name: "keys", Short: "Rotate the ES256 keys that sign the Auth app's tokens", Subcommands: []cliSub{
				{Name: "list", Short: "List signing keys and their state", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "scope", Value: "SCOPE", Short: "environment scope"}}},
				{Name: "rotate", Short: "Add a key, wait until it is published, then make it sign", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "scope", Value: "SCOPE", Short: "environment scope"}}},
				{Name: "create", Short: "Add a standby key that verifies but does not sign yet", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "scope", Value: "SCOPE", Short: "environment scope"}}},
				{Name: "activate", Short: "Make a published key sign; the previous key keeps verifying", Positionals: []string{"<name>", "<id>"}, Flags: []cliFlag{{Name: "scope", Value: "SCOPE", Short: "environment scope"}}},
				{Name: "revoke", Short: "Remove a key that no longer signs; its tokens stop verifying", Positionals: []string{"<name>", "<id>"}, Flags: []cliFlag{{Name: "scope", Value: "SCOPE", Short: "environment scope"}}},
			}},
			{Name: "admin-key", Short: "Manage owner keys for user and organization administration and imports", Subcommands: []cliSub{
				{Name: "create", Short: "Create a key; it is shown once and only its hash is stored", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "scope", Value: "SCOPE", Short: "environment scope"}}},
				{Name: "list", Short: "List admin key IDs", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "scope", Value: "SCOPE", Short: "environment scope"}}},
				{Name: "revoke", Short: "Revoke an admin key", Positionals: []string{"<name>", "<id>"}, Flags: []cliFlag{{Name: "scope", Value: "SCOPE", Short: "environment scope"}}},
			}},
			{Name: "users", Short: "Find, sign out, ban and delete the application's users", Subcommands: []cliSub{
				{Name: "list", Short: "List users, newest first", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "search", Value: "TEXT", Short: "part of an email address or name"}, {Name: "page", Value: "N", Short: "page to show"}, {Name: "limit", Value: "N", Short: "entries per page (1-100)"}, {Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"}}},
				{Name: "get", Short: "Show a user with their organizations and active sessions", Positionals: []string{"<name>", "<user>"}, Flags: []cliFlag{{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"}}},
				{Name: "sign-out", Short: "End every session of a user", Positionals: []string{"<name>", "<user>"}, Flags: []cliFlag{{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"}}},
				{Name: "ban", Short: "Ban a user for a time and end their sessions", Positionals: []string{"<name>", "<user>"}, Flags: []cliFlag{
					{Name: "for", Value: "DURATION", Req: true, Short: "hours (24h), days (7d) or forever"},
					{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"},
				}},
				{Name: "unban", Short: "Lift a user's ban", Positionals: []string{"<name>", "<user>"}, Flags: []cliFlag{{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"}}},
				{Name: "delete", Short: "Delete a user with their sessions and sign-in methods", Positionals: []string{"<name>", "<user>"}, Flags: []cliFlag{
					{Name: "yes", Short: "delete without the typed confirmation (needed with --admin-key-stdin)"},
					{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"},
				}},
			}, Examples: []string{"gregale auth users list shop-auth --search example.com", "gregale auth users ban shop-auth ada@example.com --for 7d", "printf '%s' \"$ADMIN_KEY\" | gregale auth users delete shop-auth 0b5f2f0e-7a51-4d8f-8a3a-5c1b9d2e6f70 --yes --admin-key-stdin"}},
			{Name: "orgs", Short: "List the application's organizations with their members and invitations", Subcommands: []cliSub{
				{Name: "list", Short: "List organizations, newest first", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "search", Value: "TEXT", Short: "part of a slug or name"}, {Name: "page", Value: "N", Short: "page to show"}, {Name: "limit", Value: "N", Short: "entries per page (1-100)"}, {Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"}}},
				{Name: "get", Short: "Show an organization's members and pending invitations", Positionals: []string{"<name>", "<org>"}, Flags: []cliFlag{{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"}}},
			}},
			{Name: "emails", Short: "Change the wording of the emails the Auth app sends", Subcommands: []cliSub{
				{Name: "list", Short: "List the email types and whether each uses your wording", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"}}},
				{Name: "show", Short: "Show an email's subject, body and placeholders", Positionals: []string{"<name>", "<type>"}, Flags: []cliFlag{{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"}}},
				{Name: "set", Short: "Change an email's subject, body or both", Positionals: []string{"<name>", "<type>"}, Flags: []cliFlag{{Name: "subject", Value: "TEXT", Short: "the subject line; placeholders such as {{app}} are filled in"}, {Name: "body", Value: "TEXT", Short: "the body text; separate paragraphs with a blank line"}, {Name: "body-file", Value: "FILE", Short: "read the body text from this file"}, {Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"}}},
				{Name: "reset", Short: "Return an email to the default wording", Positionals: []string{"<name>", "<type>"}, Flags: []cliFlag{{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"}}},
				{Name: "preview", Short: "Render an email with example values, optionally a draft, and send it to yourself", Positionals: []string{"<name>", "<type>"}, Flags: []cliFlag{
					{Name: "subject", Value: "TEXT", Short: "the subject line; placeholders such as {{app}} are filled in"}, {Name: "body", Value: "TEXT", Short: "the body text; separate paragraphs with a blank line"}, {Name: "body-file", Value: "FILE", Short: "read the body text from this file"}, {Name: "send-to", Value: "EMAIL", Short: "also send the preview to this address through your email provider"}, {Name: "html", Value: "FILE", Short: "write the preview's HTML version to this file"}, {Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"},
				}},
			}, Examples: []string{"gregale auth emails set shop-auth signup --subject 'Welcome to {{app}}' --body-file signup.txt", "gregale auth emails preview shop-auth signup --send-to me@example.com"}},
			{Name: "import", Short: "Import users with their password hashes from a JSON or JSON Lines file", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "file", Value: "FILE", Req: true, Short: "JSON array or JSON Lines file of users"},
				{Name: "dry-run", Short: "validate the file without importing"},
				{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"},
			}, Examples: []string{"gregale auth import shop-auth --file users.jsonl --dry-run", "printf '%s' \"$ADMIN_KEY\" | gregale auth import shop-auth --file users.jsonl --admin-key-stdin"}},
			{Name: "activity", Short: "List sign-ins, sign-ups and account changes, newest first", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "user", Value: "ID", Short: "only this user's activity (user ID)"},
				{Name: "search", Value: "TEXT", Short: "an event such as login, or part of an email address"},
				{Name: "limit", Value: "N", Short: "entries to show (1-100)"},
				{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"},
			}, Examples: []string{"printf '%s' \"$ADMIN_KEY\" | gregale auth activity shop-auth --search login --admin-key-stdin"}},
			{Name: "sso", Short: "Register SAML identity providers for organization single sign-on", Subcommands: []cliSub{
				{Name: "list", Short: "List identity providers and the organizations their users join", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"}}},
				{Name: "add", Short: "Register an identity provider for email domains, optionally joining its users to an organization", Positionals: []string{"<name>"}, Flags: []cliFlag{
					{Name: "metadata-url", Value: "URL", Short: "the identity provider's HTTPS SAML metadata URL"},
					{Name: "metadata-file", Value: "FILE", Short: "a file holding the identity provider's SAML metadata XML"},
					{Name: "domains", Value: "DOMAINS", Req: true, Short: "comma-separated email domains whose users sign in through this provider"},
					{Name: "org", Value: "ORG", Short: "organization (ID or slug) its users join"},
					{Name: "role", Value: "ROLE", Short: "role its users join the organization with (not owner)"},
					{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"},
				}, Examples: []string{"printf '%s' \"$ADMIN_KEY\" | gregale auth sso add shop-auth --metadata-url https://acme.okta.com/app/abc/sso/saml/metadata --domains acme.com --org acme --admin-key-stdin"}},
				{Name: "update", Short: "Change a provider's metadata, domains or organization", Positionals: []string{"<name>", "<provider-id>"}, Flags: []cliFlag{
					{Name: "metadata-url", Value: "URL", Short: "the identity provider's HTTPS SAML metadata URL"},
					{Name: "metadata-file", Value: "FILE", Short: "a file holding the identity provider's SAML metadata XML"},
					{Name: "domains", Value: "DOMAINS", Short: "comma-separated email domains; replaces the list"},
					{Name: "org", Value: "ORG", Short: "organization (ID or slug) its users join, or none"},
					{Name: "role", Value: "ROLE", Short: "role its users join the organization with (not owner)"},
					{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"},
				}},
				{Name: "remove", Short: "Remove an identity provider; its users keep their memberships", Positionals: []string{"<name>", "<provider-id>"}, Flags: []cliFlag{{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"}}},
				{Name: "scim-token", Short: "Create the token an identity provider uses to provision people through SCIM, or revoke it", Positionals: []string{"<name>", "<provider-id>"}, Flags: []cliFlag{
					{Name: "revoke", Short: "revoke the token instead of creating one"},
					{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"},
				}},
			}},
			{Name: "oauth-clients", Short: "Register the apps and MCP clients that may use the Auth app as their OAuth 2.1 server", Subcommands: []cliSub{
				{Name: "list", Short: "List OAuth clients, owner-registered and self-registered", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"}}},
				{Name: "add", Short: "Register a client; a confidential client's secret is shown once", Positionals: []string{"<name>"}, Flags: []cliFlag{
					{Name: "name", Value: "NAME", Short: "the client's name, shown on the consent page"},
					{Name: "redirect-uri", Value: "URI", Req: true, Short: "an exact redirect URI (HTTPS, loopback HTTP or an app scheme); repeat for more"},
					{Name: "type", Value: "TYPE", Short: "public (PKCE, no secret) or confidential (keeps a secret)", ClosedSet: []string{"public", "confidential"}},
					{Name: "client-uri", Value: "URL", Short: "the client's HTTPS home page"},
					{Name: "logo-uri", Value: "URL", Short: "the client's HTTPS logo URL"},
					{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"},
				}, Examples: []string{"printf '%s' \"$ADMIN_KEY\" | gregale auth oauth-clients add shop-auth --name shop-cli --redirect-uri http://127.0.0.1:8976/callback --admin-key-stdin"}},
				{Name: "update", Short: "Change a client's name, redirect URIs or links", Positionals: []string{"<name>", "<client-id>"}, Flags: []cliFlag{
					{Name: "name", Value: "NAME", Short: "the client's name, shown on the consent page"},
					{Name: "redirect-uri", Value: "URI", Short: "an exact redirect URI; repeat for more; replaces the list"},
					{Name: "client-uri", Value: "URL", Short: "the client's HTTPS home page"},
					{Name: "logo-uri", Value: "URL", Short: "the client's HTTPS logo URL"},
					{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"},
				}},
				{Name: "remove", Short: "Remove a client; it can no longer get tokens", Positionals: []string{"<name>", "<client-id>"}, Flags: []cliFlag{{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"}}},
				{Name: "rotate-secret", Short: "Replace a confidential client's secret; the new one is shown once", Positionals: []string{"<name>", "<client-id>"}, Flags: []cliFlag{{Name: "admin-key-stdin", Short: "read the admin key from stdin (prompted on a terminal)"}}},
			}},
		},
	},
	{
		Name: "pipeline", DocSlug: "data-pipelines", Short: "Connect PostgreSQL sources and operate synchronized read-only mirrors",
		Subcommands: []cliSub{
			{Name: "connection", Short: "Register and manage encrypted source connections", Subcommands: []cliSub{
				{Name: "add", Short: "Register a PostgreSQL source", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "url-file", Value: "FILE", Req: true, Short: "private file containing a verified-TLS source URL"}}},
				{Name: "list", Short: "List sources without credentials"},
				{Name: "check", Short: "Check source compatibility and discover public tables", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "database", Value: "ID", Short: "also check an existing empty managed mirror"}, {Name: "wait", Short: "wait for a fresh result (default true; use --wait=false to queue only)"}, {Name: "timeout", Value: "DURATION", Short: "maximum wait (default 2m; maximum 1h)"}}},
				{Name: "tables", Short: "Read the latest checked source schema and compatibility issues", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "wait", Short: "wait for a fresh result (default true)"}, {Name: "timeout", Value: "DURATION", Short: "maximum wait (default 2m; maximum 1h)"}}},
				{Name: "rotate", Short: "Rotate a source credential without changing its database identity", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "url-file", Value: "FILE", Req: true, Short: "private file containing the new source URL"}}},
				{Name: "delete", Short: "Delete an unused source credential", Positionals: []string{"<id>"}},
			}},
			{Name: "create", Short: "Create a managed mirror and begin initial copying", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "connection", Value: "ID", Short: "registered source connection (or use --url-file)"}, {Name: "url-file", Value: "FILE", Short: "register a source from a private URL file"}, {Name: "database", Value: "DATABASE", Short: "existing empty managed database; otherwise create NAME-mirror"}, {Name: "region", Value: "REGION", Short: "placement of an automatically created mirror"}, {Name: "tables", Value: "TABLES", Req: true, Short: "comma-separated public table names"}, {Name: "read-subjects", Value: "SUBJECTS", Req: true, Short: "application identities allowed to read the mirror"}, {Name: "wait", Short: "wait for initial copying and catch-up"}, {Name: "timeout", Value: "DURATION", Short: "maximum wait (default 10m; maximum 1h)"}, {Name: "api", Value: "NAME", Short: "deploy a read-only Data API after initial sync", Unreleased: true}, {Name: "auth", Value: "AUTH_APP", Short: "Auth app used by --api", Unreleased: true},
			}},
			{Name: "list", Short: "List pipeline intent and observations"},
			{Name: "status", Short: "Read pipeline intent and observed progress", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "wait", Short: "wait for the requested observation"}, {Name: "timeout", Value: "DURATION", Short: "maximum wait (default 10m; maximum 1h)"}}},
			{Name: "wait", Short: "Wait for the pipeline to become live", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "wait", Short: "wait for the requested observation"}, {Name: "timeout", Value: "DURATION", Short: "maximum wait (default 10m; maximum 1h)"}}},
			{Name: "pause", Short: "Request a pause", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "wait", Short: "wait for the requested observation"}, {Name: "timeout", Value: "DURATION", Short: "maximum wait (default 10m; maximum 1h)"}}},
			{Name: "resume", Short: "Resume replication", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "wait", Short: "wait for the requested observation"}, {Name: "timeout", Value: "DURATION", Short: "maximum wait (default 10m; maximum 1h)"}}},
			{Name: "resync", Short: "Replace the owned mirror with a fresh copy", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "wait", Short: "wait for the requested observation"}, {Name: "timeout", Value: "DURATION", Short: "maximum wait (default 10m; maximum 1h)"}}},
			{Name: "verify", Short: "Request a bounded consistency check", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "wait", Short: "wait for the requested observation"}, {Name: "timeout", Value: "DURATION", Short: "maximum wait (default 10m; maximum 1h)"}}},
			{Name: "delete", Short: "Clean up replication while retaining mirror data", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "wait", Short: "wait for the requested observation"}, {Name: "timeout", Value: "DURATION", Short: "maximum wait (default 10m; maximum 1h)"}}},
		},
	},
	{
		Name: "data-api", DocSlug: "data-api", Short: "Create schema-generated PostgreSQL APIs and export application types",
		Subcommands: []cliSub{
			{Name: "transaction", Short: "Build a typed atomic business-operation starter", Subcommands: []cliSub{{Name: "init", Short: "Generate an editable tenant-scoped order/inventory RPC package", Flags: []cliFlag{{Name: "name", Value: "NAME", Req: true, Short: "new function/table prefix"}, {Name: "output", Value: "DIR", Req: true, Short: "new transaction package directory"}, {Name: "contract", Value: "VERSION", Short: "receipt contract (default v1)", ClosedSet: []string{"v1", "v2"}}}}}},
			{Name: "rotation", Short: "Inspect credential rotation prerequisites and rollout evidence", Subcommands: []cliSub{
				{Name: "preflight", Short: "Check owner catalog prerequisites and current binding rollout", Positionals: []string{"<name>"}, Flags: []cliFlag{
					{Name: "binding", Value: "UUID", Req: true, Short: "managed Data API binding UUID"},
					{Name: "owner-check", Value: "FILE", Req: true, Short: "trusted local read-only owner preflight command configuration"},
					{Name: "timeout", Value: "DURATION", Short: "check deadline (default 2m, maximum 1h)"},
				}},
				{Name: "status", Short: "Inspect binding refresh and credential retirement metadata", Positionals: []string{"<name>"}, Flags: []cliFlag{
					{Name: "binding", Value: "UUID", Req: true, Short: "managed Data API binding UUID"},
					{Name: "timeout", Value: "DURATION", Short: "check deadline (default 2m, maximum 1h)"},
				}},
			}},
			{Name: "verify", Short: "Verify named data integrity assertions and project authorization locally", Subcommands: []cliSub{{Name: "release", Short: "Run local Data API release checks and consolidate reports", Flags: []cliFlag{{Name: "config", Value: "FILE", Req: true, Short: "version 1 release-check configuration"}}}, {Name: "coverage", Short: "Check customer-declared verification coverage offline", Flags: []cliFlag{
				{Name: "schema", Value: "FILE", Req: true, Short: "current saved schema contract"},
				{Name: "policy", Value: "FILE", Req: true, Short: "explicit relation requirements and named check mappings"},
				{Name: "verification", Value: "FILE", Short: "data-api verify JSON report"},
				{Name: "plans", Value: "FILE", Short: "budget-checked data-api explain JSON report"},
				{Name: "baseline-schema", Value: "FILE", Short: "previous contract to identify newly added relations"},
				{Name: "check", Short: "fail if customer-required coverage is missing"},
			}}, {Name: "init", Short: "Generate editable verification templates offline", Flags: []cliFlag{
				{Name: "schema", Value: "FILE", Req: true, Short: "saved schema contract"},
				{Name: "config", Value: "FILE", Req: true, Short: "development config containing identity names"},
				{Name: "output", Value: "DIR", Req: true, Short: "new verification package directory"},
				{Name: "plans", Value: "FILE", Short: "successful explain JSON report for query budgets"},
			}}}, Flags: []cliFlag{
				{Name: "directory", Value: "DIR", Short: "Data API project directory (default .)"},
				{Name: "config", Value: "FILE", Short: "project-relative development config"},
				{Name: "checks", Value: "FILE", Short: "version 1 named SQL assertion manifest (or use --access-checks)"},
				{Name: "access-checks", Value: "FILE", Short: "request collection or version 3/4/5 guided RLS check manifest"},
				{Name: "fixture", Value: "FILE", Short: "SQL fixture for the current schema (default: configured seed)"},
			}, Examples: []string{"gregale --json data-api verify --checks data-checks.json --fixture fixture.sql"}},
			{Name: "explain", Short: "Review saved read-query plans against a disposable local database", Flags: []cliFlag{
				{Name: "directory", Value: "DIR", Short: "Data API project directory (default .)"},
				{Name: "config", Value: "FILE", Short: "project-relative development config"},
				{Name: "collection", Value: "FILE", Req: true, Short: "saved API request collection"},
				{Name: "fixture", Value: "FILE", Short: "representative SQL fixture (default: configured seed)"},
				{Name: "scenario", Value: "NAME", Short: "one named collection scenario"},
				{Name: "baseline", Value: "FILE", Short: "previous explain JSON report for estimated plan comparison"},
				{Name: "check", Short: "fail when query budgets or coverage checks fail"},
				{Name: "budget", Value: "FILE", Short: "version 1 query budget JSON (requires --check and --baseline)"},
			}, Examples: []string{"gregale --json data-api explain --collection data-api.requests.json --fixture fixtures/before-upgrade/fixture.sql"}},
			{Name: "fixture", Short: "Build representative migration rehearsal data", Subcommands: []cliSub{
				{Name: "build", Short: "Export allowlisted and explicitly masked related rows", Flags: []cliFlag{
					{Name: "config", Value: "FILE", Req: true, Short: "JSON column allowlists, masks and root filters"},
					{Name: "output", Value: "DIR", Req: true, Short: "new fixture package directory"},
					{Name: "database-url-env", Value: "ENV", Short: "environment variable holding the local or test PostgreSQL URL (default DATA_API_FIXTURE_DATABASE_URL)"},
					{Name: "api-role", Value: "ROLE", Short: "source API role used for contract introspection"},
				}, Examples: []string{"gregale --json data-api fixture build --config fixture-policy.json --output fixtures/before-upgrade --api-role gregale_data_api"}},
			}},
			{Name: "migration", Short: "Rehearse local database migrations", Subcommands: []cliSub{
				{Name: "guide", Short: "Generate reviewable client migration guidance offline", Flags: []cliFlag{
					{Name: "baseline", Value: "FILE", Req: true, Short: "previous schema contract"},
					{Name: "current", Value: "FILE", Req: true, Short: "candidate schema contract"},
					{Name: "requests", Value: "FILE", Short: "saved API request collection"},
					{Name: "dependencies", Value: "FILE", Short: "customer-declared consumer dependencies"},
					{Name: "coverage-policy", Value: "FILE", Short: "explicit verification mappings to review"},
				}},
				{Name: "preview", Short: "Rebuild a disposable database and report API, types, client and replay checks", Flags: []cliFlag{
					{Name: "directory", Value: "DIR", Short: "Data API project directory (default .)"},
					{Name: "from-version", Value: "N", Short: "upgrade from an existing numbered migration version"},
					{Name: "fixture", Value: "FILE", Short: "SQL data fixture for the older schema (default: configured seed)"},
					{Name: "data-check", Value: "FILE", Short: "JSON manifest of post-migration SQL data assertions"},
					{Name: "baseline", Value: "FILE", Req: true, Short: "read-only schema contract baseline"},
					{Name: "config", Value: "FILE", Short: "project-relative development config"},
					{Name: "output", Value: "FILE", Short: "committed types path (default: project config or client/src/database.types.ts)"},
					{Name: "dependencies", Value: "FILE", Short: "customer-declared consumer dependency manifest"},
					{Name: "approval", Value: "FILE", Short: "fingerprint-bound breaking change approvals"},
					{Name: "replay", Value: "FILE", Short: "saved request collection"},
					{Name: "scenario", Value: "NAME", Short: "one named replay scenario (requires --replay)"},
				}, Examples: []string{"gregale --json data-api migration preview --baseline schema.json --replay data-api.requests.json"}},
			}},
			{Name: "onboard", Short: "Create and verify a local typed application with two-user isolation", Flags: []cliFlag{
				{Name: "template", Value: "NAME", Short: "data-api-starter or data-api-teams", ClosedSet: []string{"data-api-starter", "data-api-teams"}},
				{Name: "path", Value: "DIR", Short: "new starter project directory", Req: true},
				{Name: "bundle", Value: "MANIFEST", Short: "approved local data-api-bundle.json", Req: true},
			}, Examples: []string{"gregale data-api onboard --path notes --bundle /path/to/data-api-bundle.json"}},
			{Name: "install", Short: "Install or restore a verified Data API SDK bundle", Flags: []cliFlag{
				{Name: "bundle", Value: "MANIFEST", Short: "approved local data-api-bundle.json"},
				{Name: "directory", Value: "DIR", Short: "project directory (default .)"},
				{Name: "client-directory", Value: "DIR", Short: "client directory relative to the project (default .)"},
				{Name: "base-url", Value: "HTTPS_URL", Short: "immutable artifact directory for future restoration"},
				{Name: "restore", Short: "restore the committed artifact pin"},
				{Name: "from", Value: "DIR", Short: "local artifact directory for restoration"},
			}, Examples: []string{"gregale data-api install --bundle /path/to/data-api-bundle.json", "gregale data-api install --restore"}},
			{Name: "dev", Short: "Run a disposable local Data API with test identities", Flags: []cliFlag{
				{Name: "config", Value: "FILE", Short: "project-relative development config (default: data-api.dev.json)"},
				{Name: "directory", Value: "DIR", Short: "project directory (default .)"},
				{Name: "output", Value: "FILE", Short: "types output relative to the project"},
				{Name: "port", Value: "PORT", Short: "loopback gateway port (default 8080; 0 selects a free port)"},
				{Name: "check", Short: "run configured client checks"},
				{Name: "check-types", Short: "compare committed types without rewriting them (requires --once)"},
				{Name: "validators", Short: "generate runtime table and RPC argument validators alongside types"},
				{Name: "once", Short: "verify and exit, removing disposable containers"},
				{Name: "watch", Short: "watch migrations and refresh API/types (default true; use --watch=false to disable)"},
				{Name: "baseline", Value: "FILE", Short: "read-only saved contract (default: last successful local contract)"},
				{Name: "check-breaking", Short: "reject breaking changes; requires an existing baseline"},
				{Name: "approval", Value: "FILE", Short: "reviewed compatibility approvals; requires --baseline and --check-breaking"},
				{Name: "accepted-contracts", Value: "FILE", Short: "project-relative reviewed runtime contract policy"},
				{Name: "replay", Value: "FILE", Short: "replay saved requests relative to the project (requires --once)"},
				{Name: "scenario", Value: "NAME", Short: "select one named scenario (requires --replay)"},
			}, Examples: []string{"gregale data-api dev --check", "gregale data-api dev --port 0 --check --once"}},
			{Name: "create", Short: "Deploy a managed PostgREST Data API", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "database", Value: "DATABASE", Req: true, Short: "ready managed database name or ID"},
				{Name: "schema", Value: "SCHEMA", Short: "exposed schema (api, or public with --read-only)"},
				{Name: "read-only", Short: "use a SELECT-only public-schema binding and reject mutations"},
				{Name: "scope", Value: "SCOPE", Short: "environment scope"},
				{Name: "auth", Value: "AUTH_APP", Short: "Auth app whose tokens to verify (replaces --issuer, --jwks-url and --audience)", Unreleased: true},
				{Name: "issuer", Value: "HTTPS_URL", Short: "application JWT issuer"},
				{Name: "jwks-url", Value: "HTTPS_URL", Short: "application JWKS URL"},
				{Name: "audience", Value: "AUDIENCE", Short: "application JWT audience"},
				{Name: "origins", Value: "ORIGINS", Short: "comma-separated browser origins"},
				{Name: "resume", Short: "resume configuration and deployment of an existing app"},
			}},
			{Name: "types", Short: "Generate types in an owner-authenticated app task", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "output", Value: "FILE", Short: "generated TypeScript or snapshot output"},
				{Name: "check", Short: "fail if the output file is stale"},
				{Name: "snapshot", Short: "export a JSON baseline for data-api diff"},
				{Name: "validators", Short: "include typed runtime table and RPC argument validators"},
				{Name: "timeout", Value: "DURATION", Short: "task wait deadline (default 2m)"},
			}},
			{Name: "check", Short: "Check compatibility and committed types without rewriting files", Positionals: []string{"[<name>]"}, Flags: []cliFlag{
				{Name: "baseline", Value: "FILE", Req: true, Short: "committed JSON contract baseline"},
				{Name: "types", Value: "FILE", Req: true, Short: "committed TypeScript types to compare"},
				{Name: "current", Value: "FILE", Short: "saved current contract for an offline check; omit the app name"},
				{Name: "approval", Value: "FILE", Short: "reviewed breaking changes bound to both fingerprints"},
				{Name: "timeout", Value: "DURATION", Short: "entire check deadline (default 2m, maximum 1h)"},
			}, Examples: []string{"gregale --json data-api check notes-data --baseline schema.json --types client/src/database.types.ts", "gregale data-api check --current candidate.json --baseline schema.json --types client/src/database.types.ts"}},
			{Name: "diff", Short: "Compare the current schema with a saved JSON baseline", Positionals: []string{"[<name>]"}, Flags: []cliFlag{
				{Name: "baseline", Value: "FILE", Req: true, Short: "JSON snapshot exported with types --snapshot"},
				{Name: "current", Value: "FILE", Short: "compare a saved current contract offline; omit the app name"},
				{Name: "check", Short: "fail on unapproved breaking contract changes"},
				{Name: "check-compatible", Short: "fail on unapproved breaking changes (alias of --check)"},
				{Name: "approval", Value: "FILE", Short: "reviewed changes bound to both contract fingerprints"},
				{Name: "accept-contract", Value: "FILE", Short: "write an owner runtime policy; requires zero breaking changes"},
				{Name: "dependencies", Value: "FILE", Short: "customer-declared saved-view and RPC dependencies for impact reporting"},
				{Name: "timeout", Value: "DURATION", Short: "task wait deadline (default 2m)"},
			}, Examples: []string{"gregale data-api diff notes-data --baseline schema.json --check"}},
			{Name: "refresh", Short: "Request a fresh restart to reload the database schema", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "wait", Short: "wait for fresh-restart completion and Data API readiness"},
				{Name: "timeout", Value: "DURATION", Short: "complete wait deadline (default 5m, maximum 1h; requires --wait)"},
			}, Examples: []string{"gregale data-api refresh notes-data --wait --timeout 5m"}},
			{Name: "replay", Short: "Verify saved authorization scenarios against the hosted API", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "collection", Value: "FILE", Req: true, Short: "saved request collection"},
				{Name: "identities", Value: "FILE", Req: true, Short: "JSON map of identities to JWT environment variables"},
				{Name: "scenario", Value: "NAME", Short: "select one named scenario"},
				{Name: "refresh-token-env", Value: "ENV", Short: "optional private refresh token environment variable"},
				{Name: "timeout", Value: "DURATION", Short: "replay deadline (default 5m, maximum 1h)"},
			}, Examples: []string{"gregale data-api replay notes-data --collection data-api.requests.json --identities data-api.identities.json --scenario notes-rls"}},
			{Name: "health", Short: "Check current Data API health and recent request failures", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "since", Value: "DURATION|RFC3339", Short: "retained request log window (default 15m)"},
				{Name: "timeout", Value: "DURATION", Short: "entire report deadline (default 30s, maximum 1h)"},
			}, Examples: []string{"gregale data-api health notes-data", "gregale --json data-api health notes-data --since 15m"}},
			{Name: "diagnostics", Short: "Summarize retained request logs and look up request IDs", Positionals: []string{"[<name>]"}, Flags: []cliFlag{
				{Name: "file", Value: "FILE", Short: "saved runtime NDJSON or JSON log envelopes; omit the app name"},
				{Name: "since", Value: "DURATION|RFC3339", Short: "request time lower bound (for example 15m)"},
				{Name: "deployment", Value: "ID", Short: "restrict hosted logs to a deployment"},
				{Name: "request-id", Value: "UUID", Short: "look up one gateway-generated request ID"},
				{Name: "sort", Value: "requests|p95|errors", Short: "query shape ranking (default requests; p95 uses gateway latency)"},
				{Name: "timeout", Value: "DURATION", Short: "read deadline (default 2m, maximum 1h)"},
			}, Examples: []string{"gregale --json data-api diagnostics notes-data --since 15m", "gregale data-api diagnostics --file request-logs.ndjson"}},
			{Name: "status", Short: "Inspect serving contracts and durable refresh state", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "baseline", Value: "FILE", Short: "optional JSON snapshot to compare with the serving contract"},
				{Name: "access-token-env", Value: "ENV", Short: "application JWT environment variable"},
				{Name: "refresh-token-env", Value: "ENV", Short: "optional private refresh token environment variable"},
			}, Examples: []string{"gregale --json data-api status notes-data"}},
			{Name: "sync", Short: "Run migrations, refresh the API, export types and check the client", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "check", Short: "check coordinated setup without pausing, migrating or restarting"},
				{Name: "resume-only", Short: "reverify an owned paused refresh without rerunning migrations"},
				{Name: "config", Value: "FILE", Req: true, Short: "JSON workflow with output, migrate and check argument arrays"},
				{Name: "timeout", Value: "DURATION", Short: "entire workflow deadline (default 20m, maximum 1h)"},
			}, Examples: []string{"gregale data-api sync notes-data --config data-api.json"}},
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
			{Name: "create", Short: "Create a managed PostgreSQL database", Positionals: []string{"<name>"}, Flags: []cliFlag{
				{Name: "region", Short: "provider-neutral region", Req: true, Value: "REGION"},
				{Name: "postgres-major", Short: "PostgreSQL major version", Value: "N"},
				{Name: "class", Short: "service class", Value: "CLASS", ClosedSet: []string{"development", "burstable", "production"}},
				{Name: "availability", Short: "availability mode", Value: "MODE", ClosedSet: []string{"single_zone", "high_availability"}},
				{Name: "scale-to-zero", Short: "suspend compute when idle"},
				{Name: "storage-bytes", Short: "storage limit in bytes", Value: "N"},
				{Name: "restore-window-seconds", Short: "point-in-time restore window", Value: "N"},
			}, Examples: []string{"gregale postgres create orders --region us-east-2 --class development --scale-to-zero"}},
			{Name: "get", Short: "Show one managed PostgreSQL database", Positionals: []string{"<database>"}},
			{Name: "delete", Short: "Delete a managed PostgreSQL database", Positionals: []string{"<database>"}},
			{Name: "compute-policy", Short: "Durably switch between idle suspension and always-on compute", Positionals: []string{"<database>"}, Flags: []cliFlag{
				{Name: "scale-to-zero", Short: "true for idle suspension, false for always-on", Req: true, Value: "BOOL", ClosedSet: []string{"true", "false"}},
				{Name: "request-id", Short: "stable request UUID; reuse after uncertain responses", Req: true, Value: "UUID"},
			}},
			{Name: "compute-policy-status", Short: "Read compute policy change progress", Positionals: []string{"<database>", "<request_uuid>"}},
			{Name: "resize", Short: "Durably resize compute; clients may disconnect", Positionals: []string{"<database>"}, Flags: []cliFlag{
				{Name: "class", Short: "target service class", Req: true, Value: "CLASS", ClosedSet: []string{"development", "burstable", "production"}},
				{Name: "request-id", Short: "stable request UUID; reuse after uncertain responses", Req: true, Value: "UUID"},
			}},
			{Name: "resize-status", Short: "Read compute resize progress", Positionals: []string{"<database>", "<request_uuid>"}},
			{Name: "restore", Short: "Restore a database to a new database", Positionals: []string{"<database>"}, Flags: []cliFlag{
				{Name: "name", Short: "name for the restored database", Req: true, Value: "NAME"},
				{Name: "point-in-time", Short: "RFC3339 restore timestamp", Req: true, Value: "TIMESTAMP"},
			}},
			{Name: "live", Short: "Enable live queries and manage the live secret (operator preview)", Subcommands: []cliSub{
				{Name: "status", Short: "Show whether live queries are enabled and the live secrets' key IDs", Positionals: []string{"<database>"}},
				{Name: "enable", Short: "Enable live queries and issue the live secret", Positionals: []string{"<database>"}, Flags: []cliFlag{
					{Name: "app", Short: "store the secret as an app secret on this app", Value: "APP"},
					{Name: "scope", Short: "environment scope for --app", Value: "SCOPE"},
					{Name: "env", Short: "secret name for --app (default GREGALE_LIVE_SECRET)", Value: "NAME"},
				}, Examples: []string{"gregale postgres live enable orders --app web"}},
				{Name: "rotate", Short: "Issue a new live secret; the previous one retires after the overlap", Positionals: []string{"<database>"}, Flags: []cliFlag{
					{Name: "app", Short: "store the secret as an app secret on this app", Value: "APP"},
					{Name: "scope", Short: "environment scope for --app", Value: "SCOPE"},
					{Name: "env", Short: "secret name for --app (default GREGALE_LIVE_SECRET)", Value: "NAME"},
					{Name: "overlap", Short: "how long the previous secret keeps working (default 24h, 0 revokes it at once)", Value: "DURATION"},
				}},
				{Name: "disable", Short: "Revoke every live secret and turn live queries off", Positionals: []string{"<database>"}},
			}},
			{Name: "cutover", Short: "Stage and verify a restore target without moving workloads", Subcommands: []cliSub{
				{Name: "prepare", Short: "Stage all source bindings for one app and scope", Positionals: []string{"<source>", "<target>", "<app>"}, Flags: []cliFlag{{Name: "scope", Short: "environment scope", Value: "SCOPE"}}},
				{Name: "get", Short: "Read cutover progress and SQL evidence", Positionals: []string{"<id>"}},
				{Name: "verify", Short: "Queue control-plane SQL verification", Positionals: []string{"<id>"}},
				{Name: "cancel", Short: "Revoke staged credentials and release pins", Positionals: []string{"<id>"}},
			}},
			{Name: "bindings", Short: "Manage app database bindings", Subcommands: []cliSub{
				{Name: "list", Short: "List a database's bindings", Positionals: []string{"<database>"}},
				{Name: "create", Short: "Bind a database to an app by app ID (attach takes slugs)", Positionals: []string{"<database_id>"}, Flags: []cliFlag{
					{Name: "app", Short: "app ID", Req: true, Value: "APP_ID"},
					{Name: "scope", Short: "environment scope", Req: true, Value: "SCOPE"},
					{Name: "environment-key", Short: "environment variable name", Req: true, Value: "KEY"},
					{Name: "access", Short: "credential access", Value: "MODE", ClosedSet: []string{"read_write", "read_only", "migration", "data_api"}},
				}},
				{Name: "get", Short: "Show one binding", Positionals: []string{"<id>"}},
				{Name: "delete", Short: "Delete a binding", Positionals: []string{"<id>"}},
				{Name: "rotate", Short: "Rotate a binding and optionally wait for the previous credential to retire", Positionals: []string{"<id>"}, Flags: []cliFlag{
					{Name: "wait", Short: "wait for the previous credential to retire"},
					{Name: "wait-timeout", Short: "maximum time to wait for rotation (default 5m)", Value: "DURATION"},
					{Name: "poll-interval", Short: "status polling interval while waiting (default 1s)", Value: "DURATION"},
				}, Examples: []string{"gregale postgres bindings rotate BINDING_ID --wait"}},
			}},
			{Name: "attach", Short: "Attach a database to an app", Positionals: []string{"<database>", "<app>"}, Flags: []cliFlag{
				{Name: "scope", Short: "environment scope (defaults to linked project environment, otherwise default)", Value: "SCOPE"},
				{Name: "env", Short: "connection environment variable", Value: "KEY"},
				{Name: "access", Short: "credential access", Value: "MODE", ClosedSet: []string{"read_write", "read_only", "migration", "data_api"}},
			}, Examples: []string{"gregale postgres attach orders my-api --env DATABASE_URL --access read_write"}},
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
				{Name: "all", Short: "purge all events (asks for typed confirmation)"},
				{Name: "yes", Short: "skip the --all confirmation (for scripts)", Bool: true},
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
				{Name: "auth", Short: "Auth app that verifies clients and authorizes channels; replaces the callback and auth flags", Value: "AUTH_APP", Unreleased: true},
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
			{Name: "delete", Short: "Delete a managed realtime endpoint", Positionals: []string{"<app>", "<endpoint-id>"}, Flags: []cliFlag{{Name: "yes", Short: "confirm the deletion", Req: true, Bool: true}}, FlagsAfterPositionals: true},
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
		Examples:    []string{"gregale rollback my-api --interactive", "gregale rollback my-api", "gregale rollback my-api --to v41", "gregale rollback my-api --to v41 --expected-current v42 --wait"},
		Positionals: []string{"<slug>"},
		Flags: []cliFlag{
			{Name: "interactive", Short: "choose and review a historical release, confirm, and follow rollback progress", Bool: true},
			{Name: "to", Short: "target deployment id or vN revision (e.g. v41)", Value: "deployment_id|vN"},
			{Name: "expected-current", Short: "exact completed serving deployment; requires --to", Value: "deployment_id|vN"},
			{Name: "reason", Short: "one-line reason of at most 256 bytes; requires --expected-current", Value: "TEXT"},
			{Name: "acknowledge-releases", Short: "roll back even though release commands ran after the target last served; rollback does not undo them", Bool: true},
			{Name: "wait", Short: "wait until the rollback completes (default for a plain rollback; a checked rollback waits only with this flag)"},
			{Name: "no-wait", Short: "return once a plain rollback is requested instead of waiting for the previous release to serve"},
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
				{Name: "release-sets", Short: "List release graphs; qualify activation, rollback and deactivation with binding checks", Subcommands: []cliSub{
					{Name: "stage", Short: "Lease billable capacity for exact candidate services without switching routes", Positionals: []string{"<project>", "<environment>"}, Flags: []cliFlag{{Name: "file", Value: "FILE", Req: true, Short: "prepared forward or rollback manifest JSON"}, {Name: "ttl-seconds", Value: "N", Short: "lease duration, 1–1800 seconds; repeating does not extend"}}},
					{Name: "stage-status", Short: "Observe staging readiness, blockers and expiry", Positionals: []string{"<project>", "<environment>"}, Flags: []cliFlag{{Name: "stage", Value: "UUID", Req: true, Short: "stage identity"}}},
					{Name: "cancel-stage", Short: "Cancel temporary candidate capacity", Positionals: []string{"<project>", "<environment>"}, Flags: []cliFlag{{Name: "stage", Value: "UUID", Req: true, Short: "stage identity"}}},
					{Name: "prepare-rollback", Short: "Select retained historical graph and freeze current routing and policies", Positionals: []string{"<project>", "<environment>"}, Flags: []cliFlag{{Name: "file", Value: "FILE", Req: true, Short: "historical release ID and retention TTL JSON"}}},
					{Name: "rollback", Short: "Atomically recover a qualified historical graph and record recovery lineage", Positionals: []string{"<project>", "<environment>"}, Flags: []cliFlag{{Name: "file", Value: "FILE", Req: true, Short: "prepared recovery manifest JSON"}}},
					{Name: "prepare", Short: "Freeze candidate graph, current routing and enforced policies; emit a JSON manifest", Positionals: []string{"<project>", "<environment>"}, Flags: []cliFlag{{Name: "file", Value: "FILE", Req: true, Short: "release selection or prepared manifest JSON"}}},
					{Name: "verification-runs", Short: "Inspect, resume or cancel durable graph qualification", Subcommands: []cliSub{
						{Name: "list", Short: "List retained graph receipt roots", Positionals: []string{"<project>", "<environment>"}, Flags: []cliFlag{{Name: "limit", Value: "N", Short: "page size (default 20; range 1–100)"}, {Name: "cursor", Value: "CURSOR", Short: "next page cursor; reuse filters"}, {Name: "deployment", Value: "UUID", Short: "exact deployment filter"}, {Name: "status", Value: "STATUS", Short: "stored state or observed expiry"}}},
						{Name: "diagnostics", Short: "Explain blockers, public changes and recovery actions", Positionals: []string{"<project>", "<environment>", "<run-id>"}},
						{Name: "status", Short: "Inspect the frozen graph and member progress", Positionals: []string{"<project>", "<environment>", "<run-id>"}},
						{Name: "resume", Short: "Resume the original graph without duplicate work", Positionals: []string{"<project>", "<environment>", "<run-id>"}, Flags: []cliFlag{{Name: "progress", Value: "MODE", Short: "durable stderr progress: auto, plain, ndjson or off"}, {Name: "status-read-retries", Value: "N", Short: "transient durable status GET retries (default 3; range 0–8)"}, {Name: "concurrency", Value: "N", Short: "shared graph probe budget (1–8)"}, {Name: "poll-interval", Value: "DURATION", Short: "polling interval"}, {Name: "wait-timeout", Value: "DURATION", Short: "remaining wait budget"}}},
						{Name: "cancel", Short: "Fence graph admissions and cancel unfinished owned work", Positionals: []string{"<project>", "<environment>", "<run-id>"}},
					}},
					{Name: "verify", Short: "Qualify the prepared graph without changing routes", Positionals: []string{"<project>", "<environment>"}, Flags: []cliFlag{{Name: "durable", Short: "persist graph and member receipts for recovery"}, {Name: "run-id", Value: "UUID", Short: "caller-selected creation recovery identity"}, {Name: "progress", Value: "MODE", Short: "durable stderr progress: auto, plain, ndjson or off"}, {Name: "status-read-retries", Value: "N", Short: "transient durable status GET retries (default 3; range 0–8)"}, {Name: "concurrency", Value: "N", Short: "shared graph probe limit (default 2; range 1–8)"}, {Name: "file", Value: "FILE", Req: true, Short: "release selection or prepared manifest JSON"}, {Name: "dry-run", Short: "preview every member without tasks"}, {Name: "queue-publish", Short: "consent to planned safe queue messages"}, {Name: "object-storage-write", Short: "consent to planned disposable object canaries"}, {Name: "wait-timeout", Value: "DURATION", Short: "graph qualification time budget"}, {Name: "poll-interval", Value: "DURATION", Short: "probe polling interval"}}},
					{Name: "activate", Short: "Atomically activate after checking every member and service recipient", Positionals: []string{"<project>", "<environment>"}, Flags: []cliFlag{{Name: "file", Value: "FILE", Req: true, Short: "release selection or prepared manifest JSON"}}},
					{Name: "prepare-deactivation", Short: "Freeze the active graph and every 100% fallback; emit a qualification manifest", Positionals: []string{"<project>", "<environment>"}},
					{Name: "deactivate", Short: "Atomically return to qualified fallback routes and record deactivation", Positionals: []string{"<project>", "<environment>"}, Flags: []cliFlag{{Name: "file", Value: "FILE", Req: true, Short: "prepared deactivation manifest JSON"}}},
				}, Flags: []cliFlag{{Name: "before", Value: "CURSOR", Short: "alias for --cursor"}, {Name: "cursor", Value: "CURSOR", Short: "opaque continuation cursor"}, {Name: "all", Short: "walk every page"}, {Name: "limit", Value: "N", Short: "page size"}}},
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
						{Name: "provider", Value: "PROVIDER", Short: "github (default) or gitlab"},
						{Name: "repository", Value: "GROUP/PROJECT", Short: "GitLab.com repository path"},
						{Name: "approval-policy", Value: "POLICY", Short: "manual or protected_branch (GitHub only)"},
						{Name: "ref", Value: "REF", Short: "Git ref selecting revision candidates"},
						{Name: "mode", Value: "MODE", Short: "report (default; enforcement unavailable in preview)"},
						{Name: "prune", Short: "Allow removal of previously owned fields"},
					}},
					{Name: "rebind", Positionals: []string{"<project>", "<environment>"}, Short: "Release ownership and replace the source binding", Flags: []cliFlag{{Name: "expected-generation", Value: "N", Short: "Reviewed current source generation", Req: true}, {Name: "manifest-path", Value: "PATH", Short: "Replacement definition path", Req: true}, {Name: "ref", Value: "REF", Short: "Replacement Git ref"}, {Name: "approval-policy", Value: "POLICY", Short: "manual or protected_branch"}, {Name: "yes", Short: "Confirm ownership release while preserving values"}}},
					{Name: "unbind", Positionals: []string{"<project>", "<environment>"}, Short: "Disconnect the source and release its ownership", Flags: []cliFlag{{Name: "expected-generation", Value: "N", Short: "Reviewed current source generation", Req: true}, {Name: "yes", Short: "Confirm ownership release while preserving values"}}},
					{Name: "review", Positionals: []string{"<project>", "<environment>"}, Short: "Fetch an immutable commit and output a review receipt", Flags: []cliFlag{{Name: "commit", Value: "SHA", Short: "Exact lowercase GitHub commit SHA"}}},
					{Name: "plan", Positionals: []string{"<project>", "<environment>"}, Short: "Preview candidate field changes without approval or writes", Flags: []cliFlag{{Name: "commit", Value: "SHA", Short: "Exact immutable commit SHA", Req: true}, {Name: "format", Value: "FORMAT", Short: "json (default) or markdown"}, {Name: "fail-on-blocked", Short: "Exit 2 after printing ownership or unsupported-operation blockers"}}, Examples: []string{"gregale projects environments gitops plan shop staging --commit COMMIT_SHA --format markdown --fail-on-blocked"}},
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
				{Name: "promote", Short: "Promote workloads or choose environments interactively", Examples: []string{"gregale projects environments promote my-project --interactive"}, Flags: []cliFlag{
					{Name: "interactive", Short: "choose source and destination, review changes, confirm, and follow progress", Bool: true},
					{Name: "from", Short: "source environment (required unless interactive)", Value: "ENV"},
					{Name: "to", Short: "target environment (required unless interactive)", Value: "ENV"},
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
			{Name: "requirements", Short: "Manage local required secret names without reading or changing values", Subcommands: []cliSub{
				{Name: "check", Short: "Compare saved required names with a dotenv example offline; exit 1 on differences or missing declaration", Flags: []cliFlag{{Name: "from-env-file", Value: "PATH", Short: "dotenv example (default: .env.example at the requirements project root); compare names only"}}, Examples: []string{"gregale secrets requirements check", "gregale secrets requirements check --from-env-file config/example.env"}},
				{Name: "set", Short: "Replace the project declaration with validated, deduplicated names", Positionals: []string{"[<KEY>...]"}, Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "preview current/proposed names and differences without writing"}, {Name: "from-env-file", Value: "PATH", Short: "import key names from a local dotenv file; excludes positional names, never stores values"}}, Examples: []string{"gregale secrets requirements set DATABASE_URL API_KEY --dry-run", "gregale secrets requirements set --from-env-file .env.example --dry-run"}},
				{Name: "add", Short: "Append required names while preserving existing names and skipping duplicates", Positionals: []string{"[<KEY>...]"}, Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "preview added names without writing"}, {Name: "from-env-file", Value: "PATH", Short: "merge key names from a local dotenv file; excludes positional names, never stores values"}}, Examples: []string{"gregale secrets requirements add REDIS_URL --dry-run", "gregale secrets requirements add --from-env-file .env.example --dry-run"}},
				{Name: "remove", Short: "Remove declared names; unknown names fail without changes", Positionals: []string{"<KEY>..."}, Flags: []cliFlag{{Name: "dry-run", Bool: true, Short: "preview removed names and explicit-empty outcome without writing"}}, Examples: []string{"gregale secrets requirements remove OLD_API_KEY --dry-run"}},
				{Name: "show", Short: "Show required names, declaration status, and source path"},
				{Name: "clear", Short: "Remove the local declaration; missing requirements mean unknown completeness"},
			}},
			{Name: "defaults", Short: "Show or manage local saved secret comparison scopes", Examples: []string{"gregale secrets defaults", "gregale secrets defaults --help"}, Subcommands: []cliSub{
				{Name: "set", Short: "Replace saved comparison scope names", Flags: []cliFlag{{Name: "environments", Value: "SCOPE,...", Short: "comma-separated scope names; 1–10 distinct names"}}},
				{Name: "show", Short: "Show saved scopes and their source file"},
				{Name: "clear", Short: "Remove only the local comparison defaults"},
			}},
			{Name: "check", Short: "Check required scoped secrets and current delivery evidence without reading values", Examples: []string{"gregale secrets check", "gregale secrets check --app my-api --required DATABASE_URL", "gregale secrets check --required-file .gregale/required-secrets.json"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug (defaults to linked app)", Value: "slug"},
				{Name: "scope", Short: "single environment scope (defaults to linked environment)", Value: "SCOPE"},
				{Name: "required", Short: "required secret key name (repeatable)", Value: "KEY", Repeatable: true},
				{Name: "required-file", Short: "explicit JSON array of required keys; overrides project discovery", Value: "PATH"},
				{Name: "missing-template", Short: "create a new private dotenv file containing only missing required names with empty values", Value: "PATH"},
				{Name: "environments", Short: "compare required names across explicit scopes or saved local scopes; excludes scope/template flags", Value: "SCOPE,...|saved"},
			}},
			{Name: "refs", Short: "Manage destination-to-source names in a registered environment", Subcommands: []cliSub{
				{Name: "list", Short: "List reference names and shared environment-key quota", Examples: []string{"gregale secrets refs list --app my-api --environment production"}, Flags: secretReferenceCLIFlags()},
				{Name: "set", Short: "Select an existing scoped secret; respects Git field ownership", Examples: []string{"gregale secrets refs set --app my-api --environment production DATABASE_URL=secret:DATABASE_PRIMARY"}, Positionals: []string{"<KEY=secret:NAME>"}, Flags: secretReferenceCLIFlags()},
				{Name: "unset", Short: "Suppress a primary workload secret destination and preserve the sealed source", Examples: []string{"gregale secrets refs unset --app my-api --environment production DATABASE_URL"}, Positionals: []string{"<KEY>"}, Flags: secretReferenceCLIFlags()},
			}},
			{Name: "list", Short: "List sealed secrets", Examples: []string{"gregale secrets list --app my-api", "gregale secrets list --app my-api --scope __all__", "gregale secrets list --app my-api --class ephemeral", "gregale secrets list --app my-api --older-than 90d"}, Flags: []cliFlag{
				{Name: "key", Short: "include an exact secret key name (repeatable)", Value: "KEY", Repeatable: true},
				{Name: "watch", Short: "follow metadata changes; JSON emits snapshot/change/unavailable events", Bool: true},
				{Name: "interval", Short: "watch poll interval (1s to 1h; default 5s)", Value: "DURATION"},
				{Name: "timeout", Short: "watch duration (zero until interrupted); expiry exits 124", Value: "DURATION"},
				{Name: "delivery-status", Short: "filter runtime-start delivery; unknown includes missing or inconsistent current-version evidence", Value: "STATUS", ClosedSet: secretDeliveryFilterValues},
				{Name: "app", Short: "app slug (defaults to linked project app)", Value: "slug"},
				{Name: "scope", Short: "env scope filter (defaults to linked project environment)", Value: "SCOPE|__all__"},
				{Name: "class", Short: "filter by snapshot-retention class", Value: "CLASS", ClosedSet: []string{api.SecretClassPersistent, api.SecretClassEphemeral}},
				{Name: "older-than", Short: "filter to secrets not updated within a duration (for example 90d or 2160h); unknown timestamps are excluded", Value: "DURATION"},
			}},
			{Name: "set", Short: "Set a sealed secret; ephemeral values disable VM snapshots for the scope", Examples: []string{"gregale secrets set --file secrets.env --dry-run", "gregale secrets set --app my-api --interactive", "gregale secrets set --app my-api DATABASE_URL=\"$DATABASE_URL\"", "printf '%s\\n' \"DATABASE_URL=$DATABASE_URL\" | gregale secrets set --app my-api --from-stdin", "gregale secrets set --app my-api DATABASE_URL=\"$DATABASE_URL\" --restart", "gregale secrets set --app my-api SESSION_TOKEN=\"$SESSION_TOKEN\" --class ephemeral"}, Positionals: []string{"[<KEY=VALUE>...]"}, Flags: []cliFlag{
				{Name: "wait-for-ack", Short: "wait for application acknowledgements for all updated keys", Bool: true},
				{Name: "timeout", Short: "restart and acknowledgement wait limit after saving (default 2m; requires --wait-for-ack)", Value: "DURATION"},
				{Name: "dry-run", Short: "validate local input and preview keys and runtime options without API requests or changes", Bool: true},
				{Name: "app", Short: "app slug (defaults to linked project app)", Value: "slug"},
				{Name: "interactive", Short: "enter hidden values and confirm names, scope, and retention before saving", Bool: true},
				{Name: "from-stdin", Short: "read KEY=VALUE pairs from stdin"},
				{Name: "file", Short: "read a private KEY=VALUE file; excludes stdin, interactive input, and positional pairs", Value: "PATH"},
				{Name: "scope", Short: "env scope to write (defaults to linked project environment)", Value: "SCOPE"},
				{Name: "class", Short: "retention: persistent by default; ephemeral disables init/warm captures and forces cold boots; omission preserves an existing class", Value: "CLASS", ClosedSet: []string{api.SecretClassPersistent, api.SecretClassEphemeral}},
				{Name: "restart", Short: "restart the app and apply updated secrets now"},
				{Name: "no-guard", Short: "do not open a config guard for this change (ADR-1451)", Bool: true},
			}},
			{Name: "unset", Short: "Remove named sealed secrets (alias: rm)", Examples: []string{"gregale secrets unset OLD_TOKEN UNUSED_KEY --dry-run", "gregale secrets unset --app my-api --interactive", "gregale secrets unset --app my-api OLD_API_KEY", "gregale secrets unset --app my-api OLD_API_KEY --scope staging", "gregale secrets unset --app my-api OLD_API_KEY --wait-for-ack", "gregale secrets unset --app my-api OLD_API_KEY --restart"}, Positionals: []string{"[<KEY>...]"}, Flags: []cliFlag{
				{Name: "dry-run", Short: "validate local input and preview keys and runtime options without API requests or changes", Bool: true},
				{Name: "interactive", Short: "choose a secret name, review scope and runtime options, and confirm removal", Bool: true},
				{Name: "app", Short: "app slug (defaults to linked project app)", Value: "slug"},
				{Name: "scope", Short: "env scope to delete from (defaults to linked project environment)", Value: "SCOPE"},
				{Name: "restart", Short: "restart the app so running instances drop the removed secret now"},
				{Name: "wait-for-ack", Short: "wait until every active authorized runtime confirms it removed the secret"},
				{Name: "timeout", Short: "maximum time to wait for runtime acknowledgements (requires --wait-for-ack)", Value: "DURATION"},
				{Name: "no-guard", Short: "do not open a config guard for this change (ADR-1451)", Bool: true},
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
				{Name: "dry-run", Short: "validate local input and preview keys and runtime options without API requests or changes", Bool: true},
				{Name: "app", Short: "app slug (defaults to linked project app)", Value: "slug"},
				{Name: "from-stdin", Short: "read one KEY=VALUE pair from stdin"},
				{Name: "scope", Short: "env scope to rotate (defaults to linked project environment)", Value: "SCOPE"},
				{Name: "restart", Short: "restart the app and apply the rotated secret now"},
				{Name: "wait-for-ack", Short: "wait until every active authorized runtime confirms it applied the secret; needs com.gregale.secret-reload-signal, even with --restart"},
				{Name: "timeout", Short: "maximum time to wait for application acknowledgements (requires --wait-for-ack)", Value: "DURATION"},
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
		Name:     "config",
		DocSlug:  "config",
		Short:    "Show or manage non-secret local CLI settings (config [get|set|list])",
		Examples: []string{"gregale config", "gregale config list", "gregale config --help"},
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
		Name:        "wake-ahead",
		DocSlug:     "service-wake-ahead",
		Short:       "Wake the services an app calls while the app itself wakes (opt-in)",
		Positionals: []string{"<slug>", "[on|off]"},
		Examples:    []string{"gregale wake-ahead my-api", "gregale wake-ahead my-api on", "gregale wake-ahead my-api off --json"},
	},
	{
		Name:    "traffic",
		DocSlug: "traffic",
		Short:   "Manage deployment traffic split (available on every plan)",
		Subcommands: []cliSub{
			{
				Name:  "set",
				Short: "Set the traffic split for a deployment",
				// hunt #8: the slug is accepted only before the flags; listing it
				// as a positional rendered it after them, an order the parser
				// rejects.
				Examples: []string{
					"gregale traffic set my-api --deployment v7 --percent 30",
					"gregale traffic set --app my-api --deployment v7 --percent 30",
				},
				Flags: []cliFlag{
					{Name: "app", Short: "app slug; only needed to resolve a vN revision outside a linked project", Value: "SLUG"},
					{Name: "deployment", Short: "deployment id or vN revision to set the traffic split on", Req: true, Value: "ID"},
					{Name: "percent", Short: "traffic weight in [0, 100]; -1 = unset (server default 100)", Req: true, Value: "N"},
				},
			},
			{
				Name:  "promote",
				Short: "Promote a live deployment to 100% production traffic",
				Examples: []string{
					"gregale traffic promote my-api --deployment v7",
					"gregale traffic promote --app my-api --deployment v7 --if-serving v6",
				},
				Flags: []cliFlag{
					{Name: "app", Short: "app slug; only needed to resolve a vN revision outside a linked project", Value: "SLUG"},
					{Name: "deployment", Short: "deployment id or vN revision to promote", Req: true, Value: "ID"},
					{Name: "if-serving", Short: "require this deployment id or vN revision to remain at 100% traffic", Value: "ID"},
					{Name: "require-bindings", Short: "enforce a bindings check at the server's traffic write"},
					{Name: "max-verification-age", Short: "maximum probe age (default 10m); requires --require-bindings", Value: "DURATION"},
					{Name: "allow-unsupported", Short: "waive unsupported queue/outbound probes; requires --require-bindings"},
					{Name: "require-application-ack", Short: "require current application acknowledgements; requires --require-bindings"},
					{Name: "route-removal-mode", Short: "opt-in CLI gate: report or enforce; requires --app and --if-serving", Value: "MODE", ClosedSet: []string{"report", "enforce"}},
					{Name: "route-readiness", Short: "readiness JSON for the serving deployment", Value: "PATH"},
					{Name: "route-mapping", Short: "reviewed successor mapping JSON", Value: "PATH"},
					{Name: "route-owner-approval", Short: "owner attestation bound to this exact change", Value: "PATH"},
					{Name: "route-evidence-max-age", Short: "maximum evidence age (default and maximum 72h)", Value: "DURATION"},
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
				{Name: "auth-header-env", Short: "environment variable holding the Authorization value ('Bearer …') or one 'Name: value' header", Value: "ENV"},
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
				{Name: "auth-header-env", Short: "environment variable holding the new Authorization value ('Bearer …') or one 'Name: value' header", Value: "ENV"},
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
			{Name: "add", Short: "Add a webhook", Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "slug"}, {Name: "target-url", Short: "http(s) target URL (https recommended: deliveries are signed, not encrypted)", Req: true, Value: "URL"}, {Name: "event", Short: "event to deliver (repeat)", Value: "EVENT"}, {Name: "retry-policy", Short: "default|aggressive|none", Value: "POLICY"}, {Name: "delivery-format", Short: "json|cloudevents", Value: "FORMAT"}}},
			{Name: "info", Short: "Show one webhook", Positionals: []string{"<webhook-id>"}, Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "slug"}}},
			{Name: "update", Short: "Update one webhook", Positionals: []string{"<id>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "slug"},
				{Name: "target-url", Short: "new http(s) target URL", Value: "URL"},
				{Name: "retry-policy", Short: "new retry policy", Value: "POLICY", ClosedSet: []string{"default", "aggressive", "none"}},
				{Name: "delivery-format", Short: "new delivery format", Value: "FORMAT", ClosedSet: []string{"json", "cloudevents"}},
				{Name: "enable", Short: "enable the webhook"},
				{Name: "disable", Short: "disable the webhook"},
			}},
			{Name: "rm", Short: "Delete one webhook", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "slug"}}},
			{Name: "deliveries", Short: "Show the delivery ledger", Positionals: []string{"<id>"}, Flags: []cliFlag{
				{Name: "app", Short: "app slug", Req: true, Value: "SLUG"},
				{Name: "status", Short: "filter delivery status", Value: "STATUS", ClosedSet: []string{"pending", "in_flight", "succeeded", "failed", "dead"}},
				{Name: "limit", Short: "page size (1..100, default 50)", Value: "N"},
				{Name: "page-size", Short: "alias for --limit", Value: "N"},
				{Name: "cursor", Short: "opaque continuation cursor", Value: "CURSOR"},
				{Name: "page-token", Short: "alias for --cursor", Value: "CURSOR"},
				{Name: "all", Short: "walk every page using --limit and --cursor"},
			}},
			{Name: "retry", Short: "Retry a failed delivery", Positionals: []string{"<webhook-id>", "<delivery-id>"}, Flags: []cliFlag{{Name: "app", Short: "app slug", Req: true, Value: "slug"}}},
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
		Short:   "Generate shell completion scripts or install them interactively",
		Subcommands: []cliSub{
			{Name: "install", Short: "Choose a shell, review files, and confirm completion setup", Examples: []string{"gregale completion install --interactive"}, Flags: []cliFlag{{Name: "interactive", Short: "guide shell completion installation", Bool: true, Req: true}}},
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
