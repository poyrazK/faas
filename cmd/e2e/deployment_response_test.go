package e2e_test

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Direct images have no source build. Keep parseQueuedDeployment's build-ID
// requirement for source uploads and use this stricter image contract instead.
func parseImageDeployment(t *testing.T, body []byte) string {
	t.Helper()
	id, err := decodeImageDeploymentID(body)
	if err != nil {
		t.Fatalf("decode image deployment response: %v body=%s", err, body)
	}
	return id
}

func decodeImageDeploymentID(body []byte) (string, error) {
	var response api.DeploymentResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return "", err
	}
	if response.ID == "" || response.Kind != "image" || response.BuildID != "" {
		return "", fmt.Errorf("want an image deployment ID without a source build ID")
	}
	return response.ID, nil
}

func TestImageDeploymentResponseContract(t *testing.T) {
	for _, test := range []struct {
		name, body string
		wantID     string
	}{
		{"pending image", `{"id":"deployment-1","kind":"image","status":"pending"}`, "deployment-1"},
		{"preparing image", `{"id":"deployment-1","kind":"image","status":"preparing","build_id":""}`, "deployment-1"},
		{"missing ID", `{"kind":"image"}`, ""},
		{"missing kind", `{"id":"deployment-1"}`, ""},
		{"source deployment", `{"id":"deployment-1","kind":"tarball","build_id":"build-1"}`, ""},
		{"image with source build", `{"id":"deployment-1","kind":"image","build_id":"build-1"}`, ""},
		{"malformed JSON", `{"id":`, ""},
		{"wrong ID type", `{"id":123,"kind":"image"}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, err := decodeImageDeploymentID([]byte(test.body))
			if id != test.wantID || (err != nil) != (test.wantID == "") {
				t.Fatalf("decodeImageDeploymentID = %q, %v; want ID %q", id, err, test.wantID)
			}
			if test.wantID != "" && parseImageDeployment(t, []byte(test.body)) != test.wantID {
				t.Fatalf("image acceptance helper did not return ID %q", test.wantID)
			}
		})
	}
}

// Source-upload acceptance cannot progress with an image-only daemon set.
// Inspect the actual call sites, rather than testing a parallel mask constant.
func TestContainerSourceFixturesStartBuilder(t *testing.T) {
	files := map[string]int{
		"deploy_healthcheck_metal_test.go": 1,
		"tcp_ingress_metal_test.go":        1,
		"udp_ingress_metal_test.go":        1,
		"streaming_metal_test.go":          5,
	}
	for name, wantCalls := range files {
		t.Run(name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if !ok || pkg.Name != "e2etest" || (selector.Sel.Name != "Start" && selector.Sel.Name != "StartWithEnv") {
					return true
				}
				calls++
				if selector.Sel.Name != "Start" {
					t.Errorf("source-upload harness %d uses %s, which cannot start VMMD/Builderd", calls, selector.Sel.Name)
				}
				if len(call.Args) < 3 || !startsBuilder(call.Args[2]) {
					t.Errorf("source-upload harness %d omits Builderd", calls)
				}
				return true
			})
			if calls != wantCalls {
				t.Fatalf("checked %d harness starts; want %d", calls, wantCalls)
			}
		})
	}
}

func startsBuilder(expr ast.Expr) bool {
	switch expr := expr.(type) {
	case *ast.ParenExpr:
		return startsBuilder(expr.X)
	case *ast.BinaryExpr:
		return expr.Op == token.OR && (startsBuilder(expr.X) || startsBuilder(expr.Y))
	case *ast.SelectorExpr:
		pkg, ok := expr.X.(*ast.Ident)
		return ok && pkg.Name == "e2etest" && (expr.Sel.Name == "Builderd" || expr.Sel.Name == "All")
	}
	return false
}
