package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestTopLevelUsageGroupsCustomerCommands(t *testing.T) {
	out := topLevelUsage(false)
	for _, want := range []string{
		"Get started:",
		"Examples:",
		"Run 'gregale help --all'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("default help missing %q", want)
		}
	}
	for _, hidden := range []string{"admin", "mail", "postgres", "rollouts", "github-webhook-secret", "tenant-surfaces"} {
		if strings.Contains(out, "  "+hidden+" ") {
			t.Errorf("default help exposes hidden command %q:\n%s", hidden, out)
		}
	}
	for _, visible := range []string{"deploy", "dev", "inspect", "logs", "openapi"} {
		if !strings.Contains(out, "  "+visible+" ") {
			t.Errorf("default help omits customer command %q", visible)
		}
	}
	if lines := strings.Count(out, "\n"); lines > 45 {
		t.Errorf("default help has %d lines; keep the first screen concise", lines)
	}
}

func TestTopLevelUsageAllKeepsCompatibilityAliasesDiscoverable(t *testing.T) {
	out := topLevelUsage(true)
	if !strings.Contains(out, "Advanced/operator compatibility:") {
		t.Fatalf("--all help missing advanced section:\n%s", out)
	}
	for _, section := range []string{"Customer commands:", "Core:", "API:", "Data:", "Delivery:", "Observe:"} {
		if !strings.Contains(out, section) {
			t.Errorf("--all help omits %q", section)
		}
	}
	for _, command := range []string{"admin", "mail", "postgres", "rollouts", "github-webhook-secret", "tenant-surfaces"} {
		if !strings.Contains(out, "  "+command+" ") {
			t.Errorf("--all help omits compatibility command %q", command)
		}
	}
}

func TestCliAudienceFiltersOnlyDiscoverySurfaces(t *testing.T) {
	for _, command := range cliCommands {
		if command.Audience == cliAudienceCustomer {
			continue
		}
		if _, ok := lookupCliCommand(command.Name); !ok {
			t.Errorf("hidden command %q is missing from lookup manifest", command.Name)
		}
	}
	if got := len(customerCliCommands()) + len(advancedCliCommands()); got != len(cliCommands) {
		t.Fatalf("audience partition lost commands: customer+advanced=%d manifest=%d", got, len(cliCommands))
	}
}

func TestManifestHelpResolvesTopicsAndFlagOrder(t *testing.T) {
	oldOut := osStdout
	t.Cleanup(func() { osStdout = oldOut })
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"help", "deploy"}, "Source defaults to committed HEAD"},
		{[]string{"deploy", "--plan", "--help"}, "--execution-mode"},
		{[]string{"deployment", "wait", "--help"}, "gregale deployment wait <id|vN>"},
		{[]string{"help", "deployment", "wait"}, "--rollout"},
		{[]string{"deployments", "alias", "list", "--help"}, "gregale deployments alias list"},
	} {
		var out bytes.Buffer
		osStdout = &out
		if code := run(tc.args); code != 0 {
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
	oldOut := osStdout
	t.Cleanup(func() { osStdout = oldOut })
	parsed := flagsRegisteredIn(t, "commands_edge_rules.go")
	for subcommand, fn := range map[string]string{"create": "cmdEdgeRulesCreate", "update": "cmdEdgeRulesUpdate"} {
		if len(parsed[fn]) == 0 {
			t.Fatalf("found no flags registered in %s", fn)
		}
		var out bytes.Buffer
		osStdout = &out
		if code := run([]string{"edge-rules", subcommand, "--help"}); code != 0 {
			t.Fatalf("edge-rules %s --help = %d", subcommand, code)
		}
		for _, flag := range parsed[fn] {
			if !strings.Contains(out.String(), "--"+flag+" ") && !strings.Contains(out.String(), "--"+flag+"[") {
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
	for _, name := range []string{"apps", "deploy", "dev", "doctor", "init", "inspect", "login", "logs"} {
		t.Run(name, func(t *testing.T) {
			command, ok := lookupCliCommand(name)
			if !ok || len(command.Examples) < 2 {
				t.Fatalf("%s needs at least two runnable examples", name)
			}
			var help, manual, reference bytes.Buffer
			printLocalCommandHelp(&help, command)
			renderManCommand(&manual, command)
			renderMarkdownReference(&reference, []cliCommand{command})
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
	resetJSONOut(t)
	for _, tc := range []struct {
		args       []string
		suggestion string
	}{
		{args: []string{"deply"}, suggestion: "gregale deploy"},
		{args: []string{"help", "deply"}, suggestion: "gregale help deploy"},
		{args: []string{"aps"}}, // app, apps, and ps are equally close.
	} {
		stderr, restore := captureStderr(t)
		if code := run(tc.args); code != 1 {
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
	resetJSONOut(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"deply", "--json"}, want: "gregale deploy"},
		{args: []string{"help", "deply", "--json"}, want: "gregale help deploy"},
	} {
		stderr, restore := captureStderr(t)
		if code := run(tc.args); code != 1 {
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
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			receiver, ok := sel.X.(*ast.Ident)
			if !ok || receiver.Name != "fs" {
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
	command, ok := lookupCliCommand("deploy")
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
