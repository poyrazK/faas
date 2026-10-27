package main_test

import (
	"os"
	"path/filepath"
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
	"resolve", "audit", "accept", "deploy", "suppress",
	// Plain verbs today's commands already use with one meaning.
	"abandon", "activate", "deactivate", "adopt", "allow", "assign", "attach", "detach",
	"authorize", "bind", "unbind", "rebind", "capture", "clear", "close", "compare", "configure",
	"confirm", "copy", "correlate", "diagnose", "dismiss", "download", "upload", "drain", "drop",
	"enroll", "evaluate", "export", "import", "finalize", "grant", "ignore", "init", "install",
	"invite", "investigate", "lift", "lock", "merge", "minimize", "onboard", "prepare", "protect",
	"unprotect", "qualify", "reconcile", "reconnect", "refresh", "rehearse", "reopen", "require",
	"resize", "resync", "retire", "revoke", "review", "scan", "split", "start", "stop", "subscribe",
	"unsubscribe", "suggest", "suspend", "test", "track", "use", "validate", "override", "advise",
	"reset", "pull", "push", "signal", "generate",
}

// A path may end with a noun it lists or shows (`app instances`).
var cliTreeReadNouns = []string{
	"instances", "requests", "tools", "prompts", "resources", "plan", "invoices", "spend-cap",
	"streaming-cap", "wake-timeline", "trace", "runtime", "chaos", "items", "history", "health",
	"policy", "actions", "activity", "alerts", "artifacts", "attempts", "backlog", "bundle",
	"capabilities", "captures", "catalog", "channels", "codeowners", "completeness", "connections",
	"controls", "costs", "coverage", "credits", "daily", "declarations", "deliveries",
	"dependencies", "deployments", "diagnostics", "events", "evidence", "feedback", "forecast",
	"guardrails", "impact", "incidents", "insights", "keys", "links", "me", "metrics",
	"occurrences", "outcomes", "overview", "parts", "pending", "policies", "prices", "priority",
	"profiles", "progress", "provenance", "readiness", "receipt", "records", "releases", "report",
	"results", "retention", "routes", "runs", "running", "sbom", "stats", "steps", "storage",
	"summary", "tables", "tags", "tasks", "executions", "milestones", "timeline", "teams", "tickets", "tokens", "traces", "types", "usage",
	"users", "views", "signups", "statements", "switches", "recipes", "repos", "resumes", "state",
	"stages",
}

// Rule 6: hyphens only inside these compound words.
var cliTreeCompoundWords = []string{
	"data-api", "rule-list", "egress-allowlist", "egress-ports", "static-egress-ip",
	"port-forward", "wake-ahead", "wake-timeline", "streaming-cap", "spend-cap", "upload-cache",
	"object-lock", "subscription-version", "subscription-retry", "subscription-circuit",
	"subscription-capacity", "lock-policy",
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
		if path[0] == "admin" {
			continue // operator tools keep their names (rule 7)
		}
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
			if _, hasVerbs := main.CliPathHasVerbs(path); hasVerbs {
				t.Errorf("%q: today's command takes verbs, so the tree's verbs never apply", alias.New)
			}
			continue
		}
		if main.OldPathDepth(path) >= len(path) {
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
		{"bucket encryption set AES256 --app api", "bucket encryption AES256 --app api"},
		{"bucket encryption status --app api", "bucket encryption status --app api"},
		{"bucket object-lock set GOVERNANCE", "bucket object-lock GOVERNANCE"},
		{"bucket multipart list", "bucket uploads list"},
		{"bucket multipart get u1", "bucket uploads status u1"},
		{"bucket uploads list", "bucket uploads list"},
		{"bucket version delete k", "bucket version-delete k"},
		{"bucket version list", "bucket versions list"},
		{"bucket deletion create", "bucket deletions start"},
		{"bucket inventory get", "bucket reconcile status"},
		{"bucket inventory cancel", "bucket reconcile cancel"},
		{"event subscription pause orders", "events subscription-pause orders"},
		{"event subscription-circuit get orders", "events subscription-circuit-status orders"},
		{"event backfill create --source s", "events backfill --source s"},
		{"event recovery update r1", "events recovery-rate r1"},
		{"event publish --source s", "events publish --source s"},
		{"automation recovery health --app api", "automations recovery-health --app api"},
		{"automation failure list --app api", "automations failures --app api"},
		{"release stages 42", "deploys show 42"},
		{"release summary 42", "deployment summary 42"},
		{"release attempt cancel a1", "deploy abort a1"},
		{"app listener list api", "app api tcp list"},
		{"app listener create api --port 7000 --protocol udp", "app api udp add --port 7000"},
		{"app listener delete --protocol=tcp api l1", "app api tcp delete l1"},
		{"alert maintenance create --app api --reason x", "mcp readiness maintenance create --app api --reason x"},
		{"alert pagerduty get --app api", "mcp readiness pagerduty get --app api"},
		{"job lock-policy set exports --file p.json", "operations policy upsert exports --file p.json"},
		{"admin sites list", "sites list"},
		{"admin postgres reconcile acct-1", "postgres reconcile acct-1"},
		{"admin credit --reason x", "admin credit --reason x"},
		{"dashboard list", "dashboards list"},
		{"dashboard", "dashboard"},
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

// Step 5 (the flip): a noun that is also today's command lists the tree's
// verbs once the tree reshapes it, a subnoun only the tree has gets help of
// its own, and a noun the tree passes through keeps today's fuller help.
func TestCliTreeHelpForTodaysCommands(t *testing.T) {
	enableEveryCLIFeature(t)
	for _, tc := range []struct {
		args       []string
		want, lack []string
	}{
		{[]string{"env", "--help"}, []string{"set", "unset", "pull", "rollback"}, []string{"create", "clone-status"}},
		{[]string{"help", "env"}, []string{"set", "push"}, nil},
		{[]string{"mcp", "--help"}, []string{"serve", "connection", "server", "client"}, []string{"call", "task-get"}},
		{[]string{"mcp", "client"}, []string{"tools", "call", "status"}, []string{"task-get"}},
		{[]string{"release", "attempt", "--help"}, []string{"get", "cancel", "retry"}, nil},
		{[]string{"app", "--help"}, []string{"list", "get", "update", "scale", "health", "listener"}, []string{"tcp", "udp"}},
		{[]string{"bucket", "--help"}, []string{"multipart", "version", "inventory"}, []string{"uploads", "versions", "reconcile"}},
	} {
		code, stdout, stderr := runCLI(t, tc.args...)
		if code != 0 {
			t.Errorf("gregale %s = %d %q", strings.Join(tc.args, " "), code, stderr)
			continue
		}
		for _, verb := range tc.want {
			if !listsCommand(stdout, verb) {
				t.Errorf("gregale %s does not list %q:\n%s", strings.Join(tc.args, " "), verb, stdout)
			}
		}
		for _, verb := range tc.lack {
			if listsCommand(stdout, verb) {
				t.Errorf("gregale %s lists %q, which the tree renames or moves:\n%s", strings.Join(tc.args, " "), verb, stdout)
			}
		}
	}
	if _, stdout, _ := runCLI(t, "preview", "--help"); !strings.Contains(stdout, "gregale preview <create|") {
		t.Errorf("preview --help must keep today's help:\n%s", stdout)
	}
	if _, stdout, _ := runCLI(t, "app", "--help", "--all"); !strings.Contains(stdout, "Access and security:") {
		t.Errorf("app --help --all must list every option of today's app command:\n%s", stdout)
	}
}

// Today's commands have a tree spelling that runs exactly them.
func TestCliTreeSpelling(t *testing.T) {
	enableEveryCLIFeature(t)
	t.Chdir(t.TempDir())
	for _, tc := range []struct{ in, want string }{
		{"crons info c1", "cron get c1"},
		{"crons add --path /sync", "cron create --path /sync"},
		{"apps", "app list"},
		{"apps rm api --yes", "app delete api --yes"},
		{"app api", "app get api"},
		{"app api scale --ram 512", "app scale api --ram 512"},
		{"app api egress-allowlist add 10.0.0.0/8", "app egress-allowlist add api 10.0.0.0/8"},
		{"app api tcp list", "app listener list api"},
		{"app api udp add --port 7000", "app listener create api --port 7000 --protocol udp"},
		{"deploys status 42", "release get 42"},
		{"secrets set K=V", "env set K=V"},
		{"bucket uploads list", "bucket multipart list"},
		{"ps api", "app instances api"},
		// Already the tree's names, or nothing in the tree runs exactly this.
		{"cron list", ""},
		{"deploy --prod", ""},
		{"crons", ""},
		{"env push", ""},
		{"preview show 12", "preview get 12"},
		{"mcp serve --transport stdio", ""},
		{"mcp doctor", "mcp server doctor"},
		{"mcp server readiness", ""}, // already the tree's names
		{"dashboard list", ""},
		{"run --image alpine", ""}, // a shortcut keeps its name
		{"rollback api", ""},
		{"app api --ram", ""}, // `app <slug> --flag` updates the app
	} {
		got, ok := main.CliTreeSpelling(strings.Fields(tc.in))
		if gotText := strings.Join(got, " "); (tc.want == "") == ok || gotText != tc.want {
			t.Errorf("%q spelled %q (%v), want %q", tc.in, gotText, ok, tc.want)
		}
	}
}

// An earlier name still runs; with stderr in a terminal it names the tree
// path there, and help, --json or --non-interactive print nothing extra.
func TestCliTreeHintsEarlierNamesInATerminal(t *testing.T) {
	enableEveryCLIFeature(t)
	t.Chdir(t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	tty := true
	prev := *main.CliTreeHintTerminal
	*main.CliTreeHintTerminal = func() bool { return tty }
	t.Cleanup(func() { *main.CliTreeHintTerminal = prev })
	const hint = "Tip: 'gregale crons list api' is now 'gregale cron list api'"
	if _, _, stderr := runCLI(t, "crons", "list", "api"); !strings.Contains(stderr, hint) {
		t.Errorf("crons list in a terminal = %q, want the tree name", stderr)
	}
	for _, args := range [][]string{
		{"--json", "crons", "list", "api"},
		{"--non-interactive", "crons", "list", "api"},
		{"cron", "list", "api"},
		{"crons", "list", "--help"},
		{"help", "crons", "list"},
	} {
		if _, _, stderr := runCLI(t, args...); strings.Contains(stderr, "Tip: ") {
			t.Errorf("gregale %s printed a hint: %q", strings.Join(args, " "), stderr)
		}
	}
	tty = false
	if _, _, stderr := runCLI(t, "crons", "list", "api"); strings.Contains(stderr, "Tip: ") {
		t.Errorf("crons list without a terminal printed a hint: %q", stderr)
	}
}

// Help reached through a tree path is spelled in that path.
func TestCliTreeLeafHelpUsesTheTypedPath(t *testing.T) {
	enableEveryCLIFeature(t)
	for _, tc := range []struct {
		args       []string
		want, lack string
	}{
		{[]string{"env", "set", "--help"}, "gregale env set", "gregale secrets"},
		{[]string{"cron", "get", "--help"}, "gregale cron get", "gregale crons"},
		{[]string{"help", "release", "list"}, "gregale release list", "See also:"},
		{[]string{"release", "canary", "--help"}, "gregale release canary", "gregale canary"},
	} {
		code, stdout, stderr := runCLI(t, tc.args...)
		if code != 0 || !strings.Contains(stdout, tc.want) || strings.Contains(stdout, tc.lack) {
			t.Errorf("gregale %s = %d %q, want %q and not %q:\n%s", strings.Join(tc.args, " "), code, stderr, tc.want, tc.lack, stdout)
		}
	}
	if _, stdout, _ := runCLI(t, "crons", "info", "--help"); !strings.Contains(stdout, "gregale crons info") {
		t.Errorf("today's path keeps today's spelling:\n%s", stdout)
	}
}

const cliTreeVerbBaselinePath = "testdata/cli_tree_verb_baseline.txt"

// cliTreeVerbViolations walks the verbs a tree noun passes through from
// today's command (`cron` → every `crons` verb) and reports each path that
// breaks the naming rules. Verbs that have a tree name of their own are
// skipped.
func cliTreeVerbViolations() []string {
	renamed, explicit := map[string]bool{}, map[string]bool{}
	for _, alias := range *main.CliTreeAliases {
		renamed[alias.Old], explicit[alias.New] = true, true
	}
	found := map[string]bool{}
	check := func(path []string, leaf bool) {
		var reasons []string
		if len(path) > 3 {
			reasons = append(reasons, "more than three words")
		}
		for _, word := range path {
			if strings.Contains(word, "-") && !slices.Contains(cliTreeCompoundWords, word) {
				reasons = append(reasons, "hyphenated "+word)
			}
		}
		last := path[len(path)-1]
		if leaf && !slices.Contains(cliTreeVerbs, last) && !slices.Contains(cliTreeReadNouns, last) {
			reasons = append(reasons, "verb "+last)
		}
		if len(reasons) > 0 {
			found[strings.Join(path, " ")+"  # "+strings.Join(reasons, ", ")] = true
		}
	}
	var walk func(newPath, oldPath []string, depth int)
	walk = func(newPath, oldPath []string, depth int) {
		for _, child := range main.CliPathChildren(oldPath) {
			old := append(slices.Clone(oldPath), child.Name)
			path := append(slices.Clone(newPath), main.CliTreeStandardVerb(child.Name))
			if renamed[strings.Join(old, " ")] || explicit[strings.Join(path, " ")] {
				continue
			}
			check(path, !child.HasVerbs)
			if child.HasVerbs && depth < 2 {
				walk(path, old, depth+1)
			}
		}
	}
	for _, alias := range *main.CliTreeAliases {
		if !strings.HasPrefix(alias.New, "admin ") {
			walk(strings.Fields(alias.New), strings.Fields(alias.Old), 0)
		}
	}
	lines := make([]string, 0, len(found))
	for line := range found {
		lines = append(lines, line)
	}
	slices.Sort(lines)
	return lines
}

// Today's verbs reachable through the tree follow the naming rules, or are
// listed in the baseline, which only shrinks (ADR-20261026145832 step 2).
// After renaming verbs, rewrite it with GREGALE_UPDATE_CLI_TREE_BASELINE=1.
func TestCliTreeVerbBaseline(t *testing.T) {
	enableEveryCLIFeature(t)
	got := cliTreeVerbViolations()
	if os.Getenv("GREGALE_UPDATE_CLI_TREE_BASELINE") == "1" {
		body := "# Paths in the ADR-20261026145832 tree that still break its naming rules.\n# This list only shrinks: give a path a tree name in cli_tree.go instead of adding it.\n" + strings.Join(got, "\n") + "\n"
		if err := os.WriteFile(cliTreeVerbBaselinePath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(cliTreeVerbBaselinePath)
	if err != nil {
		t.Fatal(err)
	}
	baseline := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if path, _, _ := strings.Cut(line, "  #"); path != "" && !strings.HasPrefix(path, "#") {
			baseline[path] = true
		}
	}
	current := map[string]bool{}
	for _, line := range got {
		path, _, _ := strings.Cut(line, "  #")
		current[path] = true
		if !baseline[path] {
			t.Errorf("%s breaks the tree's naming rules; give it a tree name in cli_tree.go", line)
		}
	}
	for path := range baseline {
		if !current[path] {
			t.Errorf("%q now follows the rules; remove it from %s (GREGALE_UPDATE_CLI_TREE_BASELINE=1)", path, cliTreeVerbBaselinePath)
		}
	}
}

// ADR-20261026145832 step 4: canonical flag spellings run today's
// equivalent flag, only where its help names the same concept.
func TestCliTreeFlagSpellings(t *testing.T) {
	enableEveryCLIFeature(t)
	cases := []struct{ in, want string }{
		{"app api health --env staging", "app api health --environment staging"},
		{"app api health --cursor=c1 --history", "app api health --before=c1 --history"},
		{"env pull --env staging", "env pull --scope staging"},
		{"debug requests list --release d1", "debug requests list --deployment-id d1"},
		{"dlq list demo --cursor c2", "dlq list demo --before c2"},
		{"events backfill demo --since 2026-10-01T00:00:00Z", "events backfill demo --from 2026-10-01T00:00:00Z"},
		{"trace t1 --follow", "trace t1 --watch"},
		{"env create --since x staging", "env create --since x staging"},
		{"logs api --release v3", "logs api --release v3"},
		{"help app --env", "help app --env"},
	}
	for _, tc := range cases {
		if got := strings.Join(main.NormalizeTreeFlags(strings.Fields(tc.in)), " "); got != tc.want {
			t.Errorf("%q normalized to %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Tree help and gating follow the account's capabilities like today's
// commands: a hidden capability's verbs are not listed, its paths answer
// with the name the user typed, and a flag that reaches it is dropped.
func TestCliTreeFollowsCapabilityVisibility(t *testing.T) {
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "")
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))

	code, help, _ := runCLI(t, "alert")
	if code != 0 || !listsCommand(help, "channel") {
		t.Fatalf("alert help = %d, want the beta channel verb:\n%s", code, help)
	}
	for _, hidden := range []string{"incident", "slo", "check", "maintenance list"} {
		if listsCommand(help, hidden) {
			t.Errorf("logged-out alert help lists %q, whose capability is preview or internal:\n%s", hidden, help)
		}
	}
	if code, _, stderr := runCLI(t, "alert", "incident", "list", "--help"); code == 0 || !strings.Contains(stderr, `unknown command "alert incident list"`) {
		t.Errorf("alert incident list --help = %d %q, want an unknown command in the typed name", code, stderr)
	}
	if got := main.TierDiscoverableFlags([]string{"app", "at", "lock"}, []string{"", "delayed-execution", "operations"}); strings.Join(got, " ") != "app at" {
		t.Errorf("tier discovery kept flags %v, want app and the beta delayed-execution flag only", got)
	}
}
