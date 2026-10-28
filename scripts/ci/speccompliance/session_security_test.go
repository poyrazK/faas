package speccompliance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestSessionRoutesDeclareCookieAuth is issue #2721: a route apid mounts
// behind sessionAuth accepts only the dashboard session cookie, so its
// OpenAPI operation must not inherit the document's bearerAuth default. An
// SDK or API-key client following the spec would otherwise send a bearer
// token and get a redirect to /login.
func TestSessionRoutesDeclareCookieAuth(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("find repo root: %v", err)
	}
	routes, err := scanSessionRoutes(filepath.Join(root, "pkg/apid/apidserver", serverSrcPath))
	if err != nil {
		t.Fatalf("scan session routes: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("found no sessionAuth routes in server.go; the scanner no longer matches the route table")
	}
	raw, err := os.ReadFile(filepath.Join(root, "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Security []map[string]any                `yaml:"security"`
		Paths    map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	var missing []string
	for _, route := range routes {
		method, path, _ := strings.Cut(route, " ")
		node, ok := doc.Paths[path][strings.ToLower(method)]
		if !ok {
			continue // route parity reports undocumented routes
		}
		var op specOperation
		if err := node.Decode(&op); err != nil {
			t.Fatalf("decode %s: %v", route, err)
		}
		security := doc.Security
		if op.Security != nil {
			security = *op.Security
		}
		if !declaresScheme(security, "cookieAuth") {
			missing = append(missing, route+" ("+op.OperationID+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("sessionAuth routes whose OpenAPI operation does not declare cookieAuth:\n  %s\nAdd `security: [{ cookieAuth: [] }]` to each operation.", strings.Join(missing, "\n  "))
	}
}

type specOperation struct {
	OperationID string            `yaml:"operationId"`
	Security    *[]map[string]any `yaml:"security"`
}

func declaresScheme(security []map[string]any, scheme string) bool {
	for _, requirement := range security {
		if _, ok := requirement[scheme]; ok {
			return true
		}
	}
	return false
}

// scanSessionRoutes returns "METHOD /path" for every mux.Handle/HandleFunc in
// path whose handler expression calls sessionAuth.
func scanSessionRoutes(path string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	var routes []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		pattern, err := strconvUnquote(lit.Value)
		if err != nil {
			return true
		}
		method, p, ok := strings.Cut(pattern, " ")
		if !ok || !callsSelector(call.Args[1], "sessionAuth") {
			return true
		}
		routes = append(routes, strings.ToUpper(method)+" "+p)
		return true
	})
	return routes, nil
}

func callsSelector(expr ast.Expr, name string) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
				found = true
			}
		}
		return !found
	})
	return found
}
