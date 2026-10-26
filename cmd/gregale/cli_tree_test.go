package main_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// ADR-20261026145832 rules 3 and 4: the verbs a tree path may end with.
var cliTreeVerbs = []string{
	"list", "get", "create", "update", "delete", "set", "unset", "enable", "disable",
	"pause", "resume", "cancel", "retry", "wait", "watch", "logs", "diff", "doctor", "status",
	"promote", "rollback", "purge", "rotate", "approve", "reject", "join", "leave", "explain",
	"replay", "recover", "scale", "park", "wake", "wake-ahead", "restart", "rename", "restore",
	"exec", "cp", "port-forward", "run", "publish", "send", "deliver", "receive", "peek", "ack",
	"apply", "check", "verify", "setup", "sync", "connect", "disconnect", "link", "advance",
	"simulate", "hide", "prune", "reorder", "serve", "call", "complete", "read", "render",
	"resolve", "audit", "accept", "deploy",
}

// A path may end with a noun it lists or shows (`app instances`).
var cliTreeReadNouns = []string{
	"instances", "requests", "tools", "prompts", "resources", "plan", "invoices", "spend-cap",
	"streaming-cap", "wake-timeline", "trace", "runtime", "chaos",
}

// Rule 6: hyphens only inside these compound words.
var cliTreeCompoundWords = []string{
	"data-api", "rule-list", "egress-allowlist", "egress-ports", "static-egress-ip",
	"port-forward", "wake-ahead", "wake-timeline", "streaming-cap", "spend-cap", "upload-cache",
}

// Rule 5: top-level verbs that may start a tree path.
var cliTreeShortcuts = []string{"open", "logs"}

var cliTreeWord = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func TestCliTreeAliasTargetsExist(t *testing.T) {
	enableEveryCLIFeature(t)
	seen := map[string]bool{}
	for _, alias := range *main.CliTreeAliases {
		if seen[alias.New] {
			t.Errorf("%q is listed twice", alias.New)
		}
		seen[alias.New] = true
		if exists, _ := main.CliPathHasVerbs(strings.Fields(alias.Old)); !exists {
			t.Errorf("%q maps to %q, which is not a command today", alias.New, alias.Old)
		}
	}
}

func TestCliTreePathsFollowNamingRules(t *testing.T) {
	enableEveryCLIFeature(t)
	nouns := map[string]bool{}
	for _, noun := range *main.CliTreeNouns {
		if strings.HasSuffix(noun.Name, "s") && noun.Name != "postgres" {
			t.Errorf("noun %q must be singular", noun.Name)
		}
		nouns[noun.Name] = true
	}
	for _, alias := range *main.CliTreeAliases {
		path := strings.Fields(alias.New)
		if len(path) > 3 {
			t.Errorf("%q: at most three words come before the target", alias.New)
		}
		shortcut := slices.Contains(cliTreeShortcuts, path[0])
		if !nouns[path[0]] && !shortcut {
			t.Errorf("%q must start with a tree noun or a shortcut", alias.New)
		}
		for _, word := range path {
			if !cliTreeWord.MatchString(word) {
				t.Errorf("%q: %q is not a lowercase word", alias.New, word)
			}
			if strings.Contains(word, "-") && !slices.Contains(cliTreeCompoundWords, word) {
				t.Errorf("%q: %q is a hyphenated verb chain; use a subnoun and a verb", alias.New, word)
			}
		}
		if shortcut || len(path) == 1 {
			continue
		}
		if _, hasVerbs := main.CliPathHasVerbs(strings.Fields(alias.Old)); hasVerbs {
			continue // path ends with a subnoun; today's verbs follow it
		}
		last := path[len(path)-1]
		if !slices.Contains(cliTreeVerbs, last) && !slices.Contains(cliTreeReadNouns, last) {
			t.Errorf("%q ends with %q, which is not a standard or domain verb", alias.New, last)
		}
	}
}

// Nouns whose bare name still runs today's command until the flip
// (ADR-20261026145832 step 5): `gregale dashboard` opens the browser, while
// `gregale dashboard list` already reaches saved dashboards.
var cliTreeNounsChangingMeaning = []string{"dashboard"}

// A new path that names one of today's commands would never be rewritten.
func TestCliTreePathsDoNotShadowTodaysCommands(t *testing.T) {
	enableEveryCLIFeature(t)
	for _, alias := range *main.CliTreeAliases {
		path := strings.Fields(alias.New)
		if alias.New == alias.Old || path[0] == "app" {
			continue
		}
		if len(path) == 1 && slices.Contains(cliTreeNounsChangingMeaning, path[0]) {
			if main.IsOldCommandPath([]string{path[0], "list"}) {
				t.Errorf("%q: today's command takes verbs, so the tree's verbs never apply", alias.New)
			}
			continue
		}
		if main.IsOldCommandPath(path) {
			t.Errorf("%q already names today's command; the alias to %q never applies", alias.New, alias.Old)
		}
	}
}

func TestCliTreeRewrite(t *testing.T) {
	enableEveryCLIFeature(t)
	cases := []struct{ in, want string }{
		{"release list --app api", "deployments --app api"},
		{"release get 42", "deploys status 42"},
		{"release canary advance --app api", "canary advance --app api"},
		{"release contract resolve id", "deploy resolve-contract id"},
		{"cron create --path /sync", "crons add --path /sync"},
		{"cron get c1", "crons info c1"},
		{"domain delete api.acme.com", "domains rm api.acme.com"},
		{"preview delete 12", "preview destroy 12"},
		{"preview get 12", "preview show 12"},
		{"env set K=V", "secrets set K=V"},
		{"env list", "secrets list"},
		{"env push", "env push"},
		{"app list", "apps"},
		{"app delete api --yes", "apps rm api --yes"},
		{"app get api", "app api"},
		{"app update api --ram 512", "app api --ram 512"},
		{"app scale api --ram 512", "app api scale --ram 512"},
		{"app api scale --ram 512", "app api scale --ram 512"},
		{"app health --app api --wait", "app api health --wait"},
		{"app health --app=api", "app api health"},
		{"app restart status api", "app api restart status"},
		{"app egress-allowlist add api 10.0.0.0/8", "app api egress-allowlist add 10.0.0.0/8"},
		{"app instances api", "ps api"},
		{"test chaos --scenario s", "chaos inject --scenario s"},
		{"test rehearse", "test rehearse"},
		{"mcp server deploy", "mcp deploy"},
		{"mcp client call tool", "mcp call tool"},
		{"mcp serve", "mcp serve"},
		{"open dashboard", "dashboard"},
		{"logs api --follow", "logs api --follow"},
		{"logs drain list", "log-drains list"},
		{"billing plan pro", "plan pro"},
		{"account mfa enroll", "mfa enroll"},
		{"deploys status 1", "deploys status 1"},
		{"help release list", "help deployments"},
		{"--version", "--version"},
	}
	for _, tc := range cases {
		got, err := main.RewriteCliTree(strings.Fields(tc.in))
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if strings.Join(got, " ") != tc.want {
			t.Errorf("%q rewrote to %q, want %q", tc.in, strings.Join(got, " "), tc.want)
		}
	}
}

func TestCliTreeAppVerbNeedsAnApp(t *testing.T) {
	enableEveryCLIFeature(t)
	t.Chdir(t.TempDir())
	code, _, stderr := runCLI(t, "app", "health")
	if code == 0 || !strings.Contains(stderr, "name the app") {
		t.Fatalf("app health without an app = %d %q, want a usage error naming the app", code, stderr)
	}
	if code, _, stderr := runCLI(t, "app", "network", "--help"); code != 0 {
		t.Fatalf("app network --help without an app = %d %q, want help", code, stderr)
	}
}

func TestCliTreeNounHelp(t *testing.T) {
	enableEveryCLIFeature(t)
	for _, args := range [][]string{{"release"}, {"release", "--help"}, {"help", "release"}, {"cron", "--help"}, {"help", "cron"}} {
		if args[len(args)-1] == "cron" || args[0] == "cron" {
			code, stdout, _ := runCLI(t, args...)
			if code != 0 || !listsCommand(stdout, "create") {
				t.Errorf("gregale %s = %d, want cron help:\n%s", strings.Join(args, " "), code, stdout)
			}
			continue
		}
		code, stdout, _ := runCLI(t, args...)
		if code != 0 || !listsCommand(stdout, "list") || !listsCommand(stdout, "canary") {
			t.Errorf("gregale %s = %d, want release help listing list and canary:\n%s", strings.Join(args, " "), code, stdout)
		}
	}
	code, stdout, _ := runCLI(t, "cron")
	if code != 0 || !listsCommand(stdout, "create") || !listsCommand(stdout, "delete") || listsCommand(stdout, "add") {
		t.Errorf("gregale cron = %d, want today's crons verbs in standard spelling:\n%s", code, stdout)
	}
	code, _, stderr := runCLI(t, "release", "bogus")
	if code == 0 || !strings.Contains(stderr, "gregale release --help") {
		t.Errorf("gregale release bogus = %d %q, want an unknown command pointing at release help", code, stderr)
	}
}
