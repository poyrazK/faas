package main_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ChronoLake finding 16: `gregale test chaos` made every scenario service a
// public app, even one the project declares `visibility: internal`.
func TestScenarioInternalServicesFollowProjectVisibility(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"compose.yaml":          "services:\n  api:\n    build: ./api\n    ports: [\"3000:3000\"]\n  ledger:\n    build: ./ledger\n    ports: [\"4000:4000\"]\n  billing:\n    build: ./billing\n    ports: [\"5000:5000\"]\n",
		".gregale/project.yaml": "services:\n  ledger:\n    visibility: internal\n  billing:\n    visibility: internal\n",
		"api/package.json":      `{"name":"api","scripts":{"start":"node index.js"}}`,
		"api/index.js":          "console.log(1)\n",
		"ledger/package.json":   `{"name":"ledger","scripts":{"start":"node index.js"}}`,
		"ledger/index.js":       "console.log(1)\n",
		"billing/package.json":  `{"name":"billing","scripts":{"start":"node index.js"}}`,
		"billing/index.js":      "console.log(1)\n",
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	scenario := main.TestScenario{Project: "api", Source: ".", Services: map[string]main.TestService{
		"ledger":   {Source: "./ledger"},       // matched by name
		"payments": {Source: "./billing"},      // matched by source directory
		"api-twin": {Source: "./api"},          // public in the project
		"sink":     {Fixture: "delivery-sink"}, // platform fixture, never internal
	}}
	got := main.ScenarioInternalServices(root, scenario)
	if !got["ledger"] || !got["payments"] || got["api-twin"] || got["sink"] || len(got) != 2 {
		t.Fatalf("internal services = %v, want ledger and payments", got)
	}

	client := &testAccessFakeClient{}
	if err := main.ConfigureTestWorkloadVisibility(context.Background(), client, "dev-api-ledger", true); err != nil {
		t.Fatal(err)
	}
	if client.slug != "dev-api-ledger" || client.req.Visibility == nil || *client.req.Visibility != string(api.AppVisibilityInternal) {
		t.Fatalf("visibility update = (%q, %+v)", client.slug, client.req)
	}
	client = &testAccessFakeClient{}
	if err := main.ConfigureTestWorkloadVisibility(context.Background(), client, "dev-api-twin", false); err != nil || client.slug != "" {
		t.Fatalf("public service was updated: %q %v", client.slug, err)
	}
}

// An interrupted test run must cancel (and so release its apps) on a closed
// terminal as well as on SIGINT and SIGTERM.
func TestTestTerminationSignalsIncludeHangup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix signals")
	}
	signals := main.TestTerminationSignals()
	for _, want := range []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP} {
		if !slices.Contains(signals, want) {
			t.Fatalf("termination signals %v miss %v", signals, want)
		}
	}
}
