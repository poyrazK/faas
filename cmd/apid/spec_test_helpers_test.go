package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// These helpers support focused APID contract tests. The full OpenAPI parity
// gate lives in scripts/ci/speccompliance so running it doesn't compile APID.
const serverSrcPath = "server.go"

type specDoc struct {
	Schemas map[string]map[string]any
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for range 16 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("go.mod not found above %s", dir)
}

func loadSpec(path string) (*specDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	doc := &specDoc{Schemas: map[string]map[string]any{}}
	components, _ := raw["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	for name, value := range schemas {
		if schema, ok := value.(map[string]any); ok {
			doc.Schemas[name] = schema
		}
	}
	return doc, nil
}

func scanServerRoutes(path string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var routes []string
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (selector.Sel.Name != "HandleFunc" && selector.Sel.Name != "Handle") || len(call.Args) == 0 {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		route, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}
		parts := strings.SplitN(route, " ", 2)
		if len(parts) == 2 {
			routes = append(routes, strings.ToUpper(parts[0])+" "+parts[1])
		}
		return true
	})
	return routes, nil
}

func strconvUnquote(literal string) (string, error) {
	return strconv.Unquote(literal)
}
