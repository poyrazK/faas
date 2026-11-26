package main_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

// Prod hunt r34: a webhook, Slack or QStash sender cannot present a Gregale
// API key, and apps are private by default, so following the
// webhook-receiver README answered every request with 401. These templates
// deploy public on purpose (the handler checks the sender's own secret or
// signature) in the init next steps, the README and `init --deploy`. The
// object-storage templates check their own STORAGE_APP_TOKEN in the
// Authorization header a private app reserves for a Gregale API key (prod
// hunt r35).
func TestPublicSenderTemplatesDeployPublic(t *testing.T) {
	want := []string{"webhook-receiver", "slack-bot", "cron-worker", "object-storage-python", "object-storage-node"}
	for _, name := range want {
		if !main.TemplatesPublicOnPurpose[name] {
			t.Errorf("%s is not in templatesPublicOnPurpose", name)
		}
	}
	for name := range main.TemplatesPublicOnPurpose {
		t.Run(name, func(t *testing.T) {
			steps := strings.Join(main.NextStepsFor(name), "\n")
			if !strings.Contains(steps, "--secrets-file <secrets-file> --no-require-authn") {
				t.Errorf("init next steps deploy without --no-require-authn:\n%s", steps)
			}
			if strings.Contains(steps, ".gregale.dev/") {
				t.Errorf("init next steps name the old apps domain:\n%s", steps)
			}
			if args := main.InitDeployArgs(name, "/dest", "demo", "/s"); !slices.Contains(args, "--no-require-authn") {
				t.Errorf("init --deploy runs %q, want --no-require-authn", args)
			}
			dest := filepath.Join(t.TempDir(), name)
			if err := templates.Materialize(name, dest); err != nil {
				t.Fatal(err)
			}
			readme, err := os.ReadFile(filepath.Join(dest, "README.md"))
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(string(readme), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "gregale deploy") && !strings.Contains(line, "--create-only") && !strings.Contains(line, "--no-require-authn") {
					t.Errorf("README deploys private: %q", line)
				}
			}
		})
	}
	if args := main.InitDeployArgs("hello-node", "/dest", "demo", ""); slices.Contains(args, "--no-require-authn") {
		t.Errorf("hello-node deploys public: %q", args)
	}
}

// Prod hunt r37: `init --template mcp-python --deploy` ran a plain deploy, so
// the app was private and every MCP client got 401. MCP templates deploy
// through `mcp server deploy`, as their next steps say.
func TestMCPTemplatesInitDeployAsMCPServers(t *testing.T) {
	for _, name := range []string{"mcp-node", "mcp-node-tasks", "mcp-go", "mcp-go-tasks", "mcp-python", "mcp-python-tasks"} {
		if err := templates.Materialize(name, filepath.Join(t.TempDir(), name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := []string{"--path", "/dest", "--name", "demo", "--secrets-file", "/s"}
		if args := main.InitDeployArgs(name, "/dest", "demo", "/s"); !slices.Equal(args, want) {
			t.Errorf("%s: init --deploy runs %q, want mcp server deploy flags %q", name, args, want)
		}
		if steps := strings.Join(main.NextStepsFor(name), "\n"); !strings.Contains(steps, "gregale mcp server deploy") {
			t.Errorf("%s next steps do not use mcp server deploy:\n%s", name, steps)
		}
	}
}
