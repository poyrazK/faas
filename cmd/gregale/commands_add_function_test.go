package main_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func runAddFunction(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := (*main.OsStdout), (*main.OsStderr)
	(*main.OsStdout), (*main.OsStderr) = &stdout, &stderr
	defer func() { (*main.OsStdout), (*main.OsStderr) = oldOut, oldErr }()
	code := main.Run(append([]string{"add", "function"}, args...))
	return code, stdout.String(), stderr.String()
}

// adr: 20261107193000
// Every scaffold is discovered by deploy and passes manifest validation.
func TestAddFunctionScaffoldsAreDiscoveredAndValid(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, lang := range []string{"node", "python"} {
		for _, trigger := range []string{"http", "cron", "queue", "bucket", "webhook"} {
			t.Run(lang+"-"+trigger, func(t *testing.T) {
				dir := t.TempDir()
				args := []string{"job", "--lang", lang, "--trigger", trigger, "--path", dir, "--bucket", "uploads", "--provider", "stripe"}
				code, out, errOut := runAddFunction(t, args...)
				if code != 0 {
					t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
				}
				if !strings.Contains(out, "Created function job") || !strings.Contains(out, "gregale deploy --plan") {
					t.Errorf("output:\n%s", out)
				}
				m, sources, err := gregalemanifest.ApplyInlineConfig(nil, dir)
				if err != nil || m == nil || len(m.Functions) != 1 || m.Functions[0].Name != "job" {
					t.Fatalf("discovery = %+v %v", m, err)
				}
				if err := m.Validate(); err != nil {
					t.Fatalf("scaffold invalid: %v", err)
				}
				if !sources[0].Discovered {
					t.Errorf("sources = %+v", sources)
				}
				switch trigger {
				case "cron":
					if len(m.Triggers) != 1 || m.Triggers[0].Schedule != "0 * * * *" {
						t.Errorf("triggers = %+v", m.Triggers)
					}
				case "queue":
					if qb := m.Functions[0].QueueBindings; len(qb) != 1 || qb[0].QueueName != "job" {
						t.Errorf("queue bindings = %+v", qb)
					}
				case "bucket":
					if be := m.Functions[0].BucketEvents; len(be) != 1 || be[0].Bucket != "uploads" {
						t.Errorf("bucket events = %+v", be)
					}
				case "webhook":
					if wh := m.Functions[0].Webhooks; len(wh) != 1 || wh[0].SigningSecretEnv != "STRIPE_WEBHOOK_SECRET" {
						t.Errorf("webhooks = %+v", wh)
					}
				}
			})
		}
	}
}

func TestAddFunctionRefusalsAndManifestSnippet(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	if code, _, _ := runAddFunction(t, "digest", "--path", dir); code != 0 {
		t.Fatal("first scaffold failed")
	}
	if code, _, errOut := runAddFunction(t, "digest", "--path", dir); code == 0 || !strings.Contains(errOut, "already exists") {
		t.Errorf("second scaffold: exit %d stderr %q, want already exists", code, errOut)
	}
	for _, bad := range [][]string{
		{"Bad_Name"},
		{"x", "--trigger", "sqs"},
		{"x", "--trigger", "bucket"},
		{"x", "--lang", "ruby"},
	} {
		if code, _, _ := runAddFunction(t, append(bad, "--path", dir)...); code == 0 {
			t.Errorf("%v: exit 0, want a refusal", bad)
		}
	}

	listed := t.TempDir()
	if err := os.WriteFile(filepath.Join(listed, "gregale.yaml"), []byte("functions:\n  - name: api\n    root: functions/api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runAddFunction(t, "digest", "--trigger", "cron", "--path", listed, "--dry-run")
	if code != 0 || !strings.Contains(out, "Would create") || !strings.Contains(out, "- name: digest\n    root: functions/digest") {
		t.Errorf("listed manifest: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(listed, "functions", "digest")); !os.IsNotExist(err) {
		t.Errorf("--dry-run wrote files: %v", err)
	}
}

func TestAddFunctionInfersPython(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runAddFunction(t, "thumbs", "--path", dir); code != 0 || !strings.Contains(out, "functions/thumbs/handler.py") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
}
