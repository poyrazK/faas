package speccompliance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 20261115080000 — private evidence is encoded by pkg/api and the
// operatorobs domain. Check its actual flattened fields under the SDK names.
func addRuntimeBuildSchemas(t *testing.T, root string, spec *specDoc, dtos map[string]map[string]bool) {
	t.Helper()
	files := []string{
		filepath.Join(root, "pkg/api/obs_runtime_builds.go"),
		filepath.Join(root, "pkg/api/obs_runtime_process.go"),
	}
	domain, err := filepath.Glob(filepath.Join(root, "pkg/apid/operatorobs/runtime*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range domain {
		if !strings.HasSuffix(file, "_test.go") {
			files = append(files, file)
		}
	}
	fields, err := scanDTOs(files)
	if err != nil {
		t.Fatalf("scan runtime DTOs: %v", err)
	}
	flattenRuntimeBuildFields(t, files, fields)
	aliases := map[string]string{
		"ObsRuntimeBuilds":                    "RuntimeBuildsLoopResponse",
		"ObsRuntimeReadinessReasonCount":      "ObsRuntimeReadinessReason",
		"ObsRuntimeCommandCronTimingCounts":   "RuntimeAppTaskOperationCounts",
		"ObsRuntimeCommandCronOutcomeCounts":  "RuntimeAppTaskOperationCounts",
		"ObsRuntimeAppTaskResultCounts":       "RuntimeAppTaskOperationCounts",
		"ObsRuntimeHTTPCronDeliveryCounts":    "RuntimeAppTaskOperationCounts",
		"ObsRuntimeDelayedTaskDispatchCounts": "RuntimeAppTaskOperationCounts",
	}
	// These named schemas refine string fields whose closed vocabularies are
	// enforced by operatorobs contract validators, rather than named Go structs.
	enums := map[string]bool{
		"ObsRuntimeBuilderEvidenceReason": true, "ObsRuntimeHandoffEvidenceReason": true,
		"ObsRuntimeCommandCronTimingName": true, "ObsRuntimeDiagnosticGroup": true,
		"ObsRuntimeDelayedTaskDispatchName": true, "ObsRuntimeHTTPCronDeliveryName": true,
		"ObsRuntimeCommandCronOutcomeSource": true, "ObsRuntimeCommandCronOutcomeName": true,
		"ObsRuntimeAppTaskResultSource": true, "ObsRuntimeAppTaskResultName": true,
		"ObsRuntimeAppTaskOperationName": true, "ObsRuntimeAppTaskOperationOutcome": true,
		"ObsRuntimeAppTaskOperationReason": true, "ObsRuntimeAppTaskClassName": true,
		"ObsRuntimeAppTaskKindName": true, "ObsRuntimeCommandCronClassName": true,
		"ObsRuntimePostCaptureClassName": true, "ObsRuntimeSnapshotPrimeClassName": true,
		"ObsRuntimePreparationPhaseName": true,
	}
	for name, schema := range spec.Schemas {
		if !strings.HasPrefix(name, "ObsRuntime") {
			continue
		}
		if enums[name] {
			if schema["type"] != "string" || len(anyStrings(schema["enum"])) == 0 {
				t.Errorf("%s must remain a nonempty string enum", name)
			}
			dtos[name] = map[string]bool{}
			continue
		}
		goName := name
		if alias := aliases[name]; alias != "" {
			goName = alias
		} else if fields[goName] == nil {
			goName = strings.TrimPrefix(name, "Obs")
		}
		actual, ok := fields[goName]
		if !ok {
			t.Errorf("%s has no runtime DTO %s", name, goName)
			continue
		}
		dtos[name] = actual
		properties, ok := extractSchemaProperties(schema)
		if !ok {
			t.Errorf("%s must describe object properties", name)
			continue
		}
		var actualNames, propertyNames []string
		for field := range actual {
			actualNames = append(actualNames, field)
		}
		for field := range properties {
			propertyNames = append(propertyNames, field)
		}
		if missing, extra := stageStateSetDiff(actualNames, propertyNames); len(missing)+len(extra) != 0 {
			t.Errorf("%s JSON fields differ: missing %v extra %v", name, missing, extra)
		}
	}
}

func flattenRuntimeBuildFields(t *testing.T, files []string, fields map[string]map[string]bool) {
	t.Helper()
	embedded := map[string][]string{}
	for _, path := range files {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			typ, ok := node.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := typ.Type.(*ast.StructType)
			if !ok || !typ.Name.IsExported() {
				return false
			}
			for _, field := range st.Fields.List {
				if len(field.Names) == 0 && field.Tag == nil {
					embedded[typ.Name.Name] = append(embedded[typ.Name.Name], elemTypeName(field.Type))
				}
			}
			return false
		})
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var flatten func(string)
	flatten = func(name string) {
		if done[name] {
			return
		}
		if visiting[name] || fields[name] == nil {
			t.Fatalf("invalid runtime DTO embedding: %s", name)
		}
		visiting[name] = true
		for _, parent := range embedded[name] {
			flatten(parent)
			for field := range fields[parent] {
				fields[name][field] = true
			}
		}
		delete(visiting, name)
		done[name] = true
	}
	for name := range fields {
		flatten(name)
	}
}
