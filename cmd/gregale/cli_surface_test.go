package main_test

import (
	"bytes"
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestTopLevelUsageGroupsCustomerCommands(t *testing.T) {
	out := main.TopLevelUsage(false, false)
	for _, want := range []string{
		"Get started:",
		"Examples:",
		"gregale deploy --preview",
		"Run 'gregale help --all'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("default help missing %q", want)
		}
	}
	for _, hidden := range []string{"admin", "mail", "postgres", "rollouts", "github-webhook-secret", "tenant-surfaces", "product"} {
		if strings.Contains(out, "  "+hidden+" ") {
			t.Errorf("default help exposes hidden command %q:\n%s", hidden, out)
		}
	}
	// The first screen speaks the command tree (ADR-20261026145832).
	for _, visible := range []string{"deploy", "dev", "app", "env", "logs", "status"} {
		if !strings.Contains(out, "  "+visible+" ") {
			t.Errorf("default help omits %q:\n%s", visible, out)
		}
	}
	for _, earlier := range []string{"apps", "inspect", "openapi", "doctor"} {
		if strings.Contains(out, "  "+earlier+" ") {
			t.Errorf("default help lists today's name %q instead of the tree's:\n%s", earlier, out)
		}
	}
	if lines := strings.Count(out, "\n"); lines > 45 {
		t.Errorf("default help has %d lines; keep the first screen concise", lines)
	}
}

// operatorCommands are callable but listed only by `help --all --operator`.
var operatorCommands = []string{"admin", "commit", "data-repair", "entities", "github-webhook-secret", "mail", "postgres", "product", "rollouts", "tenant-surfaces"}

// cliTreeFamilies are the sections of `help --all` after the shortcuts.
var cliTreeFamilies = []string{"Shortcuts:", "Ship:", "Run:", "Configure:", "Data:", "Work:", "Observe:", "Agents:", "Account:"}

// helpSection returns the lines of a root help section.
func helpSection(out, title string) string {
	start := strings.Index(out, "\n"+title+"\n")
	if start < 0 {
		return ""
	}
	end := strings.Index(out[start+1:], "\n\n") + start + 1
	return out[start:end]
}

func TestTopLevelUsageAllListsTheCommandTree(t *testing.T) {
	enableEveryCLIFeature(t)
	out := main.TopLevelUsage(true, false)
	last := -1
	for _, section := range cliTreeFamilies {
		at := strings.Index(out, "\n"+section+"\n")
		if at < 0 {
			t.Fatalf("--all help omits %q:\n%s", section, out)
		}
		if at < last {
			t.Errorf("--all help lists %q out of family order", section)
		}
		last = at
	}
	for _, tc := range []struct{ command, section string }{
		{"deploy", "Shortcuts:"}, {"logs", "Shortcuts:"}, {"release", "Ship:"}, {"app", "Run:"},
		{"env", "Configure:"}, {"bucket", "Data:"}, {"cron", "Work:"}, {"alert", "Observe:"},
		{"mcp", "Agents:"}, {"billing", "Account:"}, {"start", "Shortcuts:"}, {"operation", "Work:"},
	} {
		if !strings.Contains(helpSection(out, tc.section), "\n  "+tc.command+" ") {
			t.Errorf("--all help does not list %q under %q:\n%s", tc.command, tc.section, out)
		}
	}
	for _, earlier := range append([]string{"crons", "deploys", "deployments", "secrets", "apps", "slos"}, operatorCommands...) {
		if strings.Contains(out, "\n  "+earlier+" ") {
			t.Errorf("--all help lists %q; customers see the tree's names", earlier)
		}
	}
	entries := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  ") && len(line) > 2 && line[2] >= 'a' && line[2] <= 'z' {
			entries++
		}
	}
	// 19 shortcuts and 44 nouns, down from 120 commands; unplaced commands
	// are listed under More until they find a noun.
	if entries > 80 {
		t.Errorf("--all help lists %d commands; the tree keeps it near 75", entries)
	}
	if !strings.Contains(out, "gregale help --all --operator") {
		t.Error("--all help does not say how to list operator commands")
	}
}

func TestTopLevelUsageOperatorKeepsCompatibilityCommandsDiscoverable(t *testing.T) {
	out := main.TopLevelUsage(true, true)
	section := strings.Index(out, "\nOperator and compatibility:\n")
	if section < 0 {
		t.Fatalf("operator help missing its section:\n%s", out)
	}
	for _, command := range operatorCommands {
		if !strings.Contains(out[section:], "\n  "+command+" ") {
			t.Errorf("operator help omits %q", command)
		}
	}
	earlier := helpSection(out, "Earlier names (still accepted):")
	for _, tc := range []struct{ name, now string }{{"crons", "now gregale cron"}, {"secrets", "now gregale env"}, {"apps", "now gregale app"}} {
		if !strings.Contains(earlier, "\n  "+tc.name+" ") || !strings.Contains(earlier, tc.now) {
			t.Errorf("operator help does not map %q to %q:\n%s", tc.name, tc.now, earlier)
		}
	}
	if !strings.Contains(out, "\nShortcuts:\n") {
		t.Error("operator help drops the customer tree")
	}
}

func TestRootHelpFlagsSelectTheView(t *testing.T) {
	oldOut := (*main.OsStdout)
	t.Cleanup(func() { (*main.OsStdout) = oldOut })
	for _, tc := range []struct {
		args           []string
		all, operators bool
	}{
		{[]string{"help"}, false, false},
		{[]string{"--help"}, false, false},
		{[]string{"help", "--help"}, false, false},
		{[]string{"help", "--all"}, true, false},
		{[]string{"--help", "--all"}, true, false},
		{[]string{"help", "--all", "--operator"}, true, true},
		{[]string{"help", "--operator", "--all"}, true, true},
		{[]string{"help", "--operator"}, true, true},
	} {
		var out bytes.Buffer
		(*main.OsStdout) = &out
		if code := main.Run(tc.args); code != 0 {
			t.Fatalf("%v exit = %d", tc.args, code)
		}
		if got := strings.Contains(out.String(), "\nWork:\n"); got != tc.all {
			t.Errorf("%v lists every command = %v, want %v", tc.args, got, tc.all)
		}
		if got := strings.Contains(out.String(), "\nOperator and compatibility:\n"); got != tc.operators {
			t.Errorf("%v lists operator commands = %v, want %v", tc.args, got, tc.operators)
		}
	}
}

func TestCliAudienceFiltersOnlyDiscoverySurfaces(t *testing.T) {
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "1")
	for _, command := range *main.CliCommands {
		if command.Audience == main.CliAudienceCustomer {
			continue
		}
		if _, ok := main.LookupCliCommand(command.Name); !ok {
			t.Errorf("hidden command %q is missing from lookup manifest", command.Name)
		}
	}
	if got := len(main.CustomerCliCommands()) + len(main.AdvancedCliCommands()); got != len(main.VisibleCliCommands()) {
		t.Fatalf("audience partition lost commands: customer+advanced=%d visible=%d", got, len(main.VisibleCliCommands()))
	}
}

// Application Auth is a dark launch: without FAAS_APPLICATION_AUTH_ENABLED
// nothing of it reaches help, completions, the reference, suggestions,
// dispatch, flag parsing or templates.
func TestApplicationAuthStaysOutOfEveryCustomerSurface(t *testing.T) {
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "")
	if _, ok := main.LookupCliCommand("auth"); ok {
		t.Fatal("lookup finds the auth command")
	}
	var surfaces bytes.Buffer
	surfaces.WriteString(main.TopLevelUsage(true, true))
	main.RenderMarkdownReference(&surfaces, main.CustomerCliCommands())
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		oldOut := (*main.OsStdout)
		(*main.OsStdout) = &surfaces
		code := main.Run([]string{"completion", shell})
		(*main.OsStdout) = oldOut
		if code != 0 {
			t.Fatalf("completion %s = %d", shell, code)
		}
	}
	for _, leak := range []string{"gregale auth", "Application Auth", "application Auth", "jwt-auth", "AUTH_APP"} {
		if strings.Contains(surfaces.String(), leak) {
			t.Errorf("a customer surface mentions %q", leak)
		}
	}
	if suggestion, ok := main.SuggestCommand("auht"); ok && suggestion == "auth" {
		t.Error("an unknown command suggests auth")
	}
	_, stderr, restore := main.SwapIO(t)
	defer restore()
	if code := main.Run([]string{"auth", "create", "shop-auth"}); code == 0 || !strings.Contains(stderr(), "unknown command") {
		t.Fatalf("gregale auth = %d: %s", code, stderr())
	}
	for _, args := range [][]string{{"data-api", "create", "notes", "--database", "db", "--auth", "shop-auth"}, {"pipeline", "create", "notes", "--auth", "shop-auth"}, {"pipeline", "create", "notes", "--api", "notes-data"}, {"edge-rules", "create", "--app", "shop", "--kind", "jwt", "--jwt-auth", "shop-auth"}} {
		before := strings.Count(stderr(), "unknown flag")
		if code := main.Run(args); code == 0 || strings.Count(stderr(), "unknown flag") != before+1 {
			t.Errorf("%v = %d: %s", args, code, stderr())
		}
	}
	if main.TemplateAvailable("auth") || !main.TemplateAvailable("hello-node") {
		t.Fatal("the auth template is available")
	}
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "1")
	if _, ok := main.LookupCliCommand("auth"); !ok || !main.TemplateAvailable("auth") {
		t.Fatal("turning Auth on does not show it")
	}
	before := len(stderr())
	if code := main.Run([]string{"pipeline", "create", "notes", "--api", "notes-data", "--auth", "shop-auth"}); code != 1 || !strings.Contains(stderr()[before:], "Invalid pipeline") {
		t.Fatalf("turning Auth on does not recognize the pipeline API shortcut: %d: %s", code, stderr()[before:])
	}
}

func TestManifestHelpResolvesTopicsAndFlagOrder(t *testing.T) {
	oldOut := (*main.OsStdout)
	t.Cleanup(func() { (*main.OsStdout) = oldOut })
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"help", "deploy"}, "Source defaults to committed HEAD"},
		{[]string{"deploy", "--plan", "--help"}, "Common flags:"},
		{[]string{"deploy", "--plan", "--help", "--all"}, "--execution-mode"},
		{[]string{"deployment", "wait", "--help"}, "gregale deployment wait <id|vN>"},
		{[]string{"help", "deployment", "wait"}, "--rollout"},
		{[]string{"deployments", "alias", "list", "--help"}, "gregale deployments alias list"},
	} {
		var out bytes.Buffer
		(*main.OsStdout) = &out
		if code := main.Run(tc.args); code != 0 {
			t.Errorf("run(%v) = %d", tc.args, code)
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("run(%v) output missing %q: %s", tc.args, tc.want, out.String())
		}
	}
}

// Every flag the edge-rules create/update parsers accept must be in their
// help and the CLI reference. Production-us rc.251: `--kind cors` failed
// with "requires at least one allow_origin" while create's help listed no
// --cors-allow-origin; 21 create flags (all of kind=headers, CORS origins,
// JWT audience/claims, --name, --expires-*) were parsed but undocumented.
func TestEdgeRuleParsedFlagsAppearInCreateAndUpdateHelp(t *testing.T) {
	oldOut := (*main.OsStdout)
	t.Cleanup(func() { (*main.OsStdout) = oldOut })
	parsed := flagsRegisteredIn(t, "commands_edge_rules.go")
	for subcommand, fn := range map[string]string{"create": "cmdEdgeRulesCreate", "update": "cmdEdgeRulesUpdate"} {
		if len(parsed[fn]) == 0 {
			t.Fatalf("found no flags registered in %s", fn)
		}
		var out bytes.Buffer
		(*main.OsStdout) = &out
		if code := main.Run([]string{"edge-rules", subcommand, "--help"}); code != 0 {
			t.Fatalf("edge-rules %s --help = %d", subcommand, code)
		}
		for _, flag := range parsed[fn] {
			if !strings.Contains(out.String(), "--"+flag+" ") && !strings.Contains(out.String(), "--"+flag+"[") && !strings.Contains(out.String(), "--"+flag+"\n") {
				t.Errorf("edge-rules %s parses --%s but its help omits it", subcommand, flag)
			}
		}
	}
}

// flagsRegisteredIn returns, per top-level function in file, the names of
// flags registered on a FlagSet variable named fs.
func flagsRegisteredIn(t *testing.T, file string) map[string][]string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]string)
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if recv, ok := sel.X.(*ast.Ident); !ok || recv.Name != "fs" {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				if name, err := strconv.Unquote(lit.Value); err == nil {
					out[fn.Name.Name] = append(out[fn.Name.Name], name)
				}
				break
			}
			return true
		})
	}
	return out
}

func TestCommonCommandExamplesAgreeAcrossHelpAndReference(t *testing.T) {
	for _, name := range []string{"app", "apps", "deploy", "dev", "doctor", "init", "inspect", "login", "logs", "start"} {
		t.Run(name, func(t *testing.T) {
			command, ok := main.LookupCliCommand(name)
			if !ok || len(command.Examples) < 2 {
				t.Fatalf("%s needs at least two runnable examples", name)
			}
			var help, manual, reference bytes.Buffer
			main.PrintLocalCommandHelp(&help, command)
			main.RenderManCommand(&manual, command)
			main.RenderMarkdownReference(&reference, []main.CliCommand{command})
			for _, example := range command.Examples {
				for surface, output := range map[string]string{
					"help": help.String(), "manual": manual.String(), "reference": reference.String(),
				} {
					if !strings.Contains(output, example) {
						t.Errorf("%s missing example %q", surface, example)
					}
				}
			}
		})
	}
}

func TestUnknownCommandSuggestsOnlyClearMatch(t *testing.T) {
	main.ResetJSONOut(t)
	for _, tc := range []struct {
		args       []string
		suggestion string
	}{
		{args: []string{"deply"}, suggestion: "gregale deploy"},
		{args: []string{"help", "deply"}, suggestion: "gregale help deploy"},
		{args: []string{"aps"}}, // app, apps, and ps are equally close.
	} {
		stderr, restore := main.CaptureStderr(t)
		if code := main.Run(tc.args); code != 1 {
			t.Errorf("run(%v) = %d, want 1", tc.args, code)
		}
		restore()
		if tc.suggestion != "" && !strings.Contains(stderr.String(), tc.suggestion) {
			t.Errorf("run(%v) omitted %q: %s", tc.args, tc.suggestion, stderr.String())
		}
		if tc.suggestion == "" && strings.Contains(stderr.String(), "did you mean") {
			t.Errorf("ambiguous typo got suggestion: %s", stderr.String())
		}
	}
}

func TestUnknownCommandSuggestionKeepsJSONErrorParseable(t *testing.T) {
	main.ResetJSONOut(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"deply", "--json"}, want: "gregale deploy"},
		{args: []string{"help", "deply", "--json"}, want: "gregale help deploy"},
	} {
		stderr, restore := main.CaptureStderr(t)
		if code := main.Run(tc.args); code != 1 {
			t.Errorf("run(%v) = %d, want 1", tc.args, code)
		}
		restore()
		var problem struct {
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal([]byte(stderr.String()), &problem); err != nil {
			t.Fatalf("run(%v) emitted invalid JSON error %q: %v", tc.args, stderr.String(), err)
		}
		if !strings.Contains(problem.Detail, tc.want) {
			t.Errorf("run(%v) omitted %q: %q", tc.args, tc.want, problem.Detail)
		}
	}
}

// Keep the parser, generated reference, completion, and local help aligned.
func TestDeployManifestIncludesEveryParserFlag(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "commands2.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	parsed := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "cmdDeployTarballToExisting" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			// secondsOrDurationFlag(fs, "timeout", ...) defines a flag too.
			if ident, isIdent := call.Fun.(*ast.Ident); isIdent && ident.Name == "secondsOrDurationFlag" && len(call.Args) > 1 {
				if literal, isLit := call.Args[1].(*ast.BasicLit); isLit {
					if name, err := strconv.Unquote(literal.Value); err == nil {
						parsed[name] = true
					}
				}
				return true
			}
			// resourceSizeFlag(fs, ...) defines --size and its deprecated --profile.
			if ident, isIdent := call.Fun.(*ast.Ident); isIdent && ident.Name == "resourceSizeFlag" {
				parsed["size"], parsed["profile"] = true, true
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			receiver, ok := sel.X.(*ast.Ident)
			if !ok || receiver.Name != "fs" {
				return true
			}
			if (sel.Sel.Name == "Var" || sel.Sel.Name == "BoolVar" || sel.Sel.Name == "StringVar") && len(call.Args) > 1 {
				if literal, ok := call.Args[1].(*ast.BasicLit); ok {
					name, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Error(err)
					} else {
						parsed[name] = true
					}
				}
				return true
			}
			switch sel.Sel.Name {
			case "Bool", "String", "Int", "Int64", "Duration":
				literal, ok := call.Args[0].(*ast.BasicLit)
				if !ok {
					t.Errorf("non-literal deploy flag in %s", sel.Sel.Name)
					return true
				}
				name, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Error(err)
					return true
				}
				parsed[name] = true
			}
			return true
		})
	}
	command, ok := main.LookupCliCommand("deploy")
	if !ok || len(parsed) == 0 {
		t.Fatal("deploy parser or manifest missing")
	}
	manifest := map[string]bool{"json": true} // --json is consumed globally.
	for _, flag := range command.Flags {
		manifest[flag.Name] = true
	}
	for name := range parsed {
		if !manifest[name] {
			t.Errorf("deploy parser flag --%s missing from manifest", name)
		}
	}
	for name := range manifest {
		if !parsed[name] {
			t.Errorf("deploy manifest flag --%s missing from parser", name)
		}
	}
}
