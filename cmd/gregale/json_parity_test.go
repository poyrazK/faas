// json_parity_test.go — Tier A8.2 / ADR-083 follow-up.
//
// Pins the "every JSON-emitting top-level cmdXxx has a test"
// contract. We walk the cliCommands manifest (the user-facing
// surface) and for each entry whose dispatcher branches on
// jsonOutput, assert that a sibling test exists which sets
// jsonOutput = true for the dispatcher path.
//
// We intentionally do NOT audit per-leaf jsonOutput branches
// (cmdAlertList, cmdRegistryList, etc.). Leaves are exercised
// through their parent dispatcher's test; the parent dispatcher
// is the user-facing surface and the right unit of audit.
//
// The nonJSONAllowList MUST stay co-located with the rationale
// comment in json_flag.go:18 — both lists move together when a
// command is added or removed.

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// nonJSONAllowList is the closed set of top-level cmdXxx funcs
// that DELIBERATELY emit no JSON. Mirrored in the comment block
// in json_flag.go so the audit list and the rationale stay
// co-located. Both lists must move together when adding or
// removing a non-JSON command.
//
// Keep entries in alphabetical order for review diff readability.
var nonJSONAllowList = map[string]string{
	"cmdAccount":    "delegate leaves; cmdAccountStatus is the only JSON leaf (covered)",
	"cmdInit":       "file writes + human template table",
	"cmdLogin":      "interactive paste-code flow",
	"cmdMfa":        "enroll is the only JSON leaf (covered); others are write-only",
	"cmdOverageCap": "side-effect (set/clear both write-only)",
	"cmdRestore":    "side-effect only; no body",
}

// cmdTrustedPublishers and other operator-side verbs (cmdBackup,
// cmdHostAge, cmdPKI, cmdSignKeys, cmdManifestDispatch,
// cmdReleaseDispatch) moved to cmd/gregalectl/ in PR-6.5 — their
// nonJSONAllowList entries live in cmd/gregalectl/json_parity_test.go.

// TestJSONOutputHonored is the parity gate. Fails loudly when a
// top-level cmdXxx references jsonOutput (or any of its leaves
// does) but no test exercises that branch. Fails loudly when
// an old test gets removed and the reference lingers.
func TestJSONOutputHonored(t *testing.T) {
	jsonCmds, err := collectJSONEmitters()
	if err != nil {
		t.Fatalf("walk jsonOutput emitters: %v", err)
	}
	if len(jsonCmds) == 0 {
		t.Fatal("no jsonOutput emitters found — extractor is broken")
	}

	// Reduce leaf bodies to their top-level dispatcher name.
	topLevel := map[string]bool{}
	for _, name := range jsonCmds {
		top := topLevelDispatcher(name)
		if top != "" {
			topLevel[top] = true
		}
	}

	tested := jsonTestedTopLevel()
	if len(tested) == 0 {
		t.Fatal("no jsonOutput = true assignments found — extractor is broken")
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

// topLevelDispatcher maps a leaf cmdXxx to its top-level parent
// by walking cliCommands and matching the longest prefix. For
// example, cmdRegistryList → cmdRegistry, cmdAlertAdd → cmdAlerts.
// Returns "" for the top-level cmd itself.
func topLevelDispatcher(leaf string) string {
	best := ""
	for _, c := range cliCommands {
		if !strings.HasPrefix(leaf, c.Name) {
			continue
		}
		if len(c.Name) > len(best) {
			best = c.Name
		}
	}
	return best
}

// collectJSONEmitters walks every non-test .go file in the
// current package directory and returns the set of func cmdXxx
// names whose body references the package-level jsonOutput
// identifier. Mirrors the AST-walk pattern in
// extractPrintUsageTopics (commands_completion_test.go).
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
				if id.Name == "jsonOutput" {
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

// jsonTestedTopLevel walks every _test.go file and maps every
// jsonOutput reference to the enclosing top-level cmdXxx
// dispatcher. Strips the "Test" prefix and the "_..." suffix to
// derive the cmdXxx name. Returns the set of top-level cmdXxx
// names that have at least one JSON test.
func jsonTestedTopLevel() map[string]bool {
	tested := map[string]bool{}
	entries, _ := os.ReadDir(".")
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, e.Name(), nil, parser.ParseComments)
		if err != nil {
			continue
		}
		// Build list of Test* funcs with start positions.
		type tfunc struct {
			name  string
			start token.Pos
		}
		var tests []tfunc
		ast.Inspect(f, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok {
				return true
			}
			if !strings.HasPrefix(fn.Name.Name, "Test") {
				return true
			}
			rest := fn.Name.Name[4:]
			if !strings.HasPrefix(rest, "Cmd") {
				return true
			}
			rest = strings.TrimPrefix(rest, "Cmd")
			// Strip optional _<subtest> suffix.
			if idx := strings.Index(rest, "_"); idx >= 0 {
				rest = rest[:idx]
			}
			tests = append(tests, tfunc{name: rest, start: fn.Body.Pos()})
			return true
		})
		// Find every jsonOutput reference; bucket it into the
		// earliest test whose start position is ≤ the reference.
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || id.Name != "jsonOutput" {
				return true
			}
			for _, tf := range tests {
				if tf.start <= id.Pos() {
					// Map the test name back to a top-level
					// dispatcher. The test name is "Cmd" + leaf
					// (e.g. "CmdBillingPortal"); reduce via the
					// same `topLevelDispatcher` helper.
					full := "cmd" + tf.name
					top := topLevelDispatcher(full)
					if top == "" {
						top = full
					}
					tested[top] = true
					break
				}
			}
			return true
		})
	}
	return tested
}
