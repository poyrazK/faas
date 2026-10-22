package main_test

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

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

func TestDevSetupSavePreviewAndFreshCheckout(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	source := main.StartTestSource(t)
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
	out, restore := main.CaptureStdout(t)
	defer restore()
	args := []string{"--save", "--env-file", ".env.dev", "--ttl", "72h", "--postgres"}
	if code := main.CmdDevSetup(append(append([]string{}, args...), "--dry-run")); code != 0 {
		t.Fatalf("preview exit=%d output=%s", code, out)
	}
	current, _ := os.ReadFile(manifestPath)
	if !bytes.Equal(current, before) {
		t.Fatal("preview modified the manifest")
	}
	var preview main.DevSetupReceipt
	if err := json.Unmarshal(out.Bytes(), &preview); err != nil || preview.SavedConfig == nil || !preview.SavedConfig.DryRun {
		t.Fatalf("missing structured preview: %s %v", out, err)
	}
	out = &main.SafeBuffer{}
	(*main.OsStdout) = out
	if code := main.CmdDevSetup(args); code != 0 {
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
	manifest, err := main.LoadDevManifest(checkout)
	if err != nil {
		t.Fatal(err)
	}
	var env, services, region, ttl string
	var postgres bool
	main.ApplyDevManifestDefaults(manifest, map[string]bool{}, checkout, &env, &services, &postgres, &region, &ttl)
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
	if _, err := main.PortableDevFile(root, external); err == nil {
		t.Fatal("accepted nonportable external file")
	}
	link := filepath.Join(root, ".env")
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	if _, err := main.PortableDevFile(root, link); err == nil {
		t.Fatal("accepted symlink to external file")
	}
	edit, exists, err := main.DevSetupManifestEdit(root, map[string]any{"ttl": "48h"})
	if err != nil || exists {
		t.Fatalf("prepare manifest: exists=%t err=%v", exists, err)
	}
	if err := os.WriteFile((*edit.Path()), []byte("# someone else's file\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := main.CreateDevSetupManifest(edit); err == nil {
		t.Fatal("overwrote a newly created manifest")
	}
	current, _ := os.ReadFile((*edit.Path()))
	if string(current) != "# someone else's file\n" {
		t.Fatal("concurrent manifest was changed")
	}
}

func TestDevSetupSavedFalseOverridesExistingDefaults(t *testing.T) {
	before := []byte("dev:\n  postgres: true\n  postgres_region: eu\n  env_file: .env.old\n  ttl: 72h\n")
	after, err := main.MergeDevSetupYAML(before, map[string]any{"postgres": false, "postgres_region": "", "env_file": "", "ttl": "24h"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := gregalemanifest.ParseBytes(after)
	if err != nil || manifest.Dev.Postgres == nil || *manifest.Dev.Postgres || manifest.Dev.EnvFile != "" || manifest.Dev.PostgresRegion != "" {
		t.Fatalf("disabled choices lost: %s %v", after, err)
	}
}

func TestDevPreflightRejectsLoopbackBeforeProvisioning(t *testing.T) {
	main.ResetJSONOut(t)
	source := main.StartTestSource(t)
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
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))
	_, restore := main.CaptureStdout(t)
	defer restore()
	errOut, restoreErr := main.CaptureStderr(t)
	defer restoreErr()
	if code := main.CmdDev([]string{"--path", source, "--once"}); code == 0 || writes.Load() != 0 || !strings.Contains(errOut.String(), "doctor --path") {
		t.Fatalf("preflight code=%d writes=%d stderr=%s", code, writes.Load(), errOut)
	}
}
