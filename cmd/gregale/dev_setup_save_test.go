package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func TestDevSetupSavePreviewAndFreshCheckout(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	source := startTestSource(t)
	t.Chdir(source)
	manifestPath := filepath.Join(source, "gregale.yml")
	before := []byte("# keep this comment\nhosting:\n  port: 3000\ndev:\n  debug: false # keep this choice\n")
	if err := os.WriteFile(manifestPath, before, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(manifestPath, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".env.dev"), []byte("API_TOKEN=never-export-this-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "")
	out, restore := captureStdout(t)
	defer restore()
	args := []string{"--save", "--env-file", ".env.dev", "--ttl", "72h", "--postgres"}
	if code := cmdDevSetup(append(append([]string{}, args...), "--dry-run")); code != 0 {
		t.Fatalf("preview exit=%d output=%s", code, out)
	}
	current, _ := os.ReadFile(manifestPath)
	if !bytes.Equal(current, before) {
		t.Fatal("preview modified the manifest")
	}
	var preview devSetupReceipt
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil || preview.SavedConfig == nil || !preview.SavedConfig.DryRun {
		t.Fatalf("missing structured preview: %s %v", out, err)
	}
	out = &safeBuffer{}
	osStdout = out
	if code := cmdDevSetup(args); code != 0 {
		t.Fatalf("save exit=%d output=%s", code, out)
	}
	saved, _ := os.ReadFile(manifestPath)
	for _, text := range []string{string(saved), out.String()} {
		if strings.Contains(text, "never-export-this-value") {
			t.Fatal("setup exported a secret value")
		}
	}
	if calls.Load() != 0 || !strings.Contains(string(saved), "# keep this comment") || !strings.Contains(string(saved), "# keep this choice") {
		t.Fatalf("save made network calls or discarded unrelated configuration: calls=%d\n%s", calls.Load(), saved)
	}
	checkout := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkout, "gregale.yaml"), saved, 0644); err != nil {
		t.Fatal(err)
	}
	manifest, err := loadDevManifest(checkout)
	if err != nil {
		t.Fatal(err)
	}
	var env, services, region, ttl string
	var postgres bool
	applyDevManifestDefaults(manifest, map[string]bool{}, checkout, &env, &services, &postgres, &region, &ttl)
	if env != filepath.Join(checkout, ".env.dev") || !postgres || ttl != "72h" || manifest.Hosting.Port != 3000 || manifest.Dev.Debug == nil || *manifest.Dev.Debug {
		t.Fatalf("defaults did not transfer to fresh checkout: env=%s postgres=%t ttl=%s manifest=%+v", env, postgres, ttl, manifest)
	}
	info, _ := os.Stat(manifestPath)
	if info.Mode().Perm() != 0640 {
		t.Fatal("manifest permissions changed")
	}
}

func TestDevSetupSaveRefusesExternalAndChangedFiles(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	external := filepath.Join(outside, "secrets.env")
	if err := os.WriteFile(external, []byte("TOKEN=secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := portableDevFile(root, external); err == nil {
		t.Fatal("accepted nonportable external file")
	}
	link := filepath.Join(root, ".env")
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	if _, err := portableDevFile(root, link); err == nil {
		t.Fatal("accepted symlink to external file")
	}
	edit, exists, err := devSetupManifestEdit(root, map[string]any{"ttl": "48h"})
	if err != nil || exists {
		t.Fatalf("prepare manifest: exists=%t err=%v", exists, err)
	}
	if err := os.WriteFile(edit.path, []byte("# someone else's file\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := createDevSetupManifest(edit); err == nil {
		t.Fatal("overwrote a newly created manifest")
	}
	current, _ := os.ReadFile(edit.path)
	if string(current) != "# someone else's file\n" {
		t.Fatal("concurrent manifest was changed")
	}
}

func TestDevSetupSavedFalseOverridesExistingDefaults(t *testing.T) {
	before := []byte("dev:\n  postgres: true\n  postgres_region: eu\n  env_file: .env.old\n  ttl: 72h\n")
	after, err := mergeDevSetupYAML(before, map[string]any{"postgres": false, "postgres_region": "", "env_file": "", "ttl": "24h"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := gregalemanifest.ParseBytes(after)
	if err != nil || manifest.Dev.Postgres == nil || *manifest.Dev.Postgres || manifest.Dev.EnvFile != "" || manifest.Dev.PostgresRegion != "" {
		t.Fatalf("disabled choices lost: %s %v", after, err)
	}
}

func TestDevPreflightRejectsLoopbackBeforeProvisioning(t *testing.T) {
	resetJSONOut(t)
	source := startTestSource(t)
	if err := os.WriteFile(filepath.Join(source, "server.js"), []byte("app.listen(3000, '127.0.0.1');\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		w.WriteHeader(500)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", testAPIKey('a'))
	_, restore := captureStdout(t)
	defer restore()
	errOut, restoreErr := captureStderr(t)
	defer restoreErr()
	if code := cmdDev([]string{"--path", source, "--once"}); code == 0 || writes.Load() != 0 || !strings.Contains(errOut.String(), "doctor --path") {
		t.Fatalf("preflight code=%d writes=%d stderr=%s", code, writes.Load(), errOut)
	}
}
