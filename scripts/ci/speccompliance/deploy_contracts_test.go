package speccompliance

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

// Expand the wildcard using the handler's rejecting guard, rather than
// excluding it or copying an action list that could drift from runtime.
func scanPromotionControlActions(path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "HandleControl" || fn.Body == nil {
			continue
		}
		for _, stmt := range fn.Body.List {
			guard, ok := stmt.(*ast.IfStmt)
			if !ok {
				continue
			}
			condition := guard.Cond
			for {
				binary, ok := condition.(*ast.BinaryExpr)
				if !ok || binary.Op != token.LOR {
					break
				}
				condition = binary.X
			}
			actions, ok := rejectedPromotionActions(condition)
			if !ok || len(actions) == 0 || len(guard.Body.List) == 0 {
				return nil, fmt.Errorf("HandleControl must reject unknown actions before processing control requests")
			}
			if _, ok := guard.Body.List[len(guard.Body.List)-1].(*ast.ReturnStmt); !ok {
				return nil, fmt.Errorf("HandleControl action guard must return")
			}
			return actions, nil
		}
	}
	return nil, fmt.Errorf("HandleControl action guard not found")
}

func rejectedPromotionActions(expr ast.Expr) ([]string, bool) {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return nil, false
	}
	if binary.Op == token.LAND {
		left, leftOK := rejectedPromotionActions(binary.X)
		right, rightOK := rejectedPromotionActions(binary.Y)
		return append(left, right...), leftOK && rightOK
	}
	name, nameOK := binary.X.(*ast.Ident)
	value, valueOK := binary.Y.(*ast.BasicLit)
	if binary.Op != token.NEQ || !nameOK || name.Name != "action" || !valueOK || value.Kind != token.STRING {
		return nil, false
	}
	action, err := strconv.Unquote(value.Value)
	return []string{action}, err == nil && action != ""
}

func TestPromotionControlActionGuard(t *testing.T) {
	for _, tc := range []struct {
		name, condition string
		valid           bool
		actions         []string
	}{
		{"two_actions", `action != "abort" && action != "recover"`, true, []string{"abort", "recover"}},
		{"additional_action", `action != "abort" && action != "recover" && action != "pause"`, true, []string{"abort", "recover", "pause"}},
		{"rejects_supported_actions", `action == "abort" || action == "recover"`, false, nil},
		{"unbounded", `action != "abort" && valid`, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := parser.ParseExpr(tc.condition)
			if err != nil {
				t.Fatal(err)
			}
			actions, valid := rejectedPromotionActions(expr)
			if valid != tc.valid || valid && !slices.Equal(actions, tc.actions) {
				t.Fatalf("actions=%v valid=%v", actions, valid)
			}
		})
	}
}

func checkReleaseSecretVersionMap(t *testing.T, root string, spec *specDoc) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "pkg", "api", "project_release_secrets.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	ast.Inspect(file, func(node ast.Node) bool {
		decl, ok := node.(*ast.TypeSpec)
		if !ok || decl.Name.Name != "ProjectReleaseSecretConfiguration" && decl.Name.Name != "ProjectReleaseSecretStageResponse" {
			return true
		}
		body, ok := decl.Type.(*ast.StructType)
		if !ok {
			t.Fatalf("%s must be a wire object", decl.Name.Name)
		}
		for _, field := range body.Fields.List {
			if field.Tag == nil {
				continue
			}
			name := jsonFieldName(field.Tag.Value)
			if name != "versions" && name != "previous_versions" {
				continue
			}
			if !isReleaseSecretVersionMap(field.Type, 2) {
				t.Fatalf("%s.%s must match the nested secret version map schema", decl.Name.Name, name)
			}
			found++
		}
		return false
	})
	if found != 3 {
		t.Fatalf("expected three secret version map fields, found %d", found)
	}
	schema := spec.Schemas["ProjectReleaseSecretVersionMap"]
	for depth := 0; depth < 2; depth++ {
		if schema["type"] != "object" {
			t.Fatalf("secret version map depth %d must be an object", depth)
		}
		schema, _ = schema["additionalProperties"].(map[string]any)
	}
	if schema["type"] != "string" || schema["format"] != "uuid" {
		t.Fatal("secret version map values must be immutable UUID references")
	}
}

func isReleaseSecretVersionMap(expr ast.Expr, depth int) bool {
	if depth == 0 {
		name, ok := expr.(*ast.Ident)
		return ok && name.Name == "string"
	}
	mapping, ok := expr.(*ast.MapType)
	if !ok {
		return false
	}
	key, ok := mapping.Key.(*ast.Ident)
	return ok && key.Name == "string" && isReleaseSecretVersionMap(mapping.Value, depth-1)
}
