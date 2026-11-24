package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

// Prod hunt r34: a webhook, Slack or QStash sender cannot present a Gregale
// API key, and apps are private by default, so following the
// webhook-receiver README answered every request with 401. These templates
// deploy public on purpose (the handler checks the sender's own secret or
// signature), in both the init next steps and the README.
func TestPublicSenderTemplatesDeployPublic(t *testing.T) {
	for _, name := range []string{"webhook-receiver", "slack-bot", "cron-worker"} {
		t.Run(name, func(t *testing.T) {
			steps := strings.Join(main.NextStepsFor(name), "\n")
			if !strings.Contains(steps, "gregale deploy --secrets-file <secrets-file> --no-require-authn") {
				t.Errorf("init next steps deploy without --no-require-authn:\n%s", steps)
			}
			if strings.Contains(steps, ".gregale.dev/") {
				t.Errorf("init next steps name the old apps domain:\n%s", steps)
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
}
