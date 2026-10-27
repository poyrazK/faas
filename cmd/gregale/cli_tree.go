package main

// cli_tree.go — ADR-20261026145832.
//
// The new command tree: singular nouns, standard verbs, at most three words
// before the target. cliTreeAliases maps each path in that tree to the
// command that implements it today, and run() rewrites a new path before
// dispatch, capability gating and help. A path that names one of today's
// commands is never rewritten, so existing scripts, agents and CI keep their
// meaning. The one exception is `app`: the tree puts the verb first
// (`app scale api`), so a tree verb wins over an app slug spelled the same.

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cliTreeNoun is one top-level noun of the new tree.
type cliTreeNoun struct {
	Name   string
	Short  string
	Family string
}

// cliTreeNouns lists the tree's nouns in help order. Shortcuts (deploy, logs,
// run, …) are today's top-level commands and need no entry.
var cliTreeNouns = []cliTreeNoun{
	{"release", "Inspect releases and control rollouts: canary, traffic, approvals", "Ship"},
	{"preview", "Manage pull-request preview environments", "Ship"},
	{"environment", "Manage project environments, promotions and rollbacks", "Ship"},
	{"project", "Inspect and recover repository projects", "Ship"},
	{"build", "Inspect builds, their provenance and SBOMs", "Ship"},
	{"github", "Connect GitHub and bind apps to repositories", "Ship"},
	{"registry", "Manage registry credentials, signing keys and published images", "Ship"},
	{"test", "Run scenario suites, lifecycle checks, load tests and chaos", "Ship"},
	{"app", "Inspect, change, scale and operate apps", "Run"},
	{"sandbox", "Run code in disposable sandboxes and inspect past runs", "Run"},
	{"invocation", "List, inspect, wait on and watch invocations", "Run"},
	{"env", "Set, list and sync an app's sealed variables", "Configure"},
	{"domain", "Manage custom domains", "Configure"},
	{"edge", "Manage edge rules, rule lists, caching and CORS", "Configure"},
	{"api", "Manage your API: routes, OpenAPI spec, consumers and tenants", "Configure"},
	{"flag", "Roll out feature flags to selected customers", "Configure"},
	{"postgres", "Manage PostgreSQL databases: usage, overage and capabilities", "Data"},
	{"bucket", "Manage object storage buckets", "Data"},
	{"pipeline", "Sync PostgreSQL sources into read-only mirrors", "Data"},
	{"data-api", "Generate an API from a PostgreSQL schema", "Data"},
	{"ai", "Configure the AI gateway", "Data"},
	{"realtime", "Manage realtime endpoints, channels and connections", "Data"},
	{"cron", "Schedule requests and commands", "Work"},
	{"trigger", "Run apps from message brokers", "Work"},
	{"job", "Manage jobs that run to completion", "Work"},
	{"worker", "Inspect and scale background worker pools", "Work"},
	{"workflow", "Manage durable workflows", "Work"},
	{"automation", "Build, monitor and recover automations", "Work"},
	{"event", "Publish and send events, manage subscriptions and replays", "Work"},
	{"webhook", "Register webhooks and deliver events to them", "Work"},
	{"queue", "Inspect and feed an app's queues", "Work"},
	{"dlq", "Inspect, replay or purge items that failed delivery", "Work"},
	{"metric", "Show app metrics, request breakdowns and the service map", "Observe"},
	{"debug", "Inspect requests, traces, issues, profiles and regressions", "Observe"},
	{"dashboard", "Save dashboards across apps", "Observe"},
	{"audit", "Search your account's audit log", "Observe"},
	{"alert", "Manage alert rules, channels, incidents, SLOs and checks", "Observe"},
	{"mcp", "Connect agents, host MCP servers, or call MCP tools", "Agents"},
	{"account", "Manage your account, MFA and data exports", "Account"},
	{"org", "Manage orgs, members and invitations", "Account"},
	{"key", "Manage your account's API keys", "Account"},
	{"billing", "Manage plan, payment, usage, invoices and spend cap", "Account"},
	{"feature", "See Gregale features and join early access", "Account"},
	{"config", "Manage local CLI settings, profiles and presets", "Account"},
}

// cliTreeAlias maps a path in the new tree to today's command path. When the
// old path has verbs of its own, the new path is a noun and today's verbs
// follow it, with cliTreeVerbSynonyms filling in standard spellings.
type cliTreeAlias struct {
	New string
	Old string
}

var cliTreeAliases = []cliTreeAlias{
	// Ship.
	{"release list", "deployments"},
	{"release get", "deploys status"},
	{"release stages", "deploys show"},
	{"release summary", "deployment summary"},
	{"release attempt get", "deploy status"},
	{"release attempt cancel", "deploy abort"},
	{"release attempt retry", "deploy retry"},
	{"release logs", "deploys logs"},
	{"release wait", "deployment wait"},
	{"release cancel", "deploys cancel"},
	{"release retry", "deploys retry"},
	{"release resume", "deploy resume"},
	{"release recover", "deploy recover"},
	{"release diff", "deployment diff"},
	{"release hide", "deploys clear"},
	{"release prune", "deploys clear-obsolete"},
	{"release reorder", "deploys reorder"},
	{"release runtime", "deployment runtime"},
	{"release rollback", "rollback"},
	{"release alias", "deployments alias"},
	{"release contract run", "deploy contract"},
	{"release contract resolve", "deploy resolve-contract"},
	{"release canary", "canary"},
	{"release traffic", "traffic"},
	{"release approval", "approvals"},
	{"release approve", "approvals approve"},
	{"release reject", "approvals reject"},
	{"release freeze", "freeze"},
	{"release gate", "release-gates"},
	{"release schedule", "scheduled-releases"},
	{"release mirror", "mirror"},
	{"preview", "preview"},
	{"environment", "projects environments"},
	{"project", "projects"},
	{"build", "build"},
	{"github", "github"},
	{"github connect", "connect github"},
	{"github link", "github bind"},
	{"registry", "registry"},
	{"registry key", "trusted-publishers"},
	{"registry deploy", "registry published"},
	{"test", "test"},
	{"test chaos", "chaos inject"},

	// Run. `app` verbs that live in today's `app <slug> <verb>` form are
	// handled by rewriteAppPath.
	{"app list", "apps"},
	{"app delete", "apps rm"},
	{"app restore", "apps restore"},
	{"app instances", "ps"},
	{"app park", "park"},
	{"app wake", "wake"},
	{"app wake-ahead", "wake-ahead"},
	{"app wake-timeline", "wake-timeline"},
	{"app binding", "bindings"},
	{"app streaming-cap", "apps streaming-cap"},
	{"sandbox run", "run"},
	{"sandbox", "runs"},
	{"invocation watch", "tail"},
	{"invocation", "invocations"},

	// Configure.
	{"env set", "secrets set"},
	{"env unset", "secrets unset"},
	{"env list", "secrets list"},
	{"env rotate", "secrets rotate"},
	{"env audit", "secrets audit"},
	{"env check", "secrets check"},
	{"env ref", "secrets refs"},
	{"env requirement", "secrets requirements"},
	// Today's env verbs that sync variables; `env create` and
	// `env clone-status` clone project environments and are not listed here.
	{"env pull", "env pull"},
	{"env push", "env push"},
	{"env diff", "env diff"},
	{"env history", "env history"},
	{"env rollback", "env rollback"},
	{"domain", "domains"},
	{"edge rule", "edge-rules"},
	{"edge rule-list", "edge-rule-lists"},
	{"edge cache", "cache"},
	{"edge cors", "cors"},
	{"api route", "routes"},
	{"api spec", "openapi"},
	{"api consumer", "consumers"},
	{"api tenant", "platform-tenants"},
	{"flag", "flags"},

	// Data.
	{"postgres", "postgres"},
	{"bucket multipart get", "bucket uploads status"},
	{"bucket multipart", "bucket uploads"},
	{"bucket version delete", "bucket version-delete"},
	{"bucket version", "bucket versions"},
	{"bucket deletion create", "bucket deletions start"},
	{"bucket deletion get", "bucket deletions status"},
	{"bucket deletion", "bucket deletions"},
	{"bucket encryption set", "bucket encryption"},
	{"bucket object-lock set", "bucket object-lock"},
	{"bucket inventory get", "bucket reconcile status"},
	{"bucket inventory", "bucket reconcile"},
	{"bucket", "bucket"},
	{"pipeline", "pipeline"},
	{"data-api", "data-api"},
	{"ai", "ai"},
	{"realtime", "realtime"},

	// Work.
	{"cron", "crons"},
	{"trigger", "triggers"},
	// Exclusive keyed work is --lock on job runs, crons and triggers; its
	// policies live under job until a broader home is needed.
	{"job lock-policy list", "operations policy list"},
	{"job lock-policy set", "operations policy upsert"},
	{"job lock-policy delete", "operations policy retire"},
	{"job", "jobs"},
	{"worker", "workers"},
	{"workflow", "workflows"},
	{"automation recovery health", "automations recovery-health"},
	{"automation recovery suppress", "automations recovery-suppress"},
	{"automation recovery create", "automations recovery-start"},
	{"automation recovery cancel", "automations recovery-cancel"},
	{"automation recovery run", "automations recovery-plan"},
	{"automation failure list", "automations failures"},
	{"automation failure policy", "automations failure-policy"},
	{"automation failure resume", "automations failure-resume"},
	{"automation", "automations"},
	{"event send", "send"},
	{"event subscription list", "events subscriptions"},
	{"event subscription get", "events subscription-status"},
	{"event subscription pause", "events subscription-pause"},
	{"event subscription resume", "events subscription-resume"},
	{"event subscription health", "events subscription-health"},
	{"event subscription-version get", "events subscription-versions-status"},
	{"event subscription-version set", "events subscription-versions-set"},
	{"event subscription-version reset", "events subscription-versions-reset"},
	{"event subscription-retry get", "events subscription-retry-status"},
	{"event subscription-retry set", "events subscription-retry-set"},
	{"event subscription-retry reset", "events subscription-retry-reset"},
	{"event subscription-circuit get", "events subscription-circuit-status"},
	{"event subscription-circuit set", "events subscription-circuit-set"},
	{"event subscription-circuit disable", "events subscription-circuit-disable"},
	{"event subscription-circuit reset", "events subscription-circuit-reset"},
	{"event subscription-capacity set", "events subscription-capacity"},
	{"event subscription-capacity list", "events subscription-capacity-commands"},
	{"event subscription-capacity get", "events subscription-capacity-command"},
	{"event backfill create", "events backfill"},
	{"event backfill get", "events backfill-status"},
	{"event backfill items", "events backfill-items"},
	{"event backfill retry", "events backfill-retry"},
	{"event recovery list", "events recovery-list"},
	{"event recovery get", "events recovery-status"},
	{"event recovery create", "events recovery-create"},
	{"event recovery health", "events recovery-health"},
	{"event recovery history", "events recovery-history"},
	{"event recovery items", "events recovery-items"},
	{"event recovery pause", "events recovery-pause"},
	{"event recovery resume", "events recovery-resume"},
	{"event recovery update", "events recovery-rate"},
	{"event recovery cancel", "events recovery-cancel"},
	{"event delivery retry", "events delivery-retry"},
	{"event", "events"},
	{"webhook deliver", "deliver"},
	{"webhook", "webhooks"},
	{"queue watch", "queue tail"},
	{"queue", "queue"},
	{"dlq", "dlq"},

	// Observe.
	{"metric requests", "analytics"},
	{"metric", "metrics"},
	{"debug trace", "trace"},
	{"debug issue", "issues"},
	{"debug", "debug"},
	{"dashboard", "dashboards"},
	{"open dashboard", "dashboard"},
	{"audit", "audit-events"},
	{"logs drain", "log-drains"},
	// MCP readiness alerting folds into the alert family on the CLI; its
	// backend stays separate (ADR-20261026145832).
	{"alert maintenance list", "mcp readiness maintenance list"},
	{"alert maintenance create", "mcp readiness maintenance create"},
	{"alert maintenance cancel", "mcp readiness maintenance cancel"},
	{"alert pagerduty get", "mcp readiness pagerduty get"},
	{"alert pagerduty set", "mcp readiness pagerduty set"},
	{"alert pagerduty disable", "mcp readiness pagerduty disable"},
	{"alert channel", "channels"},
	{"alert incident", "incidents"},
	{"alert slo", "slos"},
	{"alert check", "synthetics"},
	{"alert", "alerts"},

	// Agents. Hosting verbs and client verbs sit directly under `mcp` today.
	{"mcp serve", "mcp serve"},
	{"mcp connection", "mcp connections"},
	{"mcp server", "mcp"},
	{"mcp client tools", "mcp tools"},
	{"mcp client call", "mcp call"},
	{"mcp client complete", "mcp complete"},
	{"mcp client prompts", "mcp prompts"},
	{"mcp client render", "mcp prompt-get"},
	{"mcp client resources", "mcp resources"},
	{"mcp client read", "mcp resource-read"},
	{"mcp client watch", "mcp resource-watch"},
	{"mcp client status", "mcp task-get"},
	{"mcp client wait", "mcp task-wait"},
	{"mcp client cancel", "mcp task-cancel"},

	// Account.
	{"account mfa", "mfa"},
	{"account", "account"},
	{"org invitation accept", "invitations accept"},
	{"org invitation peek", "invitations peek"},
	{"org invitation", "orgs invitations"},
	{"org", "orgs"},
	{"key", "keys"},
	{"billing plan", "plan"},
	{"billing usage", "usage"},
	{"billing invoices", "invoices"},
	{"billing spend-cap", "overage-cap"},
	{"billing", "billing"},
	{"feature list", "capabilities"},
	{"feature join", "previews join"},
	{"feature leave", "previews leave"},
	{"config profile", "profile"},
	{"config preset", "context presets"},
	{"config upload-cache", "upload-cache"},
	{"config", "config"},

	// Operator tools live under the hidden admin command (rule 7) and keep
	// their names; the naming rules cover the customer tree.
	{"admin sites", "sites"},
	{"admin data-repair", "data-repair"},
	{"admin commit", "commit"},
	{"admin entities", "entities"},
	{"admin product", "product"},
	{"admin tenant-surfaces", "tenant-surfaces"},
	{"admin rollouts", "rollouts"},
	{"admin github-webhook-secret", "github-webhook-secret"},
	{"admin mail", "mail"},
	{"admin postgres reconcile", "postgres reconcile"},
	{"admin postgres usage-import", "postgres usage-import"},
	{"admin postgres diagnostics", "postgres diagnostics"},
	{"admin billing refresh-invoice", "billing refresh-invoice"},
	{"admin billing backfill-invoices", "billing backfill-invoices"},
}

// cliTreeShorts summarize tree paths whose command today does more than
// the path names.
var cliTreeShorts = map[string]string{
	"mcp server": "Scaffold, deploy and check the MCP servers you host",
}

// cliTreeVerbSynonyms gives, for a standard verb, today's spellings that a
// command may use instead (ADR-20261026145832 rule 3).
var cliTreeVerbSynonyms = map[string][]string{
	"list":   {"ls"},
	"get":    {"info", "show", "inspect"},
	"create": {"add"},
	"update": {"replace"},
	"delete": {"rm", "remove", "destroy"},
}

// cliTreeStandardVerb is the reverse of cliTreeVerbSynonyms, for help.
func cliTreeStandardVerb(verb string) string {
	for standard, spellings := range cliTreeVerbSynonyms {
		if slices.Contains(spellings, verb) {
			return standard
		}
	}
	return verb
}

// cliTreeVerbFor spells a verb of siblings in its standard form. A synonym
// whose standard verb is also a sibling (`rm` beside `delete`) is not
// listed: the standard verb runs the sibling.
func cliTreeVerbFor(verb string, siblings []cliSub) (string, bool) {
	standard := cliTreeStandardVerb(verb)
	if standard != verb && slices.ContainsFunc(siblings, func(sub cliSub) bool { return sub.Name == standard }) {
		return "", false
	}
	return standard, true
}

func lookupCliTreeNoun(name string) (cliTreeNoun, bool) {
	for _, noun := range cliTreeNouns {
		if noun.Name == name {
			return noun, true
		}
	}
	return cliTreeNoun{}, false
}

// cliTreeNode is a manifest command or verb reached by a path of words.
type cliTreeNode struct {
	Short string
	Subs  []cliSub
	Flags []cliFlag
	// AfterPositionals marks `app <slug> <verb>`-shaped families, whose next
	// word is data rather than a verb.
	AfterPositionals bool
}

// lookupCliPath walks the manifest along words. Aliases count as their verb.
func lookupCliPath(words []string) (cliTreeNode, bool) {
	if len(words) == 0 {
		return cliTreeNode{}, false
	}
	command, ok := lookupCliCommand(words[0])
	if !ok {
		return cliTreeNode{}, false
	}
	return walkCliTreeNode(command, words[1:])
}

// walkCliTreeNode follows words from command through its verbs.
func walkCliTreeNode(command cliCommand, words []string) (cliTreeNode, bool) {
	node := cliTreeNode{Short: command.Short, Subs: command.Subcommands, Flags: command.Flags, AfterPositionals: command.SubcommandsAfterPositionals}
	for _, word := range words {
		sub, ok := findCliSubcommand(node.Subs, word)
		if !ok {
			return cliTreeNode{}, false
		}
		node = cliTreeNode{Short: sub.Short, Subs: sub.Subcommands, Flags: sub.Flags, AfterPositionals: sub.SubcommandsAfterPositionals}
	}
	return node, true
}

// leadingTreeWords returns up to three words before the first flag.
func leadingTreeWords(args []string) []string {
	var words []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") || len(words) == 3 {
			break
		}
		words = append(words, arg)
	}
	return words
}

// oldPathDepth counts the leading words of args that name one of today's
// commands and verbs. It stops at a flag, at an unknown word, and before the
// data word of a slug-first family.
func oldPathDepth(args []string) int {
	if len(args) == 0 {
		return 0
	}
	command, ok := lookupCliCommand(args[0])
	if !ok {
		return 0
	}
	subs, afterPositionals := command.Subcommands, command.SubcommandsAfterPositionals
	depth := 1
	for depth < len(args) && !afterPositionals && !strings.HasPrefix(args[depth], "-") {
		sub, ok := findCliSubcommand(subs, args[depth])
		if !ok {
			break
		}
		subs, afterPositionals = sub.Subcommands, sub.SubcommandsAfterPositionals
		depth++
	}
	return depth
}

// matchCliTreeAlias finds the longest tree path that prefixes args.
func matchCliTreeAlias(args []string) (cliTreeAlias, int, bool) {
	words := leadingTreeWords(args)
	var best cliTreeAlias
	bestLen := 0
	for _, alias := range cliTreeAliases {
		path := strings.Fields(alias.New)
		if len(path) > len(words) || len(path) <= bestLen || !slices.Equal(path, words[:len(path)]) {
			continue
		}
		best, bestLen = alias, len(path)
	}
	return best, bestLen, bestLen > 0
}

// rewriteTreePath replaces a new-tree path at the start of args with the
// path of the command that implements it today. It returns args unchanged
// when they name today's command or no tree path.
func rewriteTreePath(args []string) ([]string, error) {
	if len(args) == 0 {
		return args, nil
	}
	if args[0] == appSlugFallback {
		return rewriteAppPath(args)
	}
	alias, used, ok := matchCliTreeAlias(args)
	if !ok {
		return args, nil
	}
	// Today's path wins wherever it reaches at least as far as the tree path;
	// a word after it that today's command does not know still goes to the
	// tree (`bucket get` → `bucket info`, `dashboard list` → `dashboards`).
	depth := oldPathDepth(args)
	if used > depth || (used == depth && len(args) > depth && !strings.HasPrefix(args[depth], "-")) {
		return joinTreeRewrite(alias, args[used:]), nil
	}
	return args, nil
}

// joinTreeRewrite builds today's argv for alias, mapping a standard verb to
// the spelling today's command uses.
func joinTreeRewrite(alias cliTreeAlias, rest []string) []string {
	out := strings.Fields(alias.Old)
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		if node, ok := lookupCliPath(out); ok && len(node.Subs) > 0 && !node.AfterPositionals {
			if _, known := findCliSubcommand(node.Subs, rest[0]); !known {
				for _, spelling := range cliTreeVerbSynonyms[rest[0]] {
					if _, found := findCliSubcommand(node.Subs, spelling); found {
						rest = append([]string{spelling}, rest[1:]...)
						break
					}
				}
			}
		}
	}
	return append(out, rest...)
}

var errAppTarget = errors.New("name the app: gregale app <verb> <app>, --app <app>, or run it in a linked checkout")

// rewriteAppPath maps the tree's verb-first `app <verb> [<app>]` to today's
// `app <app> <verb>` or to the command that owns the verb today. Today's
// slug-first form keeps working when its first word is not a tree verb.
func rewriteAppPath(args []string) ([]string, error) {
	if len(args) < 2 || strings.HasPrefix(args[1], "-") {
		return args, nil
	}
	if alias, used, ok := matchCliTreeAlias(args); ok {
		return joinTreeRewrite(alias, args[used:]), nil
	}
	verb := args[1]
	if verb == "listener" {
		return rewriteAppListener(args)
	}
	if verb == "get" || verb == "update" {
		app, rest, err := takeAppTarget(args[2:])
		if err != nil && hasHelpFlag(args) {
			return []string{appSlugFallback, "--help"}, nil
		}
		if err != nil {
			return nil, err
		}
		return append([]string{appSlugFallback, app}, rest...), nil
	}
	command, ok := lookupCliCommand(appSlugFallback)
	if !ok {
		return args, nil
	}
	sub, ok := findCliSubcommand(command.Subcommands, verb)
	if !ok {
		return args, nil
	}
	chain := []string{verb}
	rest := args[2:]
	if len(rest) > 0 && len(sub.Subcommands) > 0 {
		if _, nested := findCliSubcommand(sub.Subcommands, rest[0]); nested {
			chain, rest = append(chain, rest[0]), rest[1:]
		}
	}
	app, rest, err := takeAppTarget(rest)
	if err != nil {
		// Help needs no app; without one it stays today's `app` help.
		if hasHelpFlag(args) {
			return args, nil
		}
		return nil, err
	}
	out := append([]string{appSlugFallback, app}, chain...)
	return append(out, rest...), nil
}

// rewriteAppListener maps `app listener <verb> [<app>] [--protocol tcp|udp]`
// to today's `app <app> tcp|udp <verb>`; TCP is the default.
func rewriteAppListener(args []string) ([]string, error) {
	protocol := "tcp"
	rest := make([]string, 0, len(args))
	for i := 2; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		if name != "--protocol" {
			rest = append(rest, args[i])
			continue
		}
		if !hasValue && i+1 < len(args) {
			i++
			value = args[i]
		}
		protocol = strings.ToLower(value)
	}
	if protocol != "tcp" && protocol != "udp" {
		return nil, fmt.Errorf("--protocol must be tcp or udp; got %q", protocol)
	}
	if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
		if hasHelpFlag(args) {
			return []string{appSlugFallback, "--help"}, nil
		}
		return nil, fmt.Errorf("name a listener verb: gregale app listener <list|create|delete|enable|disable> <app> [--protocol tcp|udp]")
	}
	verb := rest[0]
	if command, ok := lookupCliCommand(appSlugFallback); ok {
		if family, ok := findCliSubcommand(command.Subcommands, protocol); ok {
			if _, known := findCliSubcommand(family.Subcommands, verb); !known {
				for _, spelling := range cliTreeVerbSynonyms[verb] {
					if _, found := findCliSubcommand(family.Subcommands, spelling); found {
						verb = spelling
						break
					}
				}
			}
		}
	}
	app, tail, err := takeAppTarget(rest[1:])
	if err != nil {
		return nil, err
	}
	return append([]string{appSlugFallback, app, protocol, verb}, tail...), nil
}

// takeAppTarget returns the app a tree `app` verb addresses: the first
// argument when it is slug-shaped, else --app, else the linked app.
func takeAppTarget(args []string) (string, []string, error) {
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		value, isApp := appFlagValue(args, i)
		if !isApp {
			continue
		}
		if value == "" {
			return "", nil, errAppTarget
		}
		rest := append(append([]string{}, args[:i]...), args[i+1:]...)
		if !strings.Contains(args[i], "=") {
			rest = append(append([]string{}, args[:i]...), args[i+2:]...)
		}
		if len(rest) > 0 && rest[0] == value {
			rest = rest[1:]
		}
		return value, rest, nil
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") && api.ValidAppSlug(args[0]) {
		return args[0], args[1:], nil
	}
	app, err := resolveAppFlagOrContext("")
	if err != nil || app == "" {
		return "", nil, errAppTarget
	}
	return app, args, nil
}

// rewriteCliTree applies rewriteTreePath to a command, or to the topic of
// `gregale help <path>`. A request for tree help is left for
// cliTreeHelpTopic, so `gregale cron` lists the tree's verbs.
func rewriteCliTree(args []string) ([]string, error) {
	if _, ok := cliTreeHelpTopic(args); ok {
		return args, nil
	}
	if len(args) > 1 && args[0] == "help" {
		topic, err := rewriteTreePath(args[1:])
		if err != nil {
			return nil, err
		}
		return append([]string{"help"}, topic...), nil
	}
	return rewriteTreePath(args)
}

// cliTreeTopic is a path whose help lists the tree's verbs and subnouns: a
// noun, or a subnoun only the tree has (`mcp client`).
type cliTreeTopic struct {
	Path  []string
	Short string
}

// cliTreeHelpTopic reports a request for tree help: `help <topic>`,
// `<topic> --help`, or a bare topic that is not also one of today's
// commands, whose bare run keeps its meaning. `--help --all` asks for
// every option, which today's command help lists.
func cliTreeHelpTopic(args []string) (cliTreeTopic, bool) {
	if len(args) > 1 && args[0] == "help" {
		words := leadingTreeWords(args[1:])
		if len(words) != len(args)-1 {
			return cliTreeTopic{}, false
		}
		return lookupCliTreeTopic(words, true)
	}
	words := leadingTreeWords(args)
	rest := args[len(words):]
	for _, arg := range rest {
		if arg != "--help" && arg != "-h" {
			return cliTreeTopic{}, false
		}
	}
	return lookupCliTreeTopic(words, len(rest) > 0)
}

// lookupCliTreeTopic returns the tree topic words name. A noun that only
// passes today's command through (`preview`) keeps today's fuller help, and
// a subnoun the tree maps to one of today's commands gets that command's.
func lookupCliTreeTopic(words []string, help bool) (cliTreeTopic, bool) {
	if len(words) == 0 {
		return cliTreeTopic{}, false
	}
	noun, ok := lookupCliTreeNoun(words[0])
	if !ok {
		return cliTreeTopic{}, false
	}
	today := oldPathDepth(words) == len(words)
	if today && !help {
		return cliTreeTopic{}, false
	}
	if len(words) == 1 {
		if today && !cliTreeReshapes(noun.Name) {
			return cliTreeTopic{}, false
		}
		return cliTreeTopic{Path: words, Short: noun.Short}, true
	}
	path := strings.Join(words, " ")
	if today || !cliTreeHasPathUnder(path) {
		return cliTreeTopic{}, false
	}
	for _, alias := range cliTreeAliases {
		if alias.New == path {
			return cliTreeTopic{}, false
		}
	}
	return cliTreeTopic{Path: words, Short: "Commands under gregale " + path}, true
}

// cliTreeReshapes reports a noun whose tree verbs differ from today's
// command of the same name: it adds or renames verbs, or reorders `app`.
func cliTreeReshapes(noun string) bool {
	if noun == appSlugFallback {
		return true
	}
	for _, alias := range cliTreeAliases {
		if alias.New != alias.Old && strings.Fields(alias.New)[0] == noun {
			return true
		}
	}
	return false
}

func cliTreeHasPathUnder(path string) bool {
	for _, alias := range cliTreeAliases {
		if strings.HasPrefix(alias.New, path+" ") {
			return true
		}
	}
	return false
}

// cliTreeEntry is one verb or subnoun listed in a topic's help.
type cliTreeEntry struct {
	Name  string
	Short string
	Runs  string
}

// cliTreeDiscovery is the discoverable manifest, read once per help screen.
type cliTreeDiscovery struct {
	commands map[string]cliCommand
}

func newCliTreeDiscovery() cliTreeDiscovery {
	return cliTreeDiscoveryOf(discoverableCliCommands())
}

// cliTreeDiscoveryOf indexes manifest, for a surface that documents more
// than this account can see.
func cliTreeDiscoveryOf(manifest []cliCommand) cliTreeDiscovery {
	commands := map[string]cliCommand{}
	for _, command := range manifest {
		if cliCommandAvailable(command.Name) {
			commands[command.Name] = command
		}
	}
	return cliTreeDiscovery{commands: commands}
}

// node walks the discoverable customer manifest; a verb whose capability is
// hidden for the account is not found, as in today's help
// (ADR-20261023115741), and an operator command stays out of the tree.
func (d cliTreeDiscovery) node(path string) (cliTreeNode, bool) {
	words := strings.Fields(path)
	command, ok := d.commands[words[0]]
	if !ok || command.Audience != cliAudienceCustomer {
		return cliTreeNode{}, false
	}
	return walkCliTreeNode(command, words[1:])
}

// cliTreeEntries lists the verbs and subnouns under prefix that this account
// can see, with the command each one runs today.
func cliTreeEntries(prefix []string) []cliTreeEntry {
	return newCliTreeDiscovery().entries(prefix)
}

func (d cliTreeDiscovery) entries(prefix []string) []cliTreeEntry {
	var entries []cliTreeEntry
	seen := map[string]bool{}
	add := func(name, short, runs string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		entries = append(entries, cliTreeEntry{Name: name, Short: short, Runs: runs})
	}
	// A verb with a tree name of its own is listed under that name only.
	renamed := map[string]bool{}
	for _, alias := range cliTreeAliases {
		if alias.New != alias.Old {
			renamed[alias.Old] = true
		}
	}
	for _, alias := range cliTreeAliases {
		path := strings.Fields(alias.New)
		if len(path) < len(prefix) || !slices.Equal(path[:len(prefix)], prefix) {
			continue
		}
		node, ok := d.node(alias.Old)
		if !ok {
			continue
		}
		if len(path) > len(prefix) {
			short := node.Short
			if tree, ok := cliTreeShorts[alias.New]; ok {
				short = tree
			}
			add(strings.Join(path[len(prefix):], " "), short, alias.Old)
			continue
		}
		if node.AfterPositionals {
			continue
		}
		for _, sub := range node.Subs {
			verb, ok := cliTreeVerbFor(sub.Name, node.Subs)
			if old := alias.Old + " " + sub.Name; ok && !renamed[old] {
				add(verb, sub.Short, old)
			}
		}
	}
	if slices.Equal(prefix, []string{appSlugFallback}) {
		d.appEntries(add)
	}
	return groupCliTreeEntries(entries)
}

// appEntries lists today's `app <app> <verb>` verbs in the tree's
// verb-first form (rewriteAppPath).
func (d cliTreeDiscovery) appEntries(add func(name, short, runs string)) {
	command, ok := d.commands[appSlugFallback]
	if !ok {
		return
	}
	add("get", "Show an app's settings and status", appSlugFallback)
	add("update", "Change an app's settings", appSlugFallback)
	for _, sub := range command.Subcommands {
		runs := appSlugFallback + " " + sub.Name
		if sub.Name == "tcp" || sub.Name == "udp" {
			add("listener", "Manage TCP and UDP listeners (--protocol tcp|udp)", runs)
			continue
		}
		// rewriteAppPath dispatches today's spelling of an app verb.
		add(sub.Name, sub.Short, runs)
	}
}

// groupCliTreeEntries folds multi-word entries into their subnoun, so a noun
// lists `client` once instead of every `client <verb>`.
func groupCliTreeEntries(entries []cliTreeEntry) []cliTreeEntry {
	single := map[string]bool{}
	for _, entry := range entries {
		if !strings.Contains(entry.Name, " ") {
			single[entry.Name] = true
		}
	}
	grouped := make([]cliTreeEntry, 0, len(entries))
	at := map[string]int{}
	for _, entry := range entries {
		head, rest, multi := strings.Cut(entry.Name, " ")
		switch {
		case !multi:
			grouped = append(grouped, entry)
		case single[head]:
		case at[head] > 0:
			grouped[at[head]-1].Short += " · " + rest
		default:
			grouped = append(grouped, cliTreeEntry{Name: head, Short: rest, Runs: entry.Runs})
			at[head] = len(grouped)
		}
	}
	return grouped
}

// printCliTreeHelp writes help for a tree topic. A topic the tree maps to
// one of today's commands also lists that command's own flags.
func printCliTreeHelp(w io.Writer, topic cliTreeTopic) {
	name := strings.Join(topic.Path, " ")
	discovery := newCliTreeDiscovery()
	_, _ = fmt.Fprintf(w, "%s\n\nUsage:\n  gregale %s <command> [flags]\n\nCommands:\n", topic.Short, name)
	for _, entry := range discovery.entries(topic.Path) {
		printHelpEntry(w, entry.Name, entry.Short)
	}
	for _, alias := range cliTreeAliases {
		if alias.New != name {
			continue
		}
		if node, ok := discovery.node(alias.Old); ok && len(node.Flags) > 0 {
			printLocalHelpFlags(w, node.Flags, false)
		}
		break
	}
	_, _ = fmt.Fprintf(w, "\nRun 'gregale %s <command> --help' for command details.\nDocs: %s\n", name, docsURL)
}

// cliTreeHelpRename returns the path help shows in place of today's when
// run() rewrote typed to args: today's path, with the verb it reached, and
// the tree path typed in its place.
func cliTreeHelpRename(typed, args []string) (from, to string, ok bool) {
	if len(typed) > 0 && typed[0] == "help" && len(args) > 0 && args[0] == "help" {
		typed, args = typed[1:], args[1:]
	}
	if slices.Equal(typed, args) {
		return "", "", false
	}
	alias, used, ok := matchCliTreeAlias(typed)
	if !ok {
		return "", "", false
	}
	old := strings.Fields(alias.Old)
	if len(args) < len(old) || !slices.Equal(args[:len(old)], old) {
		return "", "", false
	}
	from, to = alias.Old, alias.New
	if used < len(typed) && len(old) < len(args) && !strings.HasPrefix(typed[used], "-") && typed[used] != args[len(old)] {
		from += " " + args[len(old)]
		to += " " + typed[used]
	}
	return from, to, true
}

// renameHelpPath spells `gregale <from>` as `gregale <to>` in help text. Its
// "See also" line goes: it points at today's names the tree replaces.
func renameHelpPath(text, from, to string) string {
	lines := strings.SplitAfter(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(line, "See also: ") {
			kept = append(kept, line)
		}
	}
	pattern := regexp.MustCompile(regexp.QuoteMeta("gregale "+from) + `([^a-z0-9-]|$)`)
	return pattern.ReplaceAllStringFunc(strings.Join(kept, ""), func(match string) string {
		return "gregale " + to + strings.TrimPrefix(match, "gregale "+from)
	})
}
