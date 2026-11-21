package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prod hunt 2026-11-21: a warning-only doctor run ended "Fix the above
// findings before deploy", while `deploy --dry-run` went on to say "No
// blocking findings" and deploy shipped. Warnings say they do not block.
func TestDoctorWarningsSayTheyDoNotBlockDeploy(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"requirements.txt": "flask==3.1.2\ngunicorn==23.0.0\n",
		"app.py":           "import os\nfrom flask import Flask\napp = Flask(__name__)\nMODE = os.environ[\"BH_MODE_SETTING\"]\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, stdout, stderr := runCLI(t, "doctor", "--path", dir)
	out := stdout + stderr
	if !strings.Contains(out, "env-required") {
		t.Fatalf("doctor did not report the missing variable:\n%s", out)
	}
	if strings.Contains(out, "Fix the above findings before deploy") || !strings.Contains(out, "do not block a deploy") {
		t.Fatalf("warning-only summary:\n%s", out)
	}
}
