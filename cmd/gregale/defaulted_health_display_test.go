package main_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// A source with no health route is ready once its port accepts connections
// (ADR-079 amendment 2), yet the dry-run diff and `gregale doctor` showed the
// defaulted /healthz. Codex read that and passed --healthcheck-path / in three
// of five deploys (agent eval 2026-11-19). A route the source defines is
// still shown.
func TestDefaultedHealthPathIsNotShownAsAProbe(t *testing.T) {
	for _, tt := range []struct {
		name, server, wantPlan, wantDoctor string
	}{
		{"no health route", "const app = require('express')()\napp.get('/', (q, s) => s.send('hi'))\napp.listen(process.env.PORT)\n",
			"", "health=none (ready once the port accepts connections)"},
		{"health route", "const app = require('express')()\napp.get('/healthz', (q, s) => s.send('ok'))\napp.listen(process.env.PORT)\n",
			"/healthz", "health=/healthz"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range map[string]string{
				"package.json": `{"scripts":{"start":"node server.js"},"dependencies":{"express":"^4.19.0"}}`,
				"server.js":    tt.server,
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			plan := main.BuildPreviewBuildPlan(dir, main.ShapeApp, "", "", "abc", false, false)
			if plan.HealthPath != tt.wantPlan {
				t.Fatalf("preview plan health_path = %q, want %q", plan.HealthPath, tt.wantPlan)
			}
			var out bytes.Buffer
			main.RenderDoctorHuman(&out, main.RunDoctorChecks(dir))
			if !strings.Contains(out.String(), tt.wantDoctor) {
				t.Fatalf("doctor output missing %q:\n%s", tt.wantDoctor, out.String())
			}
		})
	}
}
