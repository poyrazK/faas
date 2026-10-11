// JSON parity inventories emitting command families and their tests.
// Both CLI packages keep the same extraction rules and regression fixtures.
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// nonJSONAllowList is the closed set of operator-side cmdXxx funcs
// that DELIBERATELY emit no JSON. Keep entries alphabetical.
var nonJSONAllowList = map[string]string{
	"cmdBackup":   "operator fs writes",
	"cmdHostAge":  "operator fs writes",
	"cmdPKI":      "operator fs writes",
	"cmdSignKeys": "operator fs writes",
	"cmdNodeKey":  "operator fs writes",
}

// TestJSONOutputHonored is the parity gate. Fails loudly when a
// top-level cmdXxx references jsonOutput (or any of its leaves
// does) but no test exercises that branch.
func TestJSONOutputHonored(t *testing.T) {
	jsonCmds, err := collectJSONEmitters()
	if err != nil {
		t.Fatalf("walk jsonOutput emitters: %v", err)
	}
	if len(jsonCmds) == 0 {
		t.Fatal("no jsonOutput emitters found — extractor is broken")
	}

	topLevel := map[string]bool{}
	for _, name := range jsonCmds {
		top := topLevelDispatcher(name)
		if top == "" {
			t.Errorf("JSON emitter %q does not map to a CLI command", name)
			continue
		}
		topLevel[top] = true
	}

	if len(topLevel) == 0 {
		t.Fatal("no JSON emitters mapped to CLI commands — dispatcher mapping is broken")
	}

	tested, err := jsonTestedTopLevel()
	if err != nil {
		t.Fatalf("walk JSON tests: %v", err)
	}
	if len(tested) == 0 {
		t.Fatal("no JSON-enabled command tests found — extractor is broken")
	}

	for c := range topLevel {
		if _, isAllowlisted := nonJSONAllowList[c]; isAllowlisted {
			continue
		}
		if !tested[c] {
			t.Errorf("top-level dispatcher %q emits JSON (or has JSON-emitting leaves) but no test sets jsonOutput = true for it; add a test or move to nonJSONAllowList with a rationale comment", c)
		}
	}
}

func topLevelDispatcher(leaf string) string {
	// These operator helpers predate the user-facing dispatcher names.
	for prefix, command := range map[string]string{
		"cmdOperatorAuth":         "cmdAuth",
		"cmdOperatorBilling":      "cmdBilling",
		"cmdOperatorPriceCatalog": "cmdBilling",
		"cmdOperatorPaddle":       "cmdBilling",
	} {
		if strings.HasPrefix(leaf, prefix) {
			return command
		}
	}
	if !strings.HasPrefix(leaf, "cmd") {
		return ""
	}
	name := strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(leaf, "cmd"), "-", ""))
	bestName, bestLength := "", 0
	for _, singular := range []bool{false, true} {
		for _, command := range cliCommands {
			prefix := strings.ReplaceAll(command.Name, "-", "")
			if singular {
				prefix = strings.TrimSuffix(prefix, "s")
			}
			if strings.HasPrefix(name, prefix) && len(prefix) > bestLength {
				bestName, bestLength = command.Name, len(prefix)
			}
		}
		// Exact prefixes take precedence: cmdDeployTarball belongs to
		// deploy, while cmdDeploysRetry belongs to deploys.
		if bestName != "" {
			break
		}
	}
	if bestName == "" {
		return ""
	}
	var dispatcher strings.Builder
	dispatcher.WriteString("cmd")
	for _, part := range strings.Split(bestName, "-") {
		dispatcher.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return dispatcher.String()
}

// collectJSONEmitters walks every non-test .go file in the
// current package directory and returns the set of func cmdXxx
// names whose body references the package-level jsonOutput
// identifier. Mirrors cmd/gregale/json_parity_test.go:111.
func collectJSONEmitters() ([]string, error) {
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil, err
	}
	var emitters []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, e.Name(), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok {
				return true
			}
			if !strings.HasPrefix(fn.Name.Name, "cmd") {
				return true
			}
			if fn.Body == nil {
				return true
			}
			hasJSON := false
			ast.Inspect(fn.Body, func(inner ast.Node) bool {
				id, ok := inner.(*ast.Ident)
				if !ok {
					return true
				}
				// Operator-side dispatchers reference jsonOutput
				// either directly OR through the jsonEnabled()
				// wrapper (commands_manifest.go:211). Both signal
				// "this dispatcher honours --json".
				if id.Name == "jsonOutput" || id.Name == "jsonEnabled" {
					hasJSON = true
					return false
				}
				return true
			})
			if hasJSON {
				emitters = append(emitters, fn.Name.Name)
			}
			return true
		})
	}
	return emitters, nil
}

// jsonTestedTopLevel inventories calls inside the enclosing Test function,
// rather than guessing from the test name or borrowing another test's mode.
func jsonTestedTopLevel() (map[string]bool, error) {
	tested := map[string]bool{}
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, 0)
		if err != nil {
			return nil, err
		}
		for name := range jsonTestedFile(file) {
			tested[name] = true
		}
	}

	return tested, nil
}

func hasJSONTestMode(body *ast.BlockStmt) bool {
	enabled := false
	ast.Inspect(body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.AssignStmt:
			for index, lhs := range node.Lhs {
				id, ok := lhs.(*ast.Ident)
				if ok && id.Name == "jsonOutput" && index < len(node.Rhs) {
					value, ok := node.Rhs[index].(*ast.Ident)
					if ok && value.Name == "true" {
						enabled = true
					}
				}
			}
		case *ast.BasicLit:
			if node.Kind == token.STRING {
				value, _ := strconv.Unquote(node.Value)
				if value == "--json" || value == "-j" || value == "--json=true" {
					enabled = true
				}
			}
		case *ast.CallExpr:
			method, ok := node.Fun.(*ast.SelectorExpr)
			if ok && method.Sel.Name == "Setenv" && len(node.Args) == 2 {
				key, keyOK := node.Args[0].(*ast.BasicLit)
				value, valueOK := node.Args[1].(*ast.BasicLit)
				if keyOK && valueOK && key.Value == `"FAAS_JSON"` && value.Value == `"1"` {
					enabled = true
				}
			}
		}
		return true
	})
	return enabled
}

func jsonTestedFile(file *ast.File) map[string]bool {
	tested := map[string]bool{}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Body == nil || !hasJSONTestMode(fn.Body) {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			if top := topLevelDispatcher(id.Name); top != "" {
				tested[top] = true
			}
			if id.Name == "run" {
				// Table-driven run tests keep command vectors in literal
				// slices in this same function, sometimes passed via a variable.
				ast.Inspect(fn.Body, func(candidate ast.Node) bool {
					vector, ok := candidate.(*ast.CompositeLit)
					if !ok {
						return true
					}
					if isStringSlice(vector.Type) {
						markJSONCommandVector(tested, vector.Elts)
					} else if table, ok := vector.Type.(*ast.ArrayType); ok && table.Len == nil && isStringSlice(table.Elt) {
						for _, element := range vector.Elts {
							if row, ok := element.(*ast.CompositeLit); ok {
								markJSONCommandVector(tested, row.Elts)
							}
						}
					}
					return true
				})
			}
			return true
		})
	}
	return tested
}

func isStringSlice(expr ast.Expr) bool {
	slice, ok := expr.(*ast.ArrayType)
	if !ok || slice.Len != nil {
		return false
	}
	element, ok := slice.Elt.(*ast.Ident)
	return ok && element.Name == "string"
}

func markJSONCommandVector(tested map[string]bool, args []ast.Expr) {
	for _, arg := range args {
		literal, ok := arg.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return
		}
		value, _ := strconv.Unquote(literal.Value)
		if value == "--json" || value == "-j" || value == "--json=true" {
			continue
		}
		if _, known := lookupCliCommand(value); known {
			tested[topLevelDispatcher("cmd"+value)] = true
		}
		return
	}
}

func TestJSONAuditDispatcherMapping(t *testing.T) {
	for _, command := range cliCommands {
		want := "cmd"
		for _, part := range strings.Split(command.Name, "-") {
			want += strings.ToUpper(part[:1]) + part[1:]
		}
		for _, handler := range []string{want, want + "List", "cmd" + command.Name} {
			if got := topLevelDispatcher(handler); got != want {
				t.Errorf("%s maps to %s, want %s", handler, got, want)
			}
		}
	}
	if got := topLevelDispatcher("unrelatedHelper"); got != "" {
		t.Errorf("non-command maps to %s", got)
	}
}

func TestJSONAuditKeepsModeInsideEnclosingTest(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "fixture_test.go", `package main
func TestHumanBefore(t *testing.T) { cmdConfig(nil) }
func TestArbitraryName(t *testing.T) { jsonOutput = true; cmdObs(nil) }
func TestHumanAfter(t *testing.T) { cmdConfig(nil) }
func TestRunJSON(t *testing.T) { _ = "config"; for _, args := range [][]string{{"accounts", "--json"}} { run(args) } }
func helper() { jsonOutput = true; cmdConfig(nil) }
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	tested := jsonTestedFile(file)
	if !tested["cmdObs"] || !tested["cmdAccounts"] || tested["cmdConfig"] || len(tested) != 2 {
		t.Fatalf("JSON-tested command families = %v", tested)
	}
}
