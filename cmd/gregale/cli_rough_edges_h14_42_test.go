package main_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Production-us rc.294 still showed the H12-51 rough edges (Hunt 14 H14-42):
// `projects environments h14p` and `debug requests h14-py` named no verb.
func TestUnknownSubcommandListsTheVerbsAndTheVerbFirstForm(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func([]string) int
		arg  string
		want []string
	}{
		{"projects environments", main.CmdProjectsEnvironments, "h14p", []string{"Subcommands: list, create,", "gregale environment list h14p"}},
		{"debug requests", main.CmdDebugRequests, "h14-py", []string{"Subcommands: list, export,", "gregale debug requests list h14-py"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stderr, restore := main.CaptureStderr(t)
			code := tc.run([]string{tc.arg})
			restore()
			for _, want := range tc.want {
				if code != 1 || !strings.Contains(stderr.String(), want) {
					t.Fatalf("exit = %d; stderr missing %q:\n%s", code, want, stderr.String())
				}
			}
		})
	}
}

func TestProjectInfoMarksUnsetFields(t *testing.T) {
	var out bytes.Buffer
	previousOut, previousJSON := *main.OsStdout, *main.JsonOutput
	*main.OsStdout, *main.JsonOutput = &out, false
	t.Cleanup(func() { *main.OsStdout, *main.JsonOutput = previousOut, previousJSON })
	project := api.ProjectResponse{}
	project.Slug, project.ProductionBranch = "h14p", "main"
	main.RenderProject(project)
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.HasSuffix(line, ":") || strings.HasSuffix(line, ": ") {
			t.Fatalf("projects info line ends at the colon: %q\n%s", line, out.String())
		}
	}
}

func TestDoctorDescribesAWorkerWithoutAListener(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"gregale.yaml":     "hosting:\n  start: python main.py\nworker:\n  drain_timeout: 10s\n",
		"main.py":          "import time\nwhile True:\n    time.sleep(15)\n",
		"requirements.txt": "flask>=3.0,<4.0\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	main.RenderDoctorHuman(&out, main.RunDoctorChecks(dir))
	got := out.String()
	if !strings.Contains(got, "worker (no HTTP listener)") || strings.Contains(got, "health=/") {
		t.Fatalf("doctor described a worker as an HTTP app:\n%s", got)
	}
}
