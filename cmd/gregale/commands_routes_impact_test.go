package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/routeimpact"
)

func routeImpactGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Route Impact CLI Test", "GIT_AUTHOR_EMAIL=route-impact@example.invalid",
		"GIT_COMMITTER_NAME=Route Impact CLI Test", "GIT_COMMITTER_EMAIL=route-impact@example.invalid")
	if body, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, body)
	}
}

func routeImpactRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python 3 is required for local route impact integration tests")
	}
	dir := t.TempDir()
	routeImpactGit(t, dir, "init", "-q")
	body := "from fastapi import FastAPI\napp=FastAPI()\n@app.get('/checkout')\ndef checkout(): return 1\n"
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	routeImpactGit(t, dir, "add", ".")
	routeImpactGit(t, dir, "commit", "-qm", "baseline")
	return dir
}

func captureImpactOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	resetJSONOut(t)
	var output bytes.Buffer
	oldOut, oldErr := osStdout, osStderr
	osStdout, osStderr = &output, &output
	t.Cleanup(func() { osStdout, osStderr = oldOut, oldErr })
	return &output
}

func TestRoutesImpactLocalJSONExportAndCIGates(t *testing.T) {
	out := captureImpactOutput(t)
	// A deliberately failing API proves local analysis has no platform calls.
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "unexpected network", http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_API_KEY", "")
	dir := routeImpactRepo(t)
	destination := filepath.Join(t.TempDir(), "impact.json")
	if code := run([]string{"routes", "impact", "my-api", "--base", "HEAD", "--head", "HEAD", "--path", dir, "--out", destination, "--fail-on-impact", "--fail-on-incomplete", "--json"}); code != 0 {
		t.Fatalf("unchanged commit exit=%d %s", code, out.String())
	}
	var report routeimpact.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.App != "my-api" || report.Status != "complete" || report.Summary.NoLinkedChanges != 1 {
		t.Fatalf("report: %+v err=%v", report, err)
	}
	body, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(destination)
	if info.Mode().Perm() != 0o600 || !bytes.Equal(bytes.TrimSpace(body), bytes.TrimSpace(out.Bytes())) {
		t.Fatal("JSON artifact differs from stdout or is not owner-readable")
	}
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("from fastapi import FastAPI\napp=FastAPI()\n@app.get('/checkout')\ndef checkout(): return 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := run([]string{"routes", "impact", "--base", "HEAD", "--path", dir, "--fail-on-impact", "--json"}); code != 1 {
		t.Fatalf("impact gate exit=%d %s", code, out.String())
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Summary.SourceChanged != 1 {
		t.Fatalf("impact output lost on gate failure: %v %+v", err, report)
	}
	out.Reset()
	if code := run([]string{"routes", "impact", "--base", "HEAD", "--path", dir, "--out", destination, "--json"}); code != 1 {
		t.Fatalf("existing artifact overwritten: %d", code)
	}
	saved, _ := os.ReadFile(destination)
	if !bytes.Equal(saved, body) || calls != 0 {
		t.Fatal("local analyzer overwrote output or contacted API")
	}
}

func TestRoutesImpactUnknownGateAndMarkdown(t *testing.T) {
	out := captureImpactOutput(t)
	dir := routeImpactRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("from fastapi import FastAPI\napp=FastAPI()\napp.include_router(build_router())\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"routes", "impact", "--base", "HEAD", "--path", dir, "--fail-on-incomplete", "--format", "markdown"}); code != 1 {
		t.Fatalf("unknown gate exit=%d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "## Route impact") || !strings.Contains(out.String(), "dynamic\\_router") || !strings.Contains(out.String(), "unknown") {
		t.Fatal(out.String())
	}
	out.Reset()
	if code := run([]string{"routes", "impact", "--base", "HEAD", "--path", dir, "--fail-on-impact"}); code != 0 {
		t.Fatalf("unknown treated as a confirmed removal: %d %s", code, out.String())
	}
}

func TestRoutesImpactHelpValidationAndSafeRendering(t *testing.T) {
	out := captureImpactOutput(t)
	if code := run([]string{"routes", "impact", "--help"}); code != 0 || !strings.Contains(out.String(), "--base") || !strings.Contains(out.String(), "--entrypoint") {
		t.Fatalf("local help: %d %s", code, out.String())
	}
	for _, args := range [][]string{
		{}, {"my-api", "--base", "HEAD", "--format", "html"}, {"one", "two", "--base", "HEAD"}, {"--base", "HEAD", "--entrypoint", "broken:value:again"},
	} {
		if code := cmdRoutesImpact(args); code != 1 {
			t.Fatalf("invalid args %v accepted: %d", args, code)
		}
	}
	out.Reset()
	report := routeimpact.Report{App: "my-api", Routes: []routeimpact.Result{{Method: "GET", Path: "/[link](https://invalid)\x1b[31m", Change: "unknown"}},
		Issues: []routeimpact.Issue{{Code: "unknown", File: "bad\x1b.py", Message: "<script>\nmessage"}}}
	renderRouteImpact(out, report, "markdown")
	if strings.ContainsRune(out.String(), '\x1b') || strings.Contains(out.String(), "<script>") || strings.Contains(out.String(), "[link](") {
		t.Fatal("terminal or Markdown control content was not escaped")
	}
}

func TestRoutesImpactFunctionPrecisionAndReviewOutput(t *testing.T) {
	out := captureImpactOutput(t)
	dir := routeImpactRepo(t)
	main := "from fastapi import FastAPI\nfrom helpers import price\napp=FastAPI()\n@app.get('/checkout')\ndef checkout(): return price()\n@app.get('/health')\ndef health(): return True\n"
	for file, body := range map[string]string{"main.py": main, "helpers.py": "def price(): return 1\n"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	routeImpactGit(t, dir, "add", ".")
	routeImpactGit(t, dir, "commit", "-qm", "function baseline")
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("# comment\n\n"+main), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"routes", "impact", "--base", "HEAD", "--path", dir, "--fail-on-impact", "--fail-on-incomplete", "--json"}); code != 0 {
		t.Fatalf("formatting failed CI: %d %s", code, out.String())
	}
	var report routeimpact.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Version != 2 || report.Summary.NoLinkedChanges != 2 || len(report.ChangedSymbols) != 0 {
		t.Fatalf("formatting report: %+v %v", report, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "helpers.py"), []byte("def price(): return 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := run([]string{"routes", "impact", "--base", "HEAD", "--path", dir, "--fail-on-impact", "--format", "markdown"}); code != 1 {
		t.Fatalf("helper change did not fail CI: %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "main.checkout (main.py:7) -> helpers.price (helpers.py:1)") || !strings.Contains(out.String(), "function\\_reference") || !strings.Contains(out.String(), "Precision: function") || !strings.Contains(out.String(), "/health — no\\_linked\\_changes") {
		t.Fatalf("review output lacks precise evidence: %s", out.String())
	}
}

func TestRoutesImpactUnresolvedFunctionCallGate(t *testing.T) {
	out := captureImpactOutput(t)
	dir := routeImpactRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("from fastapi import FastAPI\napp=FastAPI()\n@app.get('/checkout')\ndef checkout(callback): return callback()\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	routeImpactGit(t, dir, "add", ".")
	routeImpactGit(t, dir, "commit", "-qm", "callback baseline")
	if code := run([]string{"routes", "impact", "--base", "HEAD", "--path", dir, "--fail-on-incomplete", "--json"}); code != 1 {
		t.Fatalf("unresolved call passed incomplete gate: %d %s", code, out.String())
	}
	var report routeimpact.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Status != "incomplete" || report.Summary.Unknown != 1 || len(report.Routes[0].Uncertainties) != 2 {
		t.Fatalf("uncertainty not exported: %+v %v", report, err)
	}
}
