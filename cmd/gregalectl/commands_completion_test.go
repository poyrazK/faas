// commands_completion_test.go — operator-side Tier A8 / ADR-083
// manifest drift test (issue #911 / ADR-110 PR-6.5).
//
// Mirrors cmd/gregale/commands_completion_test.go byte-for-byte
// except the dispatchConsts map is restricted to operator-side
// consts (dispatchHostAge, dispatchPKI, dispatchSignKeys,
// dispatchNodeKey, dispatchBackup, dispatchManifest, dispatchRelease).
// Customer-side consts stay in the gregale drift test.
//
// Each drift test runs in its own binary's `go test ./cmd/<x>/...`
// and pins its binary's surface; adding a customer command to
// gregalectl or an operator command to gregale fails CI immediately.
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestCompletion_ManifestDrift(t *testing.T) {
	// Walk main.go's switch and collect every `case "<name>":` arm.
	// Also walk the dispatch constants (constants.go) to recover
	// the values behind `case dispatchFoo:` forms.
	dispatchConsts := map[string]string{
		"dispatchHostAge":      "host-age",
		"dispatchPKI":          "pki",
		"dispatchSignKeys":     "sign-keys",
		"dispatchNodeKey":      "node-key",
		"dispatchBackup":       "backup",
		"dispatchManifest":     "manifest",
		"dispatchRelease":      "release",
		"dispatchDoctor":       "doctor",
		"dispatchSecrets":      "secrets",
		"dispatchArtifact":     "artifact",      // release-pinned shared artifact publication
		"dispatchComputeNodes": "compute-nodes", // PR-911 image rollout (PR #929; ADR-110 + ADR-111)
		"dispatchDeploy":       "deploy",        // PR-B (multi-host scale-out gap #2)
		"dispatchInstances":    "instances",     // P2 of operator-obs mega-PR (Commit 5b)
		"dispatchBuilds":       "builds",        // P2c of operator-obs mega-PR (Commit 5c)
		"dispatchObs":          "obs",           // Obs-Meta + Trace-IDs Mega-PR / C8 — operator-side meta-obs health snapshot
		"dispatchDebug":        "debug",         // ADR-127 PR-D — operator-side OTel spans writer smoke harness
	}
	caseNames, err := extractMainCaseArms(dispatchConsts)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	manifestNames := make(map[string]struct{}, len(cliCommands))
	for _, c := range cliCommands {
		manifestNames[c.Name] = struct{}{}
	}
	// Internal pseudo-commands the manifest deliberately omits:
	// help, version. They are dispatched in run() but rendered in
	// the top-level usage block, not as separate cliCommand entries.
	internal := map[string]struct{}{
		"help":       {},
		"version":    {},
		"--version":  {},
		"-v":         {},
		"--help":     {},
		"-h":         {},
		"completion": {},
		"man":        {},
	}
	for name := range caseNames {
		if _, ok := internal[name]; ok {
			continue
		}
		if _, ok := manifestNames[name]; !ok {
			t.Errorf("main.go has case %q but no cliCommand entry in cli_meta.go", name)
		}
	}
	for name := range manifestNames {
		if _, ok := internal[name]; ok {
			// Manifest may include internal commands (e.g. completion, man);
			// that's fine — they ARE in the dispatch table.
			continue
		}
		if _, ok := caseNames[name]; !ok {
			t.Errorf("cliCommand %q has no matching case arm in main.go", name)
		}
	}
}

func extractMainCaseArms(dispatchConsts map[string]string) (map[string]struct{}, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	cases := make(map[string]struct{})
	ast.Inspect(f, func(n ast.Node) bool {
		cs, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range cs.List {
			switch e := expr.(type) {
			case *ast.BasicLit:
				if e.Kind == token.STRING {
					name := strings.Trim(e.Value, `"`)
					cases[name] = struct{}{}
				}
			case *ast.Ident:
				if val, ok := dispatchConsts[e.Name]; ok {
					cases[val] = struct{}{}
				} else {
					cases[e.Name] = struct{}{}
				}
			}
		}
		return true
	})
	return cases, nil
}
