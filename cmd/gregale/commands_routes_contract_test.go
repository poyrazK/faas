package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/routecontract"
)

func TestRoutesContractCheckLocalJSONAndDriftGate(t *testing.T) {
	out := captureImpactOutput(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "unexpected network", http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_API_KEY", "")

	dir := routeImpactRepo(t)
	source := "from fastapi import FastAPI\napp=FastAPI()\n@app.get('/checkout')\ndef checkout(): return 1\n@app.get('/source-only/{id}')\ndef source_only(id: str): return id\n"
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	routeImpactGit(t, dir, "add", "main.py")
	routeImpactGit(t, dir, "commit", "-qm", "add route")
	openapiPath := filepath.Join(t.TempDir(), "openapi.yaml")
	document := "openapi: 3.1.0\ninfo:\n  title: Demo\n  version: '1'\npaths:\n  /checkout:\n    get:\n      responses: {}\n  /stale/{id}:\n    get:\n      responses: {}\n"
	if err := os.WriteFile(openapiPath, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"routes", "contract", "check", "my-api", "--openapi", openapiPath, "--base", "HEAD", "--head", "HEAD", "--path", dir, "--fail-on-drift", "--json"}
	if code := run(args); code != 1 {
		t.Fatalf("confirmed drift exit=%d output=%s", code, out.String())
	}
	var report routecontract.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode JSON report: %v: %s", err, out.String())
	}
	if report.App != "my-api" || report.Status != "complete" || report.Outcome != "drift" ||
		report.Summary.Matched != 1 || report.Summary.SourceOnly != 1 || report.Summary.ContractOnly != 1 || calls != 0 {
		t.Fatalf("unexpected route contract report or network call: %+v calls=%d", report, calls)
	}
	var foundSourceLocation bool
	for _, finding := range report.Routes {
		if finding.Status == "source_only" && finding.Source != nil && finding.Registration != nil && finding.Source.File == "main.py" {
			foundSourceLocation = true
		}
	}
	if !foundSourceLocation {
		t.Fatalf("source-only finding omitted its code location: %+v", report.Routes)
	}

	incompleteSource := "from fastapi import FastAPI\napp=FastAPI()\n@app.get('/checkout')\ndef checkout(): return 1\napp.include_router(build_router())\n"
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte(incompleteSource), 0o600); err != nil {
		t.Fatal(err)
	}
	incompleteArgs := []string{"routes", "contract", "check", "--openapi", openapiPath, "--base", "HEAD", "--path", dir, "--fail-on-drift", "--json"}
	out.Reset()
	if code := run(incompleteArgs); code != 0 {
		t.Fatalf("incomplete source was treated as confirmed drift: exit=%d output=%s", code, out.String())
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Outcome != "incomplete" || report.Summary.Unknown == 0 || report.Summary.SourceOnly != 0 || report.Summary.ContractOnly != 0 {
		t.Fatalf("incomplete source did not produce inconclusive routes: err=%v report=%+v", err, report)
	}
	incompleteArgs = append(incompleteArgs[:len(incompleteArgs)-2], "--fail-on-incomplete", "--json")
	out.Reset()
	if code := run(incompleteArgs); code != 1 {
		t.Fatalf("--fail-on-incomplete exit=%d output=%s", code, out.String())
	}
}

func TestRenderRouteContractEscapesMarkdownAndControlContent(t *testing.T) {
	out := captureImpactOutput(t)
	report := routecontract.Report{App: "api\x1b[31m", Outcome: "drift", Status: "complete", Framework: "fastapi", OpenAPIFile: "spec|x.yaml",
		Routes: []routecontract.Finding{{Method: "GET", ContractPath: "/[link](https://invalid)", Status: "contract_only", Reason: "<script>\nunknown"}}}
	renderRouteContract(out, report, "markdown")
	if containsControl(out.String()) || containsAny(out.String(), "<script>", "[link](", "spec|x.yaml") {
		t.Fatalf("Markdown rendering did not sanitize input: %s", out.String())
	}
}

func containsControl(value string) bool {
	for _, r := range value {
		if r < 0x20 && r != '\n' && r != '\t' {
			return true
		}
	}
	return false
}

func containsAny(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}
