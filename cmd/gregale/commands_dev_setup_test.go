package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevSetupReceiptIsReadyAndSecretSafe(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"start":"node server.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, ".env.dev")
	if err := os.WriteFile(envPath, []byte("API_TOKEN=do-not-render\nLOG_LEVEL=debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", testAPIKey('a'))

	config, err := resolveDevSetupSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	path, keys, err := resolveSetupEnvFile(dir, envPath)
	if err != nil {
		t.Fatal(err)
	}
	receipt := buildDevSetupReceipt(dir, dir, "demo", config, path, keys, false, false, false, false, false, "")
	if !receipt.Ready || !receipt.Authenticated {
		t.Fatalf("receipt readiness = ready:%t authenticated:%t, want both true", receipt.Ready, receipt.Authenticated)
	}
	if receipt.Class != "app" || receipt.Framework != "node" {
		t.Fatalf("detected shape = %q/%q, want app/node", receipt.Class, receipt.Framework)
	}
	if receipt.EnvKeyCount != 2 || len(receipt.DependencyFiles) != 1 || receipt.DependencyFiles[0] != "package.json" {
		t.Fatalf("env/dependencies = %d/%v, want 2/[package.json]", receipt.EnvKeyCount, receipt.DependencyFiles)
	}
	body, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "do-not-render") {
		t.Fatalf("setup receipt leaked an env value: %s", body)
	}
}

func TestDevSetupValidatesManifestBeforePlanning(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte("hosting:\n  port: 70000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveDevSetupSource(dir); err == nil || !strings.Contains(err.Error(), "port 70000") {
		t.Fatalf("resolveDevSetupSource error = %v, want manifest port validation", err)
	}
}

func TestCmdDevSetupPrintsNextCommandWithoutRemoteMutation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask==3.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("FAAS_TOKEN", testAPIKey('b'))
	stdout, restoreOut := captureStdout(t)
	defer restoreOut()
	_, restoreErr := captureStderr(t)
	defer restoreErr()

	if code := cmdDevSetup(nil); code != 0 {
		t.Fatalf("cmdDevSetup() = %d, want 0; stdout=%q", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "Developer setup ready") || !strings.Contains(stdout.String(), "gregale dev") {
		t.Fatalf("setup output missing readiness/next command: %q", stdout.String())
	}
}

func TestDevSetupRejectsStartOnlyFlags(t *testing.T) {
	if code := cmdDevSetup([]string{"--once"}); code != 1 {
		t.Fatalf("cmdDevSetup(--once) = %d, want 1", code)
	}
}
