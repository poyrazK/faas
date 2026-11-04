package main_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// rc.318 field report: `gregale deploy --preview` of a functions manifest
// reserved an app for the first function, then failed with "--preview cannot
// be combined with --runtime". The plan is now local: no login, no app.
func TestDeployPlanOfFunctionSetIsLocal(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"gregale.yaml":                "functions:\n  - name: thumbs\n    root: functions/thumbs\n    env:\n      MODE: fast\n  - name: digest\n    root: functions/digest\n",
		"functions/thumbs/handler.js": "export default async function handler() { return {} }\n",
		"functions/digest/handler.js": "export default async function handler() { return {} }\n",
	}
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	main.WithCwd(t, dir)
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))

	for _, flag := range []string{"--plan", "--preview"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			oldOut, oldErr := (*main.OsStdout), (*main.OsStderr)
			(*main.OsStdout), (*main.OsStderr) = &stdout, &stderr
			defer func() { (*main.OsStdout), (*main.OsStderr) = oldOut, oldErr }()
			if code := main.CmdDeployTarball([]string{flag, "--name", "shop"}); code != 0 {
				t.Fatalf("exit = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			got := stdout.String()
			for _, want := range []string{"no login", "Functions (2)", "shop-thumbs", "shop-digest", "gregale deploy --dry-run"} {
				if !strings.Contains(got, want) {
					t.Fatalf("plan output missing %q:\n%s", want, got)
				}
			}
			if strings.Contains(stderr.String(), "logged in") || strings.Contains(stderr.String(), "--runtime") {
				t.Fatalf("plan reached the deploy fan-out: %s", stderr.String())
			}
		})
	}
}
