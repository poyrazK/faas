package main

import (
	"bytes"
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
